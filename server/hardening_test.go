package server

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/relay"
)

func hardeningServer(t *testing.T, role string) (*Server, string) {
	t.Helper()
	u := &config.User{Username: "u", Role: role, Mounts: map[string]string{}}
	s := &Server{
		Config: &config.Config{
			ConfigPath: filepath.Join(t.TempDir(), "tinyice.json"),
			Users:      map[string]*config.User{"u": u},
			Mounts:     map[string]string{},
		},
		Relay:        relay.NewRelay(false, nil),
		sessions:     make(map[string]*session),
		authAttempts: make(map[string]*authAttempt),
		scanAttempts: make(map[string]*scanAttempt),
	}
	s.sessions["sid"] = &session{User: u, CSRFToken: "tok", CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour), LastSeen: time.Now()}
	return s, "sid"
}

func withSession(r *http.Request, sid string) *http.Request {
	r.AddCookie(&http.Cookie{Name: "sid", Value: sid})
	r.Header.Set("X-CSRF-Token", "tok")
	r.RemoteAddr = "198.51.100.9:1"
	return r
}

// Restarting the server is superadmin-only; it used to be any account.
func TestHotSwapRequiresSuperadmin(t *testing.T) {
	s, sid := hardeningServer(t, config.RoleAdmin)
	w := httptest.NewRecorder()
	s.handleHotSwap(w, withSession(httptest.NewRequest(http.MethodPost, "/admin/hotswap", nil), sid))
	if w.Code != http.StatusForbidden {
		t.Errorf("non-superadmin hotswap: status %d, want 403", w.Code)
	}
}

// The logo is served from this origin; an upload named logo.html with a
// script in it was stored XSS against every visitor. Role, extension and
// content are all checked now.
func TestLogoUploadRejectsNonImages(t *testing.T) {
	upload := func(s *Server, sid, filename string, content []byte) int {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, _ := mw.CreateFormFile("logo", filename)
		fw.Write(content)
		mw.Close()
		r := withSession(httptest.NewRequest(http.MethodPost, "/api/branding/logo", &body), sid)
		r.Header.Set("Content-Type", mw.FormDataContentType())
		w := httptest.NewRecorder()
		s.apiUploadLogo(w, r)
		return w.Code
	}
	sa, sid := hardeningServer(t, config.RoleSuperAdmin)
	orig, _ := filepath.Abs(".")
	_ = orig
	html := []byte("<html><script>fetch('/api/tokens')</script></html>")
	if code := upload(sa, sid, "logo.html", html); code == http.StatusOK {
		t.Error("logo.html was accepted")
	}
	if code := upload(sa, sid, "logo.png", html); code == http.StatusOK {
		t.Error("HTML bytes named .png were accepted")
	}
	dj, djsid := hardeningServer(t, config.RoleAdmin)
	png := append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, make([]byte, 64)...)
	if code := upload(dj, djsid, "logo.png", png); code != http.StatusForbidden {
		t.Errorf("non-superadmin upload: status %d, want 403", code)
	}
}

// A Bearer-authenticated request must not be able to mint tokens; an
// expiring token could otherwise mint a non-expiring one.
func TestBearerTokenCannotCreateTokens(t *testing.T) {
	s, _ := hardeningServer(t, config.RoleSuperAdmin)
	raw := "ti_deadbeef"
	s.Config.APITokens = []*config.APIToken{{TokenHash: hashToken(raw), Username: "u"}}
	r := httptest.NewRequest(http.MethodPost, "/api/tokens",
		strings.NewReader(`{"name":"forever","expires_at":""}`))
	r.Header.Set("Authorization", "Bearer "+raw)
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "198.51.100.9:1"
	w := httptest.NewRecorder()
	s.apiCreateToken(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("token minted a token: status %d, want 403", w.Code)
	}
	if len(s.Config.APITokens) != 1 {
		t.Errorf("token count = %d, want 1", len(s.Config.APITokens))
	}
}

// Icecast SOURCE password failures must count toward the lockout like
// every other credential check; they were unlimited.
func TestSourcePasswordFailuresLockOut(t *testing.T) {
	s, _ := hardeningServer(t, config.RoleSuperAdmin)
	hashed, _ := config.HashPassword("right")
	s.Config.DefaultSourcePassword = hashed
	s.Config.DisabledMounts = map[string]bool{}
	for i := 0; i < 6; i++ {
		r := httptest.NewRequest("SOURCE", "/live", nil)
		r.RemoteAddr = "203.0.113.5:4000"
		r.SetBasicAuth("source", "wrong")
		w := httptest.NewRecorder()
		s.handleSource(w, r)
	}
	if !s.isBanned("203.0.113.5") {
		t.Error("six failed SOURCE passwords did not lock the address out")
	}
}
