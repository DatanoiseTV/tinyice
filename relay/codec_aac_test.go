package relay

import (
	"testing"
)

func TestParseADTSFrames(t *testing.T) {
	// Build a minimal valid ADTS frame
	// Sync word: 0xFFF, ID=1(MPEG-4), Layer=0, protection_absent=1
	// Profile=1(LC), SampleRateIdx=3(48000), private=0, ChannelConfig=2(stereo)
	// Frame length = 7 (header only, no payload for this test) + 10 payload = 17
	frame := make([]byte, 17)
	frame[0] = 0xFF
	frame[1] = 0xF1 // sync + ID=0(MPEG-4) + layer=00 + protection_absent=1
	frame[2] = 0x50 // profile=01(LC) + sampling_freq_idx=0100(44100) + private=0 + channel_config high bit=0
	frame[3] = 0x80 // channel_config=010(stereo) + original=0 + home=0 + copyright=0 + frame_length high 2 bits=00
	// Frame length = 17 = 0x011 in 13 bits
	frame[3] |= byte((17 >> 11) & 0x03)
	frame[4] = byte((17 >> 3) & 0xFF)
	frame[5] = byte((17&0x07)<<5) | 0x1F // frame_length low + buffer fullness high
	frame[6] = 0xFC                      // buffer fullness low + num_raw_data_blocks=0

	// Fill payload
	for i := 7; i < 17; i++ {
		frame[i] = byte(i)
	}

	frames, remaining := ParseADTSFrames(frame)
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(frames))
	}
	if frames[0].FrameLength != 17 {
		t.Fatalf("expected frame length 17, got %d", frames[0].FrameLength)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected no remaining, got %d bytes", len(remaining))
	}
}

func TestAACSampleRate(t *testing.T) {
	if AACSampleRate(3) != 48000 {
		t.Fatalf("expected 48000, got %d", AACSampleRate(3))
	}
	if AACSampleRate(4) != 44100 {
		t.Fatalf("expected 44100, got %d", AACSampleRate(4))
	}
	if AACSampleRate(15) != 0 {
		t.Fatalf("expected 0 for invalid index, got %d", AACSampleRate(15))
	}
}

func TestBuildAudioSpecificConfig(t *testing.T) {
	// AAC-LC, 44100 Hz (idx=4), stereo (config=2)
	asc := BuildAudioSpecificConfig(1, 4, 2)
	if len(asc) != 2 {
		t.Fatalf("expected 2 bytes, got %d", len(asc))
	}
	// audioObjectType=2 (LC=profile+1), samplingFreqIdx=4, channelConfig=2
	// 00010 0100 010 00000 -> 0x12 0x10
	if asc[0] != 0x12 || asc[1] != 0x10 {
		t.Fatalf("expected 0x12 0x10, got 0x%02X 0x%02X", asc[0], asc[1])
	}
}
