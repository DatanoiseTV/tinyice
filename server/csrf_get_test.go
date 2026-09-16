package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
)

// The session cookie is SameSite=Lax, which browsers send on a top-level
// GET navigation from another site. Every legacy form handler reads its
// parameters via r.FormValue (query string included) and none checked
// r.Method, so with GET whitelisted in isCSRFSafe a cross-site link like
// /admin/add-user?username=evil&password=pw was a complete, token-free
// mutation. A mutating handler reached by GET must now be refused.
func TestMutatingHandlersRefuseGETWithSessionCookie(t *testing.T) {
	admin := &config.User{Username: "admin", Role: config.RoleSuperAdmin, Mounts: map[string]string{}}
	s := &Server{
		Config: &config.Config{
			ConfigPath: filepath.Join(t.TempDir(), "tinyice.json"),
			Users:      map[string]*config.User{"admin": admin},
			Mounts:     map[string]string{},
		},
		sessions:     make(map[string]*session),
		authAttempts: make(map[string]*authAttempt),
		scanAttempts: make(map[string]*scanAttempt),
	}
	s.sessions["sid1"] = &session{User: admin, CSRFToken: "tok", CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour), LastSeen: time.Now()}

	r := httptest.NewRequest(http.MethodGet, "/admin/add-user?username=evil&password=pw", nil)
	r.AddCookie(&http.Cookie{Name: "sid", Value: "sid1"})
	r.RemoteAddr = "198.51.100.9:1"
	w := httptest.NewRecorder()
	s.handleAddUser(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("GET with only a session cookie returned %d, want 403", w.Code)
	}
	if _, created := s.Config.Users["evil"]; created {
		t.Error("the GET created the user — CSRF via GET")
	}

	// The same request as a POST carrying the token must still work, so
	// the fix is about the method and not about the operator's session.
	r = httptest.NewRequest(http.MethodPost, "/admin/add-user?username=fine&password=pw", nil)
	r.AddCookie(&http.Cookie{Name: "sid", Value: "sid1"})
	r.Header.Set("X-CSRF-Token", "tok")
	r.RemoteAddr = "198.51.100.9:1"
	w = httptest.NewRecorder()
	s.handleAddUser(w, r)
	if _, created := s.Config.Users["fine"]; !created {
		t.Errorf("POST with CSRF token did not create the user (status %d)", w.Code)
	}
}

// checkAuth also accepts HTTP Basic, and /admin/metadata answers with a
// `WWW-Authenticate: Basic realm="TinyIce"` challenge, so a browser that
// has ever authenticated there re-attaches those credentials to every
// same-origin request — including one a third-party page triggers. With
// no `sid` cookie present, isCSRFSafe used to call that safe, so such a
// POST was a complete, token-free mutation.
func TestMutatingHandlersRefuseBasicAuthWithoutACSRFToken(t *testing.T) {
	hashed, err := config.HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	admin := &config.User{Username: "admin", Role: config.RoleSuperAdmin,
		Password: hashed, Mounts: map[string]string{}}
	s := &Server{
		Config: &config.Config{
			ConfigPath: filepath.Join(t.TempDir(), "tinyice.json"),
			Users:      map[string]*config.User{"admin": admin},
			Mounts:     map[string]string{},
		},
		sessions:     make(map[string]*session),
		authAttempts: make(map[string]*authAttempt),
		scanAttempts: make(map[string]*scanAttempt),
	}

	r := httptest.NewRequest(http.MethodPost, "/admin/add-user?username=evil&password=pw", nil)
	r.SetBasicAuth("admin", "hunter2")
	r.RemoteAddr = "198.51.100.10:1"
	w := httptest.NewRecorder()
	s.handleAddUser(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("Basic-authenticated POST with no CSRF token returned %d, want 403", w.Code)
	}
	if _, created := s.Config.Users["evil"]; created {
		t.Error("the request created the user — CSRF via cached Basic credentials")
	}
}

// A Bearer token cannot be attached by a cross-origin form, so token
// clients must keep working without a CSRF token.
func TestBearerRequestsStayCSRFSafe(t *testing.T) {
	s := &Server{Config: &config.Config{}}
	r := httptest.NewRequest(http.MethodPost, "/admin/add-user", nil)
	r.Header.Set("Authorization", "Bearer sometoken")
	if !s.isCSRFSafe(r) {
		t.Error("a Bearer-authenticated POST was refused")
	}
}
