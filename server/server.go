package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/relay"
	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
	"github.com/pion/webrtc/v4"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

//go:embed all:templates
var templateFS embed.FS

//go:embed all:assets
var assetFS embed.FS

type Server struct {
	Config      *config.Config
	Relay       *relay.Relay
	RelayM      *relay.RelayManager
	TranscoderM *relay.TranscoderManager
	WebRTCM     *relay.WebRTCManager
	StreamerM   *relay.StreamerManager
	mpdServer   *relay.MPDServer
	tmpl        *template.Template
	httpServers []*http.Server
	startTime   time.Time
	AuthLog     *logrus.Logger

	sessions   map[string]*session
	sessionsMu sync.RWMutex

	authAttempts   map[string]*authAttempt // IP -> attempt info
	authAttemptsMu sync.Mutex

	certManager *autocert.Manager

	scanAttempts   map[string]*scanAttempt // IP -> 404 count
	scanAttemptsMu sync.Mutex
}

type scanAttempt struct {
	Count     int
	LockoutBy time.Time
}

type authAttempt struct {
	Count     int
	LockoutBy time.Time
}

type session struct {
	User      *config.User
	CSRFToken string
}

// protocolSniffer allows multiplexing TLS and plain HTTP on the same port
type protocolSniffer struct {
	net.Listener
	tlsChan  chan net.Conn
	httpChan chan net.Conn
}

func (p *protocolSniffer) Accept() (net.Conn, error) {
	return nil, fmt.Errorf("use AcceptTLS or AcceptHTTP")
}

type sniffedConn struct {
	net.Conn
	reader io.Reader
}

func (c *sniffedConn) Read(b []byte) (int, error) {
	return c.reader.Read(b)
}

type chanListener struct {
	addr net.Addr
	ch   chan net.Conn
}

func (l *chanListener) Accept() (net.Conn, error) {
	c, ok := <-l.ch
	if !ok {
		return nil, fmt.Errorf("listener closed")
	}
	return c, nil
}
func (l *chanListener) Close() error   { return nil }
func (l *chanListener) Addr() net.Addr { return l.addr }

func (p *protocolSniffer) sniff() {
	for {
		conn, err := p.Listener.Accept()
		if err != nil {
			return
		}

		go func(c net.Conn) {
			// Peek at the first byte
			buf := make([]byte, 1)
			c.SetReadDeadline(time.Now().Add(5 * time.Second))
			n, err := c.Read(buf)
			c.SetReadDeadline(time.Time{})

			if err != nil || n == 0 {
				c.Close()
				return
			}

			wrapped := &sniffedConn{
				Conn:   c,
				reader: io.MultiReader(bytes.NewReader(buf), c),
			}

			if buf[0] == 0x16 { // TLS Handshake record type
				p.tlsChan <- wrapped
			} else {
				p.httpChan <- wrapped
			}
		}(conn)
	}
}

func NewServer(cfg *config.Config, authLog *logrus.Logger) *Server {
	tmpl := template.New("base")
	tmpl, err := tmpl.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		logrus.Fatalf("Error loading embedded templates: %v", err)
	}

	hm, err := relay.NewHistoryManager("history.db")
	if err != nil {
		logrus.WithError(err).Fatal("Failed to initialize history manager")
	}

	r := relay.NewRelay(cfg.LowLatencyMode, hm)
	return &Server{
		Config:      cfg,
		Relay:       r,
		RelayM:      relay.NewRelayManager(r),
		TranscoderM: relay.NewTranscoderManager(r),
		WebRTCM:     relay.NewWebRTCManager(r),
		StreamerM:   relay.NewStreamerManager(r, cfg),
		tmpl:        tmpl,
		startTime:   time.Now(),
		AuthLog:     authLog,
		sessions:    make(map[string]*session),
		authAttempts: make(map[string]*authAttempt),
		scanAttempts: make(map[string]*scanAttempt),
	}
}

func (s *Server) dispatchWebhook(event string, data map[string]interface{}) {
	if len(s.Config.Webhooks) == 0 {
		return
	}

	payload := map[string]interface{}{
		"event":     event,
		"timestamp": time.Now().Format(time.RFC3339),
		"hostname":  s.Config.HostName,
		"data":      data,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		logrus.WithError(err).Error("Failed to marshal webhook payload")
		return
	}

	for _, wh := range s.Config.Webhooks {
		if !wh.Enabled {
			continue
		}

		// Check if this webhook is interested in this event
		interested := false
		for _, e := range wh.Events {
			if e == event {
				interested = true
				break
			}
		}

		if !interested {
			continue
		}

		// Dispatch asynchronously
		go func(url string, body []byte) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", "TinyIce-Webhook/1.0")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				logrus.Warnf("Webhook delivery failed to %s: %v", url, err)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				logrus.Warnf("Webhook returned non-2xx status from %s: %d", url, resp.StatusCode)
			}
		}(wh.URL, jsonPayload)
	}
}

func (s *Server) setupRoutes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin", s.handleAdmin)
	mux.HandleFunc("/admin/add-mount", s.handleAddMount)
	mux.HandleFunc("/admin/toggle-latency", s.handleToggleLatency)
	mux.HandleFunc("/admin/stats", s.handleStats)
	mux.HandleFunc("/admin/events", s.handleEvents)
	mux.HandleFunc("/admin/metadata", s.handleMetadata)
	mux.HandleFunc("/admin/kick", s.handleKick)
	mux.HandleFunc("/admin/remove-mount", s.handleRemoveMount)
	mux.HandleFunc("/admin/kick-all-listeners", s.handleKickAllListeners)
	mux.HandleFunc("/admin/toggle-mount", s.handleToggleMount)
	mux.HandleFunc("/admin/toggle-visible", s.handleToggleVisible)
	mux.HandleFunc("/admin/update-fallback", s.handleUpdateFallback)
	mux.HandleFunc("/admin/add-user", s.handleAddUser)
	mux.HandleFunc("/admin/remove-user", s.handleRemoveUser)
	mux.HandleFunc("/admin/add-banned-ip", s.handleAddBannedIP)
	mux.HandleFunc("/admin/remove-banned-ip", s.handleRemoveBannedIP)
	mux.HandleFunc("/admin/clear-auth-lockout", s.handleClearAuthLockout)
	mux.HandleFunc("/admin/clear-scan-lockout", s.handleClearScanLockout)
	mux.HandleFunc("/admin/add-webhook", s.handleAddWebhook)
	mux.HandleFunc("/admin/delete-webhook", s.handleDeleteWebhook)
	mux.HandleFunc("/admin/player/toggle", s.handlePlayerToggle)
	mux.HandleFunc("/admin/player/scan", s.handlePlayerScan)
	mux.HandleFunc("/admin/player/reorder", s.handlePlayerReorder)
	mux.HandleFunc("/admin/player/queue", s.handlePlayerQueue)
	mux.HandleFunc("/admin/player/shuffle", s.handlePlayerShuffle)
	mux.HandleFunc("/admin/player/next", s.handlePlayerNext)
	mux.HandleFunc("/admin/player/files", s.handlePlayerFiles)
	mux.HandleFunc("/admin/player/playlist-action", s.handlePlayerPlaylistAction)
	mux.HandleFunc("/admin/autodj/add", s.handleAddAutoDJ)
	mux.HandleFunc("/admin/autodj/delete", s.handleDeleteAutoDJ)
	mux.HandleFunc("/admin/autodj/toggle", s.handleToggleAutoDJ)
	mux.HandleFunc("/admin/autodj/update", s.handleUpdateAutoDJ)
	mux.HandleFunc("/admin/add-relay", s.handleAddRelay)
	mux.HandleFunc("/admin/toggle-relay", s.handleToggleRelay)
	mux.HandleFunc("/admin/restart-relay", s.handleRestartRelay)
	mux.HandleFunc("/admin/delete-relay", s.handleDeleteRelay)
	mux.HandleFunc("/admin/add-transcoder", s.handleAddTranscoder)
	mux.HandleFunc("/admin/toggle-transcoder", s.handleToggleTranscoder)
	mux.HandleFunc("/admin/delete-transcoder", s.handleDeleteTranscoder)
	mux.HandleFunc("/admin/transcoder-stats", s.handleTranscoderStats)
	mux.HandleFunc("/admin/security-stats", s.handleGetSecurityStats)
	mux.HandleFunc("/admin/history", s.handleHistory)
	mux.HandleFunc("/admin/statistics", s.handleGetStats)
	mux.HandleFunc("/admin/insights", s.handleInsights)
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/logout", s.handleLogout)
	mux.HandleFunc("/explore", s.handleExplore)
	mux.HandleFunc("/webrtc/offer", s.handleWebRTCOffer)
	mux.HandleFunc("/player/", s.handlePlayer)
	mux.HandleFunc("/player-webrtc/", s.handleWebRTCPlayer)
	mux.HandleFunc("/embed/", s.handleEmbed)
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/events", s.handlePublicEvents)
	mux.HandleFunc("/status-json.xsl", s.handleLegacyStats)
	mux.HandleFunc("/metrics", s.handleMetrics)
	subFS, _ := fs.Sub(assetFS, "assets")
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(subFS))))
	return mux
}

func (s *Server) listenWithReuse(network, address string) (net.Listener, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var err error
			err2 := c.Control(func(fd uintptr) {
				err = setReusePort(fd)
			})
			if err2 != nil {
				return err2
			}
			return err
		},
	}
	return lc.Listen(context.Background(), network, address)
}

