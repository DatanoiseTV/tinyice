package relay

import (
	"context"
	"io"
	"time"

	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"

	"github.com/DatanoiseTV/tinyice/logger"
)

// rtpClockHz is the RTP clock rate for H.264, which is also the unit the
// Frame PTS/DTS fields use — so timestamps pass through unscaled.
const rtpClockHz = 90000

// h264Unwrapper turns the 32-bit RTP timestamp into a monotonic 90 kHz
// value based at zero. The field wraps roughly every 13 hours at 90 kHz,
// and a stream that wrapped mid-broadcast would otherwise hand the HLS
// segmenter a PTS that jumps backwards by 2^32.
type h264Unwrapper struct {
	started bool
	first   uint32
	last    uint32
	epochs  int64
}

// unwrap returns the monotonic timestamp for an RTP timestamp, in 90 kHz
// units counted from the first packet seen.
func (u *h264Unwrapper) unwrap(ts uint32) int64 {
	if !u.started {
		u.started, u.first, u.last = true, ts, ts
		return 0
	}
	// A backwards step larger than half the range is a wrap, not
	// reordering; anything smaller is treated as genuine jitter and
	// passed through so the segmenter sees the real ordering.
	if ts < u.last && u.last-ts > 1<<31 {
		u.epochs++
	} else if ts > u.last && ts-u.last > 1<<31 {
		u.epochs--
	}
	u.last = ts
	return u.epochs<<32 + int64(ts) - int64(u.first)
}

// h264AccessUnit accumulates depacketized NAL units into whole access
// units. RTP carries one frame across many packets; the HLS segmenter and
// the mpegts muxer both want the complete access unit with its own PTS,
// so packets are gathered until the timestamp changes or the marker bit
// closes the frame.
type h264AccessUnit struct {
	buf    []byte
	ts     uint32
	haveTS bool
}

// add appends a depacketized payload. It returns a completed access unit
// and its RTP timestamp when the incoming packet belongs to a new frame,
// which is the signal that the previous one is finished.
func (a *h264AccessUnit) add(payload []byte, ts uint32) (au []byte, auTS uint32, done bool) {
	if a.haveTS && ts != a.ts && len(a.buf) > 0 {
		au, auTS, done = a.buf, a.ts, true
		a.buf = nil
	}
	a.buf = append(a.buf, payload...)
	a.ts, a.haveTS = ts, true
	return au, auTS, done
}

// flush closes the pending access unit, for the marker bit or end of
// stream.
func (a *h264AccessUnit) flush() (au []byte, auTS uint32, ok bool) {
	if len(a.buf) == 0 {
		return nil, 0, false
	}
	au, auTS = a.buf, a.ts
	a.buf = nil
	return au, auTS, true
}

