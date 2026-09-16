package relay

import (
	"bufio"
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A TS packet on PID 0 with payload_unit_start set and a pointer_field
// that points past the end of the 184-byte payload. The demuxer must
// drop it, not slice past the buffer — an SRT publisher could send this
// and, with nothing recovering the ingest goroutine, end the process.
func TestTSDemuxerRejectsOversizedPointerField(t *testing.T) {
	pkt := make([]byte, 188)
	pkt[0] = 0x47
	pkt[1] = 0x40 // payload_unit_start_indicator, PID 0 (PAT)
	pkt[2] = 0x00
	pkt[3] = 0x10 // payload only, no adaptation field
	pkt[4] = 200  // pointer_field beyond the 184-byte payload

	d := NewTSDemuxer()
	d.Feed(pkt) // panics on the unfixed code: slice bounds out of range [201:184]

	// Same for the PMT path: pretend PID 0x100 is the PMT.
	d.pmtPID = 0x100
	d.patParsed = true
	pkt[1] = 0x41
	pkt[2] = 0x00
	d.Feed(pkt)
}

// `albumart "x" -1` parses to a negative offset; the lower bound was
// missing, so fakeArt[-1:] panicked in the MPD connection goroutine.
func TestMPDAlbumArtRejectsNegativeOffset(t *testing.T) {
	s, _ := newTestStreamer(t)
	m := NewMPDServer("0", "", s)
	var out bytes.Buffer
	resp := NewMPDResponse(&out)

	m.handleAlbumArt(`"x" -1`, resp) // panics on the unfixed code

	if !strings.Contains(out.String(), "ACK") {
		t.Errorf("negative offset should be refused with an ACK, got %q", out.String())
	}
}

// MPD `currentsong` used to hold Streamer.mu.RLock while calling
// writeSongInfo, which takes the same RLock again. Go's RWMutex is
// writer-preferring: a Lock() queued between the two RLocks blocks the
// inner one, the writer waits for the outer, and both hang forever — and
// the writer is the playback loop, so the AutoDJ falls silent. Simulate
// the interleaving: hold a write lock briefly while the handler runs,
// then release; the handler must finish.
func TestMPDCurrentSongDoesNotRecursivelyRLock(t *testing.T) {
	s, _ := newTestStreamer(t)
	s.mu.Lock()
	s.CurrentFile, s.CurrentPlayingPos, s.CurrentPlayingID = "a.mp3", 0, 1
	s.Playlist = []PlaylistSong{{Path: "/m/a.mp3", ID: 1}}
	s.mu.Unlock()
	m := NewMPDServer("0", "", s)

	for name, call := range map[string]func(*MPDResponse){
		"currentsong": m.handleCurrentSong,
		"playlistid":  func(r *MPDResponse) { m.handlePlaylistId("1", r) },
	} {
		t.Run(name, func(t *testing.T) {
			// A writer that grabs the lock as soon as the handler's first
			// RLock is released — or, on the old code, as soon as it can
			// queue behind the outer RLock.
			stop := make(chan struct{})
			go func() {
				for {
					select {
					case <-stop:
						return
					default:
					}
					s.mu.Lock()
					s.mu.Unlock()
				}
			}()
			done := make(chan struct{})
			go func() {
				var out bytes.Buffer
				for i := 0; i < 200; i++ {
					call(NewMPDResponse(&out))
				}
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("handler deadlocked against a queued writer (recursive RLock)")
			}
			close(stop)
		})
	}
}

// With mpd_password set, a correct `password` must unlock the connection.
// The refactor into dispatchCommand lost the assignment to the
// connection's auth flag, so every command after a correct password was
// still "permission denied" and password-protected MPD was unusable.
func TestMPDPasswordUnlocksConnection(t *testing.T) {
	s, _ := newTestStreamer(t)
	m := NewMPDServer("0", "secret", s)
	client, server := net.Pipe()
	defer client.Close()
	go m.handleConnection(server)

	rd := bufio.NewReader(client)
	if greeting, _ := rd.ReadString('\n'); !strings.HasPrefix(greeting, "OK MPD") {
		t.Fatalf("greeting = %q", greeting)
	}
	send := func(line string) string {
		client.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := client.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
		var out strings.Builder
		for {
			l, err := rd.ReadString('\n')
			if err != nil {
				t.Fatalf("reading reply to %q: %v (so far %q)", line, err, out.String())
			}
			out.WriteString(l)
			if strings.HasPrefix(l, "OK") || strings.HasPrefix(l, "ACK") {
				return out.String()
			}
		}
	}
	if r := send("status"); !strings.Contains(r, "ACK") {
		t.Fatalf("status before password should be denied, got %q", r)
	}
	if r := send(`password "secret"`); !strings.HasPrefix(r, "OK") {
		t.Fatalf("correct password rejected: %q", r)
	}
	if r := send("status"); !strings.Contains(r, "state:") {
		t.Fatalf("status after correct password still denied: %q", r)
	}
}

// `idle` must return immediately on `noidle`, and a command sent while
// idle must be executed rather than answered "unknown command". The
// synchronous reader used to park the connection for up to 30 s with
// the client's noidle unread — the ncmpcpp "every keypress freezes"
// symptom — then answer it as an error against the wrong request.
func TestMPDIdleIsInterruptible(t *testing.T) {
	s, _ := newTestStreamer(t)
	m := NewMPDServer("0", "", s)
	client, server := net.Pipe()
	defer client.Close()
	go m.handleConnection(server)
	rd := bufio.NewReader(client)
	rd.ReadString('\n') // greeting

	readReply := func(what string) string {
		var out strings.Builder
		for {
			client.SetReadDeadline(time.Now().Add(2 * time.Second))
			l, err := rd.ReadString('\n')
			if err != nil {
				t.Fatalf("%s: %v (so far %q)", what, err, out.String())
			}
			out.WriteString(l)
			if strings.HasPrefix(l, "OK") || strings.HasPrefix(l, "ACK") {
				return out.String()
			}
		}
	}

	// net.Pipe writes block until the server reads; a server parked in
	// a non-interruptible idle never reads, so bound the write too.
	write := func(line string) {
		client.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if _, err := client.Write([]byte(line)); err != nil {
			t.Fatalf("server stopped reading while idle (write %q: %v)", strings.TrimSpace(line), err)
		}
	}

	// idle, then noidle: must come back promptly with OK.
	write("idle\n")
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	write("noidle\n")
	if r := readReply("noidle"); !strings.HasPrefix(r, "OK") {
		t.Fatalf("noidle reply = %q", r)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("noidle took %v; idle was not interruptible", d)
	}

	// idle, then a real command: the idle ends (OK) and the command runs.
	write("idle\n")
	time.Sleep(100 * time.Millisecond)
	write("status\n")
	if r := readReply("idle end"); !strings.HasPrefix(r, "OK") {
		t.Fatalf("idle should end with OK when a command arrives, got %q", r)
	}
	if r := readReply("status"); !strings.Contains(r, "state:") {
		t.Fatalf("command sent during idle was not executed: %q", r)
	}

	// idle, then a player event: reported with the right subsystem.
	write("idle\n")
	time.Sleep(100 * time.Millisecond)
	s.Play()
	if r := readReply("idle event"); !strings.Contains(r, "changed: player") {
		t.Fatalf("expected 'changed: player', got %q", r)
	}
}

// MPD `rm` joined the client-supplied name into playlists/ unchecked, so
// `rm "../../x"` deleted x.pls anywhere on disk.
func TestMPDRmRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(orig) })
	os.MkdirAll("playlists", 0755)
	victim := filepath.Join(dir, "victim.pls")
	os.WriteFile(victim, []byte("x"), 0644)

	s, _ := newTestStreamer(t)
	m := NewMPDServer("0", "", s)
	var out bytes.Buffer
	m.handleRm(`"../victim"`, NewMPDResponse(&out))
	if _, err := os.Stat(victim); err != nil {
		t.Fatal("rm with a traversing name deleted a file outside playlists/")
	}
	if !strings.Contains(out.String(), "ACK") {
		t.Errorf("traversing name should be refused with ACK, got %q", out.String())
	}
}

