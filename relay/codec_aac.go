package relay

// AAC ADTS (Audio Data Transport Stream) parser.
// ADTS is the framing format for AAC audio, commonly used in MPEG-TS.

// ADTSFrame represents a parsed AAC ADTS frame.
type ADTSFrame struct {
	Profile       byte   // 0=Main, 1=LC, 2=SSR, 3=LTP
	SampleRateIdx byte   // Index into sample rate table
	ChannelConfig byte   // Channel configuration
	FrameLength   int    // Total frame length including header
	Data          []byte // Raw frame data (header + payload)
}

// Standard AAC sample rates indexed by SampleRateIdx
var aacSampleRates = []int{
	96000, 88200, 64000, 48000, 44100, 32000,
	24000, 22050, 16000, 12000, 11025, 8000, 7350,
}

// AACSampleRate returns the sample rate for the given index, or 0 if invalid.
func AACSampleRate(idx byte) int {
	if int(idx) < len(aacSampleRates) {
		return aacSampleRates[idx]
	}
	return 0
}

// ParseADTSFrames splits a byte stream into individual ADTS frames.
// Returns parsed frames and any remaining bytes that don't form a complete frame.
func ParseADTSFrames(data []byte) ([]ADTSFrame, []byte) {
	var frames []ADTSFrame
	offset := 0

	for offset+7 <= len(data) {
		// ADTS sync word: 0xFFF (12 bits)
		if data[offset] != 0xFF || (data[offset+1]&0xF0) != 0xF0 {
			offset++
			continue
		}

		// Parse ADTS header (7 bytes fixed, optionally 9 with CRC)
		hasCRC := (data[offset+1] & 0x01) == 0 // protection_absent=0 means CRC present
		headerSize := 7
		if hasCRC {
			headerSize = 9
		}

		if offset+headerSize > len(data) {
			break
		}

		profile := (data[offset+2] >> 6) & 0x03
		sampleRateIdx := (data[offset+2] >> 2) & 0x0F
		channelConfig := ((data[offset+2] & 0x01) << 2) | ((data[offset+3] >> 6) & 0x03)

		// Frame length (13 bits)
		frameLen := (int(data[offset+3]&0x03) << 11) |
			(int(data[offset+4]) << 3) |
			(int(data[offset+5]>>5) & 0x07)

		if frameLen < headerSize || offset+frameLen > len(data) {
			break // Incomplete frame
		}

		frameData := make([]byte, frameLen)
		copy(frameData, data[offset:offset+frameLen])

		frames = append(frames, ADTSFrame{
			Profile:       profile,
			SampleRateIdx: sampleRateIdx,
			ChannelConfig: channelConfig,
			FrameLength:   frameLen,
			Data:          frameData,
		})

		offset += frameLen
	}

	// Return remaining bytes
	var remaining []byte
	if offset < len(data) {
		remaining = data[offset:]
	}
	return frames, remaining
}

// BuildADTSHeader builds a 7-byte ADTS header for a raw AAC frame of
// `payloadLen` bytes. Used to wrap raw AAC frames coming out of RTMP
// (which carries AAC without ADTS) so the MPEG-TS muxer can declare
// stream_type=0x0F (ADTS-AAC) in the PMT and downstream decoders
// (ffmpeg, Safari, hls.js) can parse the audio.
//
// profile          — AAC profile from the AudioSpecificConfig
//
//	(0=Main, 1=LC, 2=SSR, 3=LTP)
//
// sampleRateIdx    — index into the AAC sample-rate table (0–12)
// channelConfig    — AAC channel configuration (1=mono, 2=stereo, …)
func BuildADTSHeader(profile, sampleRateIdx, channelConfig byte, payloadLen int) []byte {
	frameLen := payloadLen + 7
	h := make([]byte, 7)
	h[0] = 0xFF
	// 0b11110001 — sync continued, MPEG-4 version (ID=0), layer=00,
	// protection_absent=1 (no CRC).
	h[1] = 0xF1
	h[2] = (profile << 6) | ((sampleRateIdx & 0x0F) << 2) | ((channelConfig >> 2) & 0x01)
	h[3] = ((channelConfig & 0x03) << 6) | byte((frameLen>>11)&0x03)
	h[4] = byte((frameLen >> 3) & 0xFF)
	// bottom 3 bits of frame_length plus top 5 bits of buffer_fullness
	// (0x7FF VBR sentinel → top five bits are all 1).
	h[5] = byte(((frameLen & 0x07) << 5) | 0x1F)
	// Low 6 bits of buffer_fullness (0x3F) + num_raw_data_blocks (00).
	h[6] = 0xFC
	return h
}

// BuildAudioSpecificConfig creates the 2-byte AudioSpecificConfig
// used in MP4/fMP4 init segments for AAC-LC.
func BuildAudioSpecificConfig(profile, sampleRateIdx, channelConfig byte) []byte {
	// AudioSpecificConfig (ISO 14496-3):
	// 5 bits: audioObjectType (profile + 1)
	// 4 bits: samplingFrequencyIndex
	// 4 bits: channelConfiguration
	// 3 bits: padding (GASpecificConfig: frameLengthFlag=0, dependsOnCoreCoder=0, extensionFlag=0)
	objectType := profile + 1
	b0 := (objectType << 3) | (sampleRateIdx >> 1)
	b1 := (sampleRateIdx << 7) | (channelConfig << 3)
	return []byte{b0, b1}
}
