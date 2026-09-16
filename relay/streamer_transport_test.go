package relay

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// newTestStreamer builds a Streamer wired up like StartStreamer does, but
// without touching disk, MPD or the relay manager.
func newTestStreamer(t *testing.T) (*Streamer, *StreamerManager) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s := &Streamer{
		Name:        "test",
		OutputMount: "/test",
		Format:      "mp3",
		Bitrate:     128,
		ctx:         ctx,
		cancel:      cancel,
		titleCache:  make(map[string]string),
		idleCh:      make(chan string, 1),
		stateCh:     make(chan struct{}, 1),
	}
	return s, NewStreamerManager(NewRelay(false, nil), nil)
}

// Stop and Pause are transport operations: they must leave the
// streamer-lifetime context intact so playback can be resumed. Cancelling
// it there is what made "pause, then play" permanently silent.
func TestTransportStopLeavesLifetimeContextAlive(t *testing.T) {
	cases := []struct {
		name      string
		act       func(*Streamer)
		wantState StreamerState
	}{
		{"Stop", (*Streamer).Stop, StateStopped},
		{"Pause", (*Streamer).Pause, StatePaused},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestStreamer(t)
			s.Play()
			tc.act(s)

			if got := s.GetStats().State; got != tc.wantState {
				t.Errorf("state = %v, want %v", got, tc.wantState)
			}
			if err := s.ctx.Err(); err != nil {
				t.Errorf("streamer context cancelled by %s: %v", tc.name, err)
			}

			// And the transport can be driven back to playing.
			s.Play()
			if got := s.GetStats().State; got != StatePlaying {
				t.Errorf("state after Play = %v, want %v", got, StatePlaying)
			}
		})
	}
}

// Shutdown is the teardown case and must cancel the lifetime context so
// the playback loop exits and on_play_command children are killed.
func TestShutdownCancelsLifetimeContext(t *testing.T) {
	s, _ := newTestStreamer(t)
	s.Play()
	s.Shutdown()

	if got := s.GetStats().State; got != StateStopped {
		t.Errorf("state = %v, want %v", got, StateStopped)
	}
	if s.ctx.Err() == nil {
		t.Error("Shutdown did not cancel the streamer context")
	}
}

// End-to-end on the playback loop: pause it, resume it, and check the loop
// goroutine is still there to pick work up. The queued path doesn't exist,
// so the loop rejects it and moves on — draining the queue is the
// observable proof that the loop ran.
func TestStreamerLoopResumesAfterPause(t *testing.T) {
	s, sm := newTestStreamer(t)
	go sm.runStreamerLoop(s.ctx, s)

	queueDrained := func() bool {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return len(s.Queue) == 0
	}
	waitForDrain := func(what string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if queueDrained() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("playback loop did not consume the queue %s", what)
	}

	missing := filepath.Join(t.TempDir(), "does-not-exist.mp3")

	s.PushToQueue(missing)
	s.Play()
	waitForDrain("after the initial Play")

	s.Pause()
	// Paused: the loop parks and must not touch the queue.
	s.PushToQueue(missing)
	time.Sleep(200 * time.Millisecond)
	if queueDrained() {
		t.Fatal("playback loop consumed the queue while paused")
	}

	s.Play()
	waitForDrain("after resuming from pause")

	// Same again for Stop, which the MPD `stop` command and the
	// enable/disable toggle both use.
	s.Stop()
	s.PushToQueue(missing)
	time.Sleep(200 * time.Millisecond)
	if queueDrained() {
		t.Fatal("playback loop consumed the queue while stopped")
	}

	s.Play()
	waitForDrain("after resuming from stop")
}

// countingReader yields an endless deterministic byte sequence and records
// how much has been consumed, so a test can tell whether a pause resumed
// in place or skipped ahead.
type countingReader struct {
	next byte
	n    int
}

func (r *countingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.next
		r.next++
	}
	r.n += len(p)
	return len(p), nil
}