func (s *Server) Shutdown(ctx context.Context) error {
	logrus.Info("Server shutting down gracefully...")

	// Stop relays and transcoders immediately to prevent dual-pulling if a new instance starts
	s.RelayM.StopAll()
	s.TranscoderM.StopAll()
	
	// Force signal all listeners to stop reading

	s.Relay.DisconnectAllListeners()

	var wg sync.WaitGroup
	for _, srv := range s.httpServers {
		wg.Add(1)
		go func(srv *http.Server) {
			defer wg.Done()
			if err := srv.Shutdown(ctx); err != nil {
				logrus.Errorf("Error during HTTP server shutdown: %v", err)
			}
		}(srv)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) ReloadConfig(cfg *config.Config) {
	s.Config = cfg
	// Re-sync relays
	s.RelayM.StopAll()
	for _, rc := range s.Config.Relays {
		if rc.Enabled {
			s.RelayM.StartRelay(rc.URL, rc.Mount, rc.Password, rc.BurstSize, s.Config.VisibleMounts[rc.Mount])
		}
	}
	// Re-sync transcoders
	s.TranscoderM.StopAll()
	for _, tc := range s.Config.Transcoders {
		if tc.Enabled {
			s.TranscoderM.StartTranscoder(tc)
		}
	}
	logrus.Info("Configuration reloaded successfully")
}

func (s *Server) Start() error {
	mux := s.setupRoutes()
	addr := s.Config.BindHost + ":" + s.Config.Port

	if s.Config.DirectoryListing {
		go s.directoryReportingTask()
	}
	go s.statsRecordingTask()

	for _, rc := range s.Config.Relays {
		if rc.Enabled {
			s.RelayM.StartRelay(rc.URL, rc.Mount, rc.Password, rc.BurstSize, s.Config.VisibleMounts[rc.Mount])
		}
	}

	for _, tc := range s.Config.Transcoders {
		if tc.Enabled {
			s.TranscoderM.StartTranscoder(tc)
		}
	}

	// Start configured AutoDJs
	for _, adj := range s.Config.AutoDJs {
		if adj.Enabled {
			streamer, err := s.StreamerM.StartStreamer(adj.Name, adj.Mount, adj.MusicDir, adj.Loop, adj.Format, adj.Bitrate, adj.InjectMetadata, adj.Playlist, adj.MPDEnabled, adj.MPDPort, adj.MPDPassword)
			if err != nil {
				logrus.WithError(err).Errorf("Failed to start AutoDJ %s", adj.Name)
			} else {
				logrus.Infof("AutoDJ %s started on %s", adj.Name, adj.Mount)
				if adj.InjectMetadata {
					if st, ok := s.Relay.GetStream(adj.Mount); ok {
						st.SetVisible(true)
					}
				}
				if len(adj.Playlist) == 0 {
					streamer.ScanMusicDir() // Initial scan
				}
			}
		}
	}

	if !s.Config.UseHTTPS {
		logrus.Infof("Starting TinyIce on %s (HTTP)", addr)
		srv := &http.Server{
			Addr:         addr,
			Handler:      mux,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 0,
			IdleTimeout:  120 * time.Second,
		}
		s.httpServers = append(s.httpServers, srv)

		ln, err := s.listenWithReuse("tcp", addr)
		if err != nil {
			return err
		}
		return srv.Serve(ln)
	}

	return s.startHTTPS(mux, addr)
}

func (s *Server) dynamicHostPolicy(ctx context.Context, host string) error {
	for _, d := range s.Config.Domains {
		if host == d {
			return nil
		}
	}
	return fmt.Errorf("acme/autocert: host %q not configured in 'domains'", host)
}

func (s *Server) startHTTPS(mux *http.ServeMux, addr string) error {
	httpsAddr := s.Config.BindHost + ":" + s.Config.HTTPSPort

	if s.Config.AutoHTTPS {
		if len(s.Config.Domains) == 0 {
			logrus.Warn("Auto-HTTPS is enabled but no domains are configured in 'domains'. Certificates will not be issued.")
		}
		if s.Config.Port != "80" || s.Config.HTTPSPort != "443" {
			logrus.Warnf("Auto-HTTPS usually requires port 80 and 443 to satisfy ACME challenges. Current ports: HTTP=%s, HTTPS=%s. Ensure you have port forwarding (80->%s, 443->%s) configured.", s.Config.Port, s.Config.HTTPSPort, s.Config.Port, s.Config.HTTPSPort)
		}

		s.certManager = &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: s.dynamicHostPolicy,
			Cache:      autocert.DirCache("certs"),
			Email:      s.Config.ACMEEmail,
		}
		if s.Config.ACMEDirectoryURL != "" {
			s.certManager.Client = &acme.Client{DirectoryURL: s.Config.ACMEDirectoryURL}
		}
	}

	httpsSrv := &http.Server{
		Addr:         httpsAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}
	if s.certManager != nil {
		httpsSrv.TLSConfig = s.certManager.TLSConfig()
	}
	s.httpServers = append(s.httpServers, httpsSrv)

	httpSrv := &http.Server{
		Addr: addr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "PUT" || r.Method == "SOURCE" {
				mux.ServeHTTP(w, r)
				return
			}
			if s.certManager != nil && strings.HasPrefix(r.URL.Path, "/.well-known/acme-challenge/") {
				logrus.WithFields(logrus.Fields{
					"path": r.URL.Path,
					"ip":   r.RemoteAddr,
				}).Info("Handling ACME challenge request")
				s.certManager.HTTPHandler(nil).ServeHTTP(w, r)
				return
			}
			target := "https://" + r.Host + r.URL.Path
			if len(r.URL.RawQuery) > 0 {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusMovedPermanently)
		}),
		ReadTimeout: 10 * time.Second,
		IdleTimeout: 120 * time.Second,
	}
	s.httpServers = append(s.httpServers, httpSrv)

	go func() {
		logrus.Infof("Starting HTTP listener on %s", addr)
		ln, err := s.listenWithReuse("tcp", addr)
		if err != nil {
			logrus.Fatalf("HTTP listen failed: %v", err)
		}
		if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logrus.Fatalf("HTTP server failed: %v", err)
		}
	}()

	logrus.Infof("Starting dual-mode HTTPS/HTTP server on %s", httpsAddr)
	rawLn, err := s.listenWithReuse("tcp", httpsAddr)
	if err != nil {
		return err
	}

	sniffer := &protocolSniffer{
		Listener: rawLn,
		tlsChan:  make(chan net.Conn, 1024),
		httpChan: make(chan net.Conn, 1024),
	}
	go sniffer.sniff()

	tlsLn := &chanListener{addr: rawLn.Addr(), ch: sniffer.tlsChan}
	plainLn := &chanListener{addr: rawLn.Addr(), ch: sniffer.httpChan}

	// Plain HTTP handler for port 443 (sniffed)
	go func() {
		if err := httpSrv.Serve(plainLn); err != nil && err != http.ErrServerClosed {
			logrus.Errorf("Sniffed HTTP server failed: %v", err)
		}
	}()

	if s.certManager != nil {
		return httpsSrv.ServeTLS(tlsLn, "", "")
	}
	return httpsSrv.ServeTLS(tlsLn, s.Config.CertFile, s.Config.KeyFile)
}

func (s *Server) logAuth() *logrus.Entry {
	if s.AuthLog != nil {
		return s.AuthLog.WithField("subsystem", "auth")
	}
	return logrus.WithField("subsystem", "auth")
}

func (s *Server) logAuthFailed(user, ip, reason string) {
	// Format designed for fail2ban parsing
	host, _, _ := net.SplitHostPort(ip)
	s.logAuth().WithFields(logrus.Fields{
		"user":   user,
		"ip":     host,
		"reason": reason,
	}).Warnf("Authentication failed for user '%s' from %s: %s", user, host, reason)
}

func (s *Server) checkAuthLimit(ip string) error {
	s.authAttemptsMu.Lock()
	defer s.authAttemptsMu.Unlock()

	attempt, exists := s.authAttempts[ip]
	if !exists {
		return nil
	}

	if time.Now().Before(attempt.LockoutBy) {
		return fmt.Errorf("too many failed attempts. locked out until %s", attempt.LockoutBy.Format("15:04:05"))
	}

	// Reset if lockout period has passed
	if time.Now().After(attempt.LockoutBy) && attempt.Count >= 5 {
		attempt.Count = 0
	}

	return nil
}

func (s *Server) recordAuthFailure(ip string) {
	s.authAttemptsMu.Lock()
	defer s.authAttemptsMu.Unlock()

	attempt, exists := s.authAttempts[ip]
	if !exists {
		attempt = &authAttempt{}
		s.authAttempts[ip] = attempt
	}

	attempt.Count++
	if attempt.Count >= 5 {
		attempt.LockoutBy = time.Now().Add(15 * time.Minute)
		logrus.Warnf("IP %s locked out for 15 minutes due to 5 failed auth attempts", ip)
		s.dispatchWebhook("security_lockout", map[string]interface{}{
			"ip":      ip,
			"reason":  "brute_force_auth",
			"until":   attempt.LockoutBy.Format(time.RFC3339),
			"details": "5 failed authentication attempts",
		})
	}
}

func (s *Server) recordAuthSuccess(ip string) {
	s.authAttemptsMu.Lock()
	defer s.authAttemptsMu.Unlock()
	delete(s.authAttempts, ip)
}

func (s *Server) recordScanAttempt(ip string) {
	s.scanAttemptsMu.Lock()
	defer s.scanAttemptsMu.Unlock()

	attempt, exists := s.scanAttempts[ip]
	if !exists {
		attempt = &scanAttempt{}
		s.scanAttempts[ip] = attempt
	}

	attempt.Count++
	if attempt.Count >= 10 {
		attempt.LockoutBy = time.Now().Add(15 * time.Minute)
		logrus.Warnf("IP %s locked out for 15 minutes due to 10 scanning attempts (404s)", ip)
		s.dispatchWebhook("security_lockout", map[string]interface{}{
			"ip":      ip,
			"reason":  "connection_scanning",
			"until":   attempt.LockoutBy.Format(time.RFC3339),
			"details": "10 connection scanning attempts (404s)",
		})
	}
}

