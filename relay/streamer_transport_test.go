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
