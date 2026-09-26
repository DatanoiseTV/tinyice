package relay

import "testing"

// The sniffer consumes bytes straight off an ingest socket, so malformed
// or hostile input must not panic, spin, or grow without bound. Bounds
// that look right on inspection are not proof for a byte parser.
func FuzzOggCommentSnifferDoesNotPanic(f *testing.F) {
	f.Add([]byte("OggS"))
	f.Add([]byte("OggS\x00\x02"))
	// A plausible page header with a full lacing table.
	hdr := append([]byte("OggS\x00\x02"), make([]byte, 21)...)
	hdr[26] = 255
	f.Add(append(hdr, make([]byte, 255)...))
	f.Add([]byte("OggS\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x01\x0b\x03vorbis\xff\xff\xff\xff"))
	f.Add([]byte("OpusTags\xff\xff\xff\xff"))

	f.Fuzz(func(t *testing.T, data []byte) {
		got := 0
		s := NewOggCommentSniffer(func(string) { got++ })
		// Feed in several slices so split-packet reassembly is exercised.
		for i := 0; i < len(data); i += 7 {
			end := i + 7
			if end > len(data) {
				end = len(data)
			}
			s.Feed(data[i:end])
		}
		// Buffered state must stay bounded regardless of input.
		if len(s.buf) > maxOggPageSize+16 {
			t.Fatalf("sniffer buffer grew to %d bytes", len(s.buf))
		}
		if len(s.serial) > maxSniffSerials {
			t.Fatalf("serial table grew to %d entries", len(s.serial))
		}
	})
}
