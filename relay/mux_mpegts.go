package relay

import (
	"bytes"
	"encoding/binary"
)

const (
	tsPacketSize = 188
	tsSyncByte   = 0x47
	patPID       = 0x0000
	pmtPID       = 0x1000
	audioPID     = 0x0100
	videoPID     = 0x0101
	mp3StreamID  = 0xC0
	h264StreamID = 0xE0
	tsClockRate  = 90000 // 90kHz PTS clock
)

// TSMuxer creates MPEG-TS segments from audio and video frames.
type TSMuxer struct {
	audioContinuity uint8
	videoContinuity uint8
	patContinuity   uint8
	pmtContinuity   uint8
}

// NewTSMuxer creates a new MPEG-TS muxer.
func NewTSMuxer() *TSMuxer {
	return &TSMuxer{}
}

// MuxMP3Segment wraps MP3 audio data into a complete MPEG-TS segment.
// pts is the presentation timestamp in 90kHz units.
func (m *TSMuxer) MuxMP3Segment(mp3Data []byte, pts int64) []byte {
	var buf bytes.Buffer

	// 1. Write PAT
	m.writePAT(&buf)

	// 2. Write PMT
	m.writePMT(&buf)

	// 3. Write audio PES packets
	m.writeAudioPES(&buf, mp3Data, pts)

	return buf.Bytes()
}

// Audio stream_type values used in the MPEG-TS PMT.
const (
	audioStreamTypeMP3 byte = 0x03
	audioStreamTypeAAC byte = 0x0F // ADTS-wrapped AAC
)

// MuxAVSegment wraps audio and video data into a complete MPEG-TS segment.
// audioData is MP3 or ADTS-AAC (pick via audioStreamType); videoData is
// H.264 Annex-B. audioPTS and videoPTS are 90 kHz PTS values.
//
// The PES stream_id field stays mp3StreamID for MP3 and switches to
// 0xC0 for AAC as well (both are "audio stream 1" in ISO 13818-1 terms;
// the stream_type in the PMT is what actually distinguishes them).
func (m *TSMuxer) MuxAVSegment(audioData []byte, videoData []byte, audioPTS, videoPTS int64, audioStreamType byte) []byte {
	if audioStreamType == 0 {
		audioStreamType = audioStreamTypeMP3
	}
	var buf bytes.Buffer

	// Write PAT
	m.writePAT(&buf)

	// Write PMT (updated for A/V, with the correct audio stream_type)
	m.writePMTAV(&buf, audioStreamType)

	// Write video PES first (usually larger, keyframe-aligned). This is
	// the legacy one-PES-per-segment path (kept for byte-buffer HLS);
	// with no CompositionTime information we pass DTS == PTS and
	// buildPESHeader will emit just the 5-byte PTS form.
	if len(videoData) > 0 {
		m.writeVideoPES(&buf, videoData, videoPTS, videoPTS)
	}

	// Write audio PES
	if len(audioData) > 0 {
		m.writeAudioPES(&buf, audioData, audioPTS)
	}

	return buf.Bytes()
}

func (m *TSMuxer) writePMTAV(buf *bytes.Buffer, audioStreamType byte) {
	packet := make([]byte, tsPacketSize)

	packet[0] = tsSyncByte
	packet[1] = 0x40 | byte(pmtPID>>8)
	packet[2] = byte(pmtPID & 0xFF)
	packet[3] = 0x10 | (m.pmtContinuity & 0x0F)
	m.pmtContinuity++

	packet[4] = 0x00 // pointer field

	pmt := packet[5:]
	pmt[0] = 0x02                     // table_id
	pmt[1] = 0xB0                     // section_syntax_indicator
	pmt[2] = 23                       // section_length (larger for A/V)
	pmt[3] = 0x00                     // program_number high
	pmt[4] = 0x01                     // program_number low
	pmt[5] = 0xC1                     // version=0, current_next=1
	pmt[6] = 0x00                     // section_number
	pmt[7] = 0x00                     // last_section_number
	pmt[8] = 0xE0 | byte(videoPID>>8) // PCR PID = video
	pmt[9] = byte(videoPID & 0xFF)
	pmt[10] = 0xF0 // reserved + program_info_length high
	pmt[11] = 0x00 // program_info_length low

	// Video stream: H.264
	pmt[12] = 0x1B // stream_type = 0x1B (H.264)
	pmt[13] = 0xE0 | byte(videoPID>>8)
	pmt[14] = byte(videoPID & 0xFF)
	pmt[15] = 0xF0
	pmt[16] = 0x00

	// Audio stream: MP3 (0x03) or ADTS AAC (0x0F) — chosen by caller.
	pmt[17] = audioStreamType
	pmt[18] = 0xE0 | byte(audioPID>>8)
	pmt[19] = byte(audioPID & 0xFF)
	pmt[20] = 0xF0
	pmt[21] = 0x00

	// CRC32
	crc := crc32MPEG2(pmt[0:22])
	binary.BigEndian.PutUint32(pmt[22:26], crc)

	for i := 5 + 26; i < tsPacketSize; i++ {
		packet[i] = 0xFF
	}

	buf.Write(packet)
}

