package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/relay"
)

// newAutoDJTestServer builds a Server with one superadmin session ("sid" /
// CSRF "tok") and the AutoDJ configs handed in, each already started.
func newAutoDJTestServer(t *testing.T, djs ...*config.AutoDJConfig) *Server {
	t.Helper()
	admin := &config.User{Username: "root", Role: config.RoleSuperAdmin}
	s := &Server{
		Config: &config.Config{
			ConfigPath: filepath.Join(t.TempDir(), "tinyice.json"),
			Users:      map[string]*config.User{"root": admin},
			AutoDJs:    djs,
		},
		Relay:        relay.NewRelay(false, nil),
		sessions:     make(map[string]*session),
		authAttempts: make(map[string]*authAttempt),
		scanAttempts: make(map[string]*scanAttempt),
	}
	s.StreamerM = relay.NewStreamerManager(s.Relay, s.Config)
	s.sessions["sid"] = &session{User: admin, CSRFToken: "tok", CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour), LastSeen: time.Now()}
	for _, dj := range djs {
		if _, err := s.StreamerM.StartStreamer(dj.Name, dj.Mount, dj.MusicDir, dj.Loop,
			dj.Format, dj.Bitrate, dj.InjectMetadata, dj.Playlist, dj.MPDEnabled, dj.MPDPort,
			dj.MPDPassword, dj.Visible, dj.LastPlaylist, dj.SongCommand, dj.SongCommandTimeout,
			dj.OnPlayCommand, dj.OnPlayCommandTimeout); err != nil {
			t.Fatalf("StartStreamer(%s): %v", dj.Mount, err)
		}
		t.Cleanup(func() { s.StreamerM.DeleteStreamer(dj.Mount) })
	}
	return s
}

func autoDJRequest(method, target, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.AddCookie(&http.Cookie{Name: "sid", Value: "sid"})
	r.Header.Set("X-CSRF-Token", "tok")
	r.RemoteAddr = "198.51.100.11:1"
	return r
}

// The metadata endpoint used to always flip the flag. The Studio posts the
// state its switch already shows, so any disagreement (a double click, two
// tabs) left the switch reading the opposite of reality. An explicit
// {"enabled": ...} must be obeyed.
func TestPlayerMetadataHonoursExplicitEnabled(t *testing.T) {
	dj := &config.AutoDJConfig{Name: "dj", Mount: "/a", MusicDir: t.TempDir(),
		Format: "mp3", Bitrate: 128, InjectMetadata: true}
	s := newAutoDJTestServer(t, dj)

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		s.handlePlayerMetadata(w, autoDJRequest(http.MethodPost, "/admin/player/metadata?mount=/a", `{"enabled":true}`))
		if w.Code != http.StatusOK {
			t.Fatalf("post %d: status %d, want 200", i, w.Code)
		}
		if got := s.StreamerM.GetStreamer("/a").GetStats().InjectMetadata; !got {
			t.Fatalf("post %d: inject_metadata = false, want true (the handler toggled instead of setting)", i)
		}
	}

	// And an empty body still toggles, for the legacy form post.
	w := httptest.NewRecorder()
	s.handlePlayerMetadata(w, autoDJRequest(http.MethodPost, "/admin/player/metadata?mount=/a", ""))
	if got := s.StreamerM.GetStreamer("/a").GetStats().InjectMetadata; got {
		t.Error("an empty body no longer toggles inject_metadata")
	}
}

// filepath.Base("") is ".", which the handler used to accept: it loaded
// nothing and then persisted "." as the AutoDJ's last_playlist, so the next
// restart tried to reload a directory entry.
func TestPlayerLoadPlaylistRequiresAFile(t *testing.T) {
	dj := &config.AutoDJConfig{Name: "dj", Mount: "/a", MusicDir: t.TempDir(),
		Format: "mp3", Bitrate: 128, LastPlaylist: "keep.pls"}
	s := newAutoDJTestServer(t, dj)

	w := httptest.NewRecorder()
	s.handlePlayerLoadPlaylist(w, autoDJRequest(http.MethodPost, "/admin/player/load-playlist?mount=/a", ""))
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if dj.LastPlaylist != "keep.pls" {
		t.Errorf("last_playlist = %q, want it untouched", dj.LastPlaylist)
	}
}

