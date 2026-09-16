package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/relay"
)

// /kiosk has always redirected unauthenticated visitors to
// /login?next=/kiosk, but nothing consumed the parameter: every login
// landed on /admin and the user had to navigate back by hand. The
// redirect must also refuse anything that isn't a same-origin path, or
// the parameter becomes an open redirect.
func TestLoginHonoursNextAndRefusesOffSiteTargets(t *testing.T) {
	hashed, err := config.HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	newServer := func() *Server {
		return &Server{
			Config: &config.Config{
				Users: map[string]*config.User{
					"dj": {Username: "dj", Role: config.RoleAdmin, Password: hashed},
				},
			},
			Relay:        relay.NewRelay(false, nil),
			sessions:     make(map[string]*session),
			authAttempts: make(map[string]*authAttempt),
			scanAttempts: make(map[string]*scanAttempt),
		}
	}

	for _, tc := range []struct {
		next string
		want string
	}{
		{"/kiosk", "/kiosk"},
		{"/admin/settings?tab=branding", "/admin/settings?tab=branding"},
		{"", "/admin"},
		{"//evil.example/", "/admin"},
		{"/\\evil.example/", "/admin"},
		{"https://evil.example/", "/admin"},
		{"admin", "/admin"},
	} {
		form := url.Values{"username": {"dj"}, "password": {"hunter2"}, "next": {tc.next}}
		r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.RemoteAddr = "198.51.100.21:1"
		w := httptest.NewRecorder()
		newServer().handleLogin(w, r)

		if w.Code != http.StatusSeeOther {
			t.Fatalf("next=%q: status %d, want 303", tc.next, w.Code)
		}
		if got := w.Header().Get("Location"); got != tc.want {
			t.Errorf("next=%q: Location = %q, want %q", tc.next, got, tc.want)
		}
	}
}
