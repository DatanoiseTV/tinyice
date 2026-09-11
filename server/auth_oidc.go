package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
	oidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	githubOAuth "golang.org/x/oauth2/github"
)

var stateHMACKey []byte

func init() {
	stateHMACKey = make([]byte, 32)
	rand.Read(stateHMACKey)
}

func (s *Server) getOIDCProvider(id string) *config.OIDCProvider {
	for _, p := range s.Config.OIDCProviders {
		if p.ID == id && p.Enabled {
			return p
		}
	}
	return nil
}

func (s *Server) buildOAuth2Config(provider *config.OIDCProvider, r *http.Request) (*oauth2.Config, *oidc.Provider, error) {
	redirectURL := s.externalURL(r) + "/auth/" + provider.ID + "/callback"

	if provider.ID == "github" {
		return &oauth2.Config{
			ClientID:     provider.ClientID,
			ClientSecret: provider.ClientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{"user:email"},
			Endpoint:     githubOAuth.Endpoint,
		}, nil, nil
	}

	ctx := context.Background()
	oidcProvider, err := oidc.NewProvider(ctx, provider.DiscoveryURL)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to discover OIDC provider: %w", err)
	}

	return &oauth2.Config{
		ClientID:     provider.ClientID,
		ClientSecret: provider.ClientSecret,
		RedirectURL:  redirectURL,
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		Endpoint:     oidcProvider.Endpoint(),
	}, oidcProvider, nil
}

func generateOIDCState(providerID string) string {
	nonce := make([]byte, 16)
	rand.Read(nonce)
	expiry := time.Now().Add(10 * time.Minute).Unix()
	payload := fmt.Sprintf("%s:%d:%s", providerID, expiry, base64.RawURLEncoding.EncodeToString(nonce))
	mac := hmac.New(sha256.New, stateHMACKey)
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + ":" + sig
}

func verifyOIDCState(state string) (providerID string, ok bool) {
	parts := strings.SplitN(state, ":", 4)
	if len(parts) != 4 {
		return "", false
	}
	payload := parts[0] + ":" + parts[1] + ":" + parts[2]
	mac := hmac.New(sha256.New, stateHMACKey)
	mac.Write([]byte(payload))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(parts[3]), []byte(expectedSig)) {
		return "", false
	}
	var expiry int64
	fmt.Sscanf(parts[1], "%d", &expiry)
	if time.Now().Unix() > expiry {
		return "", false
	}
	return parts[0], true
}