// writeVideoPES emits one video PES. Pass dts == pts (or dts < 0) when
// the stream has no B-frames; pass the real decode timestamp otherwise
// so the decoder schedules B-frames correctly instead of the picture
// "boomeranging" between frames.
func (m *TSMuxer) writeVideoPES(buf *bytes.Buffer, data []byte, pts, dts int64) {
	pesHeader := buildPESHeader(h264StreamID, len(data), pts, dts)
	payload := append(pesHeader, data...)
	// PCR uses DTS (the earliest timestamp in the stream) when we have
	// both, otherwise PTS. PCR on the PID declared as PCR_PID in the PMT.
	pcr := dts
	if pcr < 0 || pcr > pts {
		pcr = pts
	}
	m.writePESPackets(buf, videoPID, &m.videoContinuity, payload, pcr)
}

func (m *TSMuxer) writePAT(buf *bytes.Buffer) {
	packet := make([]byte, tsPacketSize)

	// TS header
	packet[0] = tsSyncByte
	packet[1] = 0x40                            // payload_unit_start_indicator = 1, PID high bits = 0
	packet[2] = 0x00                            // PID low bits = 0 (PAT)
	packet[3] = 0x10 | (m.patContinuity & 0x0F) // no adaptation, payload only
	m.patContinuity++

	// Pointer field (required when payload_unit_start_indicator=1)
	packet[4] = 0x00

	// PAT table
	pat := packet[5:]
	pat[0] = 0x00 // table_id = 0 (PAT)
	pat[1] = 0xB0 // section_syntax_indicator=1, reserved
	pat[2] = 13   // section_length (remaining bytes including CRC)
	pat[3] = 0x00 // transport_stream_id high
	pat[4] = 0x01 // transport_stream_id low
	pat[5] = 0xC1 // version=0, current_next=1
	pat[6] = 0x00 // section_number
	pat[7] = 0x00 // last_section_number
	// Program entry: program_number=1, PMT PID=0x1000
	pat[8] = 0x00                    // program_number high
	pat[9] = 0x01                    // program_number low
	pat[10] = 0xE0 | byte(pmtPID>>8) // reserved + PID high
	pat[11] = byte(pmtPID & 0xFF)    // PID low

	// CRC32
	crc := crc32MPEG2(pat[0:12])
	binary.BigEndian.PutUint32(pat[12:16], crc)

	// Fill rest with 0xFF
	for i := 5 + 16; i < tsPacketSize; i++ {
		packet[i] = 0xFF
	}

	buf.Write(packet)
}

func (m *TSMuxer) writePMT(buf *bytes.Buffer) {
	packet := make([]byte, tsPacketSize)

	// TS header
	packet[0] = tsSyncByte
	packet[1] = 0x40 | byte(pmtPID>>8) // payload_unit_start + PID high
	packet[2] = byte(pmtPID & 0xFF)
	packet[3] = 0x10 | (m.pmtContinuity & 0x0F)
	m.pmtContinuity++

	// Pointer field
	packet[4] = 0x00

	// PMT table
	pmt := packet[5:]
	pmt[0] = 0x02                     // table_id = 2 (PMT)
	pmt[1] = 0xB0                     // section_syntax_indicator=1
	pmt[2] = 18                       // section_length
	pmt[3] = 0x00                     // program_number high
	pmt[4] = 0x01                     // program_number low
	pmt[5] = 0xC1                     // version=0, current_next=1
	pmt[6] = 0x00                     // section_number
	pmt[7] = 0x00                     // last_section_number
	pmt[8] = 0xE0 | byte(audioPID>>8) // PCR PID high
	pmt[9] = byte(audioPID & 0xFF)    // PCR PID low
	pmt[10] = 0xF0                    // reserved + program_info_length high
	pmt[11] = 0x00                    // program_info_length low = 0

	// Stream entry: MP3 audio
	pmt[12] = 0x03                     // stream_type = 0x03 (MP3/MPEG-1 Audio)
	pmt[13] = 0xE0 | byte(audioPID>>8) // reserved + elementary PID high
	pmt[14] = byte(audioPID & 0xFF)
	pmt[15] = 0xF0 // reserved + ES_info_length high
	pmt[16] = 0x00 // ES_info_length low = 0

	// CRC32
	crc := crc32MPEG2(pmt[0:17])
	binary.BigEndian.PutUint32(pmt[17:21], crc)

	for i := 5 + 21; i < tsPacketSize; i++ {
		packet[i] = 0xFF
	}

	buf.Write(packet)
}

