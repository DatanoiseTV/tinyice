package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/relay"
)

// The legacy /admin/player/* handlers checked only that the caller was
// logged in, never that they had access to the mount in the request —
// unlike the JSON API's requireMountAccess. A DJ with /a could drive,
// clear and browse /b. Every mount-taking handler must now refuse.
func TestPlayerHandlersEnforcePerMountAccess(t *testing.T) {
	dj := &config.User{Username: "dj", Role: config.RoleAdmin, Mounts: map[string]string{"/a": "x"}}
	s := &Server{
		Config: &config.Config{
			ConfigPath: filepath.Join(t.TempDir(), "tinyice.json"),
			Users:      map[string]*config.User{"dj": dj},
		},
		Relay:        relay.NewRelay(false, nil),
		sessions:     make(map[string]*session),
		authAttempts: make(map[string]*authAttempt),
		scanAttempts: make(map[string]*scanAttempt),
	}
	s.StreamerM = relay.NewStreamerManager(s.Relay, s.Config)
	s.sessions["sid"] = &session{User: dj, CSRFToken: "tok", CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour), LastSeen: time.Now()}

	handlers := map[string]http.HandlerFunc{
		"clear-playlist": s.handlePlayerClearPlaylist,
		"clear-queue":    s.handlePlayerClearQueue,
		"toggle":         s.handlePlayerToggle,
		"next":           s.handlePlayerNext,
		"restart":        s.handlePlayerRestart,
		"shuffle":        s.handlePlayerShuffle,
		"loop":           s.handlePlayerLoop,
		"scan":           s.handlePlayerScan,
		"reorder":        s.handlePlayerReorder,
		"queue":          s.handlePlayerQueue,
		"load-playlist":  s.handlePlayerLoadPlaylist,
		"save-playlist":  s.handlePlayerSavePlaylist,
		"metadata":       s.handlePlayerMetadata,
		"playlist-info":  s.handlePlayerPlaylistInfo,
		"files":          s.handlePlayerFiles,
	}
	for name, h := range handlers {
		for _, method := range []string{http.MethodPost, http.MethodGet} {
			r := httptest.NewRequest(method, "/admin/player/"+name+"?mount=/b", nil)
			r.AddCookie(&http.Cookie{Name: "sid", Value: "sid"})
			r.Header.Set("X-CSRF-Token", "tok")
			r.RemoteAddr = "198.51.100.9:1"
			w := httptest.NewRecorder()
			h(w, r)
			// Either refused outright (403), or refused as a method
			// mismatch (403 from isCSRFSafe on GET); never 200/303/404 —
			// 404 "streamer not found" would mean the access check came
			// after the mount lookup.
			if w.Code != http.StatusForbidden {
				t.Errorf("%s %s on a mount the DJ lacks: status %d, want 403", method, name, w.Code)
			}
		}
	}
}
