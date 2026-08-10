package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
)

// newSourceOfferServer builds the minimal Server needed to exercise the
// /webrtc/source-offer auth gate. Everything the gate touches is here;
// the handler never reaches WebRTCM because an unparseable SDP body
// short-circuits with 400 right after authorisation succeeds.
func newSourceOfferServer(t *testing.T) (*Server, *config.User, string, string) {
	t.Helper()

	hashed, err := config.HashPassword("s3cret-source")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	dj := &config.User{
		Username: "dj",
		Role:     config.RoleAdmin,
		Mounts:   map[string]string{"/live": hashed},
	}
	s := &Server{
		Config: &config.Config{
			Users:          map[string]*config.User{"dj": dj},
			DisabledMounts: map[string]bool{},
		},
		sessions:     make(map[string]*session),
		authAttempts: make(map[string]*authAttempt),
		scanAttempts: make(map[string]*scanAttempt),
	}

	sid, csrf := "sid-token", "csrf-token"
	s.sessions[sid] = &session{
		User:      dj,
		CSRFToken: csrf,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
		LastSeen:  time.Now(),
	}
	return s, dj, sid, csrf
}

func sourceOfferRequest(mount string) *http.Request {
	// Deliberately invalid SDP JSON: reaching the 400 proves the request
	// got past the auth gate, without needing a real peer connection.
	r := httptest.NewRequest(http.MethodPost, "/webrtc/source-offer?mount="+mount,
		strings.NewReader("not-json"))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "198.51.100.20:5000"
	return r
}

func TestWebRTCSourceOfferAuth(t *testing.T) {
	cases := []struct {
		name    string
		mount   string
		prepare func(r *http.Request, sid, csrf string)
		want    int
	}{
		{
			name:  "no credentials is rejected",
			mount: "/live",
			want:  http.StatusUnauthorized,
		},
		{
			name:  "wrong source password is rejected",
			mount: "/live",
			prepare: func(r *http.Request, _, _ string) {
				r.SetBasicAuth("source", "wrong")
			},
			want: http.StatusUnauthorized,
		},
		{
			name:  "correct source password is accepted",
			mount: "/live",
			prepare: func(r *http.Request, _, _ string) {
				r.SetBasicAuth("source", "s3cret-source")
			},
			want: http.StatusBadRequest,
		},
		{
			name:  "source password via query param is accepted",
			mount: "/live",
			prepare: func(r *http.Request, _, _ string) {
				q := r.URL.Query()
				q.Set("password", "s3cret-source")
				r.URL.RawQuery = q.Encode()
			},
			want: http.StatusBadRequest,
		},
		{
			// The browser "Go Live" page: session cookie + CSRF header,
			// no source password anywhere.
			name:  "admin session with CSRF token is accepted",
			mount: "/live",
			prepare: func(r *http.Request, sid, csrf string) {
				r.AddCookie(&http.Cookie{Name: "sid", Value: sid})
				r.Header.Set("X-CSRF-Token", csrf)
			},
			want: http.StatusBadRequest,
		},
		{
			name:  "admin session without CSRF token is rejected",
			mount: "/live",
			prepare: func(r *http.Request, sid, _ string) {
				r.AddCookie(&http.Cookie{Name: "sid", Value: sid})
			},
			want: http.StatusForbidden,
		},
		{
			name:  "admin session cannot publish to a mount it lacks access to",
			mount: "/other",
			prepare: func(r *http.Request, sid, csrf string) {
				r.AddCookie(&http.Cookie{Name: "sid", Value: sid})
				r.Header.Set("X-CSRF-Token", csrf)
			},
			want: http.StatusForbidden,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, sid, csrf := newSourceOfferServer(t)
			r := sourceOfferRequest(tc.mount)
			if tc.prepare != nil {
				tc.prepare(r, sid, csrf)
			}
			w := httptest.NewRecorder()
			s.handleWebRTCSourceOffer(w, r)
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d (body %q)", w.Code, tc.want,
					strings.TrimSpace(w.Body.String()))
			}
		})
	}
}

// A superadmin has access to every mount, including ones not listed in
// their Mounts map.
func TestWebRTCSourceOfferSuperadminAnyMount(t *testing.T) {
	s, user, sid, csrf := newSourceOfferServer(t)
	user.Role = config.RoleSuperAdmin

	r := sourceOfferRequest("/anything")
	r.AddCookie(&http.Cookie{Name: "sid", Value: sid})
	r.Header.Set("X-CSRF-Token", csrf)
	w := httptest.NewRecorder()
	s.handleWebRTCSourceOffer(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d (auth should have passed)", w.Code, http.StatusBadRequest)
	}
}

// A disabled mount stays closed even for a fully authorised publisher.
func TestWebRTCSourceOfferDisabledMount(t *testing.T) {
	s, _, sid, csrf := newSourceOfferServer(t)
	s.Config.DisabledMounts["/live"] = true

	r := sourceOfferRequest("/live")
	r.AddCookie(&http.Cookie{Name: "sid", Value: sid})
	r.Header.Set("X-CSRF-Token", csrf)
	w := httptest.NewRecorder()
	s.handleWebRTCSourceOffer(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}
