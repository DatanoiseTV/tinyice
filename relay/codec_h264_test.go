package relay

import (
	"testing"
)

func TestParseNALUs(t *testing.T) {
	// Annex B data: start code + SPS + start code + PPS + start code + IDR
	data := []byte{
		0x00, 0x00, 0x00, 0x01, // start code
		0x67, 0x42, 0x00, 0x1E, // SPS (type 7, data bytes)
		0x00, 0x00, 0x00, 0x01, // start code
		0x68, 0xCE, 0x38, 0x80, // PPS (type 8, data bytes)
		0x00, 0x00, 0x01, // 3-byte start code
		0x65, 0x88, 0x80, 0x40, // IDR (type 5, data bytes)
	}

	units := ParseNALUs(data)
	if len(units) != 3 {
		t.Fatalf("expected 3 NALUs, got %d", len(units))
	}

	if !units[0].IsSPS() {
		t.Fatalf("expected SPS, got type %d", units[0].Type)
	}
	if !units[1].IsPPS() {
		t.Fatalf("expected PPS, got type %d", units[1].Type)
	}
	if !units[2].IsKeyframe() {
		t.Fatalf("expected IDR, got type %d", units[2].Type)
	}
}

func TestContainsKeyframe(t *testing.T) {
	// With IDR
	withIDR := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88}
	if !ContainsKeyframe(withIDR) {
		t.Fatal("expected keyframe detected")
	}

	// Without IDR (just a non-IDR slice)
	noIDR := []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0x9A}
	if ContainsKeyframe(noIDR) {
		t.Fatal("expected no keyframe")
	}
}

func TestExtractSPSPPS(t *testing.T) {
	data := []byte{
		0x00, 0x00, 0x00, 0x01,
		0x67, 0x42, 0x00, 0x1E, // SPS
		0x00, 0x00, 0x00, 0x01,
		0x68, 0xCE, 0x38, 0x80, // PPS
	}

	sps, pps := ExtractSPSPPS(data)
	if sps == nil {
		t.Fatal("expected SPS")
	}
	if pps == nil {
		t.Fatal("expected PPS")
	}
	if sps[0]&0x1F != NALUTypeSPS {
		t.Fatalf("SPS type wrong: %d", sps[0]&0x1F)
	}
}

func TestAVCCToAnnexB(t *testing.T) {
	// AVCC format: 4-byte length prefix + NALU data
	avcc := []byte{
		0x00, 0x00, 0x00, 0x04, // length=4
		0x65, 0x88, 0x80, 0x40, // IDR NALU
	}

	annexB := AVCCToAnnexB(avcc, 4)

	// Should have: start code (4 bytes) + NALU (4 bytes) = 8 bytes
	if len(annexB) != 8 {
		t.Fatalf("expected 8 bytes, got %d", len(annexB))
	}
	// Check start code
	if annexB[0] != 0 || annexB[1] != 0 || annexB[2] != 0 || annexB[3] != 1 {
		t.Fatal("missing start code")
	}
	// Check NALU type
	if annexB[4]&0x1F != NALUTypeIDR {
		t.Fatalf("expected IDR type, got %d", annexB[4]&0x1F)
	}
}
