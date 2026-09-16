package relay

import (
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
	flvtag "github.com/yutopp/go-flv/tag"
	rtmp "github.com/yutopp/go-rtmp"
	rtmpmsg "github.com/yutopp/go-rtmp/message"
)

// RTMPServer accepts RTMP publish connections and feeds audio data into TinyIce streams.
type RTMPServer struct {
	relay    *Relay
	config   *config.Config
	listener net.Listener
	server   *rtmp.Server
	mu       sync.Mutex

	// Active connections tracked so Stop can close them all — closing
	// only the listener leaves existing handler goroutines blocked in
	// conn.Read forever, which in turn makes rtmp.Server.Close() hang.
	// That's what was preventing Ctrl+C from quitting the process.
	conns   map[net.Conn]struct{}
	connsMu sync.Mutex
}

// NewRTMPServer creates a new RTMP ingest server.
func NewRTMPServer(r *Relay, cfg *config.Config) *RTMPServer {
	return &RTMPServer{
		relay:  r,
		config: cfg,
		conns:  make(map[net.Conn]struct{}),
	}
}

// trackConn registers a connection so Stop can close it. Returns a
// function the handler should defer to un-register when the connection
// ends normally.
func (rs *RTMPServer) trackConn(c net.Conn) func() {
	rs.connsMu.Lock()
	rs.conns[c] = struct{}{}
	rs.connsMu.Unlock()
	return func() {
		rs.connsMu.Lock()
		delete(rs.conns, c)
		rs.connsMu.Unlock()
	}
}

