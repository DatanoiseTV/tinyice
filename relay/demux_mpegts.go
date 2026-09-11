package relay

// TSDemuxer extracts elementary stream data from MPEG-TS packets.
type TSDemuxer struct {
	audioPID    uint16
	videoPID    uint16
	onAudioData func(data []byte, pts int64)
	onVideoData func(data []byte, pts int64, isKeyframe bool)
	patParsed   bool
	pmtParsed   bool
	pmtPID      uint16
}

// NewTSDemuxer creates a new MPEG-TS demuxer.
func NewTSDemuxer() *TSDemuxer {
	return &TSDemuxer{}
}

// OnAudio registers a callback for extracted audio data.
func (d *TSDemuxer) OnAudio(fn func(data []byte, pts int64)) {
	d.onAudioData = fn
}

// OnVideo registers a callback for extracted video data.
func (d *TSDemuxer) OnVideo(fn func(data []byte, pts int64, isKeyframe bool)) {
	d.onVideoData = fn
}

// Feed processes raw MPEG-TS data. The previous implementation jumped
// 188 bytes at a time looking for the sync byte at fixed offsets — if
// the input was even 1 byte misaligned (which can happen with SRT
// payload boundaries that don't fall on TS boundaries) the demuxer
// silently dropped every packet because the sync byte never landed at
// the expected position.
//
// Re-sync by advancing one byte at a time until we see 0x47, then
// process 188-byte packets from there. Once aligned, every following
// packet is at +188 so we stay in lock-step until we hit a missing
// sync byte (typical sign of dropped UDP packets), at which point we
// resync the same way.
func (d *TSDemuxer) Feed(data []byte) {
	for i := 0; i+tsPacketSize <= len(data); {
		if data[i] != tsSyncByte {
			i++
			continue
		}
		d.processPacket(data[i : i+tsPacketSize])
		i += tsPacketSize
	}
}

func (d *TSDemuxer) processPacket(pkt []byte) {
	pid := (uint16(pkt[1]&0x1F) << 8) | uint16(pkt[2])
	payloadStart := pkt[1]&0x40 != 0
	hasAdaptation := pkt[3]&0x20 != 0
	hasPayload := pkt[3]&0x10 != 0

	if !hasPayload {
		return
	}

	offset := 4
	if hasAdaptation {
		adaptLen := int(pkt[4])
		offset = 5 + adaptLen
		if offset >= tsPacketSize {
			return
		}
	}

	payload := pkt[offset:]

	switch {
	case pid == 0x0000: // PAT
		d.parsePAT(payload, payloadStart)
	case pid == d.pmtPID: // PMT
		d.parsePMT(payload, payloadStart)
	case pid == d.audioPID && d.audioPID != 0: // Audio
		if d.onAudioData != nil {
			// Extract PES payload
			audioData := payload
			var pts int64
			if payloadStart && len(payload) > 13 {
				// PES header: start code(3) + stream_id(1) + length(2) + flags(2) + header_len(1)
				if payload[0] == 0x00 && payload[1] == 0x00 && payload[2] == 0x01 {
					headerDataLen := int(payload[8])
					// Check if PTS is present
					if payload[7]&0x80 != 0 && headerDataLen >= 5 {
						pts = decodePTS(payload[9:14])
					}
					pesHeaderEnd := 9 + headerDataLen
					if pesHeaderEnd < len(payload) {
						audioData = payload[pesHeaderEnd:]
					}
				}
			}
			if len(audioData) > 0 {
				d.onAudioData(audioData, pts)
			}
		}
	case pid == d.videoPID && d.videoPID != 0: // Video
		if d.onVideoData != nil {
			videoData := payload
			var pts int64
			if payloadStart && len(payload) > 13 {
				if payload[0] == 0x00 && payload[1] == 0x00 && payload[2] == 0x01 {
					headerDataLen := int(payload[8])
					if payload[7]&0x80 != 0 && headerDataLen >= 5 {
						pts = decodePTS(payload[9:14])
					}
					pesHeaderEnd := 9 + headerDataLen
					if pesHeaderEnd < len(payload) {
						videoData = payload[pesHeaderEnd:]
					}
				}
			}
			if len(videoData) > 0 {
				isKeyframe := ContainsKeyframe(videoData)
				d.onVideoData(videoData, pts, isKeyframe)
			}
		}
	}
}

func (d *TSDemuxer) parsePAT(payload []byte, payloadStart bool) {
	if payloadStart && len(payload) > 0 {
		// pointer_field comes off the wire; a value past the end of the
		// packet payload is malformed input, not a slice we can take.
		pointer := int(payload[0])
		if 1+pointer > len(payload) {
			return
		}
		payload = payload[1+pointer:]
	}
	if len(payload) < 12 {
		return
	}
	// table_id should be 0
	if payload[0] != 0x00 {
		return
	}
	// Program entry starts at byte 8
	// program_number(2) + reserved(3 bits) + PMT_PID(13 bits)
	if len(payload) >= 12 {
		d.pmtPID = (uint16(payload[10]&0x1F) << 8) | uint16(payload[11])
		d.patParsed = true
	}
}

func (d *TSDemuxer) parsePMT(payload []byte, payloadStart bool) {
	if payloadStart && len(payload) > 0 {
		// pointer_field comes off the wire; a value past the end of the
		// packet payload is malformed input, not a slice we can take.
		pointer := int(payload[0])
		if 1+pointer > len(payload) {
			return
		}
		payload = payload[1+pointer:]
	}
	if len(payload) < 17 {
		return
	}
	if payload[0] != 0x02 {
		return
	}
	sectionLen := int(payload[1]&0x0F)<<8 | int(payload[2])
	progInfoLen := int(payload[10]&0x0F)<<8 | int(payload[11])

	offset := 12 + progInfoLen
	endOffset := 3 + sectionLen - 4 // exclude CRC
	if endOffset > len(payload) {
		endOffset = len(payload)
	}

	for offset+5 <= endOffset {
		streamType := payload[offset]
		esPID := (uint16(payload[offset+1]&0x1F) << 8) | uint16(payload[offset+2])
		esInfoLen := int(payload[offset+3]&0x0F)<<8 | int(payload[offset+4])

		// Audio stream types: 0x03 = MP3, 0x04 = MP3, 0x0F = AAC, 0x11 = AAC-LATM
		if streamType == 0x03 || streamType == 0x04 || streamType == 0x0F || streamType == 0x11 {
			d.audioPID = esPID
		}

		// Video stream types: 0x1B = H.264, 0x24 = H.265
		if streamType == 0x1B || streamType == 0x24 {
			d.videoPID = esPID
		}

		offset += 5 + esInfoLen
	}

	if d.audioPID != 0 || d.videoPID != 0 {
		d.pmtParsed = true
	}
}

// decodePTS decodes a 33-bit PTS from the 5-byte MPEG-TS format.
func decodePTS(data []byte) int64 {
	if len(data) < 5 {
		return 0
	}
	pts := int64(data[0]&0x0E) << 29
	pts |= int64(data[1]) << 22
	pts |= int64(data[2]&0xFE) << 14
	pts |= int64(data[3]) << 7
	pts |= int64(data[4]&0xFE) >> 1
	return pts
}