func (s *Server) handleOIDCRedirect(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/auth/"), "/")
	providerID := parts[0]

	provider := s.getOIDCProvider(providerID)
	if provider == nil {
		http.NotFound(w, r)
		return
	}

	oauthConfig, _, err := s.buildOAuth2Config(provider, r)
	if err != nil {
		jsonError(w, "Provider configuration error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	state := generateOIDCState(providerID)

	// Nonce bound to this browser; the IdP will echo it back inside the
	// ID token so replay and login-CSRF are both blocked.
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		http.Error(w, "Failed to generate nonce", http.StatusInternalServerError)
		return
	}
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes)

	verifier := oauth2.GenerateVerifier()
	http.SetCookie(w, &http.Cookie{
		Name:     "pkce_verifier",
		Value:    verifier,
		Path:     "/auth/" + providerID,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
	// Bind the state value to this browser by stashing it in a cookie;
	// the callback refuses to proceed unless the query-string state
	// matches. Stops an attacker feeding their state+code pair to the
	// victim (classic login-CSRF).
	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_state",
		Value:    state,
		Path:     "/auth/" + providerID,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_nonce",
		Value:    nonce,
		Path:     "/auth/" + providerID,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})

	authURL := oauthConfig.AuthCodeURL(state,
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("nonce", nonce),
	)
	http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/auth/"), "/")
	if len(pathParts) < 2 {
		http.NotFound(w, r)
		return
	}
	providerID := pathParts[0]

	state := r.URL.Query().Get("state")
	verifiedProvider, ok := verifyOIDCState(state)
	if !ok || verifiedProvider != providerID {
		http.Error(w, "Invalid state parameter", http.StatusForbidden)
		return
	}
	// Bind state to the initiating browser: the state value we handed out
	// on redirect was stashed in a cookie; refuse if the callback's query
	// state doesn't match.
	stateCookie, err := r.Cookie("oidc_state")
	if err != nil || !hmac.Equal([]byte(stateCookie.Value), []byte(state)) {
		http.Error(w, "State cookie mismatch", http.StatusForbidden)
		return
	}

	provider := s.getOIDCProvider(providerID)
	if provider == nil {
		http.NotFound(w, r)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Missing authorization code", http.StatusBadRequest)
		return
	}

	verifierCookie, err := r.Cookie("pkce_verifier")
	if err != nil {
		http.Error(w, "Missing PKCE verifier", http.StatusBadRequest)
		return
	}
	nonceCookie, err := r.Cookie("oidc_nonce")
	if err != nil {
		http.Error(w, "Missing OIDC nonce", http.StatusBadRequest)
		return
	}

	// Invalidate the transient cookies immediately — state+PKCE+nonce are
	// all single-use.
	for _, name := range []string{"pkce_verifier", "oidc_state", "oidc_nonce"} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: "/auth/" + providerID, MaxAge: -1,
		})
	}

	oauthConfig, oidcProvider, err := s.buildOAuth2Config(provider, r)
	if err != nil {
		http.Error(w, "Provider configuration error", http.StatusInternalServerError)
		return
	}

	ctx := context.Background()
	token, err := oauthConfig.Exchange(ctx, code, oauth2.VerifierOption(verifierCookie.Value))
	if err != nil {
		logger.L.Warnw("OIDC token exchange failed", "provider", providerID, "error", err)
		http.Error(w, "Authentication failed", http.StatusUnauthorized)
		return
	}

	var email, name string

	if providerID == "github" {
		email, name, err = s.fetchGitHubUserInfo(ctx, token)
		if err != nil {
			http.Error(w, "Failed to get user info from GitHub", http.StatusInternalServerError)
			return
		}
	} else {
		rawIDToken, ok := token.Extra("id_token").(string)
		if !ok {
			http.Error(w, "Missing ID token", http.StatusInternalServerError)
			return
		}
		verifier := oidcProvider.Verifier(&oidc.Config{ClientID: provider.ClientID})
		idToken, err := verifier.Verify(ctx, rawIDToken)
		if err != nil {
			http.Error(w, "Invalid ID token", http.StatusUnauthorized)
			return
		}
		// Verify the nonce the IdP echoed back matches the one we set
		// on redirect — stops authorization-code replay.
		if idToken.Nonce == "" || !hmac.Equal([]byte(idToken.Nonce), []byte(nonceCookie.Value)) {
			http.Error(w, "ID token nonce mismatch", http.StatusUnauthorized)
			return
		}
		var claims struct {
			Email         string `json:"email"`
			EmailVerified *bool  `json:"email_verified,omitempty"`
			Name          string `json:"name"`
		}
		idToken.Claims(&claims)
		// Refuse to treat an unverified OIDC email as proof of identity —
		// otherwise any attacker with a matching-email account at an OIDC
		// provider can take over a local account.
		if claims.EmailVerified == nil || !*claims.EmailVerified {
			logger.L.Warnw("OIDC login rejected: email not verified by IdP",
				"provider", providerID, "email", claims.Email)
			http.Error(w, "Provider has not verified your email address", http.StatusForbidden)
			return
		}
		email = claims.Email
		name = claims.Name
	}

	if email == "" {
		http.Error(w, "No email returned by provider", http.StatusBadRequest)
		return
	}

	// Check if email matches an existing user
	for _, user := range s.Config.Users {
		for _, linkedEmail := range user.LinkedEmails {
			if strings.EqualFold(linkedEmail, email) {
				s.recordAuthSuccess(s.clientIP(r))
				s.createSession(w, r, user)
				logger.L.Infow("OIDC login successful", "user", user.Username, "provider", providerID, "email", email)
				s.Audit(r, "login", "user", user.Username, "oidc:"+providerID)
				http.Redirect(w, r, "/admin", http.StatusSeeOther)
				return
			}
		}
	}

	// Create pending user request
	s.configMu.Lock()

	for _, pu := range s.Config.PendingUsers {
		if strings.EqualFold(pu.Email, email) {
			if pu.DeniedAt != "" {
				deniedTime, _ := time.Parse(time.RFC3339, pu.DeniedAt)
				if time.Since(deniedTime) < 24*time.Hour {
					s.configMu.Unlock()
					s.renderAccessDenied(w, "Your access request was denied. You can try again later.")
					return
				}
				pu.DeniedAt = ""
				pu.RequestedAt = time.Now().Format(time.RFC3339)
				s.Config.SaveConfig()
				s.configMu.Unlock()
				s.renderAccessPending(w)
				return
			}
			pu.RequestedAt = time.Now().Format(time.RFC3339)
			s.Config.SaveConfig()
			s.configMu.Unlock()
			s.renderAccessPending(w)
			return
		}
	}

	pending := &config.PendingUser{
		ID:          uuid.New().String(),
		Email:       email,
		Name:        name,
		Provider:    providerID,
		RequestedAt: time.Now().Format(time.RFC3339),
	}
	s.Config.PendingUsers = append(s.Config.PendingUsers, pending)
	s.Config.SaveConfig()
	s.configMu.Unlock()

	logger.L.Infow("New pending user request", "email", email, "provider", providerID)
	go s.notifyAdminsNewPendingUser(pending)

	s.renderAccessPending(w)
}