// closeAllConns closes every tracked connection. Used on shutdown to
// unblock handlers that are parked in conn.Read.
func (rs *RTMPServer) closeAllConns() {
	rs.connsMu.Lock()
	conns := make([]net.Conn, 0, len(rs.conns))
	for c := range rs.conns {
		conns = append(conns, c)
	}
	rs.conns = make(map[net.Conn]struct{})
	rs.connsMu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// Start begins listening for RTMP connections on the configured port.
func (rs *RTMPServer) Start() error {
	port := rs.config.Ingest.RTMPPort
	if port == "" {
		port = "1935"
	}

	addr := fmt.Sprintf(":%s", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("RTMP listen on %s: %w", addr, err)
	}
	rs.mu.Lock()
	rs.listener = ln
	rs.mu.Unlock()

	rs.server = rtmp.NewServer(&rtmp.ServerConfig{
		OnConnect: func(conn net.Conn) (io.ReadWriteCloser, *rtmp.ConnConfig) {
			// Track so Stop() can close it later. Handler also holds
			// the untrack func to remove itself on clean close.
			untrack := rs.trackConn(conn)
			// Wrap the conn in an idle-watchdog reader that auto-
			// refreshes a 60s read deadline on every successful read.
			// Without this, an RTMP publisher that opens a TCP conn
			// but never finishes the handshake (or silently drops
			// mid-stream) holds an rtmp library handler goroutine
			// parked in conn.Read until the kernel TCP timeout (hours).
			// At scale that's a goroutine drip per misbehaving peer.
			wrapped := newRTMPIdleConn(conn, 60*time.Second)
			h := &rtmpHandler{
				relay:   rs.relay,
				config:  rs.config,
				conn:    conn,
				untrack: untrack,
			}
			return wrapped, &rtmp.ConnConfig{
				Handler: h,
			}
		},
	})

	logger.L.Infof("RTMP server listening on %s", addr)

	go func() {
		if err := rs.server.Serve(ln); err != nil {
			rs.mu.Lock()
			closed := rs.listener == nil
			rs.mu.Unlock()
			if !closed {
				logger.L.Errorf("RTMP server error: %v", err)
			}
		}
	}()

	return nil
}

// Stop shuts down the RTMP server. Closes the listener (so no new
// connections accept), closes every currently-tracked connection (so
// handler goroutines stuck in conn.Read return), then calls the library
// Close with a short deadline so a misbehaving client can't hold the
// process open forever.
func (rs *RTMPServer) Stop() {
	rs.mu.Lock()
	listener := rs.listener
	server := rs.server
	rs.listener = nil
	rs.server = nil
	rs.mu.Unlock()

	if listener != nil {
		_ = listener.Close()
	}
	// Unblock every handler goroutine synchronously before asking the
	// rtmp library to finalise; otherwise rtmp.Server.Close() will
	// happily wait forever for handlers parked in Read.
	rs.closeAllConns()

	if server != nil {
		done := make(chan struct{})
		go func() {
			server.Close()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			logger.L.Warnw("RTMP: Close() didn't return within 3s — abandoning")
		}
	}
}

// rtmpHandler handles a single RTMP connection.
type rtmpHandler struct {
	rtmp.DefaultHandler
	relay   *Relay
	config  *config.Config
	conn    net.Conn
	untrack func() // removes this conn from RTMPServer.conns
	app     string // RTMP application name from the connect command
	mount   string
	stream  *Stream
	started time.Time

	// Video support
	videoStream *Stream // separate video stream
	videoMount  string  // e.g., "/live/video"
	sps         []byte  // cached SPS NALU
	pps         []byte  // cached PPS NALU
	naluLenSize int     // AVCC NALU length size (usually 4)

	// AAC state parsed from the AudioSpecificConfig (first AAC
	// SequenceHeader received). Used to wrap raw AAC frames in ADTS
	// headers before broadcasting so downstream TS muxing / HLS works.
	aacProfile       byte
	aacSampleRateIdx byte
	aacChannelConfig byte
	aacConfigReady   bool
}

// OnServe is called when the connection is established.
func (h *rtmpHandler) OnServe(conn *rtmp.Conn) {
	logger.L.Infow("RTMP: Client connected", "remote", h.conn.RemoteAddr())
}

// OnConnect handles the RTMP connect command. We remember the app name so
// the publish step can combine it with the stream key when the user put
// the mount in OBS's "Server" field rather than in the stream key.
func (h *rtmpHandler) OnConnect(timestamp uint32, cmd *rtmpmsg.NetConnectionConnect) error {
	h.app = cmd.Command.App
	logger.L.Infow("RTMP: Connect", "remote", h.conn.RemoteAddr(), "app", h.app)
	return nil
}

// OnCreateStream handles stream creation.
func (h *rtmpHandler) OnCreateStream(timestamp uint32, cmd *rtmpmsg.NetConnectionCreateStream) error {
	return nil
}

// OnPublish handles the RTMP publish command — this is where we set up the mount point.
func (h *rtmpHandler) OnPublish(_ *rtmp.StreamContext, timestamp uint32, cmd *rtmpmsg.NetStreamPublish) error {
	publishName := cmd.PublishingName

	// Parse the RTMP app name + publishing name into a tinyice mount and
	// source password. Supports both OBS-friendly layouts:
	//   Server: rtmp://host/mount   Stream Key: password
	//   Server: rtmp://host/        Stream Key: mount?key=password
	mount, sourcePassword := resolveRTMPPath(h.app, publishName)

	// Validate source password
	requiredPass, found := getSourcePassword(h.config, mount)
	if !found {
		requiredPass = h.config.DefaultSourcePassword
	}
	if requiredPass != "" && !config.CheckPasswordHash(sourcePassword, requiredPass) {
		logger.L.Warnw("RTMP: Auth failed", "mount", mount, "remote", h.conn.RemoteAddr())
		return fmt.Errorf("authentication failed for mount %s", mount)
	}

	// Check if mount is disabled
	if h.config.DisabledMounts[mount] {
		return fmt.Errorf("mount %s is disabled", mount)
	}

	h.mount = mount
	h.stream = h.relay.GetOrCreateStream(mount)
	h.stream.mu.Lock()
	h.stream.SourceIP = h.conn.RemoteAddr().String()
	h.stream.ContentType = "audio/mpeg" // default, may be updated on first audio data
	h.stream.mu.Unlock()
	h.started = time.Now()

	// Create a separate video stream with an 8 MB buffer up front, so we
	// never have to swap the Buffer pointer under listeners that are
	// already reading. GetOrCreateStreamSized leaves existing streams
	// untouched.
	h.videoMount = mount + "/video"
	h.videoStream = h.relay.GetOrCreateStreamSized(h.videoMount, 8*1024*1024)
	h.videoStream.mu.Lock()
	h.videoStream.SourceIP = h.conn.RemoteAddr().String()
	h.videoStream.ContentType = "video/h264"
	h.videoStream.mu.Unlock()

	logger.L.Infow("RTMP: Publishing started",
		"mount", mount,
		"remote", h.conn.RemoteAddr(),
	)
	return nil
}

// ptsFromFLVTimestamp converts an FLV timestamp (milliseconds, uint32) into
// a 90 kHz PTS value, which is what the MPEG-TS PES header expects.
func ptsFromFLVTimestamp(ms uint32) int64 {
	return int64(ms) * 90
}

// OnAudio handles incoming audio data from the RTMP stream.
func (h *rtmpHandler) OnAudio(timestamp uint32, payload io.Reader) (err error) {
	defer recoverIngestErr("rtmp", h.conn.RemoteAddr(), &err)
	if h.stream == nil {
		return nil
	}

	// Read the FLV audio tag header
	var audioTag flvtag.AudioData
	if err := flvtag.DecodeAudioData(payload, &audioTag); err != nil {
		return err
	}

	// Read the raw audio data
	data, err := io.ReadAll(audioTag.Data)
	if err != nil {
		return err
	}

	if len(data) == 0 {
		return nil
	}

	// Determine content type from codec. Only take the stream mutex when
	// the content type actually changes to avoid locking on every audio
	// packet.
	switch audioTag.SoundFormat {
	case flvtag.SoundFormatMP3:
		h.stream.mu.RLock()
		needsUpdate := h.stream.ContentType != "audio/mpeg"
		h.stream.mu.RUnlock()
		if needsUpdate {
			h.stream.mu.Lock()
			h.stream.ContentType = "audio/mpeg"
			h.stream.mu.Unlock()
		}
	case flvtag.SoundFormatAAC:
		h.stream.mu.RLock()
		needsUpdate := h.stream.ContentType != "audio/aac"
		h.stream.mu.RUnlock()
		if needsUpdate {
			h.stream.mu.Lock()
			h.stream.ContentType = "audio/aac"
			h.stream.mu.Unlock()
		}
		if audioTag.AACPacketType == flvtag.AACPacketTypeSequenceHeader {
			// Parse AudioSpecificConfig so we can ADTS-wrap later frames.
			// ASC layout (ISO 14496-3):
			//   5 bits objectType, 4 bits samplingFreqIdx,
			//   4 bits channelConfiguration, 3 bits GASpecificConfig.
			if len(data) >= 2 {
				objType := (data[0] >> 3) & 0x1F
				sri := ((data[0] & 0x07) << 1) | ((data[1] >> 7) & 0x01)
				ch := (data[1] >> 3) & 0x0F
				h.aacProfile = 0
				if objType > 0 {
					h.aacProfile = objType - 1 // ADTS profile = objectType - 1
				}
				h.aacSampleRateIdx = sri
				h.aacChannelConfig = ch
				h.aacConfigReady = true
				logger.L.Infow("RTMP: Parsed AAC ASC",
					"mount", h.mount,
					"profile", h.aacProfile,
					"sr_idx", h.aacSampleRateIdx,
					"ch", h.aacChannelConfig,
				)
			}
			return nil
		}
		// Raw AAC payload — prepend ADTS so the MPEG-TS muxer / HLS
		// clients can actually decode it. Without this the bytes leave
		// the server as "naked" AAC and any container-based consumer
		// fails silently.
		if h.aacConfigReady {
			hdr := BuildADTSHeader(h.aacProfile, h.aacSampleRateIdx, h.aacChannelConfig, len(data))
			wrapped := make([]byte, 0, len(hdr)+len(data))
			wrapped = append(wrapped, hdr...)
			wrapped = append(wrapped, data...)
			data = wrapped
		}
	default:
		// Unknown format, pass through
	}

	// Broadcast the raw audio data to the stream (for byte-level
	// Icecast listeners) and publish a Frame to the hub (for HLS /
	// other per-frame consumers). Audio has no B-frames so DTS == PTS.
	h.stream.Broadcast(data, h.relay)
	if h.stream.Frames != nil {
		frameData := make([]byte, len(data))
		copy(frameData, data)
		pts := ptsFromFLVTimestamp(timestamp)
		h.stream.Frames.Publish(Frame{
			Kind: FrameAudio,
			PTS:  pts,
			DTS:  pts,
			Data: frameData,
		})
	}
	return nil
}

// OnVideo handles incoming video data from the RTMP stream.
func (h *rtmpHandler) OnVideo(timestamp uint32, payload io.Reader) (err error) {
	defer recoverIngestErr("rtmp", h.conn.RemoteAddr(), &err)
	if h.videoStream == nil {
		_, _ = io.ReadAll(payload)
		return nil
	}

	var videoTag flvtag.VideoData
	if err := flvtag.DecodeVideoData(payload, &videoTag); err != nil {
		return err
	}

	data, err := io.ReadAll(videoTag.Data)
	if err != nil {
		return err
	}

	if len(data) == 0 {
		return nil
	}

	// Handle AVC (H.264)
	if videoTag.CodecID == flvtag.CodecIDAVC {
		switch videoTag.AVCPacketType {
		case flvtag.AVCPacketTypeSequenceHeader:
			// Parse AVCDecoderConfigurationRecord to extract SPS/PPS
			h.parseAVCConfig(data)
			return nil
		case flvtag.AVCPacketTypeNALU:
			// Convert AVCC to Annex B and broadcast
			if h.naluLenSize == 0 {
				h.naluLenSize = 4
			}
			annexB := AVCCToAnnexB(data, h.naluLenSize)
			if len(annexB) == 0 {
				return nil
			}
			// Normalise every video access unit for iOS Safari's
			// strict HLS demuxer: rewrite inline SPS NALUs, drop
			// SEI NALUs (some sources embed unparseable user_data),
			// drop any existing AUD, and prepend a single
			// primary_pic_type-correct AUD. Without the AUD prepend
			// iOS rejects every sample with
			// kCMFormatDescriptionError_InvalidParameter even when
			// the SPS / PPS / slice data are byte-equivalent to a
			// known-good x264 output.
			annexB = h264NALUFilter(annexB, 30)

			// For every IDR (keyframe) inject the cached SPS and PPS
			// immediately before the IDR NALU — otherwise a new viewer
			// who tunes in mid-stream never receives them (RTMP only
			// delivers AVCDecoderConfigurationRecord once, at stream
			// start) and can't decode until the publisher reconnects.
			isKeyframe := ContainsKeyframe(annexB)
			if isKeyframe && !HasInlineParameterSets(annexB) {
				// Only prepend cached SPS/PPS when the encoder didn't
				// already include them with the IDR. Sources like OBS
				// and ffmpeg with -bsf:v dump_extra ship parameter sets
				// inline at every keyframe; duplicating them confuses
				// iOS Safari's hardware decoder (frozen video, audio
				// keeps playing) and adds ~50-100 bytes per keyframe
				// to every other consumer.
				annexB = h.prependParameterSets(annexB)
				// HeadOffset takes the buffer mutex; Buffer.Head directly
				// is racy under the race detector. The value we want is
				// the next-write offset after the SPS/PPS prepend will
				// have been done by Broadcast — RecordKeyframe just
				// records "the IDR begins around here" so an off-by-N is
				// fine; what matters is that we don't tear a 64-bit
				// value during a concurrent Buffer.Write.
				h.videoStream.Buffer.RecordKeyframe(h.videoStream.Buffer.HeadOffset())
			}

			h.videoStream.Broadcast(annexB, h.relay)
			// Feed the dashboard's video metrics sampler: resolution
			// (parsed from the cached SPS, zero means "unchanged"),
			// this frame's byte count, and whether it's a keyframe.
			// GOP seconds and FPS are derived server-side from the
			// stream of timestamps.
			w, hi, _ := ParseSPSResolution(h.sps)
			h.videoStream.RecordVideoSample(w, hi, len(annexB), isKeyframe, time.Now())
			// Publish a per-frame record carrying PTS *and* DTS. FLV
			// video tags have CompositionTime (int32 ms, signed) which
			// is PTS - DTS; without splitting them, B-frames display
			// in decode order and the picture boomerangs between I/P
			// and the two Bs either side of it.
			if h.videoStream.Frames != nil {
				frameData := make([]byte, len(annexB))
				copy(frameData, annexB)
				dts := ptsFromFLVTimestamp(timestamp)
				pts := dts + int64(videoTag.CompositionTime)*90
				h.videoStream.Frames.Publish(Frame{
					Kind:     FrameVideo,
					PTS:      pts,
					DTS:      dts,
					Data:     frameData,
					Keyframe: isKeyframe,
				})
			}
		}
	}

	return nil
}

// parseAVCConfig extracts SPS/PPS from AVCDecoderConfigurationRecord.
func (h *rtmpHandler) parseAVCConfig(data []byte) {
	if len(data) < 8 {
		return
	}
	// If we already had SPS/PPS from an earlier config, the publisher has
	// reconfigured its encoder (different resolution, profile, GOP size,
	// etc.). Bytes already in the video buffer were encoded under the
	// old parameters and would blow up any new listener's decoder once
	// the new SPS/PPS is active. Checkpoint the current Head so new
	// listeners only see bytes written from here on.
	reconfig := len(h.sps) > 0 || len(h.pps) > 0
	// AVCDecoderConfigurationRecord structure:
	// [0] configurationVersion
	// [1] AVCProfileIndication
	// [2] profile_compatibility
	// [3] AVCLevelIndication
	// [4] lengthSizeMinusOne (lower 2 bits) + reserved
	h.naluLenSize = int(data[4]&0x03) + 1

	// [5] numOfSPS (lower 5 bits)
	numSPS := int(data[5] & 0x1F)
	offset := 6

	for i := 0; i < numSPS && offset+2 <= len(data); i++ {
		spsLen := int(data[offset])<<8 | int(data[offset+1])
		offset += 2
		if offset+spsLen <= len(data) {
			raw := make([]byte, spsLen)
			copy(raw, data[offset:offset+spsLen])
			// Rewrite the SPS to a spec-compliant shape before we
			// cache + ship it downstream. Some sources (we observed
			// an OBS/x264 build) emit a SPS with fixed_frame_rate=1
			// but no HRD parameters — that combination implies
			// low_delay_hrd_flag=0 per H.264 §E.2.1, which iOS
			// Safari's AVFoundation HLS demuxer rejects with
			// kCMFormatDescriptionError_InvalidParameter on every
			// sample (loadedmetadata 0x0, no frames decoded).
			// IOSifyH264SPS preserves dimensions / profile / level
			// / ref counts / cropping while substituting a
			// minimal-but-conformant VUI. If the source SPS uses a
			// shape we don't parse (interlaced, separate-colour,
			// custom scaling lists), the function returns the
			// original bytes unchanged so we never make a stream
			// worse.
			h.sps = IOSifyH264SPS(raw, 60)
			offset += spsLen
		}
	}

	// numOfPPS
	if offset < len(data) {
		numPPS := int(data[offset])
		offset++
		for i := 0; i < numPPS && offset+2 <= len(data); i++ {
			ppsLen := int(data[offset])<<8 | int(data[offset+1])
			offset += 2
			if offset+ppsLen <= len(data) {
				h.pps = make([]byte, ppsLen)
				copy(h.pps, data[offset:offset+ppsLen])
				offset += ppsLen
			}
		}
	}

	// Publish SPS+PPS as Annex-B on the video stream so a listener that
	// subscribes via HTTP gets them prepended by the listener handler.
	// Each NALU is preceded by the 4-byte 00 00 00 01 start code.
	if h.videoStream != nil && (len(h.sps) > 0 || len(h.pps) > 0) {
		sc := []byte{0x00, 0x00, 0x00, 0x01}
		out := make([]byte, 0, len(h.sps)+len(h.pps)+8)
		if len(h.sps) > 0 {
			out = append(out, sc...)
			out = append(out, h.sps...)
		}
		if len(h.pps) > 0 {
			out = append(out, sc...)
			out = append(out, h.pps...)
		}
		h.videoStream.StoreVideoHeaders(out)
	}

	if reconfig && h.videoStream != nil {
		h.videoStream.CheckpointAtHead()
		logger.L.Infow("RTMP: Encoder reconfig — checkpointed video buffer",
			"mount", h.mount,
		)
	}

	logger.L.Infow("RTMP: Parsed AVC config",
		"mount", h.mount,
		"sps_len", len(h.sps),
		"pps_len", len(h.pps),
		"nalu_len_size", h.naluLenSize,
		"reconfig", reconfig,
	)
}

// prependParameterSets returns Annex-B bytes consisting of the cached SPS
// and PPS followed by `annexB`. Used to ensure every IDR access unit the
// relay hands to downstream players is preceded by the parameter sets, so
// late joiners can decode without waiting for a republish.
func (h *rtmpHandler) prependParameterSets(annexB []byte) []byte {
	if len(h.sps) == 0 && len(h.pps) == 0 {
		return annexB
	}
	startCode := []byte{0x00, 0x00, 0x00, 0x01}
	out := make([]byte, 0, len(h.sps)+len(h.pps)+len(annexB)+8)
	if len(h.sps) > 0 {
		out = append(out, startCode...)
		out = append(out, h.sps...)
	}
	if len(h.pps) > 0 {
		out = append(out, startCode...)
		out = append(out, h.pps...)
	}
	out = append(out, annexB...)
	return out
}

// OnClose is called when the connection is closed. It only removes the
// relay stream entries that still point at THIS handler's Stream object.
// Without that check, an auto-reconnecting publisher that briefly
// disconnects-and-reconnects with a tiny gap would have its successor's
// stream killed by the predecessor's delayed OnClose.
func (h *rtmpHandler) OnClose() {
	if h.untrack != nil {
		h.untrack()
	}
	if h.mount != "" {
		logger.L.Infow("RTMP: Source disconnected",
			"mount", h.mount,
			"remote", h.conn.RemoteAddr(),
			"duration", time.Since(h.started),
		)
		if st, ok := h.relay.GetStream(h.mount); ok && st == h.stream {
			h.relay.RemoveStream(h.mount)
		}
	}
	if h.videoMount != "" {
		if st, ok := h.relay.GetStream(h.videoMount); ok && st == h.videoStream {
			h.relay.RemoveStream(h.videoMount)
		}
	}
}

// resolveRTMPPath turns the RTMP `app` (from the connect command) and
// `publishName` (from the publish command) into a tinyice mount path and
// source password. Two layouts are supported:
//
//  1. OBS default — Server "rtmp://host/mount", Stream Key "password".
//     The RTMP app is the mount; the stream key is the password. This is
//     how encoders treat Twitch/YouTube/nginx-rtmp-style URLs, so OBS
//     users can just fill in the two fields the way OBS shows them.
//
//  2. Classic tinyice — Server "rtmp://host/", Stream Key
//     "mount?key=password". Kept for backward compatibility and CLIs
//     that only take a single URL.
//
// A publishing name that contains "?" always uses layout 2, so legacy
// ?key= parameters keep working regardless of what's in the app slot.
func resolveRTMPPath(app, publishName string) (mount, password string) {
	app = strings.TrimSpace(strings.Trim(app, "/"))
	publishName = strings.TrimSpace(publishName)

	// Layout 2: explicit ?key= in the stream key wins. The mount is the
	// app plus the name part, joined cleanly.
	if i := strings.Index(publishName, "?"); i >= 0 {
		nameOnly := publishName[:i]
		query := publishName[i:]
		full := nameOnly
		if app != "" {
			full = app + "/" + strings.TrimLeft(nameOnly, "/")
		}
		return parseStreamKey(full + query)
	}

	// Layout 1: OBS-style server/key split. Only kick in when the server
	// actually had a path segment, otherwise we'd misinterpret a bare
	// stream key as a password-less mount.
	if app != "" {
		return "/" + app, publishName
	}

	// No app, no ?key= — whole publishing name is the mount (and relies
	// on DefaultSourcePassword or an unauthenticated mount).
	return parseStreamKey(publishName)
}

// parseStreamKey splits "mount" or "mount?key=password" into mount and password.
func parseStreamKey(key string) (string, string) {
	if !strings.HasPrefix(key, "/") {
		key = "/" + key
	}

	parts := strings.SplitN(key, "?", 2)
	mount := parts[0]

	password := ""
	if len(parts) == 2 {
		for _, param := range strings.Split(parts[1], "&") {
			kv := strings.SplitN(param, "=", 2)
			if len(kv) == 2 && kv[0] == "key" {
				password = kv[1]
				break
			}
		}
	}

	return mount, password
}

// rtmpIdleConn wraps a net.Conn and refreshes a read deadline before
// every Read. A silent or dead RTMP peer (TCP open but no protocol
// activity) trips the deadline; conn.Read returns an i/o timeout and
// the rtmp library handler exits, freeing the goroutine + slot.
type rtmpIdleConn struct {
	net.Conn
	idle time.Duration
}

func newRTMPIdleConn(c net.Conn, idle time.Duration) *rtmpIdleConn {
	return &rtmpIdleConn{Conn: c, idle: idle}
}

func (c *rtmpIdleConn) Read(p []byte) (int, error) {
	_ = c.Conn.SetReadDeadline(time.Now().Add(c.idle))
	return c.Conn.Read(p)
}

// getSourcePassword looks up the configured source password for a mount.
func getSourcePassword(cfg *config.Config, mount string) (string, bool) {
	if am, ok := cfg.AdvancedMounts[mount]; ok && am.Password != "" {
		return am.Password, true
	}
	if pw, ok := cfg.Mounts[mount]; ok {
		return pw, true
	}
	return "", false
}
