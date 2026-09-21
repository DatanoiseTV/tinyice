package relay

import (
	"bytes"
	"encoding/binary"
	"strings"
)

const (
	vorbisCommentMagic = "\x03vorbis"
	opusCommentMagic   = "OpusTags"

	// maxOggPageSize is the largest legal Ogg page: 27-byte header,
	// 255 segment-table entries, 255*255 bytes of body.
	maxOggPageSize = 27 + 255 + 255*255

	// maxCommentPacket bounds how much of a comment header we buffer.
	// Real ones are a few hundred bytes; cover art in METADATA_BLOCK_PICTURE
	// can push it up, but we don't need those tags.
	maxCommentPacket = 64 * 1024

	maxSniffSerials = 16
)

// OggCommentSniffer watches an Ogg byte stream (Vorbis or Opus) and reports
// the TITLE/ARTIST from each comment header it sees. It parses whole Ogg
// pages, reassembling pages and packets that arrive split across Feed calls,
// so callers can hand it raw read buffers of any size.
type OggCommentSniffer struct {
	buf    []byte
	serial map[uint32]*sniffPacket
	onSong func(song string)
}

type sniffPacket struct {
	inPacket bool // a packet is open across pages
	wanted   bool // the open packet is a comment header we are buffering
	data     []byte
}

// NewOggCommentSniffer returns a sniffer that calls onSong with
// "Artist - Title" (or just the title) for every comment header found.
func NewOggCommentSniffer(onSong func(song string)) *OggCommentSniffer {
	return &OggCommentSniffer{serial: make(map[uint32]*sniffPacket), onSong: onSong}
}

// Feed consumes the next chunk of the stream.
func (s *OggCommentSniffer) Feed(p []byte) {
	s.buf = append(s.buf, p...)
	for {
		idx := bytes.Index(s.buf, []byte("OggS"))
		if idx < 0 {
			// Keep a possible partial magic at the tail.
			if len(s.buf) > 3 {
				s.buf = append(s.buf[:0], s.buf[len(s.buf)-3:]...)
			}
			return
		}
		s.buf = s.buf[idx:]
		if len(s.buf) < 27 {
			return
		}
		nseg := int(s.buf[26])
		if len(s.buf) < 27+nseg {
			return
		}
		bodyLen := 0
		for _, l := range s.buf[27 : 27+nseg] {
			bodyLen += int(l)
		}
		total := 27 + nseg + bodyLen
		if s.buf[4] != 0 || total > maxOggPageSize {
			// Not a real page header; resync past this "OggS".
			s.buf = s.buf[4:]
			continue
		}
		if len(s.buf) < total {
			return
		}
		s.page(s.buf[:total], nseg)
		s.buf = s.buf[total:]
	}
}

func (s *OggCommentSniffer) page(pg []byte, nseg int) {
	flags := pg[5]
	serial := binary.LittleEndian.Uint32(pg[14:18])
	lacing := pg[27 : 27+nseg]
	body := pg[27+nseg:]

	st := s.serial[serial]
	if st == nil {
		if len(s.serial) >= maxSniffSerials {
			s.serial = make(map[uint32]*sniffPacket)
		}
		st = &sniffPacket{}
		s.serial[serial] = st
	}
	continued := flags&0x01 != 0
	switch {
	case continued && !st.inPacket:
		// We missed the start of this packet; skip it.
		st.inPacket, st.wanted, st.data = true, false, nil
	case !continued && st.inPacket:
		st.inPacket, st.wanted, st.data = false, false, nil
	}

	off := 0
	for _, l := range lacing {
		chunk := body[off : off+int(l)]
		off += int(l)
		if !st.inPacket {
			st.inPacket = true
			st.data = nil
			st.wanted = bytes.HasPrefix(chunk, []byte(vorbisCommentMagic)) ||
				bytes.HasPrefix(chunk, []byte(opusCommentMagic))
		}
		if st.wanted {
			if len(st.data)+len(chunk) > maxCommentPacket {
				st.wanted, st.data = false, nil
			} else {
				st.data = append(st.data, chunk...)
			}
		}
		if l < 255 {
			if st.wanted {
				s.comments(st.data)
			}
			st.inPacket, st.wanted, st.data = false, false, nil
		}
	}
	if flags&0x04 != 0 {
		delete(s.serial, serial)
	}
}

func (s *OggCommentSniffer) comments(pkt []byte) {
	var rest []byte
	switch {
	case bytes.HasPrefix(pkt, []byte(vorbisCommentMagic)):
		rest = pkt[len(vorbisCommentMagic):]
	case bytes.HasPrefix(pkt, []byte(opusCommentMagic)):
		rest = pkt[len(opusCommentMagic):]
	default:
		return
	}
	readLen := func() (int, bool) {
		if len(rest) < 4 {
			return 0, false
		}
		n := int(binary.LittleEndian.Uint32(rest))
		rest = rest[4:]
		return n, n >= 0 && n <= len(rest)
	}
	n, ok := readLen()
	if !ok {
		return
	}
	rest = rest[n:] // vendor string
	count, ok := readLen()
	if !ok {
		return
	}
	var artist, title string
	for i := 0; i < count; i++ {
		n, ok := readLen()
		if !ok {
			break
		}
		kv := string(rest[:n])
		rest = rest[n:]
		k, v, found := strings.Cut(kv, "=")
		if !found {
			continue
		}
		switch strings.ToUpper(k) {
		case "TITLE":
			if title == "" {
				title = strings.TrimSpace(v)
			}
		case "ARTIST":
			if artist == "" {
				artist = strings.TrimSpace(v)
			}
		}
	}
	song := title
	if artist != "" && title != "" {
		song = artist + " - " + title
	} else if title == "" {
		song = artist
	}
	if song != "" && s.onSong != nil {
		s.onSong(song)
	}
}