func (s *Server) checkAuth(r *http.Request) (*config.User, bool) {
	// 1. Check Session Cookie first (Web UI)
	if cookie, err := r.Cookie("sid"); err == nil {
		s.sessionsMu.RLock()
		sess, ok := s.sessions[cookie.Value]
		s.sessionsMu.RUnlock()
		if ok {
			return sess.User, true
		}
	}

	// 2. Fallback to Basic Auth (API/Clients)
	u, p, ok := r.BasicAuth()
	if !ok {
		return nil, false
	}

	// Check if we already have a session, if so, don't log Basic Auth success again
	// (Browsers often keep sending both)
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if err := s.checkAuthLimit(host); err != nil {
		s.logAuthFailed(u, r.RemoteAddr, err.Error())
		return nil, false
	}

	user, exists := s.Config.Users[u]
	if !exists {
		s.recordAuthFailure(host)
		s.logAuthFailed(u, r.RemoteAddr, "user not found")
		return nil, false
	}
	if !config.CheckPasswordHash(p, user.Password) {
		s.recordAuthFailure(host)
		s.logAuthFailed(u, r.RemoteAddr, "invalid password")
		return nil, false
	}

	s.recordAuthSuccess(host)
	s.logAuth().WithFields(logrus.Fields{"user": u, "ip": host}).Info("Admin auth successful (Basic)")
	return user, true
}

func (s *Server) hasAccess(user *config.User, mount string) bool {
	if user.Role == config.RoleSuperAdmin {
		return true
	}
	_, exists := user.Mounts[mount]
	return exists
}

func (s *Server) isCSRFSafe(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return true
	}

	// If no session cookie, we assume it's a Basic Auth / API request.
	// checkAuth will handle the actual credential verification.
	cookie, err := r.Cookie("sid")
	if err != nil {
		return true
	}

	// 1. Basic Origin/Referer check for session-based requests
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	// We allow missing origin/referer for now to be less restrictive, 
	// but we MUST have a valid CSRF token if we have a session.

	// 2. Session-based Token Check
	s.sessionsMu.RLock()
	sess, ok := s.sessions[cookie.Value]
	s.sessionsMu.RUnlock()

	if !ok {
		// Session cookie exists but session not found in memory (expired/restarted).
		// We return true here to avoid "Forbidden" and let checkAuth handle the 
		// "Unauthorized" redirect/error which is much cleaner.
		return true
	}

	return r.FormValue("csrf") == sess.CSRFToken
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	logrus.WithFields(logrus.Fields{"method": r.Method, "path": r.URL.Path}).Debug("Root handler request")
	if r.Method == "PUT" || r.Method == "SOURCE" {
		s.handleSource(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, ".m3u8") || strings.HasSuffix(r.URL.Path, ".m3u") {
		s.handlePlaylist(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, ".pls") {
		s.handlePLS(w, r)
		return
	}
	if r.Method == "GET" && r.URL.Path != "/" && r.URL.Path != "/favicon.ico" && r.URL.Path != "/admin" && !strings.HasPrefix(r.URL.Path, "/player") {
		s.handleListener(w, r)
		return
	}
	s.handleStatus(w, r)
}

func (s *Server) handlePLS(w http.ResponseWriter, r *http.Request) {
	mount := strings.TrimSuffix(r.URL.Path, ".pls")
	st, ok := s.Relay.GetStream(mount)
	if !ok {
		http.NotFound(w, r)
		return
	}

	baseURL := s.Config.BaseURL
	if baseURL == "" {
		proto := "http://"
		if s.Config.UseHTTPS || r.Header.Get("X-Forwarded-Proto") == "https" {
			proto = "https://"
		}
		baseURL = proto + r.Host
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	w.Header().Set("Content-Type", "audio/x-scpls")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.pls\"", st.Name))
	fmt.Fprintf(w, "[playlist]\nNumberOfEntries=1\nFile1=%s%s\nTitle1=%s\nLength1=-1\nVersion=2\n", baseURL, mount, st.Name)
}

func (s *Server) handlePlaylist(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	ext := ".m3u8"
	if strings.HasSuffix(path, ".m3u") {
		ext = ".m3u"
	}
	mount := strings.TrimSuffix(path, ext)

	st, ok := s.Relay.GetStream(mount)
	if !ok {
		http.NotFound(w, r)
		return
	}

	baseURL := s.Config.BaseURL
	if baseURL == "" {
		proto := "http://"
		if s.Config.UseHTTPS || r.Header.Get("X-Forwarded-Proto") == "https" {
			proto = "https://"
		}
		baseURL = proto + r.Host
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	w.Header().Set("Content-Type", "audio/x-mpegurl")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s%s\"", st.Name, ext))
	fmt.Fprintf(w, "#EXTM3U\n#EXTINF:-1,%s\n%s%s\n", st.Name, baseURL, mount)
}

func (s *Server) isBanned(ipStr string) bool {
	host, _, err := net.SplitHostPort(ipStr)
	if err != nil {
		host = ipStr
	}

	// 1. Check Automated Lockouts (Auth)
	s.authAttemptsMu.Lock()
	if att, ok := s.authAttempts[host]; ok && time.Now().Before(att.LockoutBy) {
		s.authAttemptsMu.Unlock()
		return true
	}
	s.authAttemptsMu.Unlock()

	// 2. Check Automated Lockouts (Scanning)
	s.scanAttemptsMu.Lock()
	if att, ok := s.scanAttempts[host]; ok && time.Now().Before(att.LockoutBy) {
		s.scanAttemptsMu.Unlock()
		return true
	}
	s.scanAttemptsMu.Unlock()

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}

	for _, banned := range s.Config.BannedIPs {
		// Try parsing as CIDR
		if strings.Contains(banned, "/") {
			_, ipnet, err := net.ParseCIDR(banned)
			if err == nil && ipnet.Contains(ip) {
				return true
			}
		}
		// Fallback to exact match
		if banned == host {
			return true
		}
	}
	return false
}

func (s *Server) getSourcePassword(mount string) (string, bool) {
	for _, user := range s.Config.Users {
		if pass, ok := user.Mounts[mount]; ok {
			return pass, true
		}
	}
	if pass, ok := s.Config.Mounts[mount]; ok {
		return pass, true
	}
	return s.Config.DefaultSourcePassword, s.Config.DefaultSourcePassword != ""
}

