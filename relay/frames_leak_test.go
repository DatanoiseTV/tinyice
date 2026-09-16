package relay

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// Each FrameHub.Subscribe starts a watcher goroutine. It used to wait on
// the caller's context alone, so a subscription ended by Close (which is
// what a source disconnect does) left its watcher parked until the
// caller's context ended. The HLS framed loop resubscribes with the same
// long-lived output context on every source flap, so a flapping encoder
// leaked two parked goroutines per flap for the life of the output.
func TestFrameHubSubscribersDoNotLeakAcrossClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const flaps = 300
	settle := func() int {
		var n int
		deadline := time.Now().Add(3 * time.Second)
		for {
			runtime.Gosched()
			n = runtime.NumGoroutine()
			time.Sleep(20 * time.Millisecond)
			if runtime.NumGoroutine() <= n || time.Now().After(deadline) {
				return runtime.NumGoroutine()
			}
		}
	}

	before := settle()
	for i := 0; i < flaps; i++ {
		h := NewFrameHub()
		h.Subscribe(ctx) // audio
		h.Subscribe(ctx) // video
		h.Close()        // the source disconnects; RemoveStream closes the hub
	}
	after := settle()

	// 2 watchers per flap would be 600; allow generous slack for the
	// runtime's own goroutines.
	if grown := after - before; grown > flaps/4 {
		t.Errorf("goroutines grew by %d over %d flaps (%d -> %d): subscriber watchers are not exiting on Close",
			grown, flaps, before, after)
	}
}