// MPD path arguments used to be joined into MusicDir unchecked, so
// `add "../../etc/passwd"` put a file from outside the library into the
// playlist and `lsinfo ".."` listed the parent directory.
func TestMPDPathsConfinedToMusicDir(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "music"), 0755)
	os.WriteFile(filepath.Join(dir, "outside.mp3"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "music", "inside.mp3"), []byte("x"), 0644)

	s, _ := newTestStreamer(t)
	s.MusicDir = filepath.Join(dir, "music")
	m := NewMPDServer("0", "", s)

	var out bytes.Buffer
	m.handleAdd(`"../outside.mp3"`, NewMPDResponse(&out))
	for _, e := range s.GetPlaylist() {
		if strings.Contains(e, "outside.mp3") {
			t.Fatalf("add escaped the music dir: %q", e)
		}
	}
	if !strings.Contains(out.String(), "ACK") {
		t.Errorf("escaping add should be refused, got %q", out.String())
	}

	out.Reset()
	m.handleAdd(`"inside.mp3"`, NewMPDResponse(&out))
	found := false
	for _, e := range s.GetPlaylist() {
		if strings.Contains(e, "inside.mp3") {
			found = true
		}
	}
	if !found {
		t.Error("a legitimate add inside the music dir was refused")
	}
}

// MPD addresses playlist entries by stable ID. Treating the ID as
// index+1 targeted the wrong track after any removal.
func TestMPDDeleteIdUsesStableID(t *testing.T) {
	s, _ := newTestStreamer(t)
	s.MusicDir = t.TempDir()
	s.NextID = 1 // StartStreamer does this; MPD song ids must be positive
	for _, n := range []string{"a", "b", "c"} {
		s.AddToPlaylist(filepath.Join(s.MusicDir, n+".mp3"))
	}
	ids := []int{}
	for _, it := range s.GetPlaylistInfo() {
		ids = append(ids, it.ID)
	}
	m := NewMPDServer("0", "", s)
	var out bytes.Buffer
	// Remove the first entry, so IDs and indices diverge.
	m.handleDeleteId(strconvItoa(ids[0]), NewMPDResponse(&out))
	// Now delete "c" by its ID; with index arithmetic this would hit "b".
	out.Reset()
	m.handleDeleteId(strconvItoa(ids[2]), NewMPDResponse(&out))

	left := s.GetPlaylistInfo()
	if len(left) != 1 || !strings.Contains(left[0].Path, "b.mp3") {
		got := []string{}
		for _, it := range left {
			got = append(got, filepath.Base(it.Path))
		}
		t.Fatalf("playlist = %v, want only b.mp3 — deleteid used the wrong entry", got)
	}
}

func strconvItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
