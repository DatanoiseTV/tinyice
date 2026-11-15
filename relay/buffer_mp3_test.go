package relay

import "testing"

func TestFindNextMP3FrameLockedSkipsToHeader(t *testing.T) {
	cb := NewCircularBuffer(64)
	// Three bytes of a frame tail, then an MPEG-1 Layer III header (FF FB).
	cb.Write([]byte{0x12, 0x34, 0x56, 0xFF, 0xFB, 0x90, 0x64})

	if got := cb.FindNextMP3FrameLocked(0); got != 3 {
		t.Fatalf("expected header at offset 3, got %d", got)
	}
}

func TestFindNextMP3FrameLockedRejectsReservedBits(t *testing.T) {
	cb := NewCircularBuffer(64)
	// FF E8: version 01 (reserved). FF F0: layer 00 (reserved). FF F3: valid.
	cb.Write([]byte{0xFF, 0xE8, 0x00, 0xFF, 0xF0, 0x00, 0xFF, 0xF3, 0x00})

	if got := cb.FindNextMP3FrameLocked(0); got != 6 {
		t.Fatalf("expected header at offset 6, got %d", got)
	}
}

func TestFindNextMP3FrameLockedNoHeaderReturnsHead(t *testing.T) {
	cb := NewCircularBuffer(64)
	cb.Write([]byte{0x00, 0x01, 0x02, 0xFF})

	if got := cb.FindNextMP3FrameLocked(0); got != cb.HeadOffset() {
		t.Fatalf("expected Head (%d), got %d", cb.HeadOffset(), got)
	}
}

func TestFindNextMP3FrameLockedAcrossWrap(t *testing.T) {
	cb := NewCircularBuffer(8)
	// 10 bytes into an 8-byte buffer: the header at absolute 7..8 spans the wrap.
	cb.Write([]byte{0, 0, 0, 0, 0, 0, 0, 0xFF, 0xFB, 0x90})

	if got := cb.FindNextMP3FrameLocked(0); got != 7 {
		t.Fatalf("expected header at offset 7, got %d", got)
	}
}
