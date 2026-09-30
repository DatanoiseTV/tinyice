package relay

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// silentPCM returns `secs` seconds of 48 kHz stereo S16LE silence.
func silentPCM(secs int) *bytes.Reader {
	return bytes.NewReader(make([]byte, 48000*2*2*secs))
}

// A paced encoder with NO listeners must still consume audio in real
// time. The idle gate exists to skip the encode when nobody is
// listening, but it used to `continue` past the pacing sleep as well —
// so a file-backed AutoDJ source ran its decoder flat out, because the
// sleep is the only thing tying a file to the clock.
//
// Measured on r4dio 2026-09-30 with the bug live: two idle AutoDJ mounts
// held 207% CPU (1d10h of CPU in 16h wall clock), ~90% of the profile
// inside go-mp3 frame decoding, with only 27 goroutines alive — so it
// was one free-running loop per mount, not a leak or a retry storm.
//
// The threshold is a LOWER bound on elapsed time, which is the right
// shape for a timing row on a loaded machine: the defect makes the loop
// finish in milliseconds, while scheduler noise can only make it take
// longer. Broken measures ~0.00 s for 2 s of audio; correct measures
// ~2.0 s. 1.5 s sits in that gap with room for either side.
func TestPacedEncodersRespectRealTimeWithoutListeners(t *testing.T) {
	const secs = 2
	const floor = 1500 * time.Millisecond

	t.Run("mp3", func(t *testing.T) {
		r := NewRelay(false, nil)
		out := r.GetOrCreateStream("/idle-mp3")
		if out.ListenersCount() != 0 {
			t.Fatalf("fixture has %d listeners; the idle gate under test only fires at 0", out.ListenersCount())
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		start := time.Now()
		EncodeMP3(ctx, r, out, silentPCM(secs), 128, nil, true, 48000)
		elapsed := time.Since(start)

		if elapsed < floor {
			t.Errorf("EncodeMP3 consumed %ds of audio in %v with no listeners; "+
				"pacing was skipped, so the decoder runs flat out (want >= %v)",
				secs, elapsed.Round(time.Millisecond), floor)
		}
	})

	// Same property for the AutoDJ's default format. Opus mounts are what
	// r4dio actually runs, and what burned the CPU.
	t.Run("opus", func(t *testing.T) {
		if testing.Short() {
			t.Skip("opus encoder setup is cgo; skipped in -short")
		}
		r := NewRelay(false, nil)
		out := r.GetOrCreateStream("/idle-opus")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		start := time.Now()
		EncodeOpus(ctx, r, out, silentPCM(secs), 128, nil, true)
		elapsed := time.Since(start)

		if elapsed < floor {
			t.Errorf("EncodeOpus consumed %ds of audio in %v with no listeners; "+
				"pacing was skipped (want >= %v)",
				secs, elapsed.Round(time.Millisecond), floor)
		}
	})
}

// An idle paced encoder must keep its mount looking alive. Health is
// derived from LastDataReceived, which only Broadcast sets, so a mount
// whose encoder is idling (no listeners) emitted nothing and went
// "degraded" after 5 s and "dead" after 30 s — and the HealthMonitor
// auto-removes a dead non-transcoded stream, which is what an AutoDJ
// output is. The mount then vanishes from the dashboard and 404s the
// first listener to tune in.
//
// Before the pacing fix this was masked: the free-running encoder
// restarted tracks constantly and each restart wrote Ogg headers, so the
// mount looked alive in bursts — which is exactly the healthy/degraded
// flapping observed on r4dio (200 health log lines in 6 minutes).
func TestIdleEncoderKeepsMountAlive(t *testing.T) {
	r := NewRelay(false, nil)
	out := r.GetOrCreateStream("/idle-alive")

	// Backdate liveness well past the 5 s degraded threshold so a stale
	// timestamp cannot be mistaken for a fresh one.
	stale := time.Now().Add(-60 * time.Second)
	out.mu.Lock()
	out.LastDataReceived = stale
	out.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	EncodeMP3(ctx, r, out, silentPCM(1), 128, nil, true, 48000)

	out.mu.RLock()
	got := out.LastDataReceived
	out.mu.RUnlock()

	if !got.After(stale) {
		t.Fatalf("LastDataReceived still %v after an idle encode run; "+
			"the mount reads dead and gets auto-removed", got)
	}
	if calculateHealthStatus(got) != StatusHealthy {
		t.Errorf("idle mount health = %v, want healthy: the producer was "+
			"running on schedule the whole time",
			calculateHealthStatus(got))
	}
}
