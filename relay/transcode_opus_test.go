package relay

import (
	"bytes"
	"context"
	"io"
	"testing"
)

// EncodeOpus must encode every PCM sample it is handed, for the same
// reason EncodeMP3 must (#63): a dependency bump silently changed
// Encoder.Write's framing in shine-mp3 and produced frames that were
// half audio and half silence, while every frame header still parsed as
// correct. The pre-existing MP3 test checked frame headers only, so it
// saw nothing. Opus had no content test at all, and the Opus encoder
// gets bumped by dependabot on the same cadence.
//
// Limit worth stating: kazzmir/opus-go is the only Opus implementation
// here, so this encodes and decodes through one library. That makes it a
// content-continuity test, not an independent format check — it catches
// dropped or repeated audio, not a format regression where both halves
// move together.
func TestEncodeOpusRoundTripHasNoDroppedAudio(t *testing.T) {
	const sampleRate = 48000
	r := NewRelay(false, nil)
	out := r.GetOrCreateStream("/opus-roundtrip")
	offset, _ := out.Subscribe("probe", 0)

	pcm := bytes.NewReader(tonePCM(2.0, sampleRate))
	EncodeOpus(context.Background(), r, out, pcm, 128, nil, false)

	var encoded []byte
	buf := make([]byte, 64*1024)
	for {
		n, next, _ := out.Buffer.ReadAt(offset, buf)
		if n == 0 {
			break
		}
		encoded = append(encoded, buf[:n]...)
		offset = next
	}
	if len(encoded) == 0 {
		t.Fatal("encoder produced no output")
	}

	dec, err := OpenDecoder(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("decoding encoder output: %v", err)
	}
	raw, err := io.ReadAll(dec)
	if err != nil {
		t.Fatalf("reading decoded PCM: %v", err)
	}

	// Skip codec delay at both ends, then walk 64-sample windows: a 440 Hz
	// tone at this level leaves no quiet window in the steady state.
	const window = 64
	start, end := 8192*4, len(raw)-8192*4
	if end-start < sampleRate*2 {
		t.Fatalf("decoded only %d bytes from 2 s of input", len(raw))
	}
	for i := start; i+window*4 <= end; i += window * 4 {
		peak := 0
		for j := 0; j < window; j++ {
			v := int(int16(uint16(raw[i+j*4]) | uint16(raw[i+j*4+1])<<8))
			if v < 0 {
				v = -v
			}
			if v > peak {
				peak = v
			}
		}
		if peak < 3000 {
			t.Fatalf("decoded audio drops out at byte %d (window peak %d, want ~12000)", i, peak)
		}
	}
}
