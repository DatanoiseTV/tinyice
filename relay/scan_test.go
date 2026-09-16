package relay

import (
	"os"
	"path/filepath"
	"testing"
)

// ScanMusicDir never read the music directory: it re-cached titles for
// the tracks already in the playlist and returned. The boot path calls
// it exactly when the playlist is empty and then calls Play(), so an
// AutoDJ configured with nothing but a music_dir started, played
// nothing, and never appeared as a source.
func TestScanMusicDirPopulatesAnEmptyPlaylist(t *testing.T) {
	dir := t.TempDir()
	for _, rel := range []string{
		"a.mp3", "b.flac", "c.opus", "d.wav", "e.ogg",
		"notes.txt", ".hidden.mp3",
		"rock/f.mp3", "rock/deep/g.mp3",
		".git/h.mp3",
	} {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	s := &Streamer{MusicDir: dir, NextID: 1, titleCache: map[string]string{},
		idleCh: make(chan string, 10)}
	if err := s.ScanMusicDir(); err != nil {
		t.Fatalf("ScanMusicDir: %v", err)
	}

	got := map[string]bool{}
	for _, ps := range s.Playlist {
		rel, _ := filepath.Rel(dir, ps.Path)
		got[rel] = true
	}
	for _, want := range []string{"a.mp3", "b.flac", "c.opus", "d.wav", "e.ogg",
		"rock/f.mp3", "rock/deep/g.mp3"} {
		if !got[filepath.FromSlash(want)] {
			t.Errorf("%s missing from the playlist", want)
		}
	}
	for _, unwanted := range []string{"notes.txt", ".hidden.mp3", ".git/h.mp3"} {
		if got[filepath.FromSlash(unwanted)] {
			t.Errorf("%s should not have been added", unwanted)
		}
	}
	if len(s.Playlist) != 7 {
		t.Errorf("playlist has %d entries, want 7: %v", len(s.Playlist), got)
	}

	// Every entry needs a distinct, positive id — MPD addressing and the
	// playlist cursor both key off it.
	ids := map[int]bool{}
	for _, ps := range s.Playlist {
		if ps.ID <= 0 {
			t.Errorf("%s has non-positive id %d", ps.Path, ps.ID)
		}
		if ids[ps.ID] {
			t.Errorf("duplicate id %d", ps.ID)
		}
		ids[ps.ID] = true
	}
}

// A scan on a live playlist must pick up new files without reordering or
// re-adding what is already queued.
func TestScanMusicDirIsIncrementalAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.mp3")
	write("b.mp3")

	s := &Streamer{MusicDir: dir, NextID: 1, titleCache: map[string]string{},
		idleCh: make(chan string, 10)}
	if err := s.ScanMusicDir(); err != nil {
		t.Fatal(err)
	}
	first := append([]PlaylistSong(nil), s.Playlist...)
	if len(first) != 2 {
		t.Fatalf("first scan found %d, want 2", len(first))
	}
	versionAfterFirst := s.PlaylistVersion

	// Re-scanning with nothing new must change nothing at all.
	if err := s.ScanMusicDir(); err != nil {
		t.Fatal(err)
	}
	if len(s.Playlist) != 2 {
		t.Errorf("rescan duplicated entries: %d, want 2", len(s.Playlist))
	}
	if s.PlaylistVersion != versionAfterFirst {
		t.Errorf("rescan bumped playlist_version with nothing to add")
	}

	// A new file is appended, and the existing entries keep their ids.
	write("c.mp3")
	if err := s.ScanMusicDir(); err != nil {
		t.Fatal(err)
	}
	if len(s.Playlist) != 3 {
		t.Fatalf("after adding one file: %d entries, want 3", len(s.Playlist))
	}
	for i, ps := range first {
		if s.Playlist[i].Path != ps.Path || s.Playlist[i].ID != ps.ID {
			t.Errorf("entry %d moved or changed id: %+v, was %+v", i, s.Playlist[i], ps)
		}
	}
	if s.PlaylistVersion == versionAfterFirst {
		t.Error("playlist_version did not change after adding a track")
	}
}

func TestScanMusicDirWithoutADirectory(t *testing.T) {
	s := &Streamer{titleCache: map[string]string{}}
	if err := s.ScanMusicDir(); err == nil {
		t.Error("an unset music directory should be an error")
	}
}

// Loading a .pls replaced the playlist without bumping PlaylistVersion.
// The SSE autodj event carries only that version (the playlist array was
// removed from it to cut feed size), and the Studio refetches the
// playlist only when the number changes — so a loaded .pls never
// appeared in the UI.
func TestLoadPlaylistBumpsTheVersionAndResetsTheCursor(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll("playlists", 0o755); err != nil {
		t.Fatal(err)
	}
	name := "scan-test-fixture.pls"
	body := "[playlist]\nNumberOfEntries=2\n" +
		"File1=" + filepath.Join(dir, "one.mp3") + "\nTitle1=One\n" +
		"File2=" + filepath.Join(dir, "two.mp3") + "\nTitle2=Two\n"
	if err := os.WriteFile(filepath.Join("playlists", name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(filepath.Join("playlists", name)) })

	s := &Streamer{Name: "dj", MusicDir: dir, NextID: 1,
		titleCache: map[string]string{}, idleCh: make(chan string, 10),
		// A cursor left over from a longer playlist.
		CurrentPos: 7,
		Playlist: []PlaylistSong{
			{Path: "old-a.mp3", ID: 90}, {Path: "old-b.mp3", ID: 91},
		},
	}
	before := s.PlaylistVersion

	if err := s.LoadPlaylist(name); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}

	if len(s.Playlist) != 2 || s.Playlist[0].Path != filepath.Join(dir, "one.mp3") {
		t.Fatalf("playlist = %+v, want the two entries from the .pls", s.Playlist)
	}
	if s.PlaylistVersion == before {
		t.Error("playlist_version did not change, so the Studio never refetches and the load is invisible")
	}
	if s.CurrentPos >= len(s.Playlist) {
		t.Errorf("CurrentPos = %d, past the end of the new %d-track playlist",
			s.CurrentPos, len(s.Playlist))
	}
	if s.LastPlaylist != name {
		t.Errorf("LastPlaylist = %q, want %q", s.LastPlaylist, name)
	}
}
