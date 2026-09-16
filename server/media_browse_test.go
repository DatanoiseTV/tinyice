package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
)

// newBrowseServer builds a server whose only media root is dir, with a
// superadmin session "sid" and a plain admin session "djsid".
// tempDir returns a symlink-free temporary directory. t.TempDir() on macOS
// sits under /var, which is a link to /private/var; leaving that in place
// makes every path in a test depend on symlink resolution, which masks
// which row is fencing what.
func tempDir(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func newBrowseServer(t *testing.T, roots ...string) *Server {
	t.Helper()
	admin := &config.User{Username: "root", Role: config.RoleSuperAdmin}
	dj := &config.User{Username: "dj", Role: config.RoleAdmin, Mounts: map[string]string{"/a": "x"}}
	s := &Server{
		Config: &config.Config{
			ConfigPath: filepath.Join(t.TempDir(), "tinyice.json"),
			Users:      map[string]*config.User{"root": admin, "dj": dj},
			MediaRoots: roots,
		},
		sessions:     make(map[string]*session),
		authAttempts: make(map[string]*authAttempt),
		scanAttempts: make(map[string]*scanAttempt),
	}
	now := time.Now()
	s.sessions["sid"] = &session{User: admin, CSRFToken: "tok", CreatedAt: now,
		ExpiresAt: now.Add(time.Hour), LastSeen: now}
	s.sessions["djsid"] = &session{User: dj, CSRFToken: "tok", CreatedAt: now,
		ExpiresAt: now.Add(time.Hour), LastSeen: now}
	return s
}

func browse(t *testing.T, s *Server, sid, path string) (*httptest.ResponseRecorder, browseResponse) {
	t.Helper()
	target := "/api/autodj/browse"
	if path != "" {
		target += "?path=" + path
	}
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.AddCookie(&http.Cookie{Name: "sid", Value: sid})
	r.RemoteAddr = "198.51.100.30:1"
	w := httptest.NewRecorder()
	s.apiBrowseMediaDirs(w, r)
	var resp browseResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v (body %s)", err, w.Body.String())
		}
	}
	return w, resp
}

// The whole point of the browser is that it cannot be walked out of.
// Every one of these must be refused, whatever the shape of the escape.
func TestBrowserRefusesEverythingOutsideTheMediaRoots(t *testing.T) {
	root := tempDir(t)
	outside := tempDir(t)
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "rock"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A symlink inside the root pointing out of it must not be followed.
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	s := newBrowseServer(t, root)

	for _, bad := range []string{
		filepath.Join(root, ".."),
		filepath.Join(root, "..", ".."),
		filepath.Join(root, "rock", "..", "..", filepath.Base(outside)),
		outside,
		"/etc",
		"/",
		filepath.Join(root, "escape"),
	} {
		w, _ := browse(t, s, "sid", bad)
		if w.Code != http.StatusForbidden {
			t.Errorf("path %q returned %d, want 403", bad, w.Code)
		}
	}

	// The root itself and a real child stay reachable, so the refusals
	// above are the confinement and not a broken handler.
	for _, good := range []string{root, filepath.Join(root, "rock")} {
		if w, _ := browse(t, s, "sid", good); w.Code != http.StatusOK {
			t.Errorf("path %q returned %d, want 200", good, w.Code)
		}
	}
}

// A root has nowhere above it, and the UI must not be handed a parent it
// would then be refused for following.
func TestBrowserReportsNoParentAtARoot(t *testing.T) {
	root := tempDir(t)
	if err := os.MkdirAll(filepath.Join(root, "rock", "80s"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := newBrowseServer(t, root)

	_, atRoot := browse(t, s, "sid", root)
	if atRoot.Parent != "" {
		t.Errorf("parent at a root = %q, want empty", atRoot.Parent)
	}
	_, deeper := browse(t, s, "sid", filepath.Join(root, "rock", "80s"))
	if want := filepath.Join(root, "rock"); deeper.Parent != want {
		t.Errorf("parent = %q, want %q", deeper.Parent, want)
	}
}

// Track counts are what let an operator recognise the library without
// opening every candidate, and files must never be offered as entries.
func TestBrowserListsDirectoriesWithTrackCounts(t *testing.T) {
	root := tempDir(t)
	rock := filepath.Join(root, "rock")
	if err := os.MkdirAll(rock, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.mp3", "b.flac", "c.txt", ".hidden.mp3"} {
		if err := os.WriteFile(filepath.Join(rock, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := newBrowseServer(t, root)

	_, resp := browse(t, s, "sid", root)
	if len(resp.Entries) != 1 || resp.Entries[0].Name != "rock" {
		t.Fatalf("entries = %+v, want just rock (dot directories hidden)", resp.Entries)
	}
	if resp.Entries[0].TrackCount != 2 {
		t.Errorf("rock track_count = %d, want 2 (.mp3 + .flac, not .txt)", resp.Entries[0].TrackCount)
	}

	_, inRock := browse(t, s, "sid", rock)
	if len(inRock.Entries) != 0 {
		t.Errorf("entries in a leaf = %+v, want none: files are not selectable", inRock.Entries)
	}
	if inRock.TrackCount != 2 {
		t.Errorf("track_count = %d, want 2", inRock.TrackCount)
	}
}

// Picking a music directory is part of creating an AutoDJ, which is
// superadmin-only because it can register shell commands. A DJ listing
// the filesystem would be a straight information disclosure.
func TestBrowserIsSuperadminOnly(t *testing.T) {
	root := tempDir(t)
	s := newBrowseServer(t, root)

	if w, _ := browse(t, s, "djsid", root); w.Code != http.StatusForbidden {
		t.Errorf("admin (non-superadmin) got %d, want 403", w.Code)
	}

	r := httptest.NewRequest(http.MethodGet, "/api/autodj/browse", nil)
	r.RemoteAddr = "198.51.100.31:1"
	w := httptest.NewRecorder()
	s.apiBrowseMediaDirs(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous got %d, want 401", w.Code)
	}
}

// With no media_roots configured the browser must still be useful on an
// install that already works, without exposing anything the operator has
// not already pointed the server at.
func TestBrowsableRootsDefaultToTheConfiguredAutoDJs(t *testing.T) {
	music := tempDir(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	c := &config.Config{AutoDJs: []*config.AutoDJConfig{
		{Mount: "/a", MusicDir: music},
		{Mount: "/b", MusicDir: music},             // duplicate
		{Mount: "/c", MusicDir: "/nope/not/a/dir"}, // does not exist
		{Mount: "/d", MusicDir: ""},                // command-driven AutoDJ
	}}
	roots := c.BrowsableRoots()

	resolved := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	want := map[string]bool{resolved(cwd): true, resolved(music): true}
	if len(roots) != len(want) {
		t.Fatalf("roots = %v, want exactly %v (deduped, existing only)", roots, want)
	}
	for _, r := range roots {
		if !want[r] {
			t.Errorf("unexpected root %q", r)
		}
	}

	// An explicit media_roots replaces the defaults rather than adding.
	c.MediaRoots = []string{music}
	if roots := c.BrowsableRoots(); len(roots) != 1 || roots[0] != resolved(music) {
		t.Errorf("explicit media_roots = %v, want just %q", roots, resolved(music))
	}
}
