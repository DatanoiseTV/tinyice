package relay

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// makeOggPage builds the bytes of a single Ogg page with the given granule
// position and body length. The body content is zero-filled.
func makeOggPage(granule uint64, serial uint32, seq uint32, bodyLen int) []byte {
	numSegments := (bodyLen + 254) / 255
	if numSegments == 0 {
		numSegments = 1
	}
	page := make([]byte, 0, 27+numSegments+bodyLen)
	page = append(page, 'O', 'g', 'g', 'S')
	page = append(page, 0) // version
	page = append(page, 0) // header type
	var gp [8]byte
	binary.LittleEndian.PutUint64(gp[:], granule)
	page = append(page, gp[:]...)
	var ser [4]byte
	binary.LittleEndian.PutUint32(ser[:], serial)
	page = append(page, ser[:]...)
	var sq [4]byte
	binary.LittleEndian.PutUint32(sq[:], seq)
	page = append(page, sq[:]...)
	page = append(page, 0, 0, 0, 0) // CRC — FindOggHeaderEnd doesn't verify
	page = append(page, byte(numSegments))
	remaining := bodyLen
	for i := 0; i < numSegments; i++ {
		if remaining >= 255 {
			page = append(page, 255)
			remaining -= 255
		} else {
			page = append(page, byte(remaining))
			remaining = 0
		}
	}
	page = append(page, make([]byte, bodyLen)...)
	return page
}

func TestFindOggHeaderEnd_HeadersThenAudio(t *testing.T) {
	hdr1 := makeOggPage(0, 42, 0, 19)     // OpusHead-sized BOS
	hdr2 := makeOggPage(0, 42, 1, 64)     // Tags
	audio := makeOggPage(960, 42, 2, 200) // First audio page
	data := append(append(append([]byte{}, hdr1...), hdr2...), audio...)

	end, needMore, abort := FindOggHeaderEnd(data)
	if abort || needMore {
		t.Fatalf("unexpected flags: needMore=%v abort=%v", needMore, abort)
	}
	if end != len(hdr1)+len(hdr2) {
		t.Fatalf("end=%d, want %d", end, len(hdr1)+len(hdr2))
	}
}

func TestFindOggHeaderEnd_NeedMore(t *testing.T) {
	hdr := makeOggPage(0, 1, 0, 12)
	_, needMore, abort := FindOggHeaderEnd(hdr)
	if abort {
		t.Fatalf("abort unexpected")
	}
	if !needMore {
		t.Fatalf("expected needMore=true when no audio page yet")
	}
}

func TestFindOggHeaderEnd_Abort(t *testing.T) {
	// At least 27 bytes so the function gets past the "need more data" check
	// and actually evaluates the OggS magic.
	data := make([]byte, 64)
	copy(data, "ID3\x03\x00\x00")
	_, _, abort := FindOggHeaderEnd(data)
	if !abort {
		t.Fatalf("expected abort on non-Ogg bytes")
	}
}

func TestFindOggHeaderEnd_IgnoresContinuation(t *testing.T) {
	// A page whose granule is the magic continuation sentinel should be
	// treated as a header page, not audio.
	cont := makeOggPage(0xFFFFFFFFFFFFFFFF, 1, 0, 10)
	audio := makeOggPage(480, 1, 1, 20)
	data := append(cont, audio...)
	end, needMore, abort := FindOggHeaderEnd(data)
	if abort || needMore {
		t.Fatalf("unexpected flags: needMore=%v abort=%v", needMore, abort)
	}
	if end != len(cont) {
		t.Fatalf("end=%d, want %d", end, len(cont))
	}
}

func TestOpenDecoder_RejectsUnknown(t *testing.T) {
	_, err := OpenDecoder(bytes.NewReader([]byte{0x00, 0x01, 0x02, 0x03, 0x04}))
	if err == nil {
		t.Fatalf("expected error for unknown format")
	}
}

