package relay

import (
	"encoding/binary"
	"testing"
)

func commentPacket(magic string, tags ...string) []byte {
	p := []byte(magic)
	p = binary.LittleEndian.AppendUint32(p, 3)
	p = append(p, "lib"...)
	p = binary.LittleEndian.AppendUint32(p, uint32(len(tags)))
	for _, t := range tags {
		p = binary.LittleEndian.AppendUint32(p, uint32(len(t)))
		p = append(p, t...)
	}
	return p
}

// oggPage builds a page carrying body, split into lacing values; last
// segment of 255 means the packet continues on the next page.
func sniffPage(serial uint32, flags byte, body []byte, lacing []byte) []byte {
	pg := []byte("OggS\x00")
	pg = append(pg, flags)
	pg = append(pg, make([]byte, 8)...)
	pg = binary.LittleEndian.AppendUint32(pg, serial)
	pg = append(pg, make([]byte, 8)...) // seq + crc (unchecked)
	pg = append(pg, byte(len(lacing)))
	pg = append(pg, lacing...)
	return append(pg, body...)
}

func sniffLace(n int) []byte {
	var l []byte
	for ; n >= 255; n -= 255 {
		l = append(l, 255)
	}
	return append(l, byte(n))
}

func TestOggCommentSniffer(t *testing.T) {
	for _, magic := range []string{"\x03vorbis", "OpusTags"} {
		pkt := commentPacket(magic, "artist=Daft Punk", "TITLE=One More Time")
		stream := sniffPage(1, 0, pkt, sniffLace(len(pkt)))

		// Feed one byte at a time: worst-case buffer splitting.
		var got []string
		s := NewOggCommentSniffer(func(song string) { got = append(got, song) })
		for i := range stream {
			s.Feed(stream[i : i+1])
		}
		if len(got) != 1 || got[0] != "Daft Punk - One More Time" {
			t.Fatalf("%q: got %q", magic, got)
		}
	}
}

func TestOggCommentSnifferPacketSpansPages(t *testing.T) {
	pad := make([]byte, 300)
	pkt := commentPacket("\x03vorbis", "TITLE=Long", "COMMENT="+string(pad))
	first, rest := pkt[:255], pkt[255:]
	data := append(sniffPage(7, 0, first, []byte{255}), sniffPage(7, 1, rest, sniffLace(len(rest)))...)

	var got []string
	NewOggCommentSniffer(func(song string) { got = append(got, song) }).Feed(data)
	if len(got) != 1 || got[0] != "Long" {
		t.Fatalf("got %q", got)
	}
}

func TestOggCommentSnifferIgnoresAudio(t *testing.T) {
	audio := make([]byte, 100)
	called := false
	s := NewOggCommentSniffer(func(string) { called = true })
	s.Feed(sniffPage(1, 0, audio, sniffLace(len(audio))))
	s.Feed([]byte("not ogg at all"))
	if called {
		t.Fatal("unexpected song")
	}
}
