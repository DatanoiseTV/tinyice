package server

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/DatanoiseTV/tinyice/logger"
)

// requireMountAccess wraps the common auth + mount-parameter + hasAccess
// pattern used by AutoDJ / playlist / queue / files API handlers. On failure
// it writes the appropriate JSON error and returns ok=false; callers should
// just `return` immediately.
//
// The mount is taken from the "mount" query parameter by default, or from
// mountOverride if non-empty (for the path-based routes).
func (s *Server) requireMountAccess(w http.ResponseWriter, r *http.Request, mountOverride string) (mount string, ok bool) {
	user, authed := s.checkAuth(r)
	if !authed {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return "", false
	}
	mount = mountOverride
	if mount == "" {
		mount = r.URL.Query().Get("mount")
	}
	if mount == "" {
		jsonError(w, "Mount is required", http.StatusBadRequest)
		return "", false
	}
	if !s.hasAccess(user, mount) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return "", false
	}
	return mount, true
}

// safeNextPath sanitises a post-login redirect target. Only same-origin
// absolute paths are accepted: anything with a scheme, an authority
// ("//evil.example" and its "/\evil.example" browser-equivalent) or a
// relative shape falls back to /admin. Without this the ?next= that
// /kiosk already emits would be an open redirect.
func safeNextPath(next string) string {
	const fallback = "/admin"
	if next == "" || next[0] != '/' {
		return fallback
	}
	if len(next) > 1 && (next[1] == '/' || next[1] == '\\') {
		return fallback
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return fallback
	}
	return next
}

// validateOutboundURL rejects URLs that we shouldn't allow users to point
// outbound HTTP clients at — loopback, RFC1918 private ranges, link-local,
// multicast, unspecified addresses. Used to keep webhook + relay URL fields
// from being turned into SSRF vectors.
//
// Hosts given as names are not resolved here (DNS rebinding would defeat
// that anyway); we check only literal IP addresses. Callers that want
// stronger protection should additionally wrap their http.Client with a
// DialContext that blocks internal ranges at connect time.
func validateOutboundURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("scheme %q not allowed (only http/https)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL has no host")
	}
	// If the host is a literal IP, refuse private / loopback / link-local / multicast.
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("URL points at a non-routable address (%s)", ip)
		}
	}
	// Block localhost by name — a common footgun.
	switch strings.ToLower(host) {
	case "localhost", "localhost.localdomain", "ip6-localhost":
		return fmt.Errorf("URL points at localhost")
	}
	return nil
}

func (s *Server) validatePathInMusicDir(musicDir, targetPath string) (string, error) {
	absMusicDir, err := filepath.Abs(musicDir)
	if err != nil {
		return "", fmt.Errorf("invalid music directory: %w", err)
	}

	absTargetPath, err := filepath.Abs(targetPath)
	if err != nil {
		return "", fmt.Errorf("invalid target path: %w", err)
	}

	rel, err := filepath.Rel(absMusicDir, absTargetPath)

	logger.L.Debugf("PATH_VALIDATION: absMusicDir=[%s] absTargetPath=[%s] rel=[%s]", absMusicDir, absTargetPath, rel)

	if err != nil {
		logger.L.Debugf("validatePathInMusicDir: filepath.Rel error: %v", err)
		return "", fmt.Errorf("path not within music directory: %w", err)
	}

	if strings.HasPrefix(rel, "..") || rel == ".." {
		logger.L.Warnf("PATH_VALIDATION_FAILED: Traversal detected. rel=[%s]", rel)
		return "", fmt.Errorf("security: path traversal attempt detected: %s", targetPath)
	}

	return absTargetPath, nil
}

func (s *Server) safeJoin(base, rel string) (string, error) {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}

	joined := filepath.Join(absBase, rel)

	validatedPath, err := s.validatePathInMusicDir(absBase, joined)
	if err != nil {
		return "", err
	}

	return validatedPath, nil
}

// mountTaken reports whether a mount is already owned by anything a user
// must not be able to claim over: another user's or the global mount
// table, an AutoDJ output, a relay, a transcoder output, or an advanced
// mount entry. The create-mount paths used to consult only the first two,
// so a DJ could register /autodj as their own mount, pass hasAccess for
// it, and then pause/kick/reconfigure the AutoDJ (or a relay/transcoder)
// that a superadmin set up.
func (s *Server) mountTaken(mount string) bool {
	if _, ok := s.Config.Mounts[mount]; ok {
		return true
	}
	for _, u := range s.Config.Users {
		if _, ok := u.Mounts[mount]; ok {
			return true
		}
	}
	for _, adj := range s.Config.AutoDJs {
		if adj != nil && adj.Mount == mount {
			return true
		}
	}
	for _, rl := range s.Config.Relays {
		if rl != nil && rl.Mount == mount {
			return true
		}
	}
	for _, tc := range s.Config.Transcoders {
		if tc != nil && (tc.OutputMount == mount || tc.InputMount == mount) {
			return true
		}
	}
	if _, ok := s.Config.AdvancedMounts[mount]; ok {
		return true
	}
	return false
}
