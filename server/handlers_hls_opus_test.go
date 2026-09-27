package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/relay"
)

func newHLSTestServer(t *testing.T) (*Server, *relay.Relay, context.CancelFunc) {
	t.Helper()
	r := relay.NewRelay(false, nil)
	hlsCtx, cancel := context.WithCancel(context.Background())
	return &Server{
		Config:        &config.Config{},
		Relay:         r,
		hlsOutputs:    make(map[string]*relay.HLSOutput),
		hlsLastAccess: make(map[string]time.Time),
		hlsCtx:        hlsCtx,
	}, r, cancel
}

// An Ogg/Opus mount must not get an HLS output. The MPEG-TS muxer has no
// Opus stream type, so before this gate the segmenter emitted raw Ogg
// pages under stream type 0x03 (MP3) and every player rejected them —
// measured as "mp3float: Header missing" from ffprobe and
// MEDIA_ERR_SRC_NOT_SUPPORTED / DEMUXER_ERROR_COULD_NOT_PARSE in Chrome.
// A 404 instead lets the player fall back to WHEP, which carries Opus
// natively.
func TestRegisterHLSDeclinesOpusMount(t *testing.T) {
	s, r, cancel := newHLSTestServer(t)
	defer cancel()

	opus := r.GetOrCreateStream("/opus-mount")
	opus.ContentType = "audio/ogg"
	mp3 := r.GetOrCreateStream("/mp3-mount")
	mp3.ContentType = "audio/mpeg"

	if got := s.RegisterHLS("/opus-mount"); got != nil {
		t.Error("RegisterHLS built an output for an Opus mount; its segments are undecodable")
	}
	out := s.RegisterHLS("/mp3-mount")
	if out == nil {
		t.Fatal("RegisterHLS declined an MP3 mount, which the TS muxer handles natively")
	}
	out.Stop()

	// The HTTP surface must agree: a playlist request for the Opus mount
	// is a clean 404, not an empty or undecodable playlist.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/opus-mount/playlist.m3u8", nil)
	s.handleHLSPlaylist(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("playlist for Opus mount = %d, want 404 (body %q)", rec.Code, rec.Body.String())
	}
}

// The /video sub-mount appears later than its audio mount: an RTMP
// publisher only creates it on the first video tag, and a browser's
// H.264 encoder took 13 s to emit its first frame in testing. A viewer
// who requested the playlist inside that window used to pin the output
// to audio-only for the whole source session, because RegisterHLS
// samples the sub-mount exactly once. The playlist handler must swap in
// an A/V output as soon as the video shows up.
func TestHLSPicksUpLateVideoSubMount(t *testing.T) {
	s, r, cancel := newHLSTestServer(t)
	defer cancel()

	audio := r.GetOrCreateStream("/late-video")
	audio.ContentType = "audio/mpeg"

	// First request: audio only, because the video sub-mount does not
	// exist yet.
	rec := httptest.NewRecorder()
	s.handleHLSPlaylist(rec, httptest.NewRequest(http.MethodGet, "/late-video/playlist.m3u8", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("first playlist request = %d, want 200", rec.Code)
	}
	first := s.getHLSOutput("/late-video")
	if first == nil {
		t.Fatal("no HLS output registered for the audio mount")
	}
	if first.HasVideo() {
		t.Fatal("output claims video before the /video sub-mount exists")
	}

	// Video arrives.
	video := r.GetOrCreateStream("/late-video/video")
	video.ContentType = "video/h264"

	rec2 := httptest.NewRecorder()
	s.handleHLSPlaylist(rec2, httptest.NewRequest(http.MethodGet, "/late-video/playlist.m3u8", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("second playlist request = %d, want 200", rec2.Code)
	}
	second := s.getHLSOutput("/late-video")
	if second == nil {
		t.Fatal("HLS output disappeared after the video sub-mount appeared")
	}
	if !second.HasVideo() {
		t.Error("HLS output is still audio-only after the /video sub-mount appeared")
	}

	// A third request must not churn the output again: HasVideo() is now
	// true, so the swap is a once-per-source event rather than a reset
	// on every playlist poll (hls.js polls every TARGETDURATION/2).
	rec3 := httptest.NewRecorder()
	s.handleHLSPlaylist(rec3, httptest.NewRequest(http.MethodGet, "/late-video/playlist.m3u8", nil))
	if third := s.getHLSOutput("/late-video"); third != second {
		t.Error("playlist poll re-registered an output that already had video")
	}
	s.UnregisterHLS("/late-video")
}