func (m *TSMuxer) writeAudioPES(buf *bytes.Buffer, data []byte, pts int64) {
	// Audio has no B-frames, so DTS == PTS and the PES header only
	// advertises PTS (the 5-byte shorter form).
	pesHeader := buildPESHeader(mp3StreamID, len(data), pts, pts)
	payload := append(pesHeader, data...)
	// Audio-only PMT declares audio as PCR_PID, so the audio PES carries
	// the PCR. For A/V the PMT uses video as PCR_PID and writeVideoPES
	// already emits the PCR; in that case we pass -1 for pcrBase here so
	// this path emits no PCR and doesn't confuse the client with two
	// conflicting clocks.
	m.writePESPackets(buf, audioPID, &m.audioContinuity, payload, pts)
}

// writePESPackets splits `payload` across 188-byte TS packets on `pid`,
// emitting the PCR on the first packet (derived from pcrBase — usually the
// same value as the PES's PTS). Handles the edge case where exactly one
// byte of TS payload capacity is left after stuffing, which the previous
// implementation silently truncated.
func (m *TSMuxer) writePESPackets(buf *bytes.Buffer, pid uint16, continuity *uint8, payload []byte, pcrBase int64) {
	offset := 0
	first := true
	for offset < len(payload) {
		packet := make([]byte, tsPacketSize)
		packet[0] = tsSyncByte
		pidHigh := byte(pid>>8) & 0x1F
		if first {
			pidHigh |= 0x40 // payload_unit_start_indicator
		}
		packet[1] = pidHigh
		packet[2] = byte(pid & 0xFF)

		remaining := len(payload) - offset
		const payloadCapacity = tsPacketSize - 4 // 184 bytes

		wantPCR := first && pcrBase >= 0

		switch {
		case !wantPCR && remaining >= payloadCapacity:
			// Full payload packet, no adaptation.
			packet[3] = 0x10 | (*continuity & 0x0F)
			copy(packet[4:], payload[offset:offset+payloadCapacity])
			offset += payloadCapacity

		case !wantPCR && remaining == payloadCapacity-1:
			// 183 bytes of payload: one-byte adaptation with
			// adaptation_field_length=0 — the length byte itself is the
			// entire adaptation field. Previous code tried to write 2
			// bytes of adaptation plus 182 payload, silently dropping one
			// payload byte.
			packet[3] = 0x30 | (*continuity & 0x0F)
			packet[4] = 0
			copy(packet[5:], payload[offset:])
			offset = len(payload)

		case !wantPCR:
			// remaining <= 182: regular stuffing case.
			stuffingLen := payloadCapacity - remaining - 2
			if stuffingLen < 0 {
				stuffingLen = 0
			}
			adaptLen := 1 + stuffingLen
			packet[3] = 0x30 | (*continuity & 0x0F)
			packet[4] = byte(adaptLen)
			packet[5] = 0x00
			for i := 0; i < stuffingLen; i++ {
				packet[6+i] = 0xFF
			}
			copy(packet[4+1+adaptLen:], payload[offset:])
			offset = len(payload)

		default:
			// wantPCR: emit an adaptation field carrying the PCR on the
			// first packet. PCR adaptation = 1-byte flags + 6 bytes PCR,
			// total 7 bytes of non-length adaptation body. Fill with
			// stuffing if the payload is short.
			pcrAdaptBody := 1 + 6
			// Available payload once we spend the length byte + adaptation body.
			avail := payloadCapacity - (1 + pcrAdaptBody)
			stuffingLen := 0
			if remaining < avail {
				stuffingLen = avail - remaining
				avail = remaining
			}
			adaptLen := pcrAdaptBody + stuffingLen
			packet[3] = 0x30 | (*continuity & 0x0F)
			packet[4] = byte(adaptLen)
			packet[5] = 0x10 // PCR_flag
			writePCR(packet[6:12], uint64(pcrBase))
			for i := 0; i < stuffingLen; i++ {
				packet[12+i] = 0xFF
			}
			copy(packet[4+1+adaptLen:], payload[offset:offset+avail])
			offset += avail
		}

		*continuity++
		first = false
		buf.Write(packet)
	}
}

