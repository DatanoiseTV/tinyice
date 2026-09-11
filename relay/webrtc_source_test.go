package relay

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// A publisher whose offer carries two tracks used to run the OnTrack pump
// twice on the same doneCh; when it disconnected the second deferred
// close panicked with "close of closed channel" in a pion-owned goroutine
// and ended the process. This drives a real in-process peer connection
// through HandleSourceOffer with two Opus tracks and then closes it. On
// the unfixed code the test binary dies.
func TestSourceOfferWithTwoTracksDoesNotCrash(t *testing.T) {
	r := NewRelay(false, nil)
	wm := NewWebRTCManager(r)

	pub, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()
	a1, _ := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus}, "audio1", "pub")
	a2, _ := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus}, "audio2", "pub")
	for _, tr := range []*webrtc.TrackLocalStaticSample{a1, a2} {
		if _, err := pub.AddTrack(tr); err != nil {
			t.Fatal(err)
		}
	}
	offer, err := pub.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gathered := webrtc.GatheringCompletePromise(pub)
	if err := pub.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	<-gathered

	answer, err := wm.HandleSourceOffer("/two-tracks", *pub.LocalDescription())
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.SetRemoteDescription(*answer); err != nil {
		t.Fatal(err)
	}

	// Let both remote tracks come up and pump for a bit.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_ = a1.WriteSample(media.Sample{Data: []byte{0xfc, 0xff, 0xfe}, Duration: 20 * time.Millisecond})
		_ = a2.WriteSample(media.Sample{Data: []byte{0xfc, 0xff, 0xfe}, Duration: 20 * time.Millisecond})
		time.Sleep(20 * time.Millisecond)
	}
	if err := pub.Close(); err != nil {
		t.Fatal(err)
	}
	// Both pumps unwind here; on the old code the second close(doneCh)
	// panics in a goroutine we don't own and there is no way to recover
	// it from a test — the process just exits. Deregistration rides on
	// pion's connection-state callback, which is slow under -race, so
	// poll for it rather than sleeping a fixed time.
	deadline = time.Now().Add(40 * time.Second)
	for {
		wm.mu.Lock()
		_, stillRegistered := wm.sources["/two-tracks"]
		wm.mu.Unlock()
		if !stillRegistered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("source is still registered 10s after the publisher closed")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A viewer pump parked in the pre-sync "OggS" search must exit when the
// mount is closed under it. Stream.Close closes every listener's signal
// channel; a closed channel is always ready, and the old loop treated
// that as "data arrived", read zero bytes, and spun at 100% of a core
// until the process was restarted.
func TestViewerPumpExitsWhenStreamClosesBeforeSync(t *testing.T) {
	r := NewRelay(false, nil)
	wm := NewWebRTCManager(r)
	stream := r.GetOrCreateStream("/never-syncs")
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus}, "a", "v")
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		// No peer connection is needed: the pump only touches pc after
		// it has found sync, which never happens here.
		wm.streamToTrack(context.Background(), nil, track, stream)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond) // let it subscribe and park
	stream.Close()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("viewer pump did not exit after Stream.Close — it is spinning on the closed signal channel")
	}
}

// A rejected offer must not leak the peer connection. Every early return
// in the three offer handlers used to leave it open, and pion keeps four
// goroutines plus RTCP tickers alive until Close — measured: 30 offers
// that pass SetRemoteDescription's parse but carry no ICE credentials
// left 120 goroutines behind. /webrtc/offer is unauthenticated, so that
// was a free per-request leak.
func TestRejectedOfferClosesPeerConnection(t *testing.T) {
	r := NewRelay(false, nil)
	st := r.GetOrCreateStream("/leak")
	st.mu.Lock()
	st.ContentType = "audio/ogg" // HandleOffer refuses non-Opus mounts before creating a PC
	st.mu.Unlock()
	wm := NewWebRTCManager(r)
	// Parses, but has no ice-ufrag: rejected after the PC exists.
	sdp := "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\nc=IN IP4 0.0.0.0\r\na=rtpmap:111 opus/48000/2\r\na=recvonly\r\na=mid:0\r\n"

	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	before := runtime.NumGoroutine()
	for i := 0; i < 30; i++ {
		if _, err := wm.HandleOffer("/leak", webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp}); err == nil {
			t.Fatal("offer without ICE credentials was accepted")
		}
	}
	time.Sleep(700 * time.Millisecond)
	runtime.GC()
	if delta := runtime.NumGoroutine() - before; delta > 12 {
		t.Fatalf("goroutines grew by %d across 30 rejected offers — peer connections are leaking", delta)
	}
}
