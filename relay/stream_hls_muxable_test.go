package relay

import "testing"

// HLSMuxableAudio gates whether a mount is offered over HLS at all. The
// MPEG-TS muxer can declare exactly two audio stream types (0x03 MP3,
// 0x0F ADTS AAC); before this gate existed an Ogg/Opus mount produced
// segments carrying raw Ogg pages under stream type 0x03, which ffprobe
// reads as "mp3float: Header missing" and Chrome reports as
// DEMUXER_ERROR_COULD_NOT_PARSE.
//
// The Opus and Ogg rows are the ones that matter: every WebRTC publisher
// lands there. The FLAC / Vorbis rows fence the allow-list shape — a
// deny-list written as "not ogg" passes those two and is wrong.
func TestHLSMuxableAudio(t *testing.T) {
	cases := []struct {
		contentType string
		want        bool
	}{
		{"audio/mpeg", true},
		{"audio/mp3", true},
		{"AUDIO/MPEG", true}, // case folded
		{"audio/aac", true},
		{"audio/aacp", true},
		{"audio/mp4a-latm", true},
		{"", true}, // a fresh Stream's default is MP3

		{"audio/ogg", false},
		{"application/ogg", false},
		{"audio/opus", false},
		{"audio/OGG; codecs=opus", false},
		{"audio/flac", false},
		{"audio/vorbis", false},
		{"video/h264", false},
	}
	for _, c := range cases {
		s := &Stream{ContentType: c.contentType}
		if got := s.HLSMuxableAudio(); got != c.want {
			t.Errorf("HLSMuxableAudio(%q) = %v, want %v", c.contentType, got, c.want)
		}
	}
}
