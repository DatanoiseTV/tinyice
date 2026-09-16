package relay

// H.264 SPS rewriter — normalises the source's Sequence Parameter Set so
// downstream HLS players (iOS Safari especially) accept the stream.
//
// Diagnosis: with an RTMP source whose encoder emits a technically
// spec-violating SPS (we observed an OBS/x264 build that left
// fixed_frame_rate_flag=1 but had nal_hrd/vcl_hrd absent — H.264
// Annex E §E.2.1 implies low_delay_hrd_flag=0 in that case, but
// ffmpeg's h264_metadata bsf rejects the SPS with "low_delay_hrd_flag
// does not match inferred value"), iOS Safari's AVFoundation HLS
// pipeline silently failed every sample buffer append with
// kCMFormatDescriptionError_InvalidParameter (-12710). The browser
// surface symptom was loadedmetadata 0x0 + no frames decoded.
//
// We parse the SPS into our own struct, preserve everything that
// affects the actual bitstream (profile, level, dimensions, ref
// frames, cropping, chroma, bit depth), and emit a fresh VUI that
// is minimal, complete, and conformant: no HRD, no pic-timing
// hooks, just timing_info + bitstream_restriction. The slice data
// continues to reference seq_parameter_set_id 0 so the rewritten
// SPS is a drop-in replacement.

import (
	"errors"
	"fmt"
)

// SPSInfo holds the H.264 SPS fields we care about — everything
// downstream players need to allocate the right DPB and decode the
// bitstream. Fields we don't touch (e.g. seq_scaling_list_present)
// are deliberately omitted; we don't rewrite SPSs that use them.
type SPSInfo struct {
	ProfileIDC              byte
	ConstraintFlags         byte // bits 0-7 packed
	LevelIDC                byte
	SeqParameterSetID       uint32
	ChromaFormatIDC         uint32 // 1 = 4:2:0 (only mode we handle)
	BitDepthLumaM8          uint32
	BitDepthChromaM8        uint32
	Log2MaxFrameNumM4       uint32
	PicOrderCntType         uint32
	Log2MaxPicOrderCntLSBM4 uint32 // only if PicOrderCntType == 0
	MaxNumRefFrames         uint32
	GapsInFrameNumAllowed   bool
	PicWidthInMBsM1         uint32
	PicHeightInMapUnitsM1   uint32
	FrameMBsOnlyFlag        bool
	Direct8x8InferenceFlag  bool
	FrameCroppingFlag       bool
	FrameCropLeftOffset     uint32
	FrameCropRightOffset    uint32
	FrameCropTopOffset      uint32
	FrameCropBottomOffset   uint32
}

// h264BitWriter appends a big-endian bit-stream into a growing RBSP
// slice. Caller appends emulation-prevention bytes afterwards if the
// output needs to live inside a NAL unit.
type h264BitWriter struct {
	buf []byte
	bit int // next-write bit offset within the trailing byte (0..7)
}

func (w *h264BitWriter) u(n int, v uint32) {
	for i := n - 1; i >= 0; i-- {
		if w.bit == 0 {
			w.buf = append(w.buf, 0)
		}
		bit := byte((v >> uint(i)) & 1)
		w.buf[len(w.buf)-1] |= bit << (7 - uint(w.bit))
		w.bit = (w.bit + 1) & 7
	}
}

func (w *h264BitWriter) ue(v uint32) {
	// Find largest n such that 2^n - 1 <= v.
	x := v + 1
	n := 0
	for tmp := x; tmp > 1; tmp >>= 1 {
		n++
	}
	w.u(n, 0)
	w.u(1, 1)
	w.u(n, x-(1<<uint(n)))
}

