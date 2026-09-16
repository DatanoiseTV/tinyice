package relay

import "testing"

// The AutoDJ starts a fresh Ogg stream per track, so the circular buffer
// accumulates each track's OpusHead/OpusTags pages. A late-joining
// listener is also sent the CURRENT headers out of band (GetOggHead), so
// anything it reads from before OggHeaderOffset is a previous stream's
// header pair with a different serial and no audio.
//
// Subscribe clamped start to OggHeaderOffset and then let the page
// alignment walk it back again, so listeners received several orphan
// header pairs before any audio. ffmpeg skips to the chain that has
// audio; a browser latches onto the first chain, finds it empty, and
// plays silence. Measured live: five OpusHead/OpusTags pairs at granule
// 0 in the first 515 bytes.
func TestSubscribeNeverRewindsBeforeTheCurrentOggHeaders(t *testing.T) {
	s := &Stream{
		MountName:   "/dj",
		listeners:   make(map[string]chan struct{}),
		Buffer:      NewCircularBuffer(64 * 1024),
		PageOffsets: make([]int64, 64),
		IsOggStream: true,
	}

	// Simulate three tracks' worth of history: pages recorded across the
	// whole buffer, with the newest stream's headers ending at 40000.
	for i, off := range []int64{100, 900, 1800, 5000, 12000, 30000, 41000, 44000} {
		s.PageOffsets[i] = off
	}
	s.LastPageOffset = 44000
	s.Buffer.Head = 48000
	s.OggHeaderOffset = 40000 // audio of the current track starts here

	// A burst large enough that the naive computation reaches far back
	// into the previous tracks' bytes.
	offset, _ := s.Subscribe("l1", 32*1024)
	if offset < s.OggHeaderOffset {
		t.Errorf("offset %d is before the current stream's headers at %d: the listener "+
			"receives a previous track's orphan header pages first",
			offset, s.OggHeaderOffset)
	}

	// It must still align to a real page boundary at or after the floor,
	// not merely to the floor itself when a page is available.
	if offset != 41000 {
		t.Errorf("offset = %d, want 41000 (oldest tracked page at/after the header floor)", offset)
	}
}

// When no tracked page sits at or after the header floor, the listener
// must still not be rewound past it.
func TestSubscribeHoldsTheHeaderFloorWithNoAlignedPage(t *testing.T) {
	s := &Stream{
		MountName:   "/dj",
		listeners:   make(map[string]chan struct{}),
		Buffer:      NewCircularBuffer(64 * 1024),
		PageOffsets: make([]int64, 64),
		IsOggStream: true,
	}
	// Every tracked page predates the current headers.
	for i, off := range []int64{100, 900, 1800, 5000} {
		s.PageOffsets[i] = off
	}
	s.LastPageOffset = 5000
	s.Buffer.Head = 48000
	s.OggHeaderOffset = 40000

	offset, _ := s.Subscribe("l2", 32*1024)
	if offset < s.OggHeaderOffset {
		t.Errorf("offset %d rewound past the header floor %d", offset, s.OggHeaderOffset)
	}
}
