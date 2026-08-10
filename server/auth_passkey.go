package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
	"github.com/go-webauthn/webauthn/webauthn"
)

// webAuthnUser adapts config.User to the webauthn.User interface.
type webAuthnUser struct {
	user *config.User
}

func (u *webAuthnUser) WebAuthnID() []byte {
	return []byte(u.user.Username)
}

func (u *webAuthnUser) WebAuthnName() string {
	return u.user.Username
}

func (u *webAuthnUser) WebAuthnDisplayName() string {
	return u.user.Username
}

func (u *webAuthnUser) WebAuthnCredentials() []webauthn.Credential {
	var creds []webauthn.Credential
	for _, pk := range u.user.Passkeys {
		raw, err := base64.StdEncoding.DecodeString(pk.RawCredential)
		if err != nil {
			continue
		}
		var cred webauthn.Credential
		if err := json.Unmarshal(raw, &cred); err != nil {
			continue
		}
		creds = append(creds, cred)
	}
	return creds
}

// --- Registration ---

func (s *Server) handlePasskeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if s.webAuthn == nil {
		jsonError(w, "WebAuthn not configured", http.StatusServiceUnavailable)
		return
	}

	wUser := &webAuthnUser{user: user}
	options, sessionData, err := s.webAuthn.BeginRegistration(wUser)
	if err != nil {
		jsonError(w, "Failed to begin registration: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.webauthnMu.Lock()
	s.webauthnSessions["reg:"+user.Username] = sessionData
	s.webauthnMu.Unlock()

	go func() {
		time.Sleep(60 * time.Second)
		s.webauthnMu.Lock()
		delete(s.webauthnSessions, "reg:"+user.Username)
		s.webauthnMu.Unlock()
	}()

	jsonResponse(w, options)
}

func (s *Server) handlePasskeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	sessionKey := "reg:" + user.Username
	s.webauthnMu.Lock()
	sessionData, ok2 := s.webauthnSessions[sessionKey]
	if ok2 {
		delete(s.webauthnSessions, sessionKey)
	}
	s.webauthnMu.Unlock()

	if !ok2 || sessionData == nil {
		jsonError(w, "No active registration session", http.StatusBadRequest)
		return
	}

	wUser := &webAuthnUser{user: user}
	credential, err := s.webAuthn.FinishRegistration(wUser, *sessionData, r)
	if err != nil {
		jsonError(w, "Registration failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	credJSON, _ := json.Marshal(credential)
	credB64 := base64.StdEncoding.EncodeToString(credJSON)

	name := r.URL.Query().Get("name")
	if name == "" {
		name = "Passkey"
	}

	pk := &config.PasskeyCredential{
		ID:            base64.RawURLEncoding.EncodeToString(credential.ID),
		RawCredential: credB64,
		Name:          name,
		CreatedAt:     time.Now().Format(time.RFC3339),
		LastUsed:      time.Now().Format(time.RFC3339),
	}

	s.configMu.Lock()
	user.Passkeys = append(user.Passkeys, pk)
	s.Config.SaveConfig()
	s.configMu.Unlock()

	logger.L.Infow("Passkey registered", "user", user.Username, "name", name)
	s.Audit(r, "passkey_registered", "user", user.Username, name)
	jsonResponse(w, map[string]any{"success": true, "name": name})
}

// --- Login ---

func (s *Server) handlePasskeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.webAuthn == nil {
		jsonError(w, "WebAuthn not configured", http.StatusServiceUnavailable)
		return
	}

	options, sessionData, err := s.webAuthn.BeginDiscoverableLogin()
	if err != nil {
		jsonError(w, "Failed to begin login: "+err.Error(), http.StatusInternalServerError)
		return
	}

	challengeKey := "login:" + sessionData.Challenge
	s.webauthnMu.Lock()
	s.webauthnSessions[challengeKey] = sessionData
	s.webauthnMu.Unlock()

	go func() {
		time.Sleep(60 * time.Second)
		s.webauthnMu.Lock()
		delete(s.webauthnSessions, challengeKey)
		s.webauthnMu.Unlock()
	}()

	jsonResponse(w, map[string]any{
		"publicKey":    options.Response,
		"challengeKey": challengeKey,
	})
}

func (s *Server) handlePasskeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	challengeKey := r.URL.Query().Get("challengeKey")

	findUser := func(rawID, userHandle []byte) (webauthn.User, error) {
		username := string(userHandle)
		user, exists := s.Config.Users[username]
		if !exists {
			return nil, fmt.Errorf("user not found")
		}
		return &webAuthnUser{user: user}, nil
	}

	s.webauthnMu.Lock()
	sessionData, ok := s.webauthnSessions[challengeKey]
	if ok {
		delete(s.webauthnSessions, challengeKey)
	}
	s.webauthnMu.Unlock()

	if !ok || sessionData == nil {
		jsonError(w, "No active login session", http.StatusBadRequest)
		return
	}

	credential, err := s.webAuthn.FinishDiscoverableLogin(findUser, *sessionData, r)
	if err != nil {
		jsonError(w, "Login failed: "+err.Error(), http.StatusUnauthorized)
		return
	}

	var loginUser *config.User
	for _, user := range s.Config.Users {
		for _, pk := range user.Passkeys {
			if pk.ID == base64.RawURLEncoding.EncodeToString(credential.ID) {
				loginUser = user
				s.configMu.Lock()
				pk.LastUsed = time.Now().Format(time.RFC3339)
				s.Config.SaveConfig()
				s.configMu.Unlock()
				break
			}
		}
		if loginUser != nil {
			break
		}
	}

	if loginUser == nil {
		jsonError(w, "User not found for credential", http.StatusUnauthorized)
		return
	}

	host := s.clientIP(r)
	s.recordAuthSuccess(host)
	s.createSession(w, r, loginUser)
	logger.L.Infow("Passkey login successful", "user", loginUser.Username, "ip", host)
	s.Audit(r, "login", "user", loginUser.Username, "passkey")

	jsonResponse(w, map[string]any{"success": true, "redirect": "/admin"})
}

// --- Delete ---

func (s *Server) handlePasskeyDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	passkeyID := r.URL.Query().Get("id")
	if passkeyID == "" {
		jsonError(w, "Missing passkey ID", http.StatusBadRequest)
		return
	}

	s.configMu.Lock()
	defer s.configMu.Unlock()

	found := false
	for i, pk := range user.Passkeys {
		if pk.ID == passkeyID {
			user.Passkeys = append(user.Passkeys[:i], user.Passkeys[i+1:]...)
			found = true
			break
		}
	}

	if !found {
		jsonError(w, "Passkey not found", http.StatusNotFound)
		return
	}

	s.Config.SaveConfig()
	logger.L.Infow("Passkey deleted", "user", user.Username, "passkey_id", passkeyID)
	jsonResponse(w, map[string]bool{"success": true})
}