// writePCR packs a 90 kHz PCR base (33 bits) and a 0 extension (9 bits) into
// a 6-byte MPEG-TS PCR field.
func writePCR(dst []byte, base90k uint64) {
	base := base90k & 0x1FFFFFFFF
	dst[0] = byte(base >> 25)
	dst[1] = byte(base >> 17)
	dst[2] = byte(base >> 9)
	dst[3] = byte(base >> 1)
	dst[4] = byte((base&0x1)<<7) | 0x7E
	dst[5] = 0x00
}

// buildPESHeader creates a PES packet header carrying PTS (and optionally
// DTS). When dts != pts, both are emitted — the decoder needs DTS to
// schedule decoding of B-frames, and PTS to decide display order. For
// audio streams and for I/P-only video, pass dts == pts and the PES will
// advertise PTS only.
func buildPESHeader(streamID byte, dataLen int, pts, dts int64) []byte {
	var buf bytes.Buffer

	// PES start code: 00 00 01
	buf.Write([]byte{0x00, 0x00, 0x01})

	// Stream ID
	buf.WriteByte(streamID)

	includeDTS := dts >= 0 && dts != pts
	headerDataLen := 5
	ptsDtsFlags := byte(0x80) // PTS only
	if includeDTS {
		headerDataLen = 10
		ptsDtsFlags = 0xC0 // PTS + DTS
	}
	pesLen := 3 + headerDataLen + dataLen // 3 = flags(2) + header_data_length(1)
	if pesLen > 0xFFFF {
		pesLen = 0
	}
	buf.WriteByte(byte(pesLen >> 8))
	buf.WriteByte(byte(pesLen & 0xFF))

	buf.WriteByte(0x80) // 10 marker, no scrambling, no priority, no alignment, no copyright, no original
	buf.WriteByte(ptsDtsFlags)
	buf.WriteByte(byte(headerDataLen))

	if includeDTS {
		// Spec: when both flags are set, the PTS prefix nibble is 0x3
		// and the DTS prefix nibble is 0x1. encodePTSWithPrefix builds
		// the 5-byte bitfield with the right first-nibble markers.
		buf.Write(encodePTSWithPrefix(pts, 0x3))
		buf.Write(encodePTSWithPrefix(dts, 0x1))
	} else {
		buf.Write(encodePTS(pts))
	}

	return buf.Bytes()
}

// encodePTS encodes a 33-bit PTS value into the 5-byte MPEG-TS format
// (PTS-only PES header — first nibble is 0b0010).
func encodePTS(pts int64) []byte {
	return encodePTSWithPrefix(pts, 0x2)
}

// encodePTSWithPrefix encodes a 33-bit timestamp into the 5-byte
// MPEG-TS PES-header format with a caller-supplied 4-bit prefix nibble.
// The PES spec uses different prefix values for PTS-only (0010), PTS
// when DTS also present (0011), and DTS (0001) — passing the prefix
// lets a single routine produce all three.
func encodePTSWithPrefix(pts int64, prefix byte) []byte {
	p := make([]byte, 5)
	prefix &= 0x0F
	// Format: <prefix[3:0]> xxx1 | xxxxxxxx | xxxxxxx1 | xxxxxxxx | xxxxxxx1
	p[0] = (prefix << 4) | 0x01 | byte((pts>>29)&0x0E)
	p[1] = byte(pts >> 22)
	p[2] = 0x01 | byte((pts>>14)&0xFE)
	p[3] = byte(pts >> 7)
	p[4] = 0x01 | byte((pts<<1)&0xFE)
	return p
}

// crc32MPEG2 calculates the CRC-32/MPEG-2 checksum used in MPEG-TS tables.
func crc32MPEG2(data []byte) uint32 {
	crc := uint32(0xFFFFFFFF)
	for _, b := range data {
		crc ^= uint32(b) << 24
		for i := 0; i < 8; i++ {
			if crc&0x80000000 != 0 {
				crc = (crc << 1) ^ 0x04C11DB7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