func (s *Server) fetchGitHubUserInfo(ctx context.Context, token *oauth2.Token) (email, name string, err error) {
	client := oauth2.NewClient(ctx, oauth2.StaticTokenSource(token))

	resp, err := client.Get("https://api.github.com/user")
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	var profile struct {
		Name  string `json:"name"`
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return "", "", fmt.Errorf("decode github /user: %w", err)
	}
	name = profile.Name
	if name == "" {
		name = profile.Login
	}

	// Always fetch /user/emails — the primary email on /user is not
	// guaranteed to be verified, and GitHub will happily return any email
	// the user has added to their account. We only accept a primary AND
	// verified address, otherwise an attacker can add admin@victim.tld
	// as unverified-primary and take over a local account.
	resp2, err := client.Get("https://api.github.com/user/emails")
	if err != nil {
		return "", "", fmt.Errorf("fetch github /user/emails: %w", err)
	}
	defer resp2.Body.Close()
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&emails); err != nil {
		return "", "", fmt.Errorf("decode github /user/emails: %w", err)
	}
	for _, e := range emails {
		if e.Primary && e.Verified {
			email = e.Email
			break
		}
	}
	if email == "" {
		return "", "", fmt.Errorf("github account has no primary, verified email address")
	}
	return email, name, nil
}

func (s *Server) renderAccessPending(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!DOCTYPE html>
<html><head><title>Access Requested</title></head>
<body style="background:#111;color:#eee;font-family:monospace;display:flex;align-items:center;justify-content:center;height:100vh;margin:0">
<div style="text-align:center;max-width:400px">
<h2>Access Requested</h2>
<p>Your request has been submitted to the station administrator. You'll be able to log in once approved.</p>
<a href="/login" style="color:#ff6600">Back to Login</a>
</div></body></html>`))
}

func (s *Server) renderAccessDenied(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(fmt.Sprintf(`<!DOCTYPE html>
<html><head><title>Access Denied</title></head>
<body style="background:#111;color:#eee;font-family:monospace;display:flex;align-items:center;justify-content:center;height:100vh;margin:0">
<div style="text-align:center;max-width:400px">
<h2>Access Denied</h2>
<p>%s</p>
<a href="/login" style="color:#ff6600">Back to Login</a>
</div></body></html>`, msg)))
}

func (s *Server) handleOIDCProvidersList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	type publicProvider struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Icon string `json:"icon"`
	}
	var providers []publicProvider
	for _, p := range s.Config.OIDCProviders {
		if p.Enabled {
			providers = append(providers, publicProvider{ID: p.ID, Name: p.Name, Icon: p.Icon})
		}
	}
	if providers == nil {
		providers = []publicProvider{}
	}
	jsonResponse(w, providers)
}

// externalURL is the origin a browser reaches this server at, for
// building absolute URLs (OIDC redirect_uri). Deriving it from r.TLS and
// r.Host broke behind a TLS-terminating reverse proxy: TinyIce sees plain
// HTTP on an internal host, registers http://internal/... as the
// redirect_uri, and the identity provider rejects the login with
// redirect_uri mismatch. Precedence: the configured base_url; otherwise
// X-Forwarded-Proto/Host from a trusted proxy; otherwise the request.
func (s *Server) externalURL(r *http.Request) string {
	if b := strings.TrimRight(strings.TrimSpace(s.Config.BaseURL), "/"); b != "" {
		return b
	}
	scheme, host := "http", r.Host
	if r.TLS != nil {
		scheme = "https"
	}
	if s.isTrustedProxy(stripPort(r.RemoteAddr)) {
		if p := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))); p == "https" || p == "http" {
			scheme = p
		}
		if h := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); h != "" {
			host = h
		}
	}
	return scheme + "://" + host
}
