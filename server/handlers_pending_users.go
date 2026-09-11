package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
)

func (s *Server) handleGetPendingUsers(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok || user.Role != config.RoleSuperAdmin {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var active []*config.PendingUser
	for _, p := range s.Config.PendingUsers {
		if p.DeniedAt == "" {
			active = append(active, p)
		}
	}
	if active == nil {
		active = []*config.PendingUser{}
	}
	jsonResponse(w, active)
}

func (s *Server) handleApprovePendingUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// CSRF check: every other api_v2 mutating handler does this. Without it
	// an attacker page that the admin visits could submit a form-encoded
	// POST whose body json.Decoder still parses (Go's decoder doesn't gate
	// on Content-Type), promoting an attacker-controlled pending user to
	// superadmin. /api/ + Content-Type: application/json is implicitly
	// CSRF-safe (CORS preflights non-simple types) so legitimate JSON
	// callers from the admin UI keep working.
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok || user.Role != config.RoleSuperAdmin {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.Username == "" {
		jsonError(w, "Username is required", http.StatusBadRequest)
		return
	}

	role := req.Role
	if role == "" {
		role = config.RoleDJ
	}
	if role != config.RoleSuperAdmin && role != config.RoleAdmin && role != config.RoleDJ {
		jsonError(w, "Invalid role", http.StatusBadRequest)
		return
	}

	s.configMu.Lock()
	defer s.configMu.Unlock()

	var pending *config.PendingUser
	var pendingIdx int
	for i, p := range s.Config.PendingUsers {
		if p.ID == req.ID {
			pending = p
			pendingIdx = i
			break
		}
	}

	if pending == nil {
		jsonError(w, "Pending user not found", http.StatusNotFound)
		return
	}

	if _, exists := s.Config.Users[req.Username]; exists {
		jsonError(w, "Username already taken", http.StatusConflict)
		return
	}

	newUser := &config.User{
		Username:     req.Username,
		Password:     "",
		Role:         role,
		Mounts:       make(map[string]string),
		LinkedEmails: []string{pending.Email},
	}
	s.Config.LockMaps()
	s.Config.Users[req.Username] = newUser
	s.Config.UnlockMaps()

	s.Config.PendingUsers = append(s.Config.PendingUsers[:pendingIdx], s.Config.PendingUsers[pendingIdx+1:]...)
	s.Config.SaveConfig()

	logger.L.Infow("Pending user approved", "email", pending.Email, "username", req.Username, "role", role, "approved_by", user.Username)
	jsonResponse(w, map[string]any{"success": true, "username": req.Username})
	s.Audit(r, "pending_approved", "user", req.Username, pending.Email)
}

func (s *Server) handleDenyPendingUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok || user.Role != config.RoleSuperAdmin {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	s.configMu.Lock()
	defer s.configMu.Unlock()

	for _, p := range s.Config.PendingUsers {
		if p.ID == req.ID {
			p.DeniedAt = time.Now().Format(time.RFC3339)
			s.Config.SaveConfig()
			logger.L.Infow("Pending user denied", "email", p.Email, "denied_by", user.Username)
			jsonResponse(w, map[string]bool{"success": true})
			s.Audit(r, "pending_denied", "user", p.Email, "")
			return
		}
	}

	jsonError(w, "Pending user not found", http.StatusNotFound)
}
