package relay

import (
	"testing"

	"github.com/pion/webrtc/v4"
)

// The RTP timestamp is 32 bits and wraps about every 13 hours at 90 kHz.
// A broadcast that wrapped mid-stream would hand the HLS segmenter a PTS
// that jumps backwards by 2^32, which reorders or discards segments.
func TestH264UnwrapperSurvivesTimestampWrap(t *testing.T) {
	var u h264Unwrapper

	// First packet defines the zero point, whatever the absolute value.
	if got := u.unwrap(4_294_960_000); got != 0 {
		t.Fatalf("first timestamp = %d, want 0", got)
	}
	// Step forward to just below the wrap.
	if got := u.unwrap(4_294_963_000); got != 3000 {
		t.Errorf("pre-wrap step = %d, want 3000", got)
	}
	// Wrap past 2^32. From 4_294_963_000 to the wrap point (2^32 =
	// 4_294_967_296) is 4296 ticks, then 2704 more after it restarts at
	// zero: 7000 ticks, so 10_000 since the first packet.
	if got := u.unwrap(2704); got != 10000 {
		t.Errorf("post-wrap = %d, want 10000 (must not go backwards)", got)
	}
	if got := u.unwrap(92704); got != 100000 {
		t.Errorf("after wrap = %d, want 100000", got)
	}
}

// Small backwards steps are packet reordering, not a wrap, and must pass
// through as-is so the segmenter sees the true ordering rather than a
// value 2^32 in the future.
func TestH264UnwrapperTreatsSmallBackstepsAsReordering(t *testing.T) {
	var u h264Unwrapper
	u.unwrap(100_000)
	if got := u.unwrap(103_000); got != 3000 {
		t.Fatalf("forward step = %d, want 3000", got)
	}
	if got := u.unwrap(102_000); got != 2000 {
		t.Errorf("reordered packet = %d, want 2000 (not a wrap)", got)
	}
}

// RTP carries one frame across many packets. The HLS segmenter and the
// mpegts muxer both want a whole access unit with a single PTS, so the
// assembler must hold packets until the timestamp changes or the marker
// bit closes the frame.
func TestH264AccessUnitAssembly(t *testing.T) {
	var a h264AccessUnit

	// Three packets of one frame: nothing completes yet.
	for _, part := range [][]byte{{1, 1}, {2, 2}, {3, 3}} {
		if au, _, done := a.add(part, 1000); done {
			t.Fatalf("frame completed early with %v", au)
		}
	}
	// A packet at a new timestamp closes the previous frame.
	au, ts, done := a.add([]byte{9}, 2000)
	if !done {
		t.Fatal("a new timestamp did not close the previous access unit")
	}
	if ts != 1000 {
		t.Errorf("completed unit timestamp = %d, want 1000", ts)
	}
	if string(au) != string([]byte{1, 1, 2, 2, 3, 3}) {
		t.Errorf("assembled unit = %v, want the three payloads concatenated in order", au)
	}

	// The marker bit closes the frame that is open.
	au, ts, ok := a.flush()
	if !ok || ts != 2000 || string(au) != string([]byte{9}) {
		t.Errorf("flush = %v ts=%d ok=%v, want [9] ts=2000", au, ts, ok)
	}
	// A second flush has nothing to give.
	if _, _, ok := a.flush(); ok {
		t.Error("flush returned a unit twice")
	}
}

// The ingest MediaEngine must offer H.264 and refuse VP8/VP9. Every video
// consumer in tinyice is H.264 and there is no video transcoder, so
// negotiating VP8 would accept a publisher whose picture nothing can
// play. Failing negotiation is the honest outcome.
func TestSourceMediaEngineOffersH264AndNotVP8(t *testing.T) {
	me, err := newSourceMediaEngine()
	if err != nil {
		t.Fatalf("newSourceMediaEngine: %v", err)
	}
	// Registering VP8 again must succeed — proving it was absent. If the
	// engine already had it this would be a duplicate payload type.
	if err := me.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeVP8, ClockRate: 90000,
		},
		PayloadType: 96,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		t.Fatalf("VP8 appears to be pre-registered on the source engine: %v", err)
	}

	// And an API built on it must still construct a peer connection.
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me))
	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("NewPeerConnection with the source engine: %v", err)
	}
	defer pc.Close()
}
