package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/pprof"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/DatanoiseTV/tinyice/logger"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

type BannedListener struct {
	net.Listener
	s *Server
}

func (l *BannedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		remoteAddr := conn.RemoteAddr().String()
		// A trusted reverse proxy carries every client's traffic, so
		// dropping it at the TCP level would take the whole site down.
		// Bans behind a proxy are enforced per-request in the handlers,
		// where X-Forwarded-For is available.
		if l.s.isTrustedProxy(remoteAddr) {
			return conn, nil
		}
		if l.s.isBanned(remoteAddr) {
			logger.L.Warnw("Dropping connection from banned IP at TCP level", "ip", remoteAddr)
			conn.Close()
			continue
		}
		return conn, nil
	}
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

			target := p.httpChan
			if buf[0] == 0x16 {
				target = p.tlsChan
			}

			select {
			case target <- wrapped:
			default:
				select {
				case old := <-target:
					old.Close()
				default:
				}
				select {
				case target <- wrapped:
				default:
					c.Close()
				}
			}
		}(conn)
	}
}

// multiListener fans in Accept() calls from multiple net.Listeners into one.
type multiListener struct {
	listeners []net.Listener
	connCh    chan net.Conn
	once      sync.Once
	closeOnce sync.Once
	done      chan struct{}
}

func newMultiListener(ls []net.Listener) *multiListener {
	ml := &multiListener{
		listeners: ls,
		connCh:    make(chan net.Conn, 64),
		done:      make(chan struct{}),
	}
	for _, l := range ls {
		go ml.accept(l)
	}
	return ml
}

func (ml *multiListener) accept(l net.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
			select {
			case <-ml.done:
				return
			default:
				logger.L.Warnw("multiListener accept error", "error", err)
				return
			}
		}
		ml.connCh <- conn
	}
}

func (ml *multiListener) Accept() (net.Conn, error) {
	select {
	case conn := <-ml.connCh:
		return conn, nil
	case <-ml.done:
		return nil, fmt.Errorf("listener closed")
	}
}

func (ml *multiListener) Close() error {
	ml.closeOnce.Do(func() {
		close(ml.done)
		for _, l := range ml.listeners {
			l.Close()
		}
	})
	return nil
}

func (ml *multiListener) Addr() net.Addr {
	if len(ml.listeners) > 0 {
		return ml.listeners[0].Addr()
	}
	return nil
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
	ln, err := lc.Listen(context.Background(), network, address)
	if err != nil {
		return nil, err
	}
	return &BannedListener{ln, s}, nil
}

// checkPortInUse probes whether another process is already listening on the given port.
// With SO_REUSEPORT, binding will succeed even if another instance is running, which
// causes requests to be randomly split between instances. This check warns the user.
func checkPortInUse(port string) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), 500*time.Millisecond)
	if err == nil {
		conn.Close()
		logger.L.Warnf("WARNING: Another process is already listening on port %s. Requests will be split between instances (SO_REUSEPORT). Kill the other instance to avoid issues.", port)
		fmt.Printf("\n  ⚠ WARNING: Port %s is already in use by another process.\n  Requests will be randomly split between instances.\n  Run: kill $(lsof -ti :%s) to stop the other instance.\n\n", port, port)
	}
}

// buildListeners returns one listener per usable network family for the given port.
func (s *Server) buildListeners(port string) ([]net.Listener, error) {
	checkPortInUse(port)
	host := s.Config.BindHost

	if host != "" && host != "0.0.0.0" && host != "::" {
		addr := net.JoinHostPort(host, port)
		ln, err := s.listenWithReuse("tcp", addr)
		if err != nil {
			return nil, err
		}
		return []net.Listener{ln}, nil
	}

	addrs := []string{
		net.JoinHostPort("0.0.0.0", port),
		net.JoinHostPort("::", port),
	}

	var listeners []net.Listener
	for _, addr := range addrs {
		ln, err := s.listenWithReuse("tcp", addr)
		if err != nil {
			logger.L.Warnf("Could not listen on %s: %v (skipping)", addr, err)
			continue
		}
		listeners = append(listeners, ln)
	}

	if len(listeners) == 0 {
		return nil, fmt.Errorf("failed to bind on any address for port %s", port)
	}
	return listeners, nil
}

func (s *Server) dynamicHostPolicy(ctx context.Context, host string) error {
	for _, d := range s.Config.Domains {
		if host == d {
			return nil
		}
	}
	return fmt.Errorf("acme/autocert: host %q not configured in 'domains'", host)
}

