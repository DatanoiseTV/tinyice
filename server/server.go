package server

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
	"github.com/DatanoiseTV/tinyice/relay"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"go.uber.org/zap"
	"golang.org/x/crypto/acme/autocert"
)

//go:embed all:templates
var templateFS embed.FS

//go:embed all:assets
var assetFS embed.FS

// Server is the main HTTP server and application coordinator for TinyIce.
//
// The Server handles all HTTP requests, manages WebSocket connections, serves
// the web interface, and coordinates between the various subsystems (relay,
// transcoders, streamers, etc.).
//
// Key responsibilities:
//   - HTTP request routing and handling
//   - Web interface rendering (templates, assets)
//   - WebSocket connections for real-time updates
//   - Source client authentication and authorization
//   - Listener connection management
//   - Admin interface and API endpoints
//
// Lifecycle:
//   - Created with NewServer()
//   - Configured with routes and middleware
//   - Started with Start()
//   - Stopped gracefully with Stop()
//
// Thread Safety:
// The Server is designed to be thread-safe. HTTP handlers are called from
// multiple goroutines concurrently, so all handler methods must be safe
// for concurrent access.
type Server struct {
	Config      *config.Config           // Application configuration
	Relay       *relay.Relay             // Core relay/streaming engine
	RelayM      *relay.RelayManager      // Relay stream management
	TranscoderM *relay.TranscoderManager // Transcoding management
	HealthM     *relay.HealthMonitor      // Stream health monitoring
	WebRTCM     *relay.WebRTCManager     // WebRTC connection management
	StreamerM   *relay.StreamerManager   // AutoDJ/streamer management
	RTMP        *relay.RTMPServer         // RTMP ingest server (optional)
	SRT         *relay.SRTServer          // SRT ingest server (optional)
	TenantM     *relay.TenantManager      // Multi-tenant management
	mpdServer   *relay.MPDServer         // MPD protocol server (optional)
	tmpl        *template.Template       // HTML template for web interface (legacy)
	shell       *ShellRenderer           // New Preact frontend renderer
	Version     string                   // TinyIce version
	Commit      string                   // Git commit hash
	httpServers []*http.Server           // Active HTTP servers
	startTime   time.Time                // Server start time
	AuthLog     *zap.SugaredLogger

	sessions   map[string]*session
	sessionsMu sync.RWMutex

	authAttempts   map[string]*authAttempt
	authAttemptsMu sync.Mutex

	certManager *autocert.Manager

	scanAttempts   map[string]*scanAttempt
	scanAttemptsMu sync.Mutex

	hlsOutputs map[string]*relay.HLSOutput
	hlsMu      sync.RWMutex
	hlsCtx     context.Context
	hlsCancel  context.CancelFunc

	// Per-mount poster JPEG, captured client-side and uploaded once per
	// session. Kept in memory only — restart drops them and the next
	// viewer refills them. Protected by posterMu.
	posters  map[string][]byte
	posterMu sync.RWMutex

	done chan struct{}

	configMu   sync.Mutex // protects config writes
	setupToken string     // single-use token for first-run setup (empty = setup complete)

	webAuthn         *webauthn.WebAuthn
	webauthnSessions map[string]*webauthn.SessionData
	webauthnMu       sync.Mutex

	tokenSaveTimer *time.Timer
	tokenSaveMu    sync.Mutex

	// GeoIP — looks up listener IPs against the embedded DB-IP
	// country-lite database; auto-downloads + monthly-refreshes.
	GeoIP *relay.GeoLookup

	// GeoTracker — keeps a live (country, mount) listener count
	// updated by handleListener for the dashboard map.
	GeoTracker *GeoTracker

}