// The volume endpoint guessed the unit by range ("> 1 means percent"), so
// every value in 0..1 was ambiguous: the Studio's slider at 1% sent 1 and
// the AutoDJ jumped to full volume. An explicit unit must win.
func TestAutoDJVolumeHonoursTheUnit(t *testing.T) {
	dj := &config.AutoDJConfig{Name: "dj", Mount: "/a", MusicDir: t.TempDir(),
		Format: "mp3", Bitrate: 128}
	s := newAutoDJTestServer(t, dj)

	for _, tc := range []struct {
		body string
		want float64
	}{
		{`{"volume":1,"unit":"percent"}`, 0.01},
		{`{"volume":0.5,"unit":"fraction"}`, 0.5},
		{`{"volume":80,"unit":"percent"}`, 0.8},
		{`{"volume":80}`, 0.8}, // legacy guess still works
	} {
		w := httptest.NewRecorder()
		s.apiAutoDJVolume(w, autoDJRequest(http.MethodPost, "/api/v2/autodj/volume?mount=/a", tc.body))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d, want 200", tc.body, w.Code)
		}
		if got := s.StreamerM.GetStreamer("/a").GetVolume(); got != tc.want {
			t.Errorf("%s: volume = %v, want %v", tc.body, got, tc.want)
		}
	}
}

// The admin edit form doesn't submit mpd_enabled / visible / loop. Decoding
// those absent bools as false silently shut the AutoDJ's MPD server down
// and hid the mount on every unrelated edit.
func TestAutoDJUpdateLeavesOmittedFieldsAlone(t *testing.T) {
	dir := t.TempDir()
	dj := &config.AutoDJConfig{Name: "dj", Mount: "/a", MusicDir: dir, Format: "mp3",
		Bitrate: 128, Loop: true, InjectMetadata: true, Visible: true, MPDEnabled: true,
		MPDPort: "16601"}
	s := newAutoDJTestServer(t, dj)
	t.Cleanup(func() { s.StreamerM.DeleteStreamer("/a") })

	w := httptest.NewRecorder()
	body := `{"name":"renamed","music_dir":` + quote(dir) + `}`
	s.apiUpdateAutoDJ(w, autoDJRequest(http.MethodPut, "/api/v2/autodj?mount=/a", body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if dj.Name != "renamed" {
		t.Errorf("name = %q, want renamed", dj.Name)
	}
	for _, f := range []struct {
		name string
		got  bool
	}{
		{"loop", dj.Loop}, {"inject_metadata", dj.InjectMetadata},
		{"visible", dj.Visible}, {"mpd_enabled", dj.MPDEnabled},
	} {
		if !f.got {
			t.Errorf("%s was cleared by an update that never mentioned it", f.name)
		}
	}
	if dj.MPDPort != "16601" {
		t.Errorf("mpd_port = %q, want 16601", dj.MPDPort)
	}
}

// The update tore the old streamer down, saved the new config and only then
// tried to start it. A start failure (a taken MPD port) therefore left the
// mount with no AutoDJ at all and a config describing one that doesn't run.
func TestAutoDJUpdateRestoresThePreviousConfigOnAFailedRestart(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	a := &config.AutoDJConfig{Name: "a", Mount: "/a", MusicDir: dirA, Format: "mp3",
		Bitrate: 128, MPDEnabled: true, MPDPort: "16611", Visible: true}
	b := &config.AutoDJConfig{Name: "b", Mount: "/b", MusicDir: dirB, Format: "mp3",
		Bitrate: 128, Visible: true}
	s := newAutoDJTestServer(t, a, b)

	// Moving /b onto /a's MPD port is refused by StartStreamer.
	w := httptest.NewRecorder()
	body := `{"music_dir":` + quote(dirB) + `,"mpd_enabled":true,"mpd_port":"16611"}`
	s.apiUpdateAutoDJ(w, autoDJRequest(http.MethodPut, "/api/v2/autodj?mount=/b", body))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (the restart must fail): %s", w.Code, w.Body.String())
	}
	if s.StreamerM.GetStreamer("/b") == nil {
		t.Fatal("/b has no streamer after a failed update: the previous one was not restored")
	}
	if b.MPDEnabled || b.MPDPort == "16611" {
		t.Errorf("config for /b kept the rejected settings: mpd_enabled=%v port=%q", b.MPDEnabled, b.MPDPort)
	}
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"`
}
