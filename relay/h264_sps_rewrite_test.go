package relay

import (
	"encoding/hex"
	"testing"
)

func TestIOSifyH264SPS_ProductionRTMPSource(t *testing.T) {
	// Real SPS from a production RTMP source whose encoder emits
	// fixed_frame_rate_flag=1 with no HRD params — a spec violation
	// per H.264 Annex E §E.2.1 that iOS Safari rejects with
	// kCMFormatDescriptionError_InvalidParameter on every sample.
	src, err := hex.DecodeString("67640028acd940780227e5c05a808080a0000003002000000791e30632c0")
	if err != nil {
		t.Fatal(err)
	}
	info, err := ParseH264SPS(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Sanity: structural fields preserve the source's stream shape.
	if info.ProfileIDC != 100 {
		t.Errorf("profile=%d want 100 (High)", info.ProfileIDC)
	}
	if info.LevelIDC != 40 {
		t.Errorf("level=%d want 40", info.LevelIDC)
	}
	// 1920 / 16 = 120 -> width_mb_minus1 = 119
	if info.PicWidthInMBsM1 != 119 {
		t.Errorf("pic_width_in_mbs_m1=%d want 119", info.PicWidthInMBsM1)
	}
	// 1088 / 16 = 68 -> height_in_map_units_minus1 = 67 (1080 + 8 crop)
	if info.PicHeightInMapUnitsM1 != 67 {
		t.Errorf("pic_height_in_map_units_m1=%d want 67", info.PicHeightInMapUnitsM1)
	}
	if info.FrameCropBottomOffset != 4 {
		t.Errorf("frame_crop_bottom_offset=%d want 4", info.FrameCropBottomOffset)
	}
	if !info.FrameMBsOnlyFlag {
		t.Errorf("frame_mbs_only_flag should be true")
	}

	// Rewrite must produce a non-empty byte string of NAL type 7.
	rewritten := IOSifyH264SPS(src, 30)
	if len(rewritten) < 4 {
		t.Fatalf("rewritten too short: %d bytes", len(rewritten))
	}
	if rewritten[0]&0x1F != 7 {
		t.Errorf("rewritten NAL type=%d want 7", rewritten[0]&0x1F)
	}

	// Parse the rewritten SPS — should round-trip with same structural
	// dimensions. The VUI is intentionally different (we overwrite it).
	info2, err := ParseH264SPS(rewritten)
	if err != nil {
		t.Fatalf("rewritten parse: %v", err)
	}
	if info2.ProfileIDC != info.ProfileIDC ||
		info2.PicWidthInMBsM1 != info.PicWidthInMBsM1 ||
		info2.PicHeightInMapUnitsM1 != info.PicHeightInMapUnitsM1 ||
		info2.FrameCropBottomOffset != info.FrameCropBottomOffset ||
		info2.MaxNumRefFrames != info.MaxNumRefFrames ||
		info2.ChromaFormatIDC != info.ChromaFormatIDC {
		t.Errorf("structural fields changed across rewrite: %+v vs %+v", info, info2)
	}
	// Level is intentionally bumped 40 -> 41 for 1080p sources;
	// iOS Safari rejects 1080p declared at exactly Level 4.0
	// because the per-second-macroblock budget lands at the limit.
	if info.LevelIDC == 40 && info2.LevelIDC != 41 {
		t.Errorf("level not bumped 40 -> 41 for 1080p source (got %d)", info2.LevelIDC)
	}
}

func TestIOSifyH264SPS_UnparseableLeftAlone(t *testing.T) {
	// Garbage bytes should NOT panic and should pass through unchanged.
	garbage := []byte{0x67, 0xff, 0xff, 0xff, 0xff}
	out := IOSifyH264SPS(garbage, 30)
	if string(out) != string(garbage) {
		t.Errorf("garbage should pass through unchanged")
	}
}

func TestH264NALUFilter_IDRAccessUnit(t *testing.T) {
	src, _ := hex.DecodeString("67640028acd940780227e5c05a808080a0000003002000000791e30632c0")
	// Source-style access unit: SPS + PPS + SEI + IDR, no AUD.
	annexB := []byte{0x00, 0x00, 0x00, 0x01}
	annexB = append(annexB, src...)
	annexB = append(annexB, 0x00, 0x00, 0x00, 0x01, 0x68, 0xee, 0x3c, 0xb0) // PPS
	annexB = append(annexB, 0x00, 0x00, 0x00, 0x01, 0x06, 0x05, 0x01, 0x00) // SEI (will be dropped)
	annexB = append(annexB, 0x00, 0x00, 0x00, 0x01, 0x65, 0xb8, 0x20, 0x01) // IDR (truncated)

	out := h264NALUFilter(annexB, 30)
	if len(out) <= 4 {
		t.Fatalf("output too short: %d bytes", len(out))
	}
	// AUD must be the very first NALU after the leading start code.
	if !containsBytes(out[:8], []byte{0x00, 0x00, 0x00, 0x01, 0x09, 0x10}) {
		t.Errorf("output does not start with IDR-AUD; got %x", out[:min(16, len(out))])
	}
	// Source SPS must have been replaced by the iOSified rewrite.
	if containsBytes(out, src) {
		t.Errorf("original (invalid) SPS still present in output")
	}
	// PPS and IDR must survive.
	if !containsBytes(out, []byte{0x00, 0x00, 0x00, 0x01, 0x68}) {
		t.Errorf("PPS start code lost during rewrite")
	}
	if !containsBytes(out, []byte{0x00, 0x00, 0x00, 0x01, 0x65}) {
		t.Errorf("IDR start code lost during rewrite")
	}
	// SEI (NAL type 6) must have been stripped.
	if containsBytes(out, []byte{0x00, 0x00, 0x00, 0x01, 0x06, 0x05, 0x01, 0x00}) {
		t.Errorf("SEI was not stripped")
	}
}

func TestH264NALUFilter_NonIDRSlice(t *testing.T) {
	// P-frame access unit: just a non-IDR slice.
	annexB := []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0xe0, 0x20, 0x80}

	out := h264NALUFilter(annexB, 30)
	// AUD must be non-IDR-AUD with primary_pic_type=2 (0x09 0x50),
	// which is the permissive setting that covers I/P/B slices. We
	// can't tell P from B from the NAL header alone, so we use the
	// inclusive value for every non-IDR access unit.
	if !containsBytes(out[:8], []byte{0x00, 0x00, 0x00, 0x01, 0x09, 0x50}) {
		t.Errorf("output does not start with non-IDR AUD; got %x", out[:min(16, len(out))])
	}
	// Original slice must still be there.
	if !containsBytes(out, []byte{0x00, 0x00, 0x00, 0x01, 0x41}) {
		t.Errorf("non-IDR slice lost")
	}
}

func TestH264NALUFilter_DropsDuplicateAUD(t *testing.T) {
	// Input already has an AUD up front (e.g. from a libx264-style
	// encoder) — we drop it and emit our own based on the slice type.
	annexB := []byte{0x00, 0x00, 0x00, 0x01, 0x09, 0x30}        // existing AUD
	annexB = append(annexB, 0x00, 0x00, 0x00, 0x01, 0x41, 0xe0) // P-slice
	out := h264NALUFilter(annexB, 30)
	// Should have exactly ONE AUD NALU in output.
	auds := 0
	for i := 0; i+5 < len(out); i++ {
		if out[i] == 0 && out[i+1] == 0 && out[i+2] == 0 && out[i+3] == 1 && out[i+4]&0x1F == 9 {
			auds++
		}
	}
	if auds != 1 {
		t.Errorf("expected exactly 1 AUD, got %d", auds)
	}
}

func containsBytes(haystack, needle []byte) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
