package relay

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// Every `continue` in runStreamerLoop bypasses the loop's only sleeps.
// The invalid-file skip was one of them, so a playlist whose files have
// all become unreadable (music dir unmounted, share offline, permissions
// changed) spun as fast as validateAudioFile could stat — a core burned
// and one warning logged per file, forever, because Loop=true never ends.
//
// Same class as the encoder idle gate that skipped its pacing sleep and
// held 232% CPU on two mounts.
//
// Observed through the logger rather than a counter added for the test:
// the loop already logs one warning per rejected file, so the warning
// rate IS the spin rate. Unbounded measures tens of thousands of skips
// per second; bounded should take one pass per second, so 3 files over
// ~1.5 s is a handful. The threshold sits three orders of magnitude
// below the broken rate, and it is an upper bound on work, so a loaded
// machine can only push it further from failing.
func TestInvalidPlaylistDoesNotSpin(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	prev := logger.L
	logger.L = zap.New(core).Sugar()
	defer func() { logger.L = prev }()

	missing := filepath.Join(t.TempDir(), "gone")
	sm := &StreamerManager{relay: NewRelay(false, nil)}
	s := &Streamer{
		Name:        "spin-test",
		OutputMount: "/spin-test",
		State:       StatePlaying,
		Loop:        true,
		stateCh:     make(chan struct{}, 1),
		Playlist: []PlaylistSong{
			{Path: missing + "-1.mp3", ID: 1},
			{Path: missing + "-2.mp3", ID: 2},
			{Path: missing + "-3.mp3", ID: 3},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		sm.runStreamerLoop(ctx, s)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runStreamerLoop did not return after ctx cancel")
	}

	skips := logs.FilterMessageSnippet("Skipping invalid file").Len()
	if skips == 0 {
		t.Fatal("no skip warnings logged; the loop never reached the validation path, so this row proves nothing")
	}
	// 3 files per pass, ~1 s between passes, 1.5 s of runtime: at most a
	// few passes. 60 leaves generous headroom and is still ~1000x below
	// an unbounded spin.
	if skips > 60 {
		t.Errorf("logged %d invalid-file skips in 1.5s; the loop is spinning with no backoff", skips)
	}
	t.Logf("invalid-file skips in 1.5s: %d", skips)
}