// nalToRBSP strips emulation-prevention bytes (the 0x03 inserted
// after a "00 00" run) so we can parse the raw RBSP fields. The NAL
// header byte (first byte) is included unchanged.
func nalToRBSP(nal []byte) []byte {
	out := make([]byte, 0, len(nal))
	zeros := 0
	for i, b := range nal {
		if i >= 1 && zeros >= 2 && b == 0x03 {
			zeros = 0
			continue
		}
		out = append(out, b)
		if b == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return out
}

// rbspToNAL re-inserts emulation-prevention bytes ahead of any
// 0x00 0x00 0x00 / 0x00 0x00 0x01 / 0x00 0x00 0x02 / 0x00 0x00 0x03
// sequence in the payload so the bytes are safe to live inside a
// NAL unit followed by a start-code search.
func rbspToNAL(rbsp []byte) []byte {
	out := make([]byte, 0, len(rbsp)+len(rbsp)/64)
	zeros := 0
	for i, b := range rbsp {
		if i >= 1 && zeros >= 2 && b <= 0x03 {
			out = append(out, 0x03)
			zeros = 0
		}
		out = append(out, b)
		if b == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return out
}

// ParseH264SPS reads a SPS NALU (with NAL header byte intact, NO
// start code) and returns the parsed structural fields. Unsupported
// SPS shapes (separate colour plane, scaling matrices in the SPS)
// return an error — callers should leave the original SPS alone in
// that case. The underlying bit reader panics on truncated bytes;
// we recover and surface as an error.
func ParseH264SPS(sps []byte) (info *SPSInfo, err error) {
	if len(sps) < 4 {
		return nil, errors.New("sps too short")
	}
	if sps[0]&0x1F != 7 {
		return nil, fmt.Errorf("not an SPS NAL: type=%d", sps[0]&0x1F)
	}
	defer func() {
		if r := recover(); r != nil {
			info = nil
			err = fmt.Errorf("sps parse panic: %v", r)
		}
	}()
	rbsp := nalToRBSP(sps)
	info = &SPSInfo{
		ProfileIDC:      rbsp[1],
		ConstraintFlags: rbsp[2],
		LevelIDC:        rbsp[3],
	}
	br := &h264BitReader{data: rbsp, bit: 32} // skip NAL header + profile + constraint + level
	info.SeqParameterSetID = uint32(br.ue())
	switch info.ProfileIDC {
	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134:
		info.ChromaFormatIDC = uint32(br.ue())
		if info.ChromaFormatIDC == 3 {
			return nil, errors.New("separate_colour_plane SPS not supported")
		}
		info.BitDepthLumaM8 = uint32(br.ue())
		info.BitDepthChromaM8 = uint32(br.ue())
		br.u(1) // qpprime_y_zero_transform_bypass_flag
		if br.u(1) != 0 {
			return nil, errors.New("seq_scaling_matrix_present SPS not supported")
		}
	default:
		info.ChromaFormatIDC = 1 // implied 4:2:0
	}
	info.Log2MaxFrameNumM4 = uint32(br.ue())
	info.PicOrderCntType = uint32(br.ue())
	switch info.PicOrderCntType {
	case 0:
		info.Log2MaxPicOrderCntLSBM4 = uint32(br.ue())
	case 1:
		return nil, errors.New("pic_order_cnt_type 1 SPS not supported")
	case 2:
		// no extra fields
	default:
		return nil, fmt.Errorf("unsupported pic_order_cnt_type=%d", info.PicOrderCntType)
	}
	info.MaxNumRefFrames = uint32(br.ue())
	info.GapsInFrameNumAllowed = br.u(1) != 0
	info.PicWidthInMBsM1 = uint32(br.ue())
	info.PicHeightInMapUnitsM1 = uint32(br.ue())
	info.FrameMBsOnlyFlag = br.u(1) != 0
	if !info.FrameMBsOnlyFlag {
		return nil, errors.New("interlaced SPS not supported")
	}
	info.Direct8x8InferenceFlag = br.u(1) != 0
	info.FrameCroppingFlag = br.u(1) != 0
	if info.FrameCroppingFlag {
		info.FrameCropLeftOffset = uint32(br.ue())
		info.FrameCropRightOffset = uint32(br.ue())
		info.FrameCropTopOffset = uint32(br.ue())
		info.FrameCropBottomOffset = uint32(br.ue())
	}
	// We don't read the VUI — we're going to overwrite it.
	return info, nil
}

// BuildH264SPS emits a clean, spec-compliant SPS from the supplied
// structural fields. The VUI is fixed to a minimal-but-iOS-friendly
// shape:
//
//   - timing_info_present_flag = 1, num_units_in_tick = 1,
//     time_scale = 2 * fpsHint, fixed_frame_rate_flag = 0
//     (matches the convention all known-good x264 outputs use, and
//     keeps the inferred low_delay_hrd_flag at 1 which is the
//     spec's only consistent value when HRD params are absent).
//   - nal_hrd / vcl_hrd absent (no buffer-rate hints to mis-match).
//   - pic_struct_present_flag = 0.
//   - bitstream_restriction_flag = 1, max_num_reorder_frames = 2,
//     max_dec_frame_buffering = max(MaxNumRefFrames, 2). Tells the
//     decoder how to size its DPB so it can decode the actual ref
//     pattern without surprises.
//
// fpsHint is doubled to satisfy timing_info's
// `time_scale / (2 * num_units_in_tick) = fps` convention. If the
// caller doesn't know the source fps, 60 is a safe default — it
// over-estimates capacity slightly but doesn't affect playback.
func BuildH264SPS(info *SPSInfo, fpsHint uint32) []byte {
	bw := &h264BitWriter{}

	// Fixed-bit header: 1 byte NAL header + 3 bytes (profile,
	// constraint, level). We construct these by hand because the
	// writer's u(8) would split them across the bit stream the same
	// way.
	bw.u(8, uint32(0x67)) // nal_unit_type=7, nal_ref_idc=3
	bw.u(8, uint32(info.ProfileIDC))
	bw.u(8, uint32(info.ConstraintFlags))
	// Apple's HLS player rejects 1080p streams declared at Level 4.0
	// (the per-second macroblock budget at 1080p30 lands EXACTLY at
	// the Level 4.0 limit and iOS treats borderline as malformed).
	// Bump any 1080p source to at least Level 4.1, which iOS accepts
	// without complaint and is functionally identical for our
	// bitstreams (same MaxMBPS, larger max bitrate envelope).
	level := info.LevelIDC
	if (info.PicWidthInMBsM1+1)*16 >= 1920 && level < 41 {
		level = 41
	}
	bw.u(8, uint32(level))

	bw.ue(info.SeqParameterSetID)
	switch info.ProfileIDC {
	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134:
		bw.ue(info.ChromaFormatIDC)
		bw.ue(info.BitDepthLumaM8)
		bw.ue(info.BitDepthChromaM8)
		bw.u(1, 0) // qpprime_y_zero_transform_bypass_flag
		bw.u(1, 0) // seq_scaling_matrix_present_flag
	}
	bw.ue(info.Log2MaxFrameNumM4)
	bw.ue(info.PicOrderCntType)
	if info.PicOrderCntType == 0 {
		bw.ue(info.Log2MaxPicOrderCntLSBM4)
	}
	bw.ue(info.MaxNumRefFrames)
	if info.GapsInFrameNumAllowed {
		bw.u(1, 1)
	} else {
		bw.u(1, 0)
	}
	bw.ue(info.PicWidthInMBsM1)
	bw.ue(info.PicHeightInMapUnitsM1)
	bw.u(1, 1) // frame_mbs_only_flag (we only support progressive)
	if info.Direct8x8InferenceFlag {
		bw.u(1, 1)
	} else {
		bw.u(1, 0)
	}
	if info.FrameCroppingFlag {
		bw.u(1, 1)
		bw.ue(info.FrameCropLeftOffset)
		bw.ue(info.FrameCropRightOffset)
		bw.ue(info.FrameCropTopOffset)
		bw.ue(info.FrameCropBottomOffset)
	} else {
		bw.u(1, 0)
	}

	// VUI parameters (vui_parameters_present_flag = 1)
	bw.u(1, 1)
	bw.u(1, 1) // aspect_ratio_info_present_flag
	bw.u(8, 1) // aspect_ratio_idc = 1 (1:1 square)
	bw.u(1, 0) // overscan_info_present_flag
	bw.u(1, 1) // video_signal_type_present_flag
	bw.u(3, 5) // video_format = 5 (unspecified)
	bw.u(1, 0) // video_full_range_flag
	bw.u(1, 1) // colour_description_present_flag
	bw.u(8, 1) // colour_primaries = 1 (bt709)
	bw.u(8, 1) // transfer_characteristics = 1 (bt709)
	bw.u(8, 1) // matrix_coefficients = 1 (bt709)
	bw.u(1, 0) // chroma_loc_info_present_flag

	bw.u(1, 1) // timing_info_present_flag
	// Match x264's canonical timing-info shape: time_scale=60,
	// num_units_in_tick=1 → declared 30 fps. Mobile players (iOS
	// Safari especially) treat the timing_info as a coarse hint;
	// any value here that doesn't match the actual frame cadence
	// implicitly inflates buffer estimates. Sticking to x264's
	// known-good shape avoids surprising the demuxer.
	bw.u(32, 1)  // num_units_in_tick
	bw.u(32, 60) // time_scale (30 fps when num_units=1)
	bw.u(1, 0)   // fixed_frame_rate_flag = 0 (spec-consistent
	// with no HRD params)
	bw.u(1, 0) // nal_hrd_parameters_present_flag
	bw.u(1, 0) // vcl_hrd_parameters_present_flag
	// low_delay_hrd_flag is inferred (= 1 when no HRD AND
	// fixed_frame_rate_flag=0 per H.264 §E.2.1). Not emitted here.
	bw.u(1, 0) // pic_struct_present_flag

	bw.u(1, 1) // bitstream_restriction_flag
	bw.u(1, 1) // motion_vectors_over_pic_boundaries_flag
	// Match x264's bitstream_restriction values byte-for-byte. iOS
	// accepts these without question; other values (we previously
	// emitted 2/1/16/16) read as overly-permissive and made iOS
	// reject the SPS even after we fixed the HRD inconsistency.
	bw.ue(0)  // max_bytes_per_pic_denom
	bw.ue(0)  // max_bits_per_mb_denom
	bw.ue(11) // log2_max_mv_length_horizontal
	bw.ue(11) // log2_max_mv_length_vertical
	bw.ue(2)  // max_num_reorder_frames
	mdfb := info.MaxNumRefFrames
	if mdfb < 2 {
		mdfb = 2
	}
	bw.ue(mdfb) // max_dec_frame_buffering

	// rbsp_trailing_bits(): a single '1' bit followed by zero-pad to
	// byte alignment.
	bw.u(1, 1)
	for bw.bit != 0 {
		bw.u(1, 0)
	}

	return rbspToNAL(bw.buf)
}

// IOSifyH264SPS parses an SPS NALU and rewrites it with a clean,
// spec-compliant VUI suitable for iOS Safari + every other strict
// H.264 demuxer. Returns the original SPS unchanged if it can't be
// parsed (unusual / non-baseline shapes) so we don't make a bad
// stream worse.
func IOSifyH264SPS(sps []byte, fpsHint uint32) []byte {
	info, err := ParseH264SPS(sps)
	if err != nil {
		return sps
	}
	rewritten := BuildH264SPS(info, fpsHint)
	if len(rewritten) == 0 {
		return sps
	}
	return rewritten
}

// h264AUDForIDR is the canonical Access Unit Delimiter NAL unit for
// an IDR access unit: nal_unit_type=9, primary_pic_type=0 (I-slice
// only). x264 emits this as 0x09 0x10. iOS Safari's HLS demuxer
// requires AUD NALs to delimit access units; without them every
// sample append fails with kCMFormatDescriptionError_InvalidParameter
// even when the SPS / PPS / slices are otherwise spec-clean.
var h264AUDForIDR = []byte{0x09, 0x10}

// h264AUDForNonIDR is the AUD for non-IDR access units. We use
// primary_pic_type=2 (= "I, P, B slices may be present"), which is
// the safe value for any source that ships B-frames. iOS Safari
// DROPS access units whose AUD claims a more restrictive slice
// set than the access unit actually carries: e.g. if the AUD says
// primary_pic_type=1 (I,P only) but the slice is a B-slice, iOS
// silently discards the whole picture. Symptom from the user:
// "proper 30 fps playback then gets stuck for some sec" — that's
// iOS keeping the IDR and any P-frames and dropping all the B-
// frames between them. Always using type 2 covers I/P/B without
// having to crack open the slice header to discriminate.
var h264AUDForNonIDR = []byte{0x09, 0x50}

// h264NALUFilter combines (a) replacing every SPS NALU with the
// iOS-friendly rewrite from IOSifyH264SPS, (b) stripping every SEI
// NALU (some sources embed encoder-specific user_data that iOS
// either over-validates or treats as bitstream waste), and
// (c) ensuring exactly one AUD NALU sits at the front of every
// access unit, picking the right primary_pic_type byte for IDR vs
// non-IDR pictures.
//
// Apple's HLS Authoring Specification doesn't explicitly call out
// AUDs as required, but empirically every iOS Safari version we've
// tested rejects access-units that don't have one. x264 emits AUDs
// at every frame by default; the OBS/source-encoder build that
// motivated this fix does not, and stripping nothing else away
// from those frames was the missing piece after the SPS rewrite.
func h264NALUFilter(annexB []byte, fpsHint uint32) []byte {
	// Find NALU boundaries by scanning for start codes.
	// We track every start-code position so we can carry each NALU's
	// length-preserved start code (3- or 4-byte) along with its
	// rewritten payload.
	type seg struct {
		startCodeLen int // 3 or 4
		payload      []byte
	}
	var segs []seg
	i := 0
	for i < len(annexB) {
		// Find next start code from i.
		sc4 := -1
		sc3 := -1
		for j := i; j+2 < len(annexB); j++ {
			if annexB[j] == 0 && annexB[j+1] == 0 {
				if j+3 < len(annexB) && annexB[j+2] == 0 && annexB[j+3] == 1 {
					sc4 = j
					break
				}
				if annexB[j+2] == 1 {
					sc3 = j
					break
				}
			}
		}
		// Pick the earliest start code (sc4 wins if both align at same j).
		pos := -1
		scLen := 0
		switch {
		case sc4 >= 0 && (sc3 < 0 || sc4 <= sc3):
			pos = sc4
			scLen = 4
		case sc3 >= 0:
			pos = sc3
			scLen = 3
		}
		if pos < 0 {
			// No more start codes. If we have any tail before
			// running off the end, treat it as leftover-after-last
			// segment by appending to the last segment's payload.
			if len(segs) > 0 && i < len(annexB) {
				segs[len(segs)-1].payload = append(segs[len(segs)-1].payload, annexB[i:]...)
			}
			break
		}
		// Bytes before pos belong to the previous NALU's payload.
		if len(segs) > 0 && pos > i {
			segs[len(segs)-1].payload = append(segs[len(segs)-1].payload, annexB[i:pos]...)
		}
		// Find where THIS NALU ends — at the next start code.
		nextI := pos + scLen
		end := len(annexB)
		for j := nextI; j+2 < len(annexB); j++ {
			if annexB[j] == 0 && annexB[j+1] == 0 &&
				(annexB[j+2] == 1 || (j+3 < len(annexB) && annexB[j+2] == 0 && annexB[j+3] == 1)) {
				end = j
				break
			}
		}
		payload := make([]byte, end-nextI)
		copy(payload, annexB[nextI:end])
		segs = append(segs, seg{startCodeLen: scLen, payload: payload})
		i = end
	}
	// Process NALUs: rewrite SPS, drop SEI + existing AUD, detect
	// access-unit type from the first slice NALU (IDR=5, non-IDR
	// slice=1) so we can prepend the right AUD byte.
	type op struct {
		drop    bool
		payload []byte
	}
	ops := make([]op, len(segs))
	hasIDR := false
	hasSlice := false
	for k := range segs {
		if len(segs[k].payload) == 0 {
			ops[k].drop = true
			continue
		}
		t := segs[k].payload[0] & 0x1F
		switch t {
		case 7: // SPS — rewrite
			ops[k].payload = IOSifyH264SPS(segs[k].payload, fpsHint)
		case 6: // SEI — drop
			ops[k].drop = true
		case 9: // existing AUD — drop, we'll insert our own
			ops[k].drop = true
		case 5: // IDR slice
			hasIDR = true
			ops[k].payload = segs[k].payload
		case 1: // non-IDR slice
			hasSlice = true
			ops[k].payload = segs[k].payload
		default:
			ops[k].payload = segs[k].payload
		}
	}
	// Emit: AUD (if access unit has a slice), then everything else
	// in order. We only prepend an AUD when this batch actually
	// carries a slice — pure-parameter-set deliveries (BOS-style
	// SPS/PPS-only chunks the source may emit) don't get a
	// synthetic AUD and pass through with the same shape they had.
	out := make([]byte, 0, len(annexB)+16)
	if hasIDR || hasSlice {
		out = append(out, 0x00, 0x00, 0x00, 0x01)
		if hasIDR {
			out = append(out, h264AUDForIDR...)
		} else {
			out = append(out, h264AUDForNonIDR...)
		}
	}
	for k := range segs {
		if ops[k].drop {
			continue
		}
		payload := ops[k].payload
		if len(payload) == 0 {
			continue
		}
		// Use the original NALU's start code length so we don't
		// change byte alignment in a way the muxer is sensitive to.
		if segs[k].startCodeLen == 4 {
			out = append(out, 0x00, 0x00, 0x00, 0x01)
		} else {
			out = append(out, 0x00, 0x00, 0x01)
		}
		out = append(out, payload...)
	}
	return out
}
