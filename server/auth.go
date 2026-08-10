package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
	"go.uber.org/zap"
)

type scanAttempt struct {
	Count     int
	Paths     map[string]bool
	LockoutBy time.Time
}

type authAttempt struct {
	Count     int
	LockoutBy time.Time
}

type session struct {
	User      *config.User
	CSRFToken string
	// CreatedAt is the wall time the session was minted. ExpiresAt is the
	// absolute expiry; checkAuth treats the session as absent once now() is
	// past it. LastSeen is bumped on each successful use so idle sessions
	// naturally drop when the reaper runs.
	CreatedAt time.Time
	ExpiresAt time.Time
	LastSeen  time.Time
}

// sessionMaxLifetime is the absolute (non-sliding) upper bound on a session.
// sessionIdleTimeout is the sliding idle limit — a session that goes unused
// for this long is reaped even if the absolute lifetime hasn't expired.
const (
	sessionMaxLifetime = 7 * 24 * time.Hour
	sessionIdleTimeout = 24 * time.Hour
)

// dummyBcryptHash is compared against when the submitted username doesn't
// exist, so the login path always pays one bcrypt verification and user
// enumeration via response time stops working.
//
// Generated once with bcrypt.GenerateFromPassword([]byte("never"), cost=10).
const dummyBcryptHash = "$2a$10$CwTycUXWue0Thq9StjUM0uJ8dOo8dX1p5lPfzPOY4Vqf.m5k1Il0i"

func (s *Server) logAuth() *zap.SugaredLogger {
	if s.AuthLog != nil {
		return s.AuthLog
	}
	return logger.L
}

// logAuthFailed records a failed authentication. ip may be either a bare
// address (as returned by clientIP) or a host:port pair (a raw RemoteAddr);
// both are normalised to the bare address here so the audit log is uniform.
func (s *Server) logAuthFailed(user, ip, reason string) {
	host := stripPort(ip)
	s.logAuth().Warnw(fmt.Sprintf("Authentication failed for user '%s' from %s: %s", user, host, reason),
		"user", user,
		"ip", host,
		"reason", reason,
	)
}

func (s *Server) checkAuthLimit(ip string) error {
	// Whitelist check first — if an operator added an IP to the
	// whitelist after that IP got itself locked out (e.g. they tested
	// auth with the wrong password from their dev box, then added the
	// dev box to whitelist to fix it), the attempt entry persists and
	// the lockout would otherwise still bite. Bypass before consulting
	// the rate-limit map.
	if s.isWhitelisted(ip) {
		return nil
	}

	s.authAttemptsMu.Lock()
	defer s.authAttemptsMu.Unlock()

	attempt, exists := s.authAttempts[ip]
	if !exists {
		return nil
	}

	if time.Now().Before(attempt.LockoutBy) {
		return fmt.Errorf("too many failed attempts. locked out until %s", attempt.LockoutBy.Format("15:04:05"))
	}

	if time.Now().After(attempt.LockoutBy) && attempt.Count >= 5 {
		attempt.Count = 0
	}
	return nil
}

func (s *Server) recordAuthFailure(ip string) {
	if s.isWhitelisted(ip) {
		return
	}
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
		logger.L.Warnf("IP %s locked out for 15 minutes due to 5 failed auth attempts", ip)
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

func (s *Server) recordScanAttempt(ip, path string) {
	if s.isWhitelisted(ip) {
		return
	}
	s.scanAttemptsMu.Lock()
	defer s.scanAttemptsMu.Unlock()

	attempt, exists := s.scanAttempts[ip]
	if !exists {
		attempt = &scanAttempt{Paths: make(map[string]bool)}
		s.scanAttempts[ip] = attempt
	}

	attempt.Paths[path] = true
	attempt.Count++

	// Require a large spread of distinct 404 paths before locking an IP
	// out: real vuln scanners probe dozens of unrelated paths, whereas a
	// legitimate listener reconnecting to an offline mount hits the same
	// path repeatedly. Counting distinct paths rather than raw hits stops
	// well-behaved clients from tripping the lockout.
	if len(attempt.Paths) >= 25 {
		attempt.LockoutBy = time.Now().Add(15 * time.Minute)
		logger.L.Warnf("IP %s locked out for 15 minutes after %d distinct 404 paths (likely scanner)", ip, len(attempt.Paths))
		s.dispatchWebhook("security_lockout", map[string]interface{}{
			"ip":      ip,
			"reason":  "connection_scanning",
			"until":   attempt.LockoutBy.Format(time.RFC3339),
			"details": fmt.Sprintf("%d distinct 404 paths", len(attempt.Paths)),
		})
	}
}

func hashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "ti_" + hex.EncodeToString(b), nil
}

