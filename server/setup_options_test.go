package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DatanoiseTV/tinyice/config"
)

const testSetupToken = "setup-token-for-tests"

func newSetupServer(t *testing.T) *Server {
	t.Helper()
	return &Server{
		Config: &config.Config{
			ConfigPath:   filepath.Join(t.TempDir(), "tinyice.json"),
			Users:        map[string]*config.User{},
			Mounts:       map[string]string{},
			PageTitle:    "TinyIce",
			PageSubtitle: "default subtitle",
			HostName:     "localhost",
			MaxListeners: config.DefaultMaxListeners,
		},
		setupToken: testSetupToken,
		sessions:   make(map[string]*session),
	}
}

// complete posts a /setup/complete body made of the fixed credentials plus the
// extra JSON members given (a fragment like `"station":{...}`).
func complete(t *testing.T, s *Server, extra string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	body := `{"token":"` + testSetupToken + `","username":"owner","password":"correct horse"`
	if extra != "" {
		body += "," + extra
	}
	body += "}"
	r := httptest.NewRequest(http.MethodPost, "/setup/complete", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "198.51.100.7:1"
	w := httptest.NewRecorder()
	s.handleSetupComplete(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func untouched(t *testing.T, s *Server, why string) {
	t.Helper()
	if s.Config.SetupComplete || len(s.Config.Users) != 0 || len(s.Config.Mounts) != 0 || s.setupToken != testSetupToken {
		t.Fatalf("%s: server changed despite the error (complete=%v users=%d mounts=%d tokenKept=%v)",
			why, s.Config.SetupComplete, len(s.Config.Users), len(s.Config.Mounts), s.setupToken == testSetupToken)
	}
	if s.Config.PageTitle != "TinyIce" || s.Config.MaxListeners != config.DefaultMaxListeners {
		t.Fatalf("%s: settings changed despite the error (title=%q max=%d)", why, s.Config.PageTitle, s.Config.MaxListeners)
	}
}

func TestSetupCompleteAppliesAnswersAndPersistsThem(t *testing.T) {
	s := newSetupServer(t)
	w, out := complete(t, s, `
		"station":{"title":"Radio Nord","subtitle":"Jazz all night","location":"Hamburg, DE","admin_email":"ops@example.org"},
		"address":{"hostname":"radio.example.org","base_url":"https://radio.example.org/"},
		"appearance":{"accent_color":"#1E90FF"},
		"audience":{"max_listeners":0,"directory_listing":true},
		"mount":{"path":"/nord"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if out["mount"] != "/nord" {
		t.Fatalf("mount = %v", out["mount"])
	}
	applied, _ := out["applied"].(map[string]any)
	for _, g := range []string{"station", "address", "appearance", "audience"} {
		if applied[g] != true {
			t.Errorf("applied[%s] = %v, want true", g, applied[g])
		}
	}

	// What a restart would load, not just the in-memory struct.
	got, err := config.LoadConfig(s.Config.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string][2]any{
		"title":     {got.PageTitle, "Radio Nord"},
		"subtitle":  {got.PageSubtitle, "Jazz all night"},
		"location":  {got.Location, "Hamburg, DE"},
		"email":     {got.AdminEmail, "ops@example.org"},
		"hostname":  {got.HostName, "radio.example.org"},
		"base_url":  {got.BaseURL, "https://radio.example.org"}, // trailing slash normalised
		"accent":    {got.AccentColor, "#1e90ff"},
		"directory": {got.DirectoryListing, true},
		"unlimited": {got.MaxListenersForAPI(), 0},
	}
	for name, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s: stored %v, want %v", name, c[0], c[1])
		}
	}
	if _, ok := got.Mounts["/nord"]; !ok {
		t.Errorf("mount /nord not stored: %v", got.Mounts)
	}
	if _, ok := got.Mounts["/live"]; ok {
		t.Errorf("default /live mount created although /nord was chosen")
	}
}

func TestSetupCompleteWithoutOptionsKeepsDefaults(t *testing.T) {
	s := newSetupServer(t)
	w, out := complete(t, s, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if out["mount"] != "/live" {
		t.Errorf("mount = %v, want /live", out["mount"])
	}
	applied, _ := out["applied"].(map[string]any)
	for g, v := range applied {
		if v != false {
			t.Errorf("applied[%s] = %v with no options sent", g, v)
		}
	}
	if s.Config.PageTitle != "TinyIce" || s.Config.MaxListeners != config.DefaultMaxListeners {
		t.Errorf("defaults changed: title=%q max=%d", s.Config.PageTitle, s.Config.MaxListeners)
	}
	// An empty group is not "applied" either.
	s2 := newSetupServer(t)
	_, out2 := complete(t, s2, `"station":{"title":"  "}`)
	if a, _ := out2["applied"].(map[string]any); a["station"] != false {
		t.Errorf("blank station group reported as applied: %v", a)
	}
}

// Every rejected answer must name its field and leave the server untouched.
func TestSetupCompleteRejectsBadAnswersAtomically(t *testing.T) {
	long := strings.Repeat("x", 300)
	cases := []struct{ name, extra, field string }{
		{"title too long", `"station":{"title":"` + long + `"}`, "station.title"},
		{"control char", `"station":{"subtitle":"a\u0007b"}`, "station.subtitle"},
		{"bad email", `"station":{"admin_email":"not an email"}`, "station.admin_email"},
		{"email with display name", `"station":{"admin_email":"Bob <bob@example.org>"}`, "station.admin_email"},
		{"hostname with scheme", `"address":{"hostname":"http://radio.example.org"}`, "address.hostname"},
		{"hostname with port", `"address":{"hostname":"radio.example.org:8000"}`, "address.hostname"},
		{"base url with path", `"address":{"base_url":"https://radio.example.org/app"}`, "address.base_url"},
		{"base url not http", `"address":{"base_url":"ftp://radio.example.org"}`, "address.base_url"},
		{"base url with userinfo", `"address":{"base_url":"https://u:p@radio.example.org"}`, "address.base_url"},
		{"accent not hex", `"appearance":{"accent_color":"orange"}`, "appearance.accent_color"},
		{"accent short", `"appearance":{"accent_color":"#fff"}`, "appearance.accent_color"},
		{"max listeners negative", `"audience":{"max_listeners":-1}`, "audience.max_listeners"},
		{"max listeners huge", `"audience":{"max_listeners":1000001}`, "audience.max_listeners"},
		{"mount without slash", `"mount":{"path":"live"}`, "mount.path"},
		{"mount with space", `"mount":{"path":"/my radio"}`, "mount.path"},
		{"mount nested", `"mount":{"path":"/a/b"}`, "mount.path"},
		{"mount reserved admin", `"mount":{"path":"/admin"}`, "mount.path"},
		{"mount reserved api", `"mount":{"path":"/API"}`, "mount.path"},
		// A valid group before an invalid one must not be applied either.
		{"valid then invalid", `"station":{"title":"Applied?"},"appearance":{"accent_color":"nope"}`, "appearance.accent_color"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSetupServer(t)
			w, out := complete(t, s, c.extra)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", w.Code, w.Body.String())
			}
			if msg, _ := out["error"].(string); !strings.HasPrefix(msg, c.field+":") {
				t.Fatalf("error %q does not start with %q", msg, c.field+":")
			}
			untouched(t, s, c.name)
		})
	}
}

func TestSetupCompleteEnforcesPasswordLength(t *testing.T) {
	s := newSetupServer(t)
	r := httptest.NewRequest(http.MethodPost, "/setup/complete",
		bytes.NewBufferString(`{"token":"`+testSetupToken+`","username":"owner","password":"short7!"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleSetupComplete(w, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "password:") {
		t.Fatalf("7-character password: status %d body %s", w.Code, w.Body.String())
	}
	untouched(t, s, "short password")
}