func NewServer(cfg *config.Config, authLog *zap.SugaredLogger, version, commit, setupToken string) *Server {
	tmpl := template.New("base")
	tmpl, err := tmpl.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		logger.L.Fatalf("Error loading embedded templates: %v", err)
	}

	hm, err := relay.NewHistoryManager("history.db")
	if err != nil {
		logger.L.Fatalf("Failed to initialize history manager: %v", err)
	}

	r := relay.NewRelay(cfg.LowLatencyMode, hm)

	// Initialize WebAuthn
	var wa *webauthn.WebAuthn
	rpID := "localhost"
	rpName := "TinyIce"
	rpOrigins := []string{"http://localhost:8000"}
	if cfg.WebAuthn != nil {
		if cfg.WebAuthn.RPID != "" {
			rpID = cfg.WebAuthn.RPID
		}
		if cfg.WebAuthn.RPName != "" {
			rpName = cfg.WebAuthn.RPName
		}
		if len(cfg.WebAuthn.RPOrigins) > 0 {
			rpOrigins = cfg.WebAuthn.RPOrigins
		}
	} else if cfg.BaseURL != "" {
		if u, err := url.Parse(cfg.BaseURL); err == nil {
			rpID = u.Hostname()
			rpOrigins = []string{cfg.BaseURL}
		}
	}
	wa, _ = webauthn.New(&webauthn.Config{
		RPID:                  rpID,
		RPDisplayName:         rpName,
		RPOrigins:             rpOrigins,
		AttestationPreference: protocol.PreferNoAttestation,
	})

	// Default the health monitor to auto-remove streams that have been
	// silent for 2 minutes — without this the feature exists but is
	// dormant and dead sources linger forever in the admin UI / status.
	healthM := relay.NewHealthMonitor(r).WithAutoRemove(2 * time.Minute)
	healthM.OnEvent(func(e relay.StreamHealthEvent) {
		logger.L.Infow("Stream health event",
			"mount", e.Mount,
			"old_status", e.OldStatus.String(),
			"new_status", e.NewStatus.String(),
		)
	})
	hlsCtx, hlsCancel := context.WithCancel(context.Background())
	srv := &Server{
		Config:       cfg,
		Relay:        r,
		HealthM:      healthM,
		RelayM:       relay.NewRelayManager(r),
		TranscoderM:  relay.NewTranscoderManager(r),
		WebRTCM:      relay.NewWebRTCManager(r),
		StreamerM:    relay.NewStreamerManager(r, cfg),
		RTMP:         relay.NewRTMPServer(r, cfg),
		SRT:          relay.NewSRTServer(r, cfg),
		TenantM:      relay.NewTenantManager(),
		tmpl:         tmpl,
		shell:        NewShellRenderer(),
		Version:      version,
		Commit:       commit,
		startTime:    time.Now(),
		AuthLog:      authLog,
		sessions:     make(map[string]*session),
		authAttempts: make(map[string]*authAttempt),
		scanAttempts: make(map[string]*scanAttempt),
		hlsOutputs:       make(map[string]*relay.HLSOutput),
		hlsCtx:           hlsCtx,
		hlsCancel:        hlsCancel,
		posters:          make(map[string][]byte),
		done:             make(chan struct{}),
		setupToken:       setupToken,
		webAuthn:         wa,
		webauthnSessions: make(map[string]*webauthn.SessionData),
	}

	// GeoIP — bound to the same data dir we use for the history
	// SQLite. The lookup is lazy (returns "" until the first
	// download lands) so handleListener never blocks on it.
	srv.GeoIP = relay.NewGeoLookup("/var/lib/tinyice/geoip")
	srv.GeoTracker = NewGeoTracker(srv.GeoIP)

	// Ensure default tenant exists for backward compatibility
	srv.TenantM.GetOrCreateDefaultTenant()

	// Forward streamer track-start events to the webhook dispatcher so
	// subscribers can react to "now playing" changes. Done here (rather
	// than in NewStreamerManager) because the dispatcher needs the
	// fully-built Server reference and runtime config.
	srv.StreamerM.OnTrackStart = func(info relay.TrackStartInfo) {
		srv.dispatchWebhook("now_playing", map[string]interface{}{
			"mount":            info.Mount,
			"name":             info.StreamerName,
			"artist":           info.Artist,
			"title":            info.Title,
			"album":            info.Album,
			"file":             info.FilePath,
			"format":           info.Format,
			"bitrate":          info.Bitrate,
			"duration_seconds": info.DurationSeconds,
		})
	}

	return srv
}

