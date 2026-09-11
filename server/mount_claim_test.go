package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
)

// A non-superadmin could POST /api/streams for a mount that an AutoDJ,
// relay or transcoder already owns, because the "taken" check only
// looked at the plain mount table and other users' mounts. That gave
// them hasAccess() on the mount and with it pause/kick/reconfigure over
// infrastructure a superadmin set up.
func TestDJCannotClaimInfrastructureMounts(t *testing.T) {
	dj := &config.User{Username: "dj", Role: config.RoleAdmin, Mounts: map[string]string{}}
	s := &Server{
		Config: &config.Config{
			ConfigPath:  filepath.Join(t.TempDir(), "tinyice.json"),
			Users:       map[string]*config.User{"dj": dj},
			Mounts:      map[string]string{},
			AutoDJs:     []*config.AutoDJConfig{{Name: "dj1", Mount: "/autodj"}},
			Relays:      []*config.RelayConfig{{Mount: "/relay"}},
			Transcoders: []*config.TranscoderConfig{{InputMount: "/live", OutputMount: "/live-mp3"}},
		},
		sessions:     make(map[string]*session),
		authAttempts: make(map[string]*authAttempt),
		scanAttempts: make(map[string]*scanAttempt),
	}
	s.sessions["sid"] = &session{User: dj, CSRFToken: "tok", CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour), LastSeen: time.Now()}

	claim := func(mount string) int {
		r := httptest.NewRequest(http.MethodPost, "/api/streams",
			bytes.NewBufferString(`{"mount":"`+mount+`","password":"x"}`))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "sid", Value: "sid"})
		r.RemoteAddr = "198.51.100.9:1"
		w := httptest.NewRecorder()
		s.apiCreateStream(w, r)
		return w.Code
	}

	for _, m := range []string{"/autodj", "/relay", "/live-mp3"} {
		if code := claim(m); code != http.StatusConflict {
			t.Errorf("DJ claimed %s (status %d); want 409", m, code)
		}
		if _, got := dj.Mounts[m]; got {
			t.Errorf("DJ now has access to %s", m)
		}
	}
	if code := claim("/mine"); code != http.StatusOK {
		t.Errorf("DJ could not create an unused mount (status %d)", code)
	}
}