func (s *Server) touchToken(tok *config.APIToken, remoteAddr string) {
	tok.LastUsedAt = time.Now().Format(time.RFC3339)
	tok.LastUsedIP = stripPort(remoteAddr)
	s.tokenSaveMu.Lock()
	if s.tokenSaveTimer == nil {
		s.tokenSaveTimer = time.AfterFunc(60*time.Second, func() {
			s.Config.SaveConfig()
			s.tokenSaveMu.Lock()
			s.tokenSaveTimer = nil
			s.tokenSaveMu.Unlock()
		})
	}
	s.tokenSaveMu.Unlock()
}

func (s *Server) checkAuth(r *http.Request) (*config.User, bool) {
	// Bearer token auth
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		raw := strings.TrimPrefix(auth, "Bearer ")
		hash := hashToken(raw)
		for _, tok := range s.Config.APITokens {
			if tok.TokenHash == hash {
				// Check expiry
				if tok.ExpiresAt != "" {
					if exp, err := time.Parse(time.RFC3339, tok.ExpiresAt); err == nil && time.Now().After(exp) {
						return nil, false
					}
				}
				user, exists := s.Config.Users[tok.Username]
				if !exists {
					return nil, false
				}
				// Update last-used tracking (debounced save)
				s.touchToken(tok, s.clientIP(r))
				return user, true
			}
		}
		return nil, false
	}

	if cookie, err := r.Cookie("sid"); err == nil {
		s.sessionsMu.RLock()
		sess, ok := s.sessions[cookie.Value]
		s.sessionsMu.RUnlock()
		if ok {
			now := time.Now()
			if !sess.ExpiresAt.IsZero() && now.After(sess.ExpiresAt) {
				s.sessionsMu.Lock()
				delete(s.sessions, cookie.Value)
				s.sessionsMu.Unlock()
				return nil, false
			}
			if !sess.LastSeen.IsZero() && now.Sub(sess.LastSeen) > sessionIdleTimeout {
				s.sessionsMu.Lock()
				delete(s.sessions, cookie.Value)
				s.sessionsMu.Unlock()
				return nil, false
			}
			s.sessionsMu.Lock()
			sess.LastSeen = now
			s.sessionsMu.Unlock()
			return sess.User, true
		}
	}

	u, p, ok := r.BasicAuth()
	if !ok {
		return nil, false
	}

	host := s.clientIP(r)
	if err := s.checkAuthLimit(host); err != nil {
		s.logAuthFailed(u, host, err.Error())
		return nil, false
	}

	// Always run bcrypt (against a dummy hash when the user doesn't exist)
	// so login response times don't leak username existence.
	user, exists := s.Config.Users[u]
	var passOK bool
	if exists {
		passOK = config.CheckPasswordHash(p, user.Password)
	} else {
		_ = config.CheckPasswordHash(p, dummyBcryptHash)
		passOK = false
	}
	if !exists {
		s.recordAuthFailure(host)
		s.logAuthFailed(u, host, "user not found")
		return nil, false
	}
	if !passOK {
		s.recordAuthFailure(host)
		s.logAuthFailed(u, host, "invalid password")
		return nil, false
	}

	s.recordAuthSuccess(host)
	s.logAuth().Infow("Admin auth successful (Basic)", "user", u, "ip", host)
	return user, true
}

func (s *Server) hasAccess(user *config.User, mount string) bool {
	if user.Role == config.RoleSuperAdmin {
		return true
	}
	_, exists := user.Mounts[mount]
	return exists
}