func (s *Server) handleSource(w http.ResponseWriter, r *http.Request) {
	if s.isBanned(r.RemoteAddr) {
		logrus.WithField("ip", r.RemoteAddr).Warn("Banned IP source connection")
		return
	}
	mount := r.URL.Path
	requiredPass, found := s.getSourcePassword(mount)
	if !found {
		requiredPass = s.Config.DefaultSourcePassword
	}

	if s.Config.DisabledMounts[mount] {
		logrus.WithField("mount", mount).Warn("Disabled mount connection")
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	_, p, ok := r.BasicAuth()
	if !ok || !config.CheckPasswordHash(p, requiredPass) {
		u, _, _ := r.BasicAuth()
		if u == "" {
			u = "unknown"
		}
		s.logAuthFailed(u, r.RemoteAddr, "source password mismatch")
		w.Header().Set("WWW-Authenticate", `Basic realm="Icecast"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	s.logAuth().WithFields(logrus.Fields{"mount": mount, "ip": host}).Info("Source auth successful")

	// Record Source User-Agent
	if s.Relay.History != nil {
		s.Relay.History.RecordUA(r.Header.Get("User-Agent"), "source")
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking unsupported", http.StatusInternalServerError)
		return
	}
	conn, bufrw, err := hj.Hijack()
	if err != nil {
		logrus.WithError(err).Error("Hijack failed")
		return
	}
	defer conn.Close()

	bufrw.WriteString("HTTP/1.0 200 OK\r\nServer: Icecast 2.4.4\r\nConnection: Keep-Alive\r\n\r\n")
	bufrw.Flush()

	logrus.WithFields(logrus.Fields{"mount": mount, "ip": r.RemoteAddr, "ua": r.Header.Get("User-Agent")}).Info("Source connected")
	s.dispatchWebhook("source_connect", map[string]interface{}{
		"mount": mount,
		"ip":    r.RemoteAddr,
		"ua":    r.Header.Get("User-Agent"),
		"name":  r.Header.Get("Ice-Name"),
	})

	stream := s.Relay.GetOrCreateStream(mount)
	stream.SourceIP = r.RemoteAddr

	s.updateSourceMetadata(stream, mount, r)

	buf := make([]byte, 8192)
	for {
		n, err := bufrw.Read(buf)
		if n > 0 {
			stream.Broadcast(buf[:n], s.Relay)
		}
		if err != nil {
			break
		}
	}
	logrus.WithField("mount", mount).Info("Source disconnected")
	s.dispatchWebhook("source_disconnect", map[string]interface{}{
		"mount": mount,
	})
	s.Relay.RemoveStream(mount)
}

func (s *Server) updateSourceMetadata(stream *relay.Stream, mount string, r *http.Request) {
	bitrate := r.Header.Get("Ice-Bitrate")
	if bitrate == "" || bitrate == "N/A" {
		audioInfo := r.Header.Get("Ice-Audio-Info")
		if audioInfo != "" {
			parts := strings.Split(audioInfo, ";")
			for _, part := range parts {
				if strings.HasPrefix(strings.TrimSpace(part), "bitrate=") {
					bitrate = strings.TrimPrefix(strings.TrimSpace(part), "bitrate=")
					break
				}
			}
		}
	}
	isPublic := r.Header.Get("Ice-Public") == "1"
	isVisible := s.Config.VisibleMounts[mount]
	if stream.UpdateMetadata(r.Header.Get("Ice-Name"), r.Header.Get("Ice-Description"), r.Header.Get("Ice-Genre"), r.Header.Get("Ice-Url"), bitrate, r.Header.Get("Content-Type"), isPublic, isVisible) {
		s.dispatchWebhook("metadata_update", map[string]interface{}{
			"mount":        mount,
			"name":         stream.Name,
			"description":  stream.Description,
			"genre":        stream.Genre,
			"current_song": stream.CurrentSong,
		})
	}
}

func (s *Server) handleListener(w http.ResponseWriter, r *http.Request) {
	if s.isBanned(r.RemoteAddr) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	originalMount := r.URL.Path
	mount := originalMount

	// Record Listener User-Agent
	if s.Relay.History != nil {
		s.Relay.History.RecordUA(r.Header.Get("User-Agent"), "listener")
	}

	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if s.Config.LowLatencyMode {
		w.Header().Set("X-Accel-Buffering", "no")
	}

	flusher, _ := w.(http.Flusher)
	id := r.RemoteAddr + "-" + fmt.Sprintf("%d", time.Now().UnixNano())
	logrus.WithFields(logrus.Fields{"mount": mount, "ip": r.RemoteAddr, "ua": r.Header.Get("User-Agent")}).Info("Listener connected")
	defer logrus.WithFields(logrus.Fields{"mount": mount, "ip": r.RemoteAddr}).Info("Listener disconnected")

	recoveryTicker := time.NewTicker(10 * time.Second)
	defer recoveryTicker.Stop()

	for {
		if mount != originalMount {
			if _, ok := s.Relay.GetStream(originalMount); ok {
				logrus.WithField("mount", originalMount).Info("Primary stream returned, recovering from fallback")
				mount = originalMount
			}
		}

		stream, ok := s.Relay.GetStream(mount)
		if !ok {
			fallback, hasFallback := s.Config.FallbackMounts[mount]
			if hasFallback && fallback != mount {
				logrus.WithFields(logrus.Fields{"from": mount, "to": fallback}).Info("Primary stream down, falling back")
				mount = fallback
				continue
			}
			if mount != originalMount {
				mount = originalMount
				time.Sleep(1 * time.Second)
				continue
			}
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			s.recordScanAttempt(host)
			http.NotFound(w, r)
			return
		}

		// ICY Metadata negotiation
		metaint := 0
		// Ogg/Opus handles its own metadata. Injecting ICY metadata into an Ogg 
		// stream corrupts the framing and causes CRC errors.
		if r.Header.Get("Icy-MetaData") == "1" && !stream.IsOgg() {
			metaint = 16000 // Standard interval
			w.Header().Set("icy-metaint", "16000")
			w.Header().Set("icy-name", s.Config.PageTitle)
		}

		if s.Config.MaxListeners > 0 && stream.ListenersCount() >= s.Config.MaxListeners {
			http.Error(w, "Server Full", http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", stream.ContentType)
		if flusher != nil {
			flusher.Flush()
		}

		if !s.serveStreamData(w, r, stream, id, originalMount, mount, recoveryTicker, metaint) {
			return
		}
		// Loop will continue and check for fallback or re-connect to primary
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *Server) serveStreamData(w http.ResponseWriter, r *http.Request, stream *relay.Stream, id, originalMount, currentMount string, recoveryTicker *time.Ticker, metaint int) bool {
	// Subscribe with 256KB burst (approx 32s @ 64kbps Opus) for robust instant start
	offset, signal := stream.Subscribe(id, 256*1024)
	defer stream.Unsubscribe(id)

	if stream.OggHead != nil {
		// Always send stored headers for Ogg/Opus
		if _, err := w.Write(stream.OggHead); err != nil {
			return false
		}
		// The 'offset' returned by Subscribe is already intelligently aligned 
		// AFTER the headers in the buffer if OggHeaderOffset was set.
		logrus.Debugf("Ogg Listener %s: Sending stored headers (%d bytes), then starting burst at %d", id, len(stream.OggHead), offset)
	}

	buf := make([]byte, 16384)
	flusher, _ := w.(http.Flusher)

	bytesSentSinceMeta := 0
	lastSong := ""

	for {
		select {
		case <-r.Context().Done():
			return false
		case <-recoveryTicker.C:
			if currentMount != originalMount {
				if _, ok := s.Relay.GetStream(originalMount); ok {
					return true // Break to switch back to primary
				}
			}
		case _, ok := <-signal:
			if !ok {
				return true // Stream closed/source disconnected
			}
			for {
				// Calculate how much we can read before next metadata injection
				readLimit := len(buf)
				if metaint > 0 {
					remaining := metaint - bytesSentSinceMeta
					if remaining < readLimit {
						readLimit = remaining
					}
				}

				n, next, skipped := stream.Buffer.ReadAt(offset, buf[:readLimit])
				if skipped && stream.IsOggStream {
					// Listener is too slow, re-align to next valid Ogg page start
					offset = stream.Buffer.FindNextPageBoundary(next)
					continue
				}
				if n == 0 {
					break
				}
				if skipped {
					atomic.AddInt64(&stream.BytesDropped, next-offset)
				}
				offset = next

				if _, err := w.Write(buf[:n]); err != nil {
					return false
				}

				if metaint > 0 {
					bytesSentSinceMeta += n
					if bytesSentSinceMeta >= metaint {
						// Time to inject metadata
						currentSong := stream.GetCurrentSong()
						meta := ""
						if currentSong != lastSong {
							meta = fmt.Sprintf("StreamTitle='%s';", currentSong)
							lastSong = currentSong
						}

						// Metadata block: [length byte] [data...]
						// length = ceil(len(data) / 16)
						// actual bytes = length * 16
						l := (len(meta) + 15) / 16
						res := make([]byte, 1+l*16)
						res[0] = byte(l)
						copy(res[1:], meta)

						if _, err := w.Write(res); err != nil {
							return false
						}
						bytesSentSinceMeta = 0
					}
				}

				atomic.AddInt64(&s.Relay.BytesOut, int64(n))
				atomic.AddInt64(&stream.BytesOut, int64(n))
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	md := ""
	if data, err := os.ReadFile("LANDING.md"); err == nil {
		md = string(data)
	} else {
		md = `# Welcome to TinyIce
Explore our high-performance live streaming network. Discover new music, live shows, and community broadcasts from around the world.

* **High Performance**: Built with zero-allocation broadcasting.
* **Smart Recovery**: Automatic fallback and primary stream recovery.
* **Ready to Play**: Interactive web players for every station.`
	}

	extensions := parser.CommonExtensions | parser.NoEmptyLineBeforeBlock
	p := parser.NewWithExtensions(extensions)
	doc := p.Parse([]byte(md))

	htmlFlags := html.CommonFlags | html.HrefTargetBlank
	opts := html.RendererOptions{Flags: htmlFlags}
	renderer := html.NewRenderer(opts)

	content := markdown.Render(doc, renderer)

	w.Header().Set("Content-Type", "text/html")
	data := map[string]interface{}{
		"LandingContent": template.HTML(content),
		"Config":         s.Config,
	}
	if err := s.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		if !strings.Contains(err.Error(), "broken pipe") {
			logrus.WithError(err).Error("Template error")
		}
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("sid"); err == nil {
		s.sessionsMu.Lock()
		delete(s.sessions, cookie.Value)
		s.sessionsMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "sid",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)

	if r.Method == http.MethodPost {
		u := r.FormValue("username")
		p := r.FormValue("password")

		if err := s.checkAuthLimit(host); err != nil {
			data := map[string]interface{}{"Error": err.Error(), "Config": s.Config}
			s.tmpl.ExecuteTemplate(w, "login.html", data)
			return
		}

		user, exists := s.Config.Users[u]
		if !exists || !config.CheckPasswordHash(p, user.Password) {
			s.recordAuthFailure(host)
			s.logAuthFailed(u, r.RemoteAddr, "invalid credentials")
			data := map[string]interface{}{"Error": "Invalid username or password", "Config": s.Config}
			s.tmpl.ExecuteTemplate(w, "login.html", data)
			return
		}

		s.recordAuthSuccess(host)
		// Generate Session ID
		b := make([]byte, 32)
		rand.Read(b)
		sid := hex.EncodeToString(b)

		// Generate CSRF Token
		cb := make([]byte, 32)
		rand.Read(cb)
		csrf := hex.EncodeToString(cb)

		s.sessionsMu.Lock()
		s.sessions[sid] = &session{User: user, CSRFToken: csrf}
		s.sessionsMu.Unlock()

		http.SetCookie(w, &http.Cookie{
			Name:     "sid",
			Value:    sid,
			Path:     "/",
			HttpOnly: true,
			Secure:   r.TLS != nil,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   86400 * 7, // 7 days
		})

		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	data := map[string]interface{}{"Config": s.Config}
	s.tmpl.ExecuteTemplate(w, "login.html", data)
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	allStreams := s.Relay.Snapshot()
	var streams []relay.StreamStats
	mountMap := make(map[string]bool)
	for _, st := range allStreams {
		if s.hasAccess(user, st.MountName) {
			streams = append(streams, st)
			mountMap[st.MountName] = true
		}
	}

	// Collect all possible history sources
	for m := range user.Mounts {
		mountMap[m] = true
	}
	if user.Role == config.RoleSuperAdmin {
		for m := range s.Config.Mounts {
			mountMap[m] = true
		}
		for _, rc := range s.Config.Relays {
			mountMap[rc.Mount] = true
		}
	}
	var allMounts []string
	for m := range mountMap {
		allMounts = append(allMounts, m)
	}
	sort.Strings(allMounts)

	csrf := ""
	if cookie, err := r.Cookie("sid"); err == nil {
		s.sessionsMu.RLock()
		if sess, ok := s.sessions[cookie.Value]; ok {
			csrf = sess.CSRFToken
		}
		s.sessionsMu.RUnlock()
	}

	w.Header().Set("Content-Type", "text/html")
	data := map[string]interface{}{
		"Streams":        streams,
		"Config":         s.Config,
		"User":           user,
		"Mounts":         allMounts,
		"FallbackMounts": s.Config.FallbackMounts,
		"CSRFToken":      csrf,
		"Streamers":     s.StreamerM.GetStreamers(),
	}
	if err := s.tmpl.ExecuteTemplate(w, "admin.html", data); err != nil {
		if !strings.Contains(err.Error(), "broken pipe") {
			logrus.WithError(err).Error("Template error")
		}
	}
}

func (s *Server) handleUpdateFallback(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		return
	}
	mount := r.FormValue("mount")
	fallback := r.FormValue("fallback")
	if !s.hasAccess(user, mount) {
		return
	}

	if fallback == "" {
		delete(s.Config.FallbackMounts, mount)
	} else {
		if fallback[0] != '/' {
			fallback = "/" + fallback
		}
		s.Config.FallbackMounts[mount] = fallback
	}
	s.Config.SaveConfig()
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleAddMount(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	mount, password := r.FormValue("mount"), r.FormValue("password")
	if mount == "" || password == "" {
		http.Error(w, "Missing fields", http.StatusBadRequest)
		return
	}
	if mount[0] != '/' {
		mount = "/" + mount
	}
	if !s.hasAccess(user, mount) {
		exists := false
		if _, ok := s.Config.Mounts[mount]; ok {
			exists = true
		}
		if !exists {
			for _, u := range s.Config.Users {
				if _, ok := u.Mounts[mount]; ok {
					exists = true
					break
				}
			}
		}
		if exists {
			http.Error(w, "Mount taken", http.StatusConflict)
			return
		}
	}
	hashed, _ := config.HashPassword(password)
	if user.Role == config.RoleSuperAdmin {
		s.Config.Mounts[mount] = hashed
	} else {
		user.Mounts[mount] = hashed
	}
	s.Config.SaveConfig()
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleRemoveMount(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		return
	}
	mount := r.FormValue("mount")
	if !s.hasAccess(user, mount) {
		return
	}
	delete(s.Config.Mounts, mount)
	delete(s.Config.DisabledMounts, mount)
	delete(s.Config.VisibleMounts, mount)
	delete(user.Mounts, mount)
	s.Relay.RemoveStream(mount)
	s.Config.SaveConfig()
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleToggleLatency(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		return
	}
	user, ok := s.checkAuth(r)
	if !ok || user.Role != config.RoleSuperAdmin {
		return
	}
	s.Config.LowLatencyMode = !s.Config.LowLatencyMode
	s.Relay.LowLatency = s.Config.LowLatencyMode
	s.Config.SaveConfig()
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleMetadata(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	mount, song := r.URL.Query().Get("mount"), r.URL.Query().Get("song")
	if !ok {
		_, p, okAuth := r.BasicAuth()
		if !okAuth {
			w.Header().Set("WWW-Authenticate", `Basic realm="TinyIce"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		allowed := false
		if config.CheckPasswordHash(p, s.Config.DefaultSourcePassword) || config.CheckPasswordHash(p, s.Config.Mounts[mount]) {
			allowed = true
		}
		if !allowed {
			for _, u := range s.Config.Users {
				if config.CheckPasswordHash(p, u.Mounts[mount]) {
					allowed = true
					break
				}
			}
		}
		if !allowed {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	} else {
		if !s.hasAccess(user, mount) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}
	if mount != "" && song != "" {
		if st, ok := s.Relay.GetStream(mount); ok {
			st.SetCurrentSong(song, s.Relay)
		}
	}
	fmt.Fprint(w, "<?xml version=\"1.0\"?>\n<iceresponse><message>OK</message><return>1</return></iceresponse>\n")
}

func (s *Server) handleKick(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	mount := r.FormValue("mount")
	if ok && s.hasAccess(user, mount) {
		s.Relay.RemoveStream(mount)
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleKickAllListeners(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if ok && user.Role == config.RoleSuperAdmin {
		s.Relay.DisconnectAllListeners()
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleToggleMount(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	mount := r.FormValue("mount")
	if ok && s.hasAccess(user, mount) {
		s.Config.DisabledMounts[mount] = !s.Config.DisabledMounts[mount]
		if s.Config.DisabledMounts[mount] {
			s.Relay.RemoveStream(mount)
		}
		s.Config.SaveConfig()
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleToggleVisible(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		return
	}
	mount := r.FormValue("mount")
	if ok && s.hasAccess(user, mount) {
		s.Config.VisibleMounts[mount] = !s.Config.VisibleMounts[mount]
		if st, ok := s.Relay.GetStream(mount); ok {
			st.SetVisible(s.Config.VisibleMounts[mount])
		}
		s.Config.SaveConfig()
		logrus.WithFields(logrus.Fields{"mount": mount, "visible": s.Config.VisibleMounts[mount]}).Info("Admin toggled visibility")
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleAddUser(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		return
	}
	user, ok := s.checkAuth(r)
	if ok && user.Role == config.RoleSuperAdmin {
		un, pw := r.FormValue("username"), r.FormValue("password")
		if un != "" && pw != "" {
			hp, _ := config.HashPassword(pw)
			s.Config.Users[un] = &config.User{Username: un, Password: hp, Role: config.RoleAdmin, Mounts: make(map[string]string)}
			s.Config.SaveConfig()
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleRemoveUser(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		return
	}
	user, ok := s.checkAuth(r)
	if ok && user.Role == config.RoleSuperAdmin {
		un := r.FormValue("username")
		if un != user.Username {
			delete(s.Config.Users, un)
			s.Config.SaveConfig()
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleAddBannedIP(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		return
	}
	user, ok := s.checkAuth(r)
	if ok && user.Role == config.RoleSuperAdmin {
		ip := r.FormValue("ip")
		if ip != "" {
			s.Config.BannedIPs = append(s.Config.BannedIPs, ip)
			s.Config.SaveConfig()
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleRemoveBannedIP(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		return
	}
	user, ok := s.checkAuth(r)
	if ok && user.Role == config.RoleSuperAdmin {
		ip := r.FormValue("ip")
		for i, b := range s.Config.BannedIPs {
			if b == ip {
				s.Config.BannedIPs = append(s.Config.BannedIPs[:i], s.Config.BannedIPs[i+1:]...)
				s.Config.SaveConfig()
				break
			}
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleAddRelay(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		return
	}
	user, ok := s.checkAuth(r)
	if ok && user.Role == config.RoleSuperAdmin {
		u, m, pw, bs := r.FormValue("url"), r.FormValue("mount"), r.FormValue("password"), r.FormValue("burst_size")
		if u != "" && m != "" {
			if m[0] != '/' {
				m = "/" + m
			}
			burst := 20
			fmt.Sscanf(bs, "%d", &burst)

			found := false
			for _, rc := range s.Config.Relays {
				if rc.Mount == m {
					rc.URL = u
					rc.Password = pw
					rc.BurstSize = burst
					found = true
					break
				}
			}

			if !found {
				rc := &config.RelayConfig{URL: u, Mount: m, Password: pw, BurstSize: burst, Enabled: true}
				s.Config.Relays = append(s.Config.Relays, rc)
			}

			s.Config.SaveConfig()
			s.RelayM.StartRelay(u, m, pw, burst, s.Config.VisibleMounts[m])
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleToggleRelay(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		return
	}
	user, ok := s.checkAuth(r)
	mount := r.FormValue("mount")
	if ok && user.Role == config.RoleSuperAdmin {
		for _, rc := range s.Config.Relays {
			if rc.Mount == mount {
				rc.Enabled = !rc.Enabled
				if rc.Enabled {
					s.RelayM.StartRelay(rc.URL, rc.Mount, rc.Password, rc.BurstSize, s.Config.VisibleMounts[mount])
				} else {
					s.RelayM.StopRelay(mount)
				}
				s.Config.SaveConfig()
				break
			}
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleDeleteRelay(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		return
	}
	user, ok := s.checkAuth(r)
	mount := r.FormValue("mount")
	if ok && user.Role == config.RoleSuperAdmin {
		for i, rc := range s.Config.Relays {
			if rc.Mount == mount {
				s.Config.Relays = append(s.Config.Relays[:i], s.Config.Relays[i+1:]...)
				s.Config.SaveConfig()
				s.RelayM.StopRelay(mount)
				break
			}
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleRestartRelay(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		return
	}
	user, ok := s.checkAuth(r)
	mount := r.FormValue("mount")
	if ok && user.Role == config.RoleSuperAdmin {
		for _, rc := range s.Config.Relays {
			if rc.Mount == mount {
				s.RelayM.StartRelay(rc.URL, rc.Mount, rc.Password, rc.BurstSize, s.Config.VisibleMounts[mount])
				break
			}
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	mount := r.URL.Query().Get("mount")
	history := s.Relay.History.Get(mount)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

func (s *Server) handleLegacyStats(w http.ResponseWriter, r *http.Request) {
	streams := s.Relay.Snapshot()
	type IcecastSource struct {
		AudioInfo         string      `json:"audio_info"`
		Bitrate           interface{} `json:"bitrate"`
		Genre             string      `json:"genre"`
		Listeners         int         `json:"listeners"`
		ListenURL         string      `json:"listenurl"`
		Mount             string      `json:"mount"`
		ServerDescription string      `json:"server_description"`
		ServerName        string      `json:"server_name"`
		ServerType        string      `json:"server_type"`
		StreamStart       string      `json:"stream_start"`
		Title             string      `json:"title"`
		Dummy             interface{} `json:"dummy"`
	}
	sources := make([]IcecastSource, len(streams))
	host := s.Config.HostName
	if !strings.Contains(host, ":") {
		host = host + ":" + s.Config.Port
	}
	proto := "http://"
	if s.Config.UseHTTPS {
		proto = "https://"
	}
	for i, st := range streams {
		sources[i] = IcecastSource{AudioInfo: fmt.Sprintf("bitrate=%s", st.Bitrate), Bitrate: st.Bitrate, Genre: st.Genre, Listeners: st.ListenersCount, ListenURL: proto + host + st.MountName, Mount: st.MountName, ServerDescription: st.Description, ServerName: st.Name, ServerType: st.ContentType, StreamStart: st.Started.Format(time.RFC1123), Title: st.CurrentSong, Dummy: nil}
	}
	resp := map[string]interface{}{"icestats": map[string]interface{}{"admin": s.Config.AdminEmail, "host": s.Config.HostName, "location": s.Config.Location, "server_id": "Icecast 2.4.4 (TinyIce)", "server_start": time.Now().Format(time.RFC1123), "source": sources}}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	bi, bo := s.Relay.GetMetrics()
	streams := s.Relay.Snapshot()
	tl := 0
	for _, st := range streams {
		tl += st.ListenersCount
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "# HELP tinyice_bandwidth_in_bytes_total Total bytes received\n# TYPE tinyice_bandwidth_in_bytes_total counter\ntinyice_bandwidth_in_bytes_total %d\n\n", bi)
	fmt.Fprintf(w, "# HELP tinyice_bandwidth_out_bytes_total Total bytes sent\n# TYPE tinyice_bandwidth_out_bytes_total counter\ntinyice_bandwidth_out_bytes_total %d\n\n", bo)
	fmt.Fprintf(w, "# HELP tinyice_listeners_total Total active listeners\n# TYPE tinyice_listeners_total gauge\ntinyice_listeners_total %d\n\n", tl)
	fmt.Fprintf(w, "# HELP tinyice_sources_total Total active sources\n# TYPE tinyice_sources_total gauge\ntinyice_sources_total %d\n\n", len(streams))
	for _, st := range streams {
		fmt.Fprintf(w, "tinyice_mount_listeners_current{mount=\"%s\",name=\"%s\"} %d\n", st.MountName, st.Name, st.ListenersCount)
	}
}

func (s *Server) handlePublicEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	flusher, _ := w.(http.Flusher)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	type PublicStreamInfo struct {
		Mount       string `json:"mount"`
		Name        string `json:"name"`
		Listeners   int    `json:"listeners"`
		Bitrate     string `json:"bitrate"`
		Uptime      string `json:"uptime"`
		Genre       string `json:"genre"`
		Description string `json:"description"`
		CurrentSong string `json:"song"`
	}
	send := func() error {
		allStreams := s.Relay.Snapshot()
		var info []PublicStreamInfo
		for _, st := range allStreams {
			if st.Visible {
				info = append(info, PublicStreamInfo{Mount: st.MountName, Name: st.Name, Listeners: st.ListenersCount, Bitrate: st.Bitrate, Uptime: st.Uptime, Genre: st.Genre, Description: st.Description, CurrentSong: st.CurrentSong})
			}
		}
		payload, _ := json.Marshal(info)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	if err := send(); err != nil {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if err := send(); err != nil {
				return
			}
		}
	}
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	bi, bo := s.Relay.GetMetrics()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"bytes_in": bi, "bytes_out": bo})
}

func (s *Server) directoryReportingTask() {
	ticker := time.NewTicker(3 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		streams := s.Relay.Snapshot()
		for _, st := range streams {
			if st.Public {
				s.reportToDirectory(st)
			}
		}
	}
}

func (s *Server) statsRecordingTask() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		if s.Relay.History == nil {
			continue
		}
		streams := s.Relay.Snapshot()
		for _, st := range streams {
			s.Relay.History.RecordStats(st.MountName, st.ListenersCount, st.BytesIn, st.BytesOut)
		}
	}
}

func (s *Server) reportToDirectory(st relay.StreamStats) {
	proto := "http://"
	if s.Config.UseHTTPS {
		proto = "https://"
	}
	listenURL := proto + s.Config.HostName + ":" + s.Config.Port + st.MountName
	if s.Config.UseHTTPS {
		listenURL = proto + s.Config.HostName + ":" + s.Config.HTTPSPort + st.MountName
	}
	data := url.Values{}
	data.Set("action", "add")
	data.Set("sn", st.Name)
	data.Set("genre", st.Genre)
	data.Set("cps", st.Bitrate)
	data.Set("url", st.URL)
	data.Set("desc", st.Description)
	data.Set("st", st.ContentType)
	data.Set("listenurl", listenURL)
	data.Set("type", "audio/mpeg")
	resp, err := http.PostForm(s.Config.DirectoryServer, data)
	if err != nil {
		logrus.WithError(err).Warn("Failed to report to directory server")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		logrus.WithField("status", resp.Status).Warn("Directory server rejected update")
	} else {
		logrus.WithField("mount", st.MountName).Debug("Reported to directory server")
	}
}

type streamEventInfo struct {
	Mount        string  `json:"mount"`
	Name         string  `json:"name"`
	Listeners    int     `json:"listeners"`
	Bitrate      string  `json:"bitrate"`
	Uptime       string  `json:"uptime"`
	ContentType  string  `json:"type"`
	SourceIP     string  `json:"ip"`
	BytesIn      int64   `json:"bytes_in"`
	BytesOut     int64   `json:"bytes_out"`
	BytesDropped int64   `json:"bytes_dropped"`
	CurrentSong  string  `json:"song"`
	Health       float64 `json:"health"`
	IsTranscoded bool    `json:"is_transcoded"`
}

type relayEventInfo struct {
	URL     string `json:"url"`
	Mount   string `json:"mount"`
	Active  bool   `json:"active"`
	Enabled bool   `json:"enabled"`
}

type streamerEventInfo struct {
	Name         string   `json:"name"`
	Mount        string   `json:"mount"`
	State        int      `json:"state"`
	CurrentSong  string   `json:"song"`
	StartTime    int64    `json:"start_time"`
	Duration     float64  `json:"duration"`
	PlaylistPos  int      `json:"playlist_pos"`
	PlaylistLen  int      `json:"playlist_len"`
	Shuffle      bool     `json:"shuffle"`
}

func (s *Server) collectStatsPayload(user *config.User) ([]byte, error) {
	bi, bo := s.Relay.GetMetrics()
	allStreams := s.Relay.Snapshot()
	tl := 0
	var info []streamEventInfo
	tr, ts := 0, 0
	for _, st := range allStreams {
		if s.hasAccess(user, st.MountName) {
			lc := st.ListenersCount
			tl += lc
			info = append(info, streamEventInfo{
				Mount: st.MountName, Name: st.Name, Listeners: lc, Bitrate: st.Bitrate,
				Uptime: st.Uptime, ContentType: st.ContentType, SourceIP: st.SourceIP,
				BytesIn: st.BytesIn, BytesOut: st.BytesOut, BytesDropped: st.BytesDropped,
				CurrentSong: st.CurrentSong, Health: st.Health, IsTranscoded: st.IsTranscoded,
			})
			if st.SourceIP == "relay-pull" {
				tr++
			} else {
				ts++
			}
		}
	}
	if user.Role != config.RoleSuperAdmin {
		var ubi, ubo int64
		for _, st := range info {
			ubi += st.BytesIn
			ubo += st.BytesOut
		}
		bi, bo = ubi, ubo
	}

	relays := make([]relayEventInfo, len(s.Config.Relays))
	for i, rc := range s.Config.Relays {
		relays[i] = relayEventInfo{URL: rc.URL, Mount: rc.Mount, Active: false, Enabled: rc.Enabled}
		if st, ok := s.Relay.GetStream(rc.Mount); ok && st.SourceIP == "relay-pull" {
			relays[i].Active = true
		}
	}

	activeStreamers := s.StreamerM.GetStreamers()
	streamers := make([]streamerEventInfo, 0, len(activeStreamers))
	for _, st := range activeStreamers {
		if s.hasAccess(user, st.OutputMount) {
			stats := st.GetStats()
			streamers = append(streamers, streamerEventInfo{
				Name:        stats.Name,
				Mount:       stats.Mount,
				State:       int(stats.State),
				CurrentSong: stats.CurrentSong,
				StartTime:   stats.StartTime.Unix(),
				Duration:    stats.Duration.Seconds(),
				PlaylistPos: stats.PlaylistPos,
				PlaylistLen: stats.PlaylistLen,
				Shuffle:     stats.Shuffle,
			})
		}
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	totalDropped := int64(0)
	for _, st := range allStreams {
		totalDropped += st.BytesDropped
	}

	return json.Marshal(map[string]interface{}{
		"bytes_in":        bi,
		"bytes_out":       bo,
		"total_listeners": tl,
		"total_sources":   len(info),
		"total_relays":    tr,
		"total_streamers": ts,
		"streams":         info,
		"relays":          relays,
		"streamers":       streamers,
		"visible_mounts":  s.Config.VisibleMounts,
		"sys_ram":         m.Sys,
		"heap_alloc":      m.HeapAlloc,
		"stack_sys":       m.StackSys,
		"num_gc":          m.NumGC,
		"goroutines":      runtime.NumGoroutine(),
		"total_dropped":   totalDropped,
		"server_uptime":   time.Since(s.startTime).Round(time.Second).String(),
	})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	send := func() error {
		payload, err := s.collectStatsPayload(user)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	if err := send(); err != nil {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if err := send(); err != nil {
				return
			}
		}
	}
}

func (s *Server) handleGetSecurityStats(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	type ipStat struct {
		IP        string `json:"ip"`
		Count     int    `json:"count"`
		Locked    bool   `json:"locked"`
		ExpiresIn string `json:"expires_in"`
	}

	authStats := []ipStat{}
	s.authAttemptsMu.Lock()
	for ip, att := range s.authAttempts {
		expires := "0s"
		locked := time.Now().Before(att.LockoutBy)
		if locked {
			expires = time.Until(att.LockoutBy).Round(time.Second).String()
		}
		authStats = append(authStats, ipStat{ip, att.Count, locked, expires})
	}
	s.authAttemptsMu.Unlock()

	scanStats := []ipStat{}
	s.scanAttemptsMu.Lock()
	for ip, att := range s.scanAttempts {
		expires := "0s"
		locked := time.Now().Before(att.LockoutBy)
		if locked {
			expires = time.Until(att.LockoutBy).Round(time.Second).String()
		}
		scanStats = append(scanStats, ipStat{ip, att.Count, locked, expires})
	}
	s.scanAttemptsMu.Unlock()

	// Sort by count desc
	sort.Slice(authStats, func(i, j int) bool { return authStats[i].Count > authStats[j].Count })
	sort.Slice(scanStats, func(i, j int) bool { return scanStats[i].Count > scanStats[j].Count })

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"auth_fails": authStats,
		"scanners":   scanStats,
	})
}

func (s *Server) handleClearAuthLockout(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}
	ip := r.FormValue("ip")
	s.authAttemptsMu.Lock()
	delete(s.authAttempts, ip)
	s.authAttemptsMu.Unlock()
	http.Redirect(w, r, "/admin#tab-security", http.StatusSeeOther)
}

func (s *Server) handleClearScanLockout(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}
	ip := r.FormValue("ip")
	s.scanAttemptsMu.Lock()
	delete(s.scanAttempts, ip)
	s.scanAttemptsMu.Unlock()
	http.Redirect(w, r, "/admin#tab-security", http.StatusSeeOther)
}

func (s *Server) handleAddWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	url := r.FormValue("url")
	events := r.Form["events"]

	if url == "" || len(events) == 0 {
		http.Error(w, "URL and at least one event required", http.StatusBadRequest)
		return
	}

	s.Config.Webhooks = append(s.Config.Webhooks, &config.WebhookConfig{
		URL:     url,
		Events:  events,
		Enabled: true,
	})
	s.Config.SaveConfig()

	http.Redirect(w, r, "/admin#tab-webhooks", http.StatusSeeOther)
}

func (s *Server) handleDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	url := r.FormValue("url")
	newWHs := []*config.WebhookConfig{}
	for _, wh := range s.Config.Webhooks {
		if wh.URL != url {
			newWHs = append(newWHs, wh)
		}
	}
	s.Config.Webhooks = newWHs
	s.Config.SaveConfig()

	http.Redirect(w, r, "/admin#tab-webhooks", http.StatusSeeOther)
}

func (s *Server) handlePlayerToggle(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	streamer.TogglePlay()

	http.Redirect(w, r, "/admin#tab-streamer", http.StatusSeeOther)
}

func (s *Server) handlePlayerScan(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	if err := streamer.ScanMusicDir(); err != nil {
		logrus.WithError(err).Error("Failed to scan music directory")
	}

	// Persist
	playlistCopy := streamer.GetPlaylist()
	for _, adj := range s.Config.AutoDJs {
		if adj.Mount == mount {
			adj.Playlist = playlistCopy
			s.Config.SaveConfig()
			break
		}
	}

	http.Redirect(w, r, "/admin#tab-streamer", http.StatusSeeOther)
}

func (s *Server) handleAddAutoDJ(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	name := r.FormValue("name")
	mount := r.FormValue("mount")
	musicDir := r.FormValue("music_dir")
	format := r.FormValue("format")
	bitrateStr := r.FormValue("bitrate")
	loop := r.FormValue("loop") == "on"
	injectMetadata := r.FormValue("inject_metadata") == "on"
	mpdEnabled := r.FormValue("mpd_enabled") == "on"
	mpdPort := r.FormValue("mpd_port")
	mpdPassword := r.FormValue("mpd_password")

	if name == "" || mount == "" || musicDir == "" {
		http.Error(w, "Name, mount, and music directory are required", http.StatusBadRequest)
		return
	}

	bitrate := 128
	if bitrateStr != "" {
		fmt.Sscanf(bitrateStr, "%d", &bitrate)
	}
	if format == "" {
		format = "mp3"
	}

	if mount[0] != '/' {
		mount = "/" + mount
	}

	adj := &config.AutoDJConfig{
		Name:           name,
		Mount:          mount,
		MusicDir:       musicDir,
		Format:         format,
		Bitrate:        bitrate,
		Enabled:        true,
		Loop:           loop,
		InjectMetadata: injectMetadata,
		MPDEnabled:     mpdEnabled,
		MPDPort:        mpdPort,
		MPDPassword:    mpdPassword,
	}

	s.Config.AutoDJs = append(s.Config.AutoDJs, adj)
	s.Config.SaveConfig()

	// Start it immediately
	streamer, err := s.StreamerM.StartStreamer(adj.Name, adj.Mount, adj.MusicDir, adj.Loop, adj.Format, adj.Bitrate, adj.InjectMetadata, nil, adj.MPDEnabled, adj.MPDPort, adj.MPDPassword)
	if err == nil {
		if adj.InjectMetadata {
			if st, ok := s.Relay.GetStream(adj.Mount); ok {
				st.SetVisible(true)
			}
		}
		streamer.ScanMusicDir()
		streamer.Play()
	}

	http.Redirect(w, r, "/admin#tab-streamer", http.StatusSeeOther)
}

func (s *Server) handleDeleteAutoDJ(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	newADJs := []*config.AutoDJConfig{}
	for _, adj := range s.Config.AutoDJs {
		if adj.Mount != mount {
			newADJs = append(newADJs, adj)
		} else {
			s.StreamerM.StopStreamer(mount)
		}
	}
	s.Config.AutoDJs = newADJs
	s.Config.SaveConfig()

	http.Redirect(w, r, "/admin#tab-streamer", http.StatusSeeOther)
}

func (s *Server) handleToggleAutoDJ(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	found := false
	existing := s.StreamerM.GetStreamer(mount)

	for _, adj := range s.Config.AutoDJs {
		if adj.Mount == mount {
			// If it's enabled in config but not running, start it.
			// Otherwise toggle it.
			if adj.Enabled && existing == nil {
				// Just start it
			} else {
				adj.Enabled = !adj.Enabled
			}

			if adj.Enabled {
				if existing == nil {
					streamer, err := s.StreamerM.StartStreamer(adj.Name, adj.Mount, adj.MusicDir, adj.Loop, adj.Format, adj.Bitrate, adj.InjectMetadata, adj.Playlist, adj.MPDEnabled, adj.MPDPort, adj.MPDPassword)
					if err == nil {
						if adj.InjectMetadata {
							if st, ok := s.Relay.GetStream(adj.Mount); ok {
								st.SetVisible(true)
							}
						}
						if len(adj.Playlist) == 0 {
							streamer.ScanMusicDir()
						}
						streamer.Play()
					}
				} else {
					existing.Play()
				}
			} else {
				s.StreamerM.StopStreamer(mount)
			}
			found = true
			break
		}
	}

	if !found {
		if existing != nil {
			s.StreamerM.StopStreamer(mount)
		}
	}

	s.Config.SaveConfig()
	http.Redirect(w, r, "/admin#tab-streamer", http.StatusSeeOther)
}

func (s *Server) handlePlayerReorder(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	fromStr := r.FormValue("from")
	toStr := r.FormValue("to")

	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	var from, to int
	fmt.Sscanf(fromStr, "%d", &from)
	fmt.Sscanf(toStr, "%d", &to)

	streamer.MovePlaylistItem(from, to)

	// Persist
	playlistCopy := streamer.GetPlaylist()
	for _, adj := range s.Config.AutoDJs {
		if adj.Mount == mount {
			adj.Playlist = playlistCopy
			s.Config.SaveConfig()
			break
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handlePlayerQueue(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	path := r.FormValue("path")
	action := r.FormValue("action") // "add" or "remove"

	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	if action == "add" {
		streamer.PushToQueue(path)
	} else if action == "remove" {
		var index int
		fmt.Sscanf(r.FormValue("index"), "%d", &index)
		streamer.RemoveFromQueue(index)
	}

	http.Redirect(w, r, "/admin#tab-streamer", http.StatusSeeOther)
}

func (s *Server) handlePlayerShuffle(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	streamer.ToggleShuffle()
	http.Redirect(w, r, "/admin#tab-streamer", http.StatusSeeOther)
}

func (s *Server) handlePlayerNext(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	streamer.Next()
	http.Redirect(w, r, "/admin#tab-streamer", http.StatusSeeOther)
}

func (s *Server) handlePlayerFiles(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	mount := r.URL.Query().Get("mount")
	subDir := r.URL.Query().Get("path")
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	musicDir := streamer.GetMusicDir()
	fullPath := filepath.Join(musicDir, subDir)

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type fileEntry struct {
		Name  string `json:"name"`
		IsDir bool   `json:"is_dir"`
		Path  string `json:"path"`
	}
	var res []fileEntry
	for _, f := range entries {
		ext := strings.ToLower(filepath.Ext(f.Name()))
		if f.IsDir() || ext == ".mp3" {
			res = append(res, fileEntry{
				Name:  f.Name(),
				IsDir: f.IsDir(),
				Path:  filepath.Join(subDir, f.Name()),
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func (s *Server) handlePlayerPlaylistAction(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	action := r.FormValue("action")
	relPath := r.FormValue("file")

	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	if action == "add" {
		fullPath := filepath.Join(streamer.GetMusicDir(), relPath)
		info, err := os.Stat(fullPath)
		if err == nil {
			if info.IsDir() {
				// Add folder recursively
				filepath.Walk(fullPath, func(p string, i os.FileInfo, e error) error {
					if e == nil && !i.IsDir() && strings.ToLower(filepath.Ext(p)) == ".mp3" {
						abs, _ := filepath.Abs(p)
						streamer.AddToPlaylist(abs)
					}
					return nil
				})
			} else {
				abs, _ := filepath.Abs(fullPath)
				streamer.AddToPlaylist(abs)
			}
		}
	} else if action == "remove" {
		var idx int
		fmt.Sscanf(r.FormValue("index"), "%d", &idx)
		streamer.RemoveFromPlaylist(idx)
	}
	playlistCopy := streamer.GetPlaylist()

	// Persist
	for _, adj := range s.Config.AutoDJs {
		if adj.Mount == mount {
			adj.Playlist = playlistCopy
			s.Config.SaveConfig()
			break
		}
	}

	http.Redirect(w, r, "/admin#tab-streamer", http.StatusSeeOther)
}

func (s *Server) handleUpdateAutoDJ(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	oldMount := r.FormValue("old_mount")
	name := r.FormValue("name")
	newMount := r.FormValue("mount")
	musicDir := r.FormValue("music_dir")
	format := r.FormValue("format")
	bitrateStr := r.FormValue("bitrate")
	loop := r.FormValue("loop") == "on"
	injectMetadata := r.FormValue("inject_metadata") == "on"
	mpdEnabled := r.FormValue("mpd_enabled") == "on"
	mpdPort := r.FormValue("mpd_port")
	mpdPassword := r.FormValue("mpd_password")

	if name == "" || newMount == "" || musicDir == "" {
		http.Error(w, "Name, mount, and music directory are required", http.StatusBadRequest)
		return
	}

	if newMount[0] != '/' {
		newMount = "/" + newMount
	}

	bitrate := 128
	fmt.Sscanf(bitrateStr, "%d", &bitrate)

	for _, adj := range s.Config.AutoDJs {
		if adj.Mount == oldMount {
			adj.Name = name
			adj.Mount = newMount
			adj.MusicDir = musicDir
			adj.Format = format
			adj.Bitrate = bitrate
			adj.Loop = loop
			adj.InjectMetadata = injectMetadata
			adj.MPDEnabled = mpdEnabled
			adj.MPDPort = mpdPort
			adj.MPDPassword = mpdPassword

			// Restart streamer
			s.StreamerM.StopStreamer(oldMount)
			if adj.Enabled {
				streamer, err := s.StreamerM.StartStreamer(adj.Name, adj.Mount, adj.MusicDir, adj.Loop, adj.Format, adj.Bitrate, adj.InjectMetadata, adj.Playlist, adj.MPDEnabled, adj.MPDPort, adj.MPDPassword)
				if err == nil {
					streamer.Play()
				}
			}
			break
		}
	}

	s.Config.SaveConfig()
	http.Redirect(w, r, "/admin#tab-streamer", http.StatusSeeOther)
}

func (s *Server) handleGetStats(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	allStreams := s.Relay.Snapshot()
	sort.Slice(allStreams, func(i, j int) bool {
		return allStreams[i].ListenersCount > allStreams[j].ListenersCount
	})

	topStreams := allStreams
	if len(topStreams) > 10 {
		topStreams = topStreams[:10]
	}

	topListenersUA := []relay.UAStat{}
	topSourcesUA := []relay.UAStat{}
	if s.Relay.History != nil {
		if uas := s.Relay.History.GetTopUAs("listener", 10); uas != nil {
			topListenersUA = uas
		}
		if uas := s.Relay.History.GetTopUAs("source", 10); uas != nil {
			topSourcesUA = uas
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"top_streams":      topStreams,
		"top_listeners_ua": topListenersUA,
		"top_sources_ua":   topSourcesUA,
	})
}

func (s *Server) handleWebRTCOffer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	mount := r.URL.Query().Get("mount")
	if mount == "" {
		http.Error(w, "mount query param required", http.StatusBadRequest)
		return
	}

	var offer webrtc.SessionDescription
	if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	answer, err := s.WebRTCM.HandleOffer(mount, offer)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(answer)
}

func (s *Server) handlePlayer(w http.ResponseWriter, r *http.Request) {
	mount := strings.TrimPrefix(r.URL.Path, "/player")
	if mount == "" || mount == "/" {
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		return
	}

	stream, ok := s.Relay.GetStream(mount)
	if !ok {
		// Check for fallback as well to allow player to work during fallback
		fallback, hasFallback := s.Config.FallbackMounts[mount]
		if hasFallback {
			stream, ok = s.Relay.GetStream(fallback)
		}
	}

	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	data := map[string]interface{}{
		"Stream": stream.Snapshot(),
		"Config": s.Config,
	}
	if err := s.tmpl.ExecuteTemplate(w, "player.html", data); err != nil {
		logrus.WithError(err).Error("Template error")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func (s *Server) handleWebRTCPlayer(w http.ResponseWriter, r *http.Request) {
	mount := strings.TrimPrefix(r.URL.Path, "/player-webrtc")
	if mount == "" || mount == "/" {
		http.Redirect(w, r, "/explore", http.StatusSeeOther)
		return
	}

	stream, ok := s.Relay.GetStream(mount)
	if !ok {
		http.Error(w, "Stream not found", http.StatusNotFound)
		return
	}

	data := map[string]interface{}{
		"Stream": stream,
		"Config": s.Config,
	}
	if err := s.tmpl.ExecuteTemplate(w, "webrtc_player.html", data); err != nil {
		logrus.WithError(err).Error("Template error")
	}
}

func (s *Server) handleEmbed(w http.ResponseWriter, r *http.Request) {
	mount := strings.TrimPrefix(r.URL.Path, "/embed")
	if mount == "" || mount == "/" {
		http.NotFound(w, r)
		return
	}

	stream, ok := s.Relay.GetStream(mount)
	if !ok {
		fallback, hasFallback := s.Config.FallbackMounts[mount]
		if hasFallback {
			stream, ok = s.Relay.GetStream(fallback)
		}
	}

	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("X-Frame-Options", "ALLOWALL") // Allow embedding
	data := map[string]interface{}{
		"Stream": stream.Snapshot(),
		"Config": s.Config,
	}
	if err := s.tmpl.ExecuteTemplate(w, "embed.html", data); err != nil {
		logrus.WithError(err).Error("Template error")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func (s *Server) handleExplore(w http.ResponseWriter, r *http.Request) {
	allStreams := s.Relay.Snapshot()
	// Filter only visible streams and sort by listeners (descending)
	var visibleStreams []relay.StreamStats
	for _, st := range allStreams {
		if st.Visible {
			visibleStreams = append(visibleStreams, st)
		}
	}
	sort.Slice(visibleStreams, func(i, j int) bool {
		return visibleStreams[i].ListenersCount > visibleStreams[j].ListenersCount
	})

	w.Header().Set("Content-Type", "text/html")
	data := map[string]interface{}{
		"Streams": visibleStreams,
		"Config":  s.Config,
	}
	if err := s.tmpl.ExecuteTemplate(w, "explore.html", data); err != nil {
		logrus.WithError(err).Error("Template error")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func (s *Server) handleAddTranscoder(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	name := r.FormValue("name")
	input := r.FormValue("input")
	output := r.FormValue("output")
	format := r.FormValue("format")
	var bitrate int
	fmt.Sscanf(r.FormValue("bitrate"), "%d", &bitrate)

	tc := &config.TranscoderConfig{
		Name:        name,
		InputMount:  input,
		OutputMount: output,
		Format:      format,
		Bitrate:     bitrate,
		Enabled:     true,
	}

	s.Config.Transcoders = append(s.Config.Transcoders, tc)
	s.Config.SaveConfig()
	s.TranscoderM.StartTranscoder(tc)

	http.Redirect(w, r, "/admin#tab-transcoding", http.StatusSeeOther)
}

func (s *Server) handleToggleTranscoder(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	name := r.FormValue("name")
	for _, tc := range s.Config.Transcoders {
		if tc.Name == name {
			tc.Enabled = !tc.Enabled
			if tc.Enabled {
				s.TranscoderM.StartTranscoder(tc)
			} else {
				s.TranscoderM.StopTranscoder(tc.OutputMount)
			}
			break
		}
	}
	s.Config.SaveConfig()
	http.Redirect(w, r, "/admin#tab-transcoding", http.StatusSeeOther)
}

func (s *Server) handleDeleteTranscoder(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	name := r.FormValue("name")
	newTCs := []*config.TranscoderConfig{}
	for _, tc := range s.Config.Transcoders {
		if tc.Name != name {
			newTCs = append(newTCs, tc)
		} else {
			s.TranscoderM.StopTranscoder(tc.OutputMount)
		}
	}
	s.Config.Transcoders = newTCs
	s.Config.SaveConfig()
	http.Redirect(w, r, "/admin#tab-transcoding", http.StatusSeeOther)
}

func (s *Server) handleTranscoderStats(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var stats []relay.TranscoderStats
	for _, tc := range s.Config.Transcoders {
		inst := s.TranscoderM.GetInstance(tc.OutputMount)
		uptime := "OFF"
		var frames, bytes int64
		active := false
		if inst != nil {
			active = true
			uptime = time.Since(inst.StartTime).Round(time.Second).String()
			frames = atomic.LoadInt64(&inst.FramesProcessed)
			bytes = atomic.LoadInt64(&inst.BytesEncoded)
		}
		stats = append(stats, relay.TranscoderStats{
			Name:            tc.Name,
			Input:           tc.InputMount,
			Output:          tc.OutputMount,
			Format:          tc.Format,
			Bitrate:         tc.Bitrate,
			Active:          active,
			FramesProcessed: frames,
			BytesEncoded:    bytes,
			Uptime:          uptime,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func (s *Server) handleInsights(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if s.Relay.History == nil {
		http.Error(w, "History disabled", http.StatusServiceUnavailable)
		return
	}

	// Default to last 24 hours
	stats := s.Relay.History.GetAllHistoricalStats(24 * time.Hour)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}