// The whole point of #54: pausing must not lose the caller's place in the
// track. The gate blocks reads while paused and the underlying reader is
// untouched, so the byte after the pause is the byte that followed the
// last one read before it.
func TestPauseGateResumesInPlace(t *testing.T) {
	s, _ := newTestStreamer(t)
	s.Play()
	src := &countingReader{}
	g := newPauseGate(context.Background(), s, src)

	before := make([]byte, 8)
	if _, err := g.Read(before); err != nil {
		t.Fatalf("read before pause: %v", err)
	}

	s.Pause()

	// While paused, Read must block rather than return data.
	done := make(chan []byte, 1)
	go func() {
		b := make([]byte, 8)
		if _, err := g.Read(b); err == nil {
			done <- b
		}
	}()
	select {
	case <-done:
		t.Fatal("pauseGate returned data while paused")
	case <-time.After(300 * time.Millisecond):
	}

	consumedWhilePaused := src.n
	if consumedWhilePaused != len(before) {
		t.Errorf("decoder advanced while paused: consumed %d bytes, want %d",
			consumedWhilePaused, len(before))
	}

	s.Play()

	select {
	case after := <-done:
		// Resumed in place: the sequence continues with no gap.
		if after[0] != before[len(before)-1]+1 {
			t.Errorf("resumed at byte %d, want %d — the pause skipped data",
				after[0], before[len(before)-1]+1)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pauseGate did not resume after Play")
	}

	// And the paused time is recorded so the encoder's pacing can
	// discount it instead of racing to catch up.
	if p := g.PausedTotal(); p < 200*time.Millisecond {
		t.Errorf("PausedTotal = %v, want at least the ~300ms spent paused", p)
	}
}

// Pause must leave the track's context alive; cancelling it is what made
// Play() start the next track instead of resuming.
func TestPauseDoesNotCancelTrack(t *testing.T) {
	s, _ := newTestStreamer(t)
	fileCtx, fileCancel := context.WithCancel(context.Background())
	defer fileCancel()
	s.mu.Lock()
	s.fileCancel = fileCancel
	s.mu.Unlock()

	s.Play()
	s.Pause()

	if fileCtx.Err() != nil {
		t.Errorf("Pause cancelled the in-flight track: %v", fileCtx.Err())
	}
	if got := s.GetStats().State; got != StatePaused {
		t.Errorf("state = %v, want StatePaused", got)
	}

	// Stop, by contrast, ends the current track.
	s.Stop()
	if fileCtx.Err() == nil {
		t.Error("Stop did not cancel the in-flight track")
	}
}

// A non-looping playlist that ran to the end left CurrentPos past the
// last entry and the state Stopped; pressing Play re-entered the same
// "past the end" branch and stopped again, so the AutoDJ could never be
// restarted without editing its playlist.
func TestPlayRestartsAfterNonLoopingPlaylistEnds(t *testing.T) {
	s, _ := newTestStreamer(t)
	s.Loop = false
	s.SetPlaylist([]string{"/m/a.mp3", "/m/b.mp3"})
	s.mu.Lock()
	s.CurrentPos = len(s.Playlist) // where the loop leaves it at the end
	s.State = StateStopped
	s.mu.Unlock()

	s.Play()

	s.mu.RLock()
	pos, state := s.CurrentPos, s.State
	s.mu.RUnlock()
	if state != StatePlaying {
		t.Errorf("state = %v, want StatePlaying", state)
	}
	if pos != 0 {
		t.Errorf("CurrentPos = %d after Play, want 0 — the cursor was left past the end", pos)
	}
}

// Playlist edits must not move the playback cursor onto a different
// track: removing an entry before the cursor shifted everything down and
// silently skipped a song.
func TestPlaylistEditsKeepCursorOnTheSameTrack(t *testing.T) {
	s, _ := newTestStreamer(t)
	s.SetPlaylist([]string{"/m/a.mp3", "/m/b.mp3", "/m/c.mp3", "/m/d.mp3"})
	s.mu.Lock()
	s.CurrentPos = 2 // next up is c.mp3
	s.mu.Unlock()
	nextUp := func() string {
		s.mu.RLock()
		defer s.mu.RUnlock()
		if s.CurrentPos >= len(s.Playlist) {
			return "(end)"
		}
		return s.Playlist[s.CurrentPos].Path
	}
	if got := nextUp(); got != "/m/c.mp3" {
		t.Fatalf("setup: next up = %s", got)
	}

	s.RemoveFromPlaylist(0) // remove a.mp3, before the cursor
	if got := nextUp(); got != "/m/c.mp3" {
		t.Errorf("after removing an earlier entry, next up = %s, want /m/c.mp3", got)
	}

	s.RemoveFromPlaylist(2) // remove d.mp3, after the cursor
	if got := nextUp(); got != "/m/c.mp3" {
		t.Errorf("after removing a later entry, next up = %s, want /m/c.mp3", got)
	}

	// Move an earlier entry to after the cursor.
	s.SetPlaylist([]string{"/m/a.mp3", "/m/b.mp3", "/m/c.mp3", "/m/d.mp3"})
	s.mu.Lock()
	s.CurrentPos = 2
	s.mu.Unlock()
	s.MovePlaylistItem(0, 3)
	if got := nextUp(); got != "/m/c.mp3" {
		t.Errorf("after moving an earlier entry past the cursor, next up = %s, want /m/c.mp3", got)
	}
}

// The AutoDJ must not write into a mount a live encoder already owns —
// it used to overwrite the claim and interleave its bytes with the
// encoder's.
func TestAutoDJDeclinesAMountWithALiveSource(t *testing.T) {
	r := NewRelay(false, nil)
	out := r.GetOrCreateStream("/contested")
	if !out.TryClaimSource("203.0.113.9") {
		t.Fatal("setup: encoder could not claim the mount")
	}
	if out.ClaimSourceIfFree(autoDJSourceLabel) {
		t.Error("AutoDJ claimed a mount a live encoder owns")
	}
	if got := out.GetSourceIP(); got != "203.0.113.9" {
		t.Errorf("source is now %q — the encoder's claim was overwritten", got)
	}
	// Once the encoder leaves, the AutoDJ may take it.
	out.ReleaseSource()
	if !out.ClaimSourceIfFree(autoDJSourceLabel) {
		t.Error("AutoDJ could not claim a free mount")
	}
	// And re-claiming its own mount is fine.
	if !out.ClaimSourceIfFree(autoDJSourceLabel) {
		t.Error("AutoDJ could not re-claim its own mount")
	}
}