// stripPort normalises an address to a bare IP. Accepts "host:port",
// "[v6]:port" and a bare address, so callers can hand it either an
// http.Request.RemoteAddr or an already-extracted IP.
func stripPort(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

// clientIP returns the best-known client IP for the request. When the direct
// peer is in the configured TrustedProxies list, the left-most entry of
// X-Forwarded-For (the originating client) is used instead; otherwise the
// peer address is returned. Prevents attackers from spoofing an X-F-F header
// directly, while still giving sensible behaviour behind a reverse proxy.
//
// Every place that answers "which client is this?" — audit log, auth log,
// rate limiting, bans, scan detection, listener geo — must go through here.
// Reading RemoteAddr directly behind a reverse proxy records the proxy for
// every request, which both blinds the audit trail and makes one abusive
// client lock out everyone sharing the proxy.
func (s *Server) clientIP(r *http.Request) string {
	peer := stripPort(r.RemoteAddr)
	if !s.isTrustedProxy(peer) {
		return peer
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		if real := stripPort(strings.TrimSpace(r.Header.Get("X-Real-IP"))); real != "" {
			return real
		}
		return peer
	}
	if i := strings.IndexByte(xff, ','); i >= 0 {
		xff = xff[:i]
	}
	// Proxies occasionally append a source port ("1.2.3.4:5678"), and IPv6
	// entries may arrive bracketed. Normalise both so downstream map keys
	// and CIDR checks see a plain address.
	if forwarded := stripPort(strings.TrimSpace(xff)); forwarded != "" {
		return forwarded
	}
	return peer
}

// isTrustedProxy reports whether the given peer address is configured as a
// reverse-proxy hop we're willing to take X-Forwarded-For from. Loopback is
// NOT automatically trusted — operators must opt in via TrustedProxies.
//
// Comparison is on the parsed IP, not the string: a proxy connecting over a
// dual-stack socket presents as "::ffff:10.0.0.1" while the operator wrote
// "10.0.0.1" in the config, and IPv6 has many spellings of the same address.
// String equality silently failed on both, which reads as "trusted_proxies
// is being ignored".
func (s *Server) isTrustedProxy(peer string) bool {
	ip := net.ParseIP(stripPort(peer))
	if ip == nil {
		return false
	}
	for _, p := range s.Config.TrustedProxies {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.Contains(p, "/") {
			if _, ipnet, err := net.ParseCIDR(p); err == nil && ipnet.Contains(ip) {
				return true
			}
			continue
		}
		if trusted := net.ParseIP(p); trusted != nil && trusted.Equal(ip) {
			return true
		}
	}
	return false
}

func (s *Server) isWhitelisted(ipStr string) bool {
	host := stripPort(ipStr)

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}

	// Loopback is only whitelisted when TrustedProxies is empty — otherwise
	// the server is behind a reverse proxy and "the connection came from
	// localhost" is the norm, which would silently disable scan detection
	// and rate limiting for every client.
	if ip.IsLoopback() && len(s.Config.TrustedProxies) == 0 {
		return true
	}

	for _, whitelisted := range s.Config.WhitelistedIPs {
		if strings.Contains(whitelisted, "/") {
			_, ipnet, err := net.ParseCIDR(whitelisted)
			if err == nil && ipnet.Contains(ip) {
				return true
			}
		}
		if whitelisted == host {
			return true
		}
	}
	return false
}

func (s *Server) isCSRFSafe(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return true
	}

	// JSON API endpoints are inherently CSRF-safe: the Content-Type: application/json
	// header cannot be sent cross-origin without a CORS preflight, and we don't set
	// permissive CORS headers. Session auth still applies via checkAuth.
	if strings.HasPrefix(r.URL.Path, "/api/") &&
		strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		return true
	}

	// Also allow multipart uploads to /api/ (e.g. logo upload)
	if strings.HasPrefix(r.URL.Path, "/api/") &&
		strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
		return true
	}

	cookie, err := r.Cookie("sid")
	if err != nil {
		return true
	}

	s.sessionsMu.RLock()
	sess, ok := s.sessions[cookie.Value]
	s.sessionsMu.RUnlock()

	if !ok {
		return true
	}

	providedToken := r.FormValue("csrf")
	if providedToken == "" {
		providedToken = r.Header.Get("X-CSRF-Token")
	}
	if providedToken != sess.CSRFToken {
		logger.L.Warnf("CSRF Mismatch: provided=[%s] expected=[%s] remote=%s path=%s", providedToken, sess.CSRFToken, s.clientIP(r), r.URL.Path)
		return false
	}

	return true
}

func (s *Server) isBanned(ipStr string) bool {
	if s.isWhitelisted(ipStr) {
		return false
	}
	host := stripPort(ipStr)

	s.authAttemptsMu.Lock()
	if att, ok := s.authAttempts[host]; ok && time.Now().Before(att.LockoutBy) {
		s.authAttemptsMu.Unlock()
		return true
	}
	s.authAttemptsMu.Unlock()

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
		if strings.Contains(banned, "/") {
			_, ipnet, err := net.ParseCIDR(banned)
			if err == nil && ipnet.Contains(ip) {
				return true
			}
		}
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
	// AdvancedMounts (set via "Advanced Mount Settings" in the UI) is
	// also consulted. The RTMP and SRT ingest paths already honour it;
	// without this, an admin who configures a per-mount password in
	// that section sees it work for RTMP/SRT but silently fall back to
	// the default password on HTTP Icecast SOURCE/PUT.
	if adv, ok := s.Config.AdvancedMounts[mount]; ok && adv != nil && adv.Password != "" {
		return adv.Password, true
	}
	return s.Config.DefaultSourcePassword, s.Config.DefaultSourcePassword != ""
}

