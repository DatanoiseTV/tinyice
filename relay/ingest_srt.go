package relay

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
	srt "github.com/datarhei/gosrt"
)

// SRTServer accepts SRT connections and feeds audio data into TinyIce streams.
// It uses the pure-Go gosrt library (no CGO required).
type SRTServer struct {
	relay    *Relay
	config   *config.Config
	server   *srt.Server
	mountMap sync.Map // socket ID -> srtConnInfo
	mu       sync.Mutex
	running  bool
}

// srtConnInfo holds connection metadata established during HandleConnect.
type srtConnInfo struct {
	mount    string
	password string
	streamID string
}

// NewSRTServer creates a new SRT ingest server.
func NewSRTServer(r *Relay, cfg *config.Config) *SRTServer {
	return &SRTServer{
		relay:  r,
		config: cfg,
	}
}

// Start begins listening for SRT connections on the configured port.
func (ss *SRTServer) Start() error {
	port := ss.config.Ingest.SRTPort
	if port == "" {
		port = "9000"
	}

	addr := fmt.Sprintf(":%s", port)

	ss.server = &srt.Server{
		Addr: addr,
		HandleConnect: func(req srt.ConnRequest) srt.ConnType {
			streamID := req.StreamId()
			mount, password, mode := parseSRTStreamID(streamID)
			if mount == "" {
				logger.L.Warnw("SRT: Rejected connection with empty mount",
					"remote", req.RemoteAddr(),
					"stream_id", streamID,
				)
				req.Reject(srt.REJ_PEER)
				return srt.REJECT
			}

			// Only accept publish connections
			if mode != "" && mode != "publish" {
				logger.L.Warnw("SRT: Rejected non-publish connection",
					"remote", req.RemoteAddr(),
					"mode", mode,
				)
				req.Reject(srt.REJX_BAD_MODE)
				return srt.REJECT
			}

			// Check if mount is disabled
			if ss.config.DisabledMounts[mount] {
				logger.L.Warnw("SRT: Mount is disabled",
					"mount", mount,
					"remote", req.RemoteAddr(),
				)
				req.Reject(srt.REJX_FORBIDDEN)
				return srt.REJECT
			}

			// Validate source password
			requiredPass, found := getSourcePassword(ss.config, mount)
			if !found {
				requiredPass = ss.config.DefaultSourcePassword
			}
			if requiredPass != "" && !config.CheckPasswordHash(password, requiredPass) {
				logger.L.Warnw("SRT: Auth failed",
					"mount", mount,
					"remote", req.RemoteAddr(),
				)
				req.Reject(srt.REJX_UNAUTHORIZED)
				return srt.REJECT
			}

			// Store mount info for HandlePublish lookup by socket ID
			ss.mountMap.Store(req.SocketId(), &srtConnInfo{
				mount:    mount,
				password: password,
				streamID: streamID,
			})

			logger.L.Infow("SRT: Accepting publish connection",
				"mount", mount,
				"remote", req.RemoteAddr(),
			)
			return srt.PUBLISH
		},
		HandlePublish: func(conn srt.Conn) {
			ss.handlePublish(conn)
		},
	}

	logger.L.Infof("SRT server listening on %s", addr)

	ss.mu.Lock()
	ss.running = true
	ss.mu.Unlock()

	go func() {
		if err := ss.server.ListenAndServe(); err != nil {
			if err != srt.ErrServerClosed {
				logger.L.Errorf("SRT server error: %v", err)
			}
			ss.mu.Lock()
			ss.running = false
			ss.mu.Unlock()
		}
	}()

	return nil
}

// Stop shuts down the SRT server. The underlying gosrt.Server.Shutdown()
// closes the listener and terminates Serve, so goroutines waiting on
// accept exit cleanly — the previous implementation only flipped the
// running flag and left the listener up.
func (ss *SRTServer) Stop() {
	ss.mu.Lock()
	server := ss.server
	ss.running = false
	ss.mu.Unlock()
	if server != nil {
		server.Shutdown()
	}
}

// IsRunning returns whether the SRT server is currently running.
func (ss *SRTServer) IsRunning() bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.running
}

