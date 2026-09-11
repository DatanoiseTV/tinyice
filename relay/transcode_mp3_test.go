package relay

import (
	"bytes"
	"context"
	"math"
	"testing"

	shine "github.com/braheezy/shine-mp3/pkg/mp3"
)

// mpeg1Layer3Kbps is the MPEG-1 Layer III bitrate table indexed by the
// 4-bit bitrate_index in the frame header (ISO 11172-3, table B.3).
var mpeg1Layer3Kbps = [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}

// mp3FrameBitrates walks the MP3 frames in data using each header's own
// frame length (so a false sync inside audio payload can't be mistaken
// for a header) and returns the bitrate each frame declares.
func mp3FrameBitrates(t *testing.T, data []byte, sampleRate int) []int {
	t.Helper()
	var out []int
	i := 0
	for i+4 <= len(data) {
		h := data[i : i+4]
		if h[0] != 0xFF || h[1]&0xE0 != 0xE0 {
			i++
			continue
		}
		version := (h[1] >> 3) & 0x03 // 3 = MPEG-1
		layer := (h[1] >> 1) & 0x03   // 1 = Layer III
		if version != 3 || layer != 1 {
			i++
			continue
		}
		bitrateIndex := h[2] >> 4
		padding := int((h[2] >> 1) & 0x01)
		kbps := mpeg1Layer3Kbps[bitrateIndex]
		if kbps == 0 {
			i++
			continue
		}
		out = append(out, kbps)
		i += 144*kbps*1000/sampleRate + padding
	}
	return out
}

// tonePCM returns n seconds of a 440 Hz stereo S16LE tone at sampleRate,
// which is what the AutoDJ decoders hand the encoder.
func tonePCM(seconds float64, sampleRate int) []byte {
	n := int(seconds * float64(sampleRate))
	buf := make([]byte, n*4)
	for i := 0; i < n; i++ {
		v := int16(12000 * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate)))
		buf[i*4+0] = byte(v)
		buf[i*4+1] = byte(v >> 8)
		buf[i*4+2] = byte(v)
		buf[i*4+3] = byte(v >> 8)
	}
	return buf
}

// EncodeMP3 must emit frames at the bitrate it was asked for. shine-mp3's
// NewEncoder hard-codes 128 kbps and applyShineBitrate overrides that by
// writing the encoder's internal fields — a surface that has already been
// renamed once between library versions. If the override silently stops
// taking effect, every non-128 AutoDJ and transcoder would revert to 128
// kbps with no error anywhere; this is the test that notices.
func TestEncodeMP3HonoursRequestedBitrate(t *testing.T) {
	const sampleRate = 44100
	for _, kbps := range []int{64, 128, 192} {
		t.Run(itoa(kbps)+"kbps", func(t *testing.T) {
			r := NewRelay(false, nil)
			out := r.GetOrCreateStream("/mp3-test")
			// The encoder skips work while a mount has no listeners.
			offset, _ := out.Subscribe("probe", 0)

			pcm := bytes.NewReader(tonePCM(1.0, sampleRate))
			EncodeMP3(context.Background(), r, out, pcm, kbps, nil, false, sampleRate)

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

			frames := mp3FrameBitrates(t, encoded, sampleRate)
			// One second at 44.1 kHz is ~38 MPEG-1 Layer III frames.
			if len(frames) < 30 {
				t.Fatalf("parsed only %d MP3 frames from %d bytes; expected ~38", len(frames), len(encoded))
			}
			for i, got := range frames {
				if got != kbps {
					t.Fatalf("frame %d declares %d kbps, want %d (override not applied)", i, got, kbps)
				}
			}
		})
	}
}

// An unsupported combination must be reported, not silently produce a
// stream at a different rate.
func TestApplyShineBitrateRejectsUnsupported(t *testing.T) {
	enc := shine.NewEncoder(44100, 2)
	// 8 kbps is only valid for MPEG-2/2.5, not MPEG-1 at 44.1 kHz.
	if applyShineBitrate(enc, 44100, 8) {
		t.Error("applyShineBitrate accepted 8 kbps at 44.1 kHz, which MPEG-1 has no index for")
	}
	if !applyShineBitrate(enc, 44100, 320) {
		t.Error("applyShineBitrate rejected 320 kbps at 44.1 kHz, which is valid")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