func (s *Server) startHTTPS(handler http.Handler, addr string) error {
	httpsAddr := net.JoinHostPort(s.Config.BindHost, s.Config.HTTPSPort)
	if s.Config.AutoHTTPS {
		if len(s.Config.Domains) == 0 {
			logger.L.Warn("Auto-HTTPS is enabled but no domains are configured in 'domains'. Certificates will not be issued.")
		}
		if s.Config.Port != "80" || s.Config.HTTPSPort != "443" {
			logger.L.Warnf("Auto-HTTPS usually requires port 80 and 443 to satisfy ACME challenges. Current ports: HTTP=%s, HTTPS=%s. Ensure you have port forwarding (80->%s, 443->%s) configured.", s.Config.Port, s.Config.HTTPSPort, s.Config.Port, s.Config.HTTPSPort)
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
		Addr:    httpsAddr,
		Handler: handler,
		// ReadHeaderTimeout instead of ReadTimeout — the latter
		// bounds the WHOLE request including streaming bodies,
		// which is wrong for source ingest. Headers must be in
		// within 10 s; bodies use their own per-read deadline
		// (handleSource refreshes a 60 s deadline on every Read).
		// Listener writes get a per-write deadline via
		// http.NewResponseController inside the handler.
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
	}
	if s.certManager != nil {
		httpsSrv.TLSConfig = s.certManager.TLSConfig()
	}
	s.httpServers = append(s.httpServers, httpsSrv)

	httpSrv := &http.Server{
		Addr: addr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "PUT" || r.Method == "SOURCE" {
				handler.ServeHTTP(w, r)
				return
			}
			if s.certManager != nil && strings.HasPrefix(r.URL.Path, "/.well-known/acme-challenge/") {
				logger.L.Infow("Handling ACME challenge request",
					"path", r.URL.Path,
					"ip", r.RemoteAddr,
				)
				s.certManager.HTTPHandler(nil).ServeHTTP(w, r)
				return
			}
			target := "https://" + r.Host + r.URL.Path
			if len(r.URL.RawQuery) > 0 {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusMovedPermanently)
		}),
		// Same rationale as the HTTPS server above: ReadHeaderTimeout
		// caps slowloris attacks without breaking PUT/SOURCE streams
		// that use Hijack and do their own per-read deadlines.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	s.httpServers = append(s.httpServers, httpSrv)

	go func() {
		logger.L.Infof("Starting HTTP listener on %s", addr)
		ln, err := s.listenWithReuse("tcp", addr)
		if err != nil {
			logger.L.Fatalf("HTTP listen failed: %v", err)
		}
		if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.L.Fatalf("HTTP server failed: %v", err)
		}
	}()

	logger.L.Infof("Starting dual-mode HTTPS/HTTP server on %s", httpsAddr)
	rawLn, err := s.listenWithReuse("tcp", httpsAddr)
	if err != nil {
		return err
	}

	sniffer := &protocolSniffer{
		Listener: rawLn,
		tlsChan:  make(chan net.Conn, 4096),
		httpChan: make(chan net.Conn, 4096),
	}
	go sniffer.sniff()

	tlsLn := &chanListener{addr: rawLn.Addr(), ch: sniffer.tlsChan}
	plainLn := &chanListener{addr: rawLn.Addr(), ch: sniffer.httpChan}

	go func() {
		if err := httpSrv.Serve(plainLn); err != nil && err != http.ErrServerClosed {
			logger.L.Errorf("Sniffed HTTP server failed: %v", err)
		}
	}()

	if s.certManager != nil {
		return httpsSrv.ServeTLS(tlsLn, "", "")
	}
	return httpsSrv.ServeTLS(tlsLn, s.Config.CertFile, s.Config.KeyFile)
}

func (s *Server) startMetricsServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", s.handleMetrics)

	// Expose runtime diagnostics on the same internal-only port. The
	// metrics port is intended to bind on the loopback / private side of
	// the deployment, so we do not gate pprof behind auth — operators are
	// expected to firewall this port the same way they do /metrics. When
	// the server next hangs, capture goroutine + mutex profiles with:
	//   curl -o goroutines.txt 'http://HOST:8081/debug/pprof/goroutine?debug=2'
	//   curl -o mutex.txt      'http://HOST:8081/debug/pprof/mutex?debug=2'
	//   curl -o heap.pb        'http://HOST:8081/debug/pprof/heap'
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	// Sample mutex / block events so the contention profile is meaningful.
	// Rates of 1 = sample every event (cheap relative to actual contention
	// the user is hitting; tune down if it shows up in CPU profiles).
	runtime.SetMutexProfileFraction(1)
	runtime.SetBlockProfileRate(1)

	// In TinyIce, the metrics port might not be in the config yet, fallback to 8081
	addr := ":8081"

	logger.L.Infof("Starting metrics + pprof server on %s", addr)
	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.L.Errorf("Metrics server error: %v", err)
		}
	}()

	go func() {
		<-s.done
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()
}

func (s *Server) startHealthCheck() {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-s.done:
				return
			case <-ticker.C:
				allStreams := s.Relay.Snapshot()
				for _, st := range allStreams {
					if st.Health < 50 {
						logger.L.Warnw("Stream health critical", "mount", st.MountName, "health", st.Health)
					}
				}
			}
		}
	}()
}

func (s *Server) startJanitor() {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-s.done:
				return
			case <-ticker.C:
				s.scanAttemptsMu.Lock()
				s.scanAttempts = make(map[string]*scanAttempt)
				s.scanAttemptsMu.Unlock()
				logger.L.Debug("Janitor: Cleared scan counters")
			}
		}
	}()
}
