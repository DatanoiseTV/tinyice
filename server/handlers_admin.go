package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
	"github.com/DatanoiseTV/tinyice/relay"
)

// handleKiosk renders the studio / NOC wall display. Same auth as
// /admin (admin session required) but the frontend skips all the
// admin chrome (sidebar, tabs, action buttons) and shows a single
// full-screen layout with a big LTC-style clock, the listener map
// and per-mount stream cards. Page data is the slim subset the
// kiosk component reads from window.__TINYICE__.
func (s *Server) handleKiosk(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		http.Redirect(w, r, "/login?next=/kiosk", http.StatusSeeOther)
		return
	}
	pageData := s.BasePageData("")
	pageData["user"] = map[string]interface{}{
		"username": user.Username,
		"role":     user.Role,
	}
	s.shell.Render(w, "kiosk", "Kiosk — "+s.Config.PageTitle, pageData)
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

	pageData := s.BasePageData(csrf)
	pageData["user"] = map[string]interface{}{
		"username": user.Username,
		"role":     user.Role,
	}
	pageData["mounts"] = allMounts
	s.shell.Render(w, "admin", "Admin — "+s.Config.PageTitle, pageData)
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
		s.Config.LockMaps()
		delete(s.Config.FallbackMounts, mount)
		s.Config.UnlockMaps()
	} else {
		if fallback[0] != '/' {
			fallback = "/" + fallback
		}
		s.Config.LockMaps()
		s.Config.FallbackMounts[mount] = fallback
		s.Config.UnlockMaps()
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
	if !s.hasAccess(user, mount) && s.mountTaken(mount) {
		http.Error(w, "Mount taken", http.StatusConflict)
		return
	}
	hashed, err := config.HashPassword(password)
	if err != nil {
		http.Error(w, "Failed to hash password: "+err.Error(), http.StatusBadRequest)
		return
	}
	if user.Role == config.RoleSuperAdmin {
		s.Config.LockMaps()
		s.Config.Mounts[mount] = hashed
		s.Config.UnlockMaps()
	} else {
		s.Config.LockMaps()
		user.Mounts[mount] = hashed
		s.Config.UnlockMaps()
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
	s.Config.LockMaps()
	delete(s.Config.Mounts, mount)
	s.Config.UnlockMaps()
	s.Config.LockMaps()
	delete(s.Config.DisabledMounts, mount)
	s.Config.UnlockMaps()
	s.Config.LockMaps()
	delete(s.Config.VisibleMounts, mount)
	s.Config.UnlockMaps()
	s.Config.LockMaps()
	delete(user.Mounts, mount)
	s.Config.UnlockMaps()
	s.Relay.RemoveStream(mount)
	s.Config.SaveConfig()
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleToggleLatency(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
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
	// Only reject session-cookie POSTs without CSRF; the Basic-auth path
	// below targets Icecast-compatible source clients that don't carry a
	// session, so we let isCSRFSafe allow those through.
	if r.Method != http.MethodGet && !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	// GET is the Icecast-compatible `?mode=updinfo` path used by source
	// clients with Basic auth. It must keep working on GET, so it must
	// NOT honour the session cookie there — otherwise a cross-site link
	// carries the operator's cookie into a state change with no CSRF
	// token. Session users update metadata through the JSON API.
	var user *config.User
	ok := false
	if r.Method != http.MethodGet {
		user, ok = s.checkAuth(r)
	}
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
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	mount := r.FormValue("mount")
	if ok && s.hasAccess(user, mount) {
		s.Relay.RemoveStream(mount)
		s.Audit(r, "stream_kicked", "stream", mount, "")
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleKickAllListeners(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if ok && user.Role == config.RoleSuperAdmin {
		s.Relay.DisconnectAllListeners()
		s.Audit(r, "listeners_kicked_all", "server", "", "")
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleToggleMount(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	mount := r.FormValue("mount")
	if ok && s.hasAccess(user, mount) {
		s.Config.LockMaps()
		s.Config.DisabledMounts[mount] = !s.Config.DisabledMounts[mount]
		s.Config.UnlockMaps()
		if s.Config.DisabledMounts[mount] {
			s.Relay.RemoveStream(mount)
			s.Audit(r, "mount_disabled", "stream", mount, "")
		} else {
			s.Audit(r, "mount_enabled", "stream", mount, "")
		}
		s.Config.SaveConfig()
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleToggleVisible(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		return
	}
	mount := r.FormValue("mount")
	if ok && s.hasAccess(user, mount) {
		s.Config.LockMaps()
		s.Config.VisibleMounts[mount] = !s.Config.VisibleMounts[mount]
		s.Config.UnlockMaps()
		if st, ok := s.Relay.GetStream(mount); ok {
			st.SetVisible(s.Config.VisibleMounts[mount])
		}
		s.Config.SaveConfig()
		logger.L.Infow("Admin toggled visibility", "mount", mount, "visible", s.Config.VisibleMounts[mount])
		action := "mount_hidden"
		if s.Config.VisibleMounts[mount] {
			action = "mount_made_visible"
		}
		s.Audit(r, action, "stream", mount, "")
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleAddUser(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if ok && user.Role == config.RoleSuperAdmin {
		un, pw := r.FormValue("username"), r.FormValue("password")
		if un != "" && pw != "" {
			hp, err := config.HashPassword(pw)
			if err != nil {
				http.Error(w, "Failed to hash password: "+err.Error(), http.StatusBadRequest)
				return
			}
			s.Config.LockMaps()
			s.Config.Users[un] = &config.User{Username: un, Password: hp, Role: config.RoleAdmin, Mounts: make(map[string]string)}
			s.Config.UnlockMaps()
			s.Config.SaveConfig()
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleRemoveUser(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if ok && user.Role == config.RoleSuperAdmin {
		un := r.FormValue("username")
		if un != user.Username {
			s.Config.LockMaps()
			delete(s.Config.Users, un)
			s.Config.UnlockMaps()
			s.sessionsMu.Lock()
			for sid, sess := range s.sessions {
				if sess.User != nil && sess.User.Username == un {
					delete(s.sessions, sid)
				}
			}
			s.sessionsMu.Unlock()
			s.Config.SaveConfig()
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleAddBannedIP(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
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
		http.Error(w, "Forbidden", http.StatusForbidden)
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

func (s *Server) handleAddWhitelistedIP(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok || user.Role != config.RoleSuperAdmin {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ip := r.FormValue("ip")
	if ip == "" {
		http.Error(w, "IP address cannot be empty", http.StatusBadRequest)
		return
	}

	// Check for duplicates
	for _, existingIP := range s.Config.WhitelistedIPs {
		if existingIP == ip {
			http.Error(w, "IP address already in the whitelist", http.StatusConflict)
			return
		}
	}

	s.Config.WhitelistedIPs = append(s.Config.WhitelistedIPs, ip)
	// For consistency, sort the list after adding
	sort.Strings(s.Config.WhitelistedIPs)
	s.Config.SaveConfig()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"ip": ip, "status": "added"})
}

func (s *Server) handleRemoveWhitelistedIP(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok || user.Role != config.RoleSuperAdmin {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	ip := r.FormValue("ip")
	if ip == "" {
		http.Error(w, "IP address cannot be empty", http.StatusBadRequest)
		return
	}

	found := false
	for i, b := range s.Config.WhitelistedIPs {
		if b == ip {
			s.Config.WhitelistedIPs = append(s.Config.WhitelistedIPs[:i], s.Config.WhitelistedIPs[i+1:]...)
			s.Config.SaveConfig()
			found = true
			break
		}
	}

	if !found {
		http.Error(w, "IP not found in whitelist", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"ip": ip, "status": "removed"})
}

func (s *Server) handleClearAuthLockout(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	// Restrict to admin / superadmin: clearing IP-based lockouts is
	// a security-sensitive operation (re-enables an attacker that the
	// rate-limiter has temporarily blocked). A DJ-role user has no
	// business undoing it.
	if !ok || (user.Role != config.RoleSuperAdmin && user.Role != config.RoleAdmin) {
		http.Error(w, "Forbidden", http.StatusForbidden)
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
	user, ok := s.checkAuth(r)
	if !ok || (user.Role != config.RoleSuperAdmin && user.Role != config.RoleAdmin) {
		http.Error(w, "Forbidden", http.StatusForbidden)
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
	user, ok := s.checkAuth(r)
	if !ok || user.Role != config.RoleSuperAdmin {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	url := r.FormValue("url")
	events := r.Form["events"]

	if url == "" || len(events) == 0 {
		http.Error(w, "URL and at least one event required", http.StatusBadRequest)
		return
	}
	if err := validateOutboundURL(url); err != nil {
		http.Error(w, "Webhook URL rejected: "+err.Error(), http.StatusBadRequest)
		return
	}

	s.Config.Webhooks = append(s.Config.Webhooks, &config.WebhookConfig{
		ID:      config.NewWebhookID(),
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
	user, ok := s.checkAuth(r)
	if !ok || user.Role != config.RoleSuperAdmin {
		http.Error(w, "Forbidden", http.StatusForbidden)
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

	sort.Slice(authStats, func(i, j int) bool { return authStats[i].Count > authStats[j].Count })
	sort.Slice(scanStats, func(i, j int) bool { return scanStats[i].Count > scanStats[j].Count })

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"auth_fails": authStats,
		"scanners":   scanStats,
	})
}

func (s *Server) handleHotSwap(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	if err := s.HotSwap(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
