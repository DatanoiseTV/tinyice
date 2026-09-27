package relay

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pion/rtcp"
)

type recordingRTCPWriter struct {
	mu   sync.Mutex
	pkts []rtcp.Packet
}

func (w *recordingRTCPWriter) WriteRTCP(p []rtcp.Packet) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pkts = append(w.pkts, p...)
	return nil
}

func (w *recordingRTCPWriter) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.pkts)
}

func (w *recordingRTCPWriter) plis(ssrc uint32) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, p := range w.pkts {
		if pli, ok := p.(*rtcp.PictureLossIndication); ok && pli.MediaSSRC == ssrc {
			n++
		}
	}
	return n
}

// A browser encoder emits one keyframe when its track starts and then
// never again unless a receiver asks. Measured on Chrome 141 publishing
// a 640x480 camera into this ingest: the first eight seconds of the
// elementary stream carried 542 non-IDR slices, one SPS and one PPS, and
// zero type-5 IDR NALUs after the opening frame. Nothing joining
// mid-stream — an HLS segment or a WHEP viewer — can decode that.
//
// So the requester must (a) send a PLI immediately rather than waiting
// out the first interval, and (b) keep sending on the interval. With the
// same publisher and a 2 s interval the stream carried 5 IDRs, each with
// its own SPS/PPS, in the same eight-second window.
func TestRequestKeyframesSendsImmediatelyAndRepeats(t *testing.T) {
	const ssrc = uint32(0xDEADBEEF)
	w := &recordingRTCPWriter{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go requestKeyframes(ctx, w, ssrc, 40*time.Millisecond, "/kf/video")

	// Immediate: a PLI must land well inside the first interval.
	deadline := time.Now().Add(30 * time.Millisecond)
	for time.Now().Before(deadline) && w.plis(ssrc) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	if got := w.plis(ssrc); got != 1 {
		t.Fatalf("PLIs within the first interval = %d, want exactly 1 (the immediate request)", got)
	}

	// Repeating: at 40 ms cadence, 300 ms must produce several more.
	time.Sleep(300 * time.Millisecond)
	if got := w.plis(ssrc); got < 4 {
		t.Errorf("PLIs after 300ms at a 40ms interval = %d, want >= 4", got)
	}

	// Cancelling stops the loop rather than leaking a ticker goroutine.
	cancel()
	time.Sleep(120 * time.Millisecond)
	stopped := w.count()
	time.Sleep(200 * time.Millisecond)
	if after := w.count(); after != stopped {
		t.Errorf("requester kept writing after ctx cancel: %d -> %d", stopped, after)
	}
}

// Every packet the requester sends must be a PLI addressed to the
// publisher's SSRC. A PLI naming the wrong SSRC is silently ignored by
// the publisher, which looks exactly like a publisher that refuses to
// produce keyframes.
func TestRequestKeyframesTargetsTheTrackSSRC(t *testing.T) {
	w := &recordingRTCPWriter{}
	ctx, cancel := context.WithCancel(context.Background())
	go requestKeyframes(ctx, w, 0x1234, time.Hour, "/kf/video")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && w.count() == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	if w.count() == 0 {
		t.Fatal("no RTCP written")
	}
	if got := w.plis(0x1234); got != w.count() {
		t.Errorf("%d of %d packets were PLIs for SSRC 0x1234", got, w.count())
	}
}