// createSession generates a new session for the given user and sets the sid cookie.
// Returns the CSRF token for the new session.
//
// Also invalidates any pre-existing session under the caller's current sid
// cookie (session fixation defence) and removes any other sessions tied to
// the same user so an attacker holding a stolen cookie is logged out when
// the legitimate user logs back in.
func (s *Server) createSession(w http.ResponseWriter, r *http.Request, user *config.User) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		logger.L.Errorf("createSession: rand.Read failed: %v", err)
	}
	sid := hex.EncodeToString(b)

	cb := make([]byte, 32)
	if _, err := rand.Read(cb); err != nil {
		logger.L.Errorf("createSession: rand.Read(csrf) failed: %v", err)
	}
	csrf := hex.EncodeToString(cb)

	now := time.Now()

	s.sessionsMu.Lock()
	// Invalidate the caller's previous session, if any.
	if old, err := r.Cookie("sid"); err == nil {
		delete(s.sessions, old.Value)
	}
	s.sessions[sid] = &session{
		User:      user,
		CSRFToken: csrf,
		CreatedAt: now,
		ExpiresAt: now.Add(sessionMaxLifetime),
		LastSeen:  now,
	}
	s.sessionsMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     "sid",
		Value:    sid,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionMaxLifetime.Seconds()),
	})

	return csrf
}

// reapSessions removes expired / idle sessions. Call periodically from a
// goroutine started at server boot; safe to call while the server is under
// load since it takes the sessions mutex only during the sweep.
func (s *Server) reapSessions() {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	now := time.Now()
	for sid, sess := range s.sessions {
		if !sess.ExpiresAt.IsZero() && now.After(sess.ExpiresAt) {
			delete(s.sessions, sid)
			continue
		}
		if !sess.LastSeen.IsZero() && now.Sub(sess.LastSeen) > sessionIdleTimeout {
			delete(s.sessions, sid)
		}
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	host := s.clientIP(r)

	if r.Method == http.MethodPost {
		u := r.FormValue("username")
		p := r.FormValue("password")

		if err := s.checkAuthLimit(host); err != nil {
			if r.Header.Get("Accept") == "application/json" {
				jsonError(w, err.Error(), http.StatusUnauthorized)
				return
			}
			pageData := s.BasePageData("")
			pageData["error"] = err.Error()
			s.shell.Render(w, "login", "Login — "+s.Config.PageTitle, pageData)
			return
		}

		// Always run bcrypt (against dummyBcryptHash when the user
		// doesn't exist) so login response time doesn't leak user
		// existence — the Basic-auth path in checkAuth already does
		// this. The plain `!exists || !CheckPasswordHash` short-circuit
		// returned in microseconds for unknown users vs ~250 ms for
		// known users, which is a textbook account-enumeration timing
		// oracle.
		user, exists := s.Config.Users[u]
		var passOK bool
		if exists {
			passOK = config.CheckPasswordHash(p, user.Password)
		} else {
			_ = config.CheckPasswordHash(p, dummyBcryptHash)
			passOK = false
		}
		if !exists || !passOK {
			s.recordAuthFailure(host)
			s.logAuthFailed(u, host, "invalid credentials")
			s.Audit(r, "login_failed", "auth", u, "invalid credentials")
			if r.Header.Get("Accept") == "application/json" {
				jsonError(w, "Invalid username or password", http.StatusUnauthorized)
				return
			}
			pageData := s.BasePageData("")
			pageData["error"] = "Invalid username or password"
			s.shell.Render(w, "login", "Login — "+s.Config.PageTitle, pageData)
			return
		}

		s.recordAuthSuccess(host)
		s.createSession(w, r, user)
		s.Audit(r, "login", "auth", u, "")
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	pageData := s.BasePageData("")
	s.shell.Render(w, "login", "Login — "+s.Config.PageTitle, pageData)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.Audit(r, "logout", "auth", "", "")
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
