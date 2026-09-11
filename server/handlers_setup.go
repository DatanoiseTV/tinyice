package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
)

func generateRandomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "insecure_fallback"
	}
	return hex.EncodeToString(b)
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if s.Config.SetupComplete {
		http.NotFound(w, r)
		return
	}
	pageData := s.BasePageData("")
	s.shell.Render(w, "setup", "Setup — "+s.Config.PageTitle, pageData)
}

func (s *Server) handleSetupVerifyToken(w http.ResponseWriter, r *http.Request) {
	if s.Config.SetupComplete || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// Never accept an empty token: ConstantTimeCompare("", "") is 1, and an
	// empty server-side token is exactly what a mid-setup restart used to
	// leave behind.
	if s.setupToken == "" || req.Token == "" ||
		subtle.ConstantTimeCompare([]byte(req.Token), []byte(s.setupToken)) != 1 {
		jsonError(w, "Invalid setup token", http.StatusForbidden)
		return
	}

	jsonResponse(w, map[string]bool{"valid": true})
}

func (s *Server) handleSetupComplete(w http.ResponseWriter, r *http.Request) {
	if s.Config.SetupComplete || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	var req struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// Never accept an empty token: ConstantTimeCompare("", "") is 1, and an
	// empty server-side token is exactly what a mid-setup restart used to
	// leave behind.
	if s.setupToken == "" || req.Token == "" ||
		subtle.ConstantTimeCompare([]byte(req.Token), []byte(s.setupToken)) != 1 {
		jsonError(w, "Invalid setup token", http.StatusForbidden)
		return
	}

	if req.Username == "" || req.Password == "" {
		jsonError(w, "Username and password are required", http.StatusBadRequest)
		return
	}

	hashed, err := config.HashPassword(req.Password)
	if err != nil {
		jsonError(w, "Failed to hash password", http.StatusInternalServerError)
		return
	}

	s.configMu.Lock()
	defer s.configMu.Unlock()

	s.Config.AdminUser = req.Username
	s.Config.AdminPassword = hashed
	s.Config.SetupComplete = true
	s.Config.LockMaps()
	s.Config.Users[req.Username] = &config.User{
		Username: req.Username,
		Password: hashed,
		Role:     config.RoleSuperAdmin,
		Mounts:   make(map[string]string),
	}
	s.Config.UnlockMaps()

	defaultSourcePass := generateRandomString(12)
	liveMountPass := generateRandomString(12)
	hDefaultSource, _ := config.HashPassword(defaultSourcePass)
	hLiveMount, _ := config.HashPassword(liveMountPass)
	s.Config.DefaultSourcePassword = hDefaultSource
	s.Config.LockMaps()
	s.Config.Mounts["/live"] = hLiveMount
	s.Config.UnlockMaps()

	if err := s.Config.SaveConfig(); err != nil {
		jsonError(w, "Failed to save config", http.StatusInternalServerError)
		return
	}

	s.setupToken = ""

	logger.L.Infow("Setup completed", "admin_user", req.Username, "time", time.Now().Format(time.RFC3339))

	s.createSession(w, r, s.Config.Users[req.Username])
	s.Audit(r, "setup_complete", "server", "", req.Username)

	jsonResponse(w, map[string]any{
		"success":             true,
		"default_source_pass": defaultSourcePass,
		"live_mount_pass":     liveMountPass,
	})
}