// handlePublish processes an SRT publish connection, demuxing MPEG-TS and
// broadcasting extracted audio to the appropriate TinyIce stream.
func (ss *SRTServer) handlePublish(conn srt.Conn) {
	defer conn.Close()
	defer recoverIngest("srt", conn.RemoteAddr())

	remoteAddr := conn.RemoteAddr()
	started := time.Now()

	// Look up mount info stored during HandleConnect
	v, ok := ss.mountMap.LoadAndDelete(conn.SocketId())
	if !ok {
		logger.L.Warnw("SRT: No mount found for connection", "remote", remoteAddr)
		return
	}
	info := v.(*srtConnInfo)
	mount := info.mount

	stream := ss.relay.GetOrCreateStream(mount)
	stream.mu.Lock()
	stream.SourceIP = remoteAddr.String()
	stream.ContentType = "audio/mpeg" // default for MPEG-TS with MP3
	stream.mu.Unlock()

	// Mirror the RTMP ingest: spin up a dedicated /video sub-mount with
	// an 8 MB buffer so demuxed H.264 bytes have somewhere to go. The
	// sub-mount is created lazily on first video packet so audio-only
	// SRT sources don't pollute the stream list.
	videoMount := mount + "/video"
	var videoStream *Stream

	logger.L.Infow("SRT: Publishing started",
		"mount", mount,
		"remote", remoteAddr,
	)

	demuxer := NewTSDemuxer()
	demuxer.OnAudio(func(data []byte, pts int64) {
		stream.Broadcast(data, ss.relay)
	})
	// Previously the demuxer's OnVideo callback was never registered, so
	// SRT video packets were parsed, tagged with keyframe status and
	// then silently dropped. Broadcast them to the /video sub-mount so
	// HLS (and any other video-aware consumer) can pick them up.
	demuxer.OnVideo(func(data []byte, pts int64, isKeyframe bool) {
		if videoStream == nil {
			videoStream = ss.relay.GetOrCreateStreamSized(videoMount, 8*1024*1024)
			videoStream.mu.Lock()
			videoStream.SourceIP = remoteAddr.String()
			videoStream.ContentType = "video/h264"
			videoStream.mu.Unlock()
		}
		if isKeyframe {
			videoStream.Buffer.RecordKeyframe(videoStream.Buffer.HeadOffset())
		}
		videoStream.Broadcast(data, ss.relay)
	})

	// Per-read deadline. A wedged peer can keep the SRT connection
	// technically "alive" while delivering zero bytes — protocol-level
	// keepalives don't help if the publisher is silent at the data
	// layer. Without this, conn.Read parks forever, listeners get a
	// stale stream while the source still counts as connected, and on
	// the next reconnect we leak the prior handler goroutine.
	const srtIdleTimeout = 60 * time.Second

	buf := make([]byte, 4096)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(srtIdleTimeout))
		n, err := conn.Read(buf)
		if err != nil {
			if err != io.EOF {
				logger.L.Warnw("SRT: Read error",
					"mount", mount,
					"remote", remoteAddr,
					"error", err,
				)
			}
			break
		}
		if n > 0 {
			demuxer.Feed(buf[:n])
		}
	}

	logger.L.Infow("SRT: Source disconnected",
		"mount", mount,
		"remote", remoteAddr,
		"duration", time.Since(started),
	)
	// Only tear down the streams that this handler actually owns, matching
	// the RTMP path — prevents a delayed OnClose from killing a successor
	// publisher's mount.
	if st, ok := ss.relay.GetStream(mount); ok && st == stream {
		ss.relay.RemoveStream(mount)
	}
	if videoStream != nil {
		if st, ok := ss.relay.GetStream(videoMount); ok && st == videoStream {
			ss.relay.RemoveStream(videoMount)
		}
	}
}

// parseSRTStreamID extracts mount, password, and mode from an SRT stream ID.
// Supported formats:
//   - "#!::r=mountname,m=publish" (SRT access control)
//   - "#!::r=mountname,m=publish,key=password"
//   - "/mountname" or "/mountname?key=password" (simple path)
//   - "mountname" (bare name)
func parseSRTStreamID(streamID string) (mount, password, mode string) {
	streamID = strings.TrimSpace(streamID)

	// SRT access control format: #!::key=value,key=value
	if strings.HasPrefix(streamID, "#!::") {
		params := strings.TrimPrefix(streamID, "#!::")
		for _, part := range strings.Split(params, ",") {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) != 2 {
				continue
			}
			switch kv[0] {
			case "r":
				mount = kv[1]
			case "m":
				mode = kv[1]
			case "key":
				password = kv[1]
			}
		}
		if mount != "" && !strings.HasPrefix(mount, "/") {
			mount = "/" + mount
		}
		return mount, password, mode
	}

	// Simple path format
	mount = streamID
	if !strings.HasPrefix(mount, "/") {
		mount = "/" + mount
	}

	// Extract query parameters
	if idx := strings.Index(mount, "?"); idx >= 0 {
		query := mount[idx+1:]
		mount = mount[:idx]
		for _, param := range strings.Split(query, "&") {
			kv := strings.SplitN(param, "=", 2)
			if len(kv) == 2 && kv[0] == "key" {
				password = kv[1]
			}
		}
	}

	return mount, password, "publish"
}