// pumpVideoTrack reads an H.264 track from a browser (or any WHIP
// publisher) and republishes it on the mount's /video sibling in exactly
// the shape the RTMP ingest produces: Annex-B access units broadcast to
// the stream buffer, parameter sets cached and stored, keyframes recorded
// for late joiners, and a per-frame record published to the FrameHub so
// the HLS segmenter can build segments.
//
// Only H.264 reaches here. The source MediaEngine offers no other video
// codec, so a browser that cannot send H.264 fails negotiation with a
// clear error rather than delivering VP8 that nothing downstream could
// use without a transcoder we do not have.
func (wm *WebRTCManager) pumpVideoTrack(ctx context.Context, mount string, track *webrtc.TrackRemote) {
	videoMount := mount + "/video"
	// Same 8 MiB buffer the RTMP ingest uses: video frames are far
	// larger than audio, and a keyframe-aligned burst for a late joiner
	// has to fit.
	vs := wm.relay.GetOrCreateStreamSized(videoMount, 8*1024*1024)
	vs.mu.Lock()
	vs.ContentType = "video/h264"
	vs.SourceIP = "webrtc-source"
	vs.Visible = false // surfaced through the parent mount's has_video
	vs.mu.Unlock()
	// Fresh producer session: drops page/keyframe state from any previous
	// publisher on this mount and snaps existing viewers to the live edge.
	vs.BeginSession()

	logger.L.Infow("WebRTC Source: video track accepted",
		"mount", videoMount, "codec", track.Codec().MimeType, "track", track.ID())

	defer func() {
		logger.L.Infow("WebRTC Source: video track ended", "mount", videoMount)
		if st, ok := wm.relay.GetStream(videoMount); ok && st == vs {
			wm.relay.RemoveStream(videoMount)
		}
	}()

	dep := &codecs.H264Packet{}
	var (
		unwrap h264Unwrapper
		au     h264AccessUnit
		sps    []byte
		pps    []byte
	)

	publish := func(frame []byte, ts uint32) {
		if len(frame) == 0 {
			return
		}
		// Cache parameter sets as they arrive; browsers send them
		// inline with each IDR, but a mid-stream joiner still needs
		// them stored for the HLS init segment.
		if s, p := ExtractSPSPPS(frame); len(s) > 0 || len(p) > 0 {
			if len(s) > 0 {
				sps = s
			}
			if len(p) > 0 {
				pps = p
			}
			if len(sps) > 0 && len(pps) > 0 {
				out := make([]byte, 0, len(sps)+len(pps)+8)
				startCode := []byte{0x00, 0x00, 0x00, 0x01}
				out = append(append(out, startCode...), sps...)
				out = append(append(out, startCode...), pps...)
				vs.StoreVideoHeaders(out)
			}
		}

		isKeyframe := ContainsKeyframe(frame)
		if isKeyframe && !HasInlineParameterSets(frame) && (len(sps) > 0 || len(pps) > 0) {
			// Same reasoning as the RTMP path: only prepend when the
			// encoder did not already inline them, or iOS Safari's
			// hardware decoder freezes on the duplicate.
			startCode := []byte{0x00, 0x00, 0x00, 0x01}
			out := make([]byte, 0, len(sps)+len(pps)+len(frame)+8)
			if len(sps) > 0 {
				out = append(append(out, startCode...), sps...)
			}
			if len(pps) > 0 {
				out = append(append(out, startCode...), pps...)
			}
			frame = append(out, frame...)
		}
		if isKeyframe {
			vs.Buffer.RecordKeyframe(vs.Buffer.HeadOffset())
		}

		vs.Broadcast(frame, wm.relay)

		w, h, _ := ParseSPSResolution(sps)
		vs.RecordVideoSample(w, h, len(frame), isKeyframe, time.Now())

		if vs.Frames != nil {
			pts := unwrap.unwrap(ts)
			data := make([]byte, len(frame))
			copy(data, frame)
			// Browsers publish constrained-baseline / baseline H.264
			// with no B-frames, so decode order is display order and
			// DTS equals PTS.
			vs.Frames.Publish(Frame{
				Kind:     FrameVideo,
				PTS:      pts,
				DTS:      pts,
				Data:     data,
				Keyframe: isKeyframe,
			})
		}
	}

	for {
		select {
		case <-ctx.Done():
			if frame, ts, ok := au.flush(); ok {
				publish(frame, ts)
			}
			return
		default:
		}

		pkt, _, err := track.ReadRTP()
		if err != nil {
			if err != io.EOF {
				logger.L.Errorw("WebRTC Source: video RTP read failed", "mount", videoMount, "error", err)
			}
			if frame, ts, ok := au.flush(); ok {
				publish(frame, ts)
			}
			return
		}

		payload, err := dep.Unmarshal(pkt.Payload)
		if err != nil {
			// A single malformed packet is normal on a lossy path;
			// drop it and keep the pump alive.
			continue
		}
		if frame, ts, done := au.add(payload, pkt.Timestamp); done {
			publish(frame, ts)
		}
		if pkt.Marker {
			if frame, ts, ok := au.flush(); ok {
				publish(frame, ts)
			}
		}
	}
}

// newSourceMediaEngine builds the codec set offered to publishers: Opus
// for audio, H.264 for video, nothing else.
//
// Restricting it is the point. pion's default set also carries VP8 and
// VP9, and a browser will happily pick VP8 — but every video consumer in
// tinyice (HLS, the mpegts muxer, WHEP playback) is H.264, and the binary
// has no video transcoder. Negotiating VP8 would mean accepting a
// publisher whose picture nothing can play. Offering only H.264 turns
// that into an honest negotiation failure at connect time.
func newSourceMediaEngine() (*webrtc.MediaEngine, error) {
	me := &webrtc.MediaEngine{}

	if err := me.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeOpus,
			ClockRate:    48000,
			Channels:     2,
			SDPFmtpLine:  "minptime=10;useinbandfec=1",
			RTCPFeedback: nil,
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}

	// Two H.264 profiles, both constrained baseline / baseline with
	// non-interleaved packetization. packetization-mode=1 is what
	// browsers send; level-asymmetry-allowed lets the answerer accept a
	// level it would not itself send.
	videoRTCP := []webrtc.RTCPFeedback{
		{Type: "goog-remb"},
		{Type: "ccm", Parameter: "fir"},
		{Type: "nack"},
		{Type: "nack", Parameter: "pli"},
	}
	for pt, fmtp := range map[webrtc.PayloadType]string{
		102: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f",
		127: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
	} {
		if err := me.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeH264,
				ClockRate:    rtpClockHz,
				SDPFmtpLine:  fmtp,
				RTCPFeedback: videoRTCP,
			},
			PayloadType: pt,
		}, webrtc.RTPCodecTypeVideo); err != nil {
			return nil, err
		}
	}
	return me, nil
}