// makeWavHeader builds a minimal PCM WAV header followed by `dataBytes` zero
// payload bytes. The data chunk size matches dataBytes so the decoder has a
// well-formed stream for the test.
func makeWavHeader(channels, sampleRate, bitsPerSample, dataBytes int) []byte {
	byteRate := sampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8
	buf := &bytes.Buffer{}
	buf.WriteString("RIFF")
	binary.Write(buf, binary.LittleEndian, uint32(36+dataBytes))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(buf, binary.LittleEndian, uint32(16))
	binary.Write(buf, binary.LittleEndian, uint16(1)) // PCM
	binary.Write(buf, binary.LittleEndian, uint16(channels))
	binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	binary.Write(buf, binary.LittleEndian, uint32(byteRate))
	binary.Write(buf, binary.LittleEndian, uint16(blockAlign))
	binary.Write(buf, binary.LittleEndian, uint16(bitsPerSample))
	buf.WriteString("data")
	binary.Write(buf, binary.LittleEndian, uint32(dataBytes))
	buf.Write(make([]byte, dataBytes))
	return buf.Bytes()
}

func TestOpenDecoder_WAV(t *testing.T) {
	// 2ch, 48k, 16-bit, 1 second of silence = 48000 frames * 4 bytes
	data := makeWavHeader(2, 48000, 16, 48000*4)
	dec, err := OpenDecoder(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("OpenDecoder: %v", err)
	}
	if dec.SampleRate() != 48000 {
		t.Fatalf("SampleRate=%d, want 48000", dec.SampleRate())
	}
	out := make([]byte, 4096)
	total := 0
	for {
		n, err := dec.Read(out)
		total += n
		if err != nil {
			break
		}
		if total > 48000*4*2 {
			t.Fatalf("decoder returned more bytes than expected")
		}
	}
	// Input = 48000 stereo 16-bit frames = 192000 bytes. The adapter emits
	// S16LE stereo, so output should be the same.
	if total != 48000*4 {
		t.Fatalf("decoded %d bytes, want %d", total, 48000*4)
	}
}

func TestOpenDecoder_WAV_MonoFloat(t *testing.T) {
	// Build a mono float32 WAV manually.
	buf := &bytes.Buffer{}
	buf.WriteString("RIFF")
	binary.Write(buf, binary.LittleEndian, uint32(36+16))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(buf, binary.LittleEndian, uint32(16))
	binary.Write(buf, binary.LittleEndian, uint16(3)) // IEEE float
	binary.Write(buf, binary.LittleEndian, uint16(1)) // mono
	binary.Write(buf, binary.LittleEndian, uint32(44100))
	binary.Write(buf, binary.LittleEndian, uint32(44100*4))
	binary.Write(buf, binary.LittleEndian, uint16(4))
	binary.Write(buf, binary.LittleEndian, uint16(32))
	buf.WriteString("data")
	binary.Write(buf, binary.LittleEndian, uint32(16))
	// Four float32 samples at 0.5
	for i := 0; i < 4; i++ {
		binary.Write(buf, binary.LittleEndian, math.Float32bits(0.5))
	}
	dec, err := OpenDecoder(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("OpenDecoder: %v", err)
	}
	if dec.SampleRate() != 44100 {
		t.Fatalf("SampleRate=%d, want 44100", dec.SampleRate())
	}
	out := make([]byte, 64)
	n, _ := dec.Read(out)
	// 4 mono frames → 4 stereo S16LE frames = 16 bytes
	if n != 16 {
		t.Fatalf("n=%d, want 16", n)
	}
	// Verify the stereo channels are both set to ~0.5 * 32767 = 16383 (LE).
	for i := 0; i < 4; i++ {
		l := int16(binary.LittleEndian.Uint16(out[i*4:]))
		r := int16(binary.LittleEndian.Uint16(out[i*4+2:]))
		if l != r {
			t.Errorf("frame %d: L=%d R=%d, expected identical (mono duplicated)", i, l, r)
		}
		if l < 16000 || l > 17000 {
			t.Errorf("frame %d: sample %d out of expected range ~16383", i, l)
		}
	}
}

func TestOpenDecoder_DetectsMP3ID3(t *testing.T) {
	// We can't fully decode without a real MP3 frame, but the factory should
	// at least attempt the MP3 path and fail with an MP3-specific error.
	data := append([]byte("ID3\x03\x00\x00\x00\x00\x00\x00"), 0xFF, 0xFB)
	_, err := OpenDecoder(bytes.NewReader(data))
	if err == nil {
		return // some go-mp3 versions are lenient; either outcome is OK
	}
	// Error is fine — just make sure we didn't report "unknown format".
	if bytes.Contains([]byte(err.Error()), []byte("unknown audio format")) {
		t.Fatalf("MP3 sniff should not fall through to unknown: %v", err)
	}
}
