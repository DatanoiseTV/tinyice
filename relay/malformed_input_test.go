package relay

import (
	"bytes"
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