func (s *Server) setupRoutes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin", s.handleAdmin)
	mux.HandleFunc("/kiosk", s.handleKiosk)
	mux.HandleFunc("/admin/golive", s.handleGoLive)
	mux.HandleFunc("/admin/golive/chunk", s.handleGoLiveChunk)
	mux.HandleFunc("/admin/add-mount", s.handleAddMount)
	mux.HandleFunc("/admin/toggle-latency", s.handleToggleLatency)
	mux.HandleFunc("/admin/stats", s.handleStats)
	mux.HandleFunc("/admin/events", s.handleEvents)
	mux.HandleFunc("/admin/metadata", s.handleMetadata)
	mux.HandleFunc("/admin/kick", s.handleKick)
	mux.HandleFunc("/admin/remove-mount", s.handleRemoveMount)
	mux.HandleFunc("/admin/hotswap", s.handleHotSwap)
	mux.HandleFunc("/admin/kick-all-listeners", s.handleKickAllListeners)
	mux.HandleFunc("/admin/toggle-mount", s.handleToggleMount)
	mux.HandleFunc("/admin/toggle-visible", s.handleToggleVisible)
	mux.HandleFunc("/admin/update-fallback", s.handleUpdateFallback)
	mux.HandleFunc("/admin/add-user", s.handleAddUser)
	mux.HandleFunc("/admin/remove-user", s.handleRemoveUser)
	mux.HandleFunc("/admin/add-banned-ip", s.handleAddBannedIP)
	mux.HandleFunc("/admin/remove-banned-ip", s.handleRemoveBannedIP)
	mux.HandleFunc("/admin/add-whitelisted-ip", s.handleAddWhitelistedIP)
	mux.HandleFunc("/admin/remove-whitelisted-ip", s.handleRemoveWhitelistedIP)
	mux.HandleFunc("/admin/clear-auth-lockout", s.handleClearAuthLockout)
	mux.HandleFunc("/admin/clear-scan-lockout", s.handleClearScanLockout)
	mux.HandleFunc("/admin/add-webhook", s.handleAddWebhook)
	mux.HandleFunc("/admin/delete-webhook", s.handleDeleteWebhook)
	mux.HandleFunc("/admin/player/toggle", s.handlePlayerToggle)
	mux.HandleFunc("/admin/player/restart", s.handlePlayerRestart)
	mux.HandleFunc("/admin/player/scan", s.handlePlayerScan)
	mux.HandleFunc("/admin/player/clear-playlist", s.handlePlayerClearPlaylist)
	mux.HandleFunc("/admin/player/clear-queue", s.handlePlayerClearQueue)
	mux.HandleFunc("/admin/player/save-playlist", s.handlePlayerSavePlaylist)
	mux.HandleFunc("/admin/player/playlist-info", s.handlePlayerPlaylistInfo)
	mux.HandleFunc("/admin/player/load-playlist", s.handlePlayerLoadPlaylist)
	mux.HandleFunc("/admin/player/reorder", s.handlePlayerReorder)
	mux.HandleFunc("/admin/player/queue", s.handlePlayerQueue)
	mux.HandleFunc("/admin/player/shuffle", s.handlePlayerShuffle)
	mux.HandleFunc("/admin/player/loop", s.handlePlayerLoop)
	mux.HandleFunc("/admin/player/metadata", s.handlePlayerMetadata)
	mux.HandleFunc("/admin/player/next", s.handlePlayerNext)
	mux.HandleFunc("/admin/player/files", s.handlePlayerFiles)
	mux.HandleFunc("/admin/player/playlist-action", s.handlePlayerPlaylistAction)
	mux.HandleFunc("/admin/autodj/add", s.handleAddAutoDJ)
	mux.HandleFunc("/admin/autodj/delete", s.handleDeleteAutoDJ)
	mux.HandleFunc("/admin/autodj/toggle", s.handleToggleAutoDJ)
	mux.HandleFunc("/admin/autodj/studio", s.handleAutoDJStudio)
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
	mux.HandleFunc("/admin/traffic", s.handleTraffic)
	mux.HandleFunc("/admin/geo", s.handleGeo)
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/logout", s.handleLogout)

	// Setup wizard endpoints
	mux.HandleFunc("/setup", s.handleSetup)
	mux.HandleFunc("/setup/verify-token", s.handleSetupVerifyToken)
	mux.HandleFunc("/setup/complete", s.handleSetupComplete)

	// Passkey (WebAuthn) endpoints
	mux.HandleFunc("/api/passkey/register/begin", s.handlePasskeyRegisterBegin)
	mux.HandleFunc("/api/passkey/register/finish", s.handlePasskeyRegisterFinish)
	mux.HandleFunc("/api/passkey/login/begin", s.handlePasskeyLoginBegin)
	mux.HandleFunc("/api/passkey/login/finish", s.handlePasskeyLoginFinish)
	mux.HandleFunc("/api/passkey", s.handlePasskeyDelete)

	// OIDC / OAuth2 endpoints
	mux.HandleFunc("/auth/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/auth/")
		if strings.HasSuffix(path, "/callback") {
			s.handleOIDCCallback(w, r)
		} else {
			s.handleOIDCRedirect(w, r)
		}
	})
	mux.HandleFunc("/api/oidc/providers", s.handleOIDCProvidersList)

	// Pending user management
	mux.HandleFunc("/api/pending-users", s.handleGetPendingUsers)
	mux.HandleFunc("/api/pending-users/approve", s.handleApprovePendingUser)
	mux.HandleFunc("/api/pending-users/deny", s.handleDenyPendingUser)

	mux.HandleFunc("/explore", s.handleExplore)
	mux.HandleFunc("/webrtc/offer", s.handleWebRTCOffer)
	mux.HandleFunc("/webrtc/source-offer", s.handleWebRTCSourceOffer)
	mux.HandleFunc("/player/", s.handlePlayer)
	mux.HandleFunc("/player-webrtc/", s.handleWebRTCPlayer)
	mux.HandleFunc("/embed/", s.handleEmbed)
	mux.HandleFunc("/api/tenants", s.handleListTenants)
	mux.HandleFunc("/api/tenants/usage", s.handleTenantUsage)
	// HLS routes (must be before the catch-all "/" handler)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Setup mode guard: redirect everything to /setup except setup/assets paths
		if !s.Config.SetupComplete {
			path := r.URL.Path
			if path == "/setup" || strings.HasPrefix(path, "/setup/") {
				// Let the registered setup handlers handle these
				if path == "/setup" {
					s.handleSetup(w, r)
				} else if path == "/setup/verify-token" {
					s.handleSetupVerifyToken(w, r)
				} else if path == "/setup/complete" {
					s.handleSetupComplete(w, r)
				} else {
					http.NotFound(w, r)
				}
				return
			}
			if strings.HasPrefix(path, "/assets/") || strings.HasPrefix(path, "/api/passkey/") {
				// Allow through for assets and passkey API during setup
			} else {
				http.Redirect(w, r, "/setup", http.StatusTemporaryRedirect)
				return
			}
		}

		path := r.URL.Path
		if strings.HasSuffix(path, "/master.m3u8") {
			s.handleHLSMaster(w, r)
			return
		}
		if strings.HasSuffix(path, "/playlist.m3u8") {
			s.handleHLSPlaylist(w, r)
			return
		}
		if strings.HasSuffix(path, ".ts") && strings.Contains(path, "/segment-") {
			s.handleHLSSegment(w, r)
			return
		}
		if strings.HasSuffix(path, "/poster.jpg") {
			s.handlePoster(w, r)
			return
		}
		if strings.HasSuffix(path, "/whep") {
			s.handleWHEP(w, r)
			return
		}
		s.handleRoot(w, r)
	})
	mux.HandleFunc("/events", s.handlePublicEvents)
	mux.HandleFunc("/status-json.xsl", s.handleLegacyStats)
	mux.HandleFunc("/metrics", s.handleMetrics)
	// Serve frontend assets (Vite build output) at /assets/ — takes priority
	// Falls back to legacy assets (logo, lucide, sortable) if not found in dist
	mux.Handle("/assets/", http.StripPrefix("/assets/", s.shell.AssetHandler()))

	// Developer portal (new page)
	mux.HandleFunc("/developers", s.handleDevelopers)

	// JSON REST API v2
	mux.HandleFunc("/api/streams", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.apiGetStreams(w, r)
		case http.MethodPost:
			s.apiCreateStream(w, r)
		case http.MethodPut, http.MethodPatch:
			s.apiUpdateStream(w, r)
		case http.MethodDelete:
			s.apiDeleteStream(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/streams/kick", s.apiKickStream)

	mux.HandleFunc("/api/autodj", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.apiGetAutoDJ(w, r)
		case http.MethodPost:
			s.apiCreateAutoDJ(w, r)
		case http.MethodPut, http.MethodPatch:
			s.apiUpdateAutoDJ(w, r)
		case http.MethodDelete:
			s.apiDeleteAutoDJ(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/autodj/play", s.apiAutoDJPlay)
	mux.HandleFunc("/api/autodj/pause", s.apiAutoDJPause)
	mux.HandleFunc("/api/autodj/next", s.apiAutoDJNext)
	mux.HandleFunc("/api/autodj/shuffle", s.apiAutoDJShuffle)
	mux.HandleFunc("/api/autodj/loop", s.apiAutoDJLoop)

	mux.HandleFunc("/api/autodj/playlist", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.apiGetPlaylist(w, r)
		case http.MethodPost:
			s.apiAddToPlaylist(w, r)
		case http.MethodDelete:
			s.apiRemoveFromPlaylist(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/autodj/playlist/clear", s.apiClearPlaylist)
	mux.HandleFunc("/api/autodj/playlist/reorder", s.apiReorderPlaylist)

	mux.HandleFunc("/api/autodj/queue", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.apiGetQueue(w, r)
		case http.MethodPost:
			s.apiAddToQueue(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/autodj/files", s.apiGetFiles)
	// Handle /api/autodj/{mount}/... paths (frontend uses path-based mount routing)
	// The frontend sends mount names URL-encoded (e.g. %2Fdemo for /demo),
	// so we use RawPath to preserve the encoding before splitting.
	mux.HandleFunc("/api/autodj/", func(w http.ResponseWriter, r *http.Request) {
		rawPath := r.URL.RawPath
		if rawPath == "" {
			rawPath = r.URL.Path
		}
		path := strings.TrimPrefix(rawPath, "/api/autodj/")

		// Find the action by looking for known suffixes
		// Path format: {encoded-mount}/{action} e.g. "%2Fdemo/files" or "bob/playlist/add"
		var mount, action string
		actionSuffixes := []string{
			"/playlist/add", "/playlist/remove", "/playlist/clear",
			"/playlist/reorder", "/playlist/playnext",
			"/playlist/save", "/playlist/load",
			"/files", "/playlist", "/queue",
			"/play", "/pause", "/next", "/prev", "/previous",
			"/shuffle", "/loop",
			"/metadata", "/volume",
		}
		for _, suffix := range actionSuffixes {
			if strings.HasSuffix(path, suffix) {
				encodedMount := strings.TrimSuffix(path, suffix)
				// URL-decode the mount name
				decoded, err := url.PathUnescape(encodedMount)
				if err != nil {
					decoded = encodedMount
				}
				// Ensure mount starts with /
				if !strings.HasPrefix(decoded, "/") {
					decoded = "/" + decoded
				}
				mount = decoded
				action = strings.TrimPrefix(suffix, "/")
				break
			}
		}
		if mount == "" {
			http.NotFound(w, r)
			return
		}

		// Inject mount as query param for existing handlers
		q := r.URL.Query()
		q.Set("mount", mount)
		r.URL.RawQuery = q.Encode()

		switch action {
		case "files":
			s.apiGetFiles(w, r)
		case "playlist":
			switch r.Method {
			case http.MethodGet:
				s.apiGetPlaylist(w, r)
			case http.MethodPost:
				s.apiAddToPlaylist(w, r)
			case http.MethodDelete:
				s.apiRemoveFromPlaylist(w, r)
			}
		case "playlist/add":
			s.apiAddToPlaylist(w, r)
		case "playlist/remove":
			s.apiRemoveFromPlaylist(w, r)
		case "playlist/clear":
			s.apiClearPlaylist(w, r)
		case "playlist/reorder":
			s.apiReorderPlaylist(w, r)
		case "playlist/playnext":
			// Front of queue — apiAddToQueue reads this flag because the
			// caller's JSON body carries only the playlist entry id.
			q := r.URL.Query()
			q.Set("front", "true")
			r.URL.RawQuery = q.Encode()
			s.apiAddToQueue(w, r)
		case "playlist/save":
			s.handlePlayerSavePlaylist(w, r)
		case "playlist/load":
			s.handlePlayerLoadPlaylist(w, r)
		case "queue":
			switch r.Method {
			case http.MethodGet:
				s.apiGetQueue(w, r)
			case http.MethodPost:
				s.apiAddToQueue(w, r)
			}
		case "play":
			s.apiAutoDJPlay(w, r)
		case "pause":
			s.apiAutoDJPause(w, r)
		case "next":
			s.apiAutoDJNext(w, r)
		case "prev", "previous":
			s.apiAutoDJPrev(w, r)
		case "shuffle":
			s.apiAutoDJShuffle(w, r)
		case "loop":
			s.apiAutoDJLoop(w, r)
		case "metadata":
			s.handlePlayerMetadata(w, r)
		case "volume":
			s.apiAutoDJVolume(w, r)
		default:
			http.NotFound(w, r)
		}
	})

	mux.HandleFunc("/api/relays", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.apiGetRelays(w, r)
		case http.MethodPost:
			s.apiCreateRelay(w, r)
		case http.MethodDelete:
			s.apiDeleteRelay(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/relays/toggle", s.apiToggleRelay)

	mux.HandleFunc("/api/transcoders", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.apiGetTranscoders(w, r)
		case http.MethodPost:
			s.apiCreateTranscoder(w, r)
		case http.MethodPut, http.MethodPatch:
			s.apiUpdateTranscoder(w, r)
		case http.MethodDelete:
			s.apiDeleteTranscoder(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/users", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.apiGetUsers(w, r)
		case http.MethodPost:
			s.apiCreateUser(w, r)
		case http.MethodPut:
			s.apiUpdateUser(w, r)
		case http.MethodDelete:
			s.apiDeleteUser(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/security/bans", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.apiGetBans(w, r)
		case http.MethodPost:
			s.apiAddBan(w, r)
		case http.MethodDelete:
			s.apiRemoveBan(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/security/whitelist", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.apiGetWhitelist(w, r)
		case http.MethodPost:
			s.apiAddWhitelist(w, r)
		case http.MethodDelete:
			s.apiRemoveWhitelist(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/security/audit", s.apiGetAuditLog)

	// Webhook CRUD + meta + test endpoints used by the admin UI.
	mux.HandleFunc("/api/webhooks", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.apiGetWebhooks(w, r)
		case http.MethodPost:
			s.apiCreateWebhook(w, r)
		case http.MethodPut, http.MethodPatch:
			s.apiUpdateWebhook(w, r)
		case http.MethodDelete:
			s.apiDeleteWebhook(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/webhooks/meta", s.apiGetWebhookMeta)
	mux.HandleFunc("/api/webhooks/test", s.apiTestWebhook)

	mux.HandleFunc("/api/branding", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.apiGetBranding(w, r)
		case http.MethodPut:
			s.apiUpdateBranding(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.apiGetSettings(w, r)
		case http.MethodPut:
			s.apiUpdateSettings(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/stats", s.apiGetStats)

	mux.HandleFunc("/api/tokens", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.apiGetTokens(w, r)
		case http.MethodPost:
			s.apiCreateToken(w, r)
		case http.MethodDelete:
			s.apiDeleteToken(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// Logo
	mux.HandleFunc("/api/branding/logo", s.apiUploadLogo)
	mux.HandleFunc("/branding/logo", s.handleServeLogo)

	// OpenAPI / Swagger
	mux.HandleFunc("/api/openapi.yaml", s.handleOpenAPISpec)
	mux.HandleFunc("/api/docs", s.handleSwaggerUI)

	return mux
}

// withSetupGuard wraps a handler to enforce setup mode.
func (s *Server) withSetupGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.Config.SetupComplete {
			path := r.URL.Path
			if path == "/setup" || strings.HasPrefix(path, "/setup/") || strings.HasPrefix(path, "/assets/") {
				logger.L.Debugw("setupGuard: allowing through", "path", path)
				next.ServeHTTP(w, r)
				return
			}
			http.Redirect(w, r, "/setup", http.StatusTemporaryRedirect)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) Shutdown(ctx context.Context) error {
	logger.L.Info("Server shutting down gracefully...")

	if s.hlsCancel != nil {
		s.hlsCancel()
	}

	close(s.done)

	s.Relay.DisconnectAllListeners()

	s.RelayM.StopAll()
	s.TranscoderM.StopAll()

	for _, st := range s.StreamerM.GetStreamers() {
		s.StreamerM.StopStreamer(st.OutputMount)
	}

	// Run RTMP and SRT Stop in parallel. Either can hang briefly while
	// in-flight connections close; we don't want one to gate the other
	// and we don't want either to gate the HTTP shutdown below.
	var ingestWG sync.WaitGroup
	if s.RTMP != nil {
		ingestWG.Add(1)
		go func() {
			defer ingestWG.Done()
			s.RTMP.Stop()
		}()
	}
	if s.SRT != nil {
		ingestWG.Add(1)
		go func() {
			defer ingestWG.Done()
			s.SRT.Stop()
		}()
	}
	// Don't block forever on ingest shutdown.
	ingestDone := make(chan struct{})
	go func() {
		ingestWG.Wait()
		close(ingestDone)
	}()
	select {
	case <-ingestDone:
	case <-time.After(4 * time.Second):
		logger.L.Warnw("Shutdown: ingest servers didn't stop in time, continuing")
	}

	var wg sync.WaitGroup
	for _, srv := range s.httpServers {
		wg.Add(1)
		go func(srv *http.Server) {
			defer wg.Done()
			shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := srv.Shutdown(shCtx); err != nil {
				logger.L.Errorf("Error during HTTP server shutdown: %v", err)
			}
		}(srv)
	}

	waitDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		logger.L.Info("All servers shut down successfully")
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) HotSwap() error {
	logger.L.Info("Initiating zero-downtime hot swap...")

	exe, err := os.Executable()
	if err != nil {
		return err
	}

	process, err := os.StartProcess(exe, os.Args, &os.ProcAttr{
		Dir:   ".",
		Env:   os.Environ(),
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	})
	if err != nil {
		return fmt.Errorf("failed to start new process: %v", err)
	}

	logger.L.Infof("New process started with PID %d. Waiting for health check...", process.Pid)

	time.Sleep(5 * time.Second)

	logger.L.Info("Handoff period complete. Shutting down old process...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	go func() {
		s.Shutdown(ctx)
		os.Exit(0)
	}()

	return nil
}

func (s *Server) ReloadConfig(cfg *config.Config) {
	s.Config = cfg
	s.RelayM.StopAll()
	for _, rc := range s.Config.Relays {
		if rc.Enabled {
			s.RelayM.StartRelay(rc.URL, rc.Mount, rc.Password, rc.BurstSize, s.Config.VisibleMounts[rc.Mount])
		}
	}
	s.TranscoderM.StopAll()
	for _, tc := range s.Config.Transcoders {
		if tc.Enabled {
			s.TranscoderM.StartTranscoder(tc)
		}
	}
	logger.L.Info("Configuration reloaded successfully")
}

func (s *Server) Start() error {
	mux := s.setupRoutes()
	// Setup guard is handled inside the "/" catch-all handler since it intercepts all routes.
	// The withSetupGuard middleware is not used because the "/" handler in Go's ServeMux
	// catches routes before they reach more specific handlers when registered as a catch-all.
	var handler http.Handler = mux
	if s.Config.MultiTenant != nil && s.Config.MultiTenant.Enabled {
		handler = s.withTenant(handler)
	}

	if s.Config.DirectoryListing {
		go s.directoryReportingTask()
	}
	go s.statsRecordingTask()
	go s.HealthM.Start(context.Background())
	go s.sessionReaperTask()
	// Internal-only metrics + pprof port. See startMetricsServer for the
	// expected firewalling.
	s.startMetricsServer()

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
	for _, adj := range s.Config.AutoDJs {
		absMusicDir, _ := filepath.Abs(adj.MusicDir)
		streamer, err := s.StreamerM.StartStreamer(adj.Name, adj.Mount, absMusicDir, adj.Loop, adj.Format, adj.Bitrate, adj.InjectMetadata, adj.Playlist, adj.MPDEnabled, adj.MPDPort, adj.MPDPassword, adj.Visible, adj.LastPlaylist, adj.SongCommand, adj.SongCommandTimeout, adj.OnPlayCommand, adj.OnPlayCommandTimeout)
		if err == nil {
			if adj.LastPlaylist != "" {
				streamer.LoadPlaylist(adj.LastPlaylist)
			}
			if adj.Enabled {
				if adj.LastPlaylist != "" {
					streamer.LoadPlaylist(adj.LastPlaylist)
				}
				if adj.InjectMetadata {
					if st, ok := s.Relay.GetStream(adj.Mount); ok {
						st.SetVisible(adj.Visible)
					}
				}
				if len(adj.Playlist) == 0 {
					streamer.ScanMusicDir()
				}
				streamer.Play()
			}
		} else {
			logger.L.Errorf("Failed to initialize AutoDJ %s: %v", adj.Name, err)
		}
	}

	// Start RTMP server if enabled
	if s.Config.Ingest != nil && s.Config.Ingest.RTMPEnabled {
		if err := s.RTMP.Start(); err != nil {
			logger.L.Errorf("Failed to start RTMP server: %v", err)
		}
	}

	// Start SRT server if enabled
	if s.Config.Ingest != nil && s.Config.Ingest.SRTEnabled {
		if err := s.SRT.Start(); err != nil {
			logger.L.Errorf("Failed to start SRT server: %v", err)
		}
	}

	port := s.Config.Port

	if s.Config.UseHTTPS {
		addr := net.JoinHostPort(s.Config.BindHost, port)
		return s.startHTTPS(handler, addr)
	}

	srv := &http.Server{
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}
	s.httpServers = append(s.httpServers, srv)

	listeners, err := s.buildListeners(port)
	if err != nil {
		return err
	}

	if len(listeners) == 1 {
		logger.L.Infof("Starting TinyIce on %s (HTTP)", listeners[0].Addr())
		return srv.Serve(listeners[0])
	}

	logger.L.Infof("Starting TinyIce on [::]:%s and 0.0.0.0:%s (HTTP)", port, port)
	combined := newMultiListener(listeners)
	return srv.Serve(combined)
}
