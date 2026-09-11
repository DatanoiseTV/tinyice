package relay

import (
	"context"
	"testing"
	"time"
)

// An audio-only (byte-path) HLS output must survive its source going away
// and coming back. The old segmentLoopByteBuffer returned on the first
// io.EOF from the closed stream and the goroutine was gone for good, while
// the HLSOutput stayed registered — so the playlist froze after the first
// source drop and never produced another segment.
func TestByteBufferHLSResubscribesAfterSourceFlap(t *testing.T) {
	r := NewRelay(false, nil)
	mount := "/flap-audio"
	stream := r.GetOrCreateStream(mount)
	stream.mu.Lock()
	stream.ContentType = "audio/mpeg"
	stream.mu.Unlock()

	h := NewHLSOutput(mount, HLSConfig{SegmentDuration: 100 * time.Millisecond, WindowSize: 10, RingCapacity: 20}).WithRelay(r)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := h.Start(ctx, []*Track{NewAudioTrack(stream, "mp3")}); err != nil {
		t.Fatal(err)
	}
	defer h.Stop()

	feed := func(s *Stream, ms int) {
		frame := append([]byte{0xff, 0xfb, 0x90, 0x64}, make([]byte, 413)...)
		deadline := time.Now().Add(time.Duration(ms) * time.Millisecond)
		for time.Now().Before(deadline) {
			s.Broadcast(frame, r)
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitSegments := func(min int, within time.Duration) bool {
		deadline := time.Now().Add(within)
		for time.Now().Before(deadline) {
			if h.ring.Count() >= min {
				return true
			}
			time.Sleep(20 * time.Millisecond)
		}
		return false
	}

	feed(stream, 400)
	if !waitSegments(1, 2*time.Second) {
		t.Fatal("no segments produced from the first source")
	}
	before := h.ring.Count()

	// Source drops: the relay removes the mount (closes listener channels).
	r.RemoveStream(mount)
	time.Sleep(300 * time.Millisecond)

	// Source reconnects: a brand-new Stream object under the same mount.
	stream2 := r.GetOrCreateStream(mount)
	stream2.mu.Lock()
	stream2.ContentType = "audio/mpeg"
	stream2.mu.Unlock()
	if stream2 == stream {
		t.Fatal("test setup: expected a fresh Stream after RemoveStream")
	}
	go feed(stream2, 1500)

	if !waitSegments(before+1, 4*time.Second) {
		t.Fatalf("no new segments after the source came back (still %d) — the HLS loop died on the flap", h.ring.Count())
	}
}
