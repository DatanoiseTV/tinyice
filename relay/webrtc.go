package relay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DatanoiseTV/tinyice/logger"
	"github.com/kazzmir/opus-go/ogg"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/pion/webrtc/v4/pkg/media/oggwriter"
)

type SimplePacer struct {
	startTime time.Time
	sentMS    int64
}

func (p *SimplePacer) Pace(duration time.Duration) {
	if p.startTime.IsZero() {
		p.startTime = time.Now()
	}
	p.sentMS += duration.Milliseconds()
	targetTime := p.startTime.Add(time.Duration(p.sentMS) * time.Millisecond)

	wait := time.Until(targetTime)

	// Catch-up logic: If we are more than 500ms behind, reset the pacer baseline
	if wait < -500*time.Millisecond {
		p.startTime = time.Now()
		p.sentMS = 0
		return
	}

	if wait > 0 {
		time.Sleep(wait)
	}
}

type WebRTCManager struct {
	api     *webrtc.API
	relay   *Relay
	mu      sync.RWMutex
	sources map[string]*webrtc.PeerConnection
	// sourceDone[mount] is closed by the OnTrack pump goroutine when
	// it exits. Lets a successor HandleSourceOffer wait for the
	// previous pump to drain before starting its own, preventing the
	// brief two-pumps-into-one-stream overlap that produces torn Ogg
	// pages. Indexed by mount so each source rotation is isolated.
	sourceDone map[string]chan struct{}
}

func NewWebRTCManager(r *Relay) *WebRTCManager {
	s := webrtc.SettingEngine{}
	// Optimize for many listeners
	s.SetICETimeouts(10*time.Second, 20*time.Second, 2*time.Second)

	api := webrtc.NewAPI(webrtc.WithSettingEngine(s))
	return &WebRTCManager{
		api:        api,
		relay:      r,
		sources:    make(map[string]*webrtc.PeerConnection),
		sourceDone: make(map[string]chan struct{}),
	}
}

func (wm *WebRTCManager) HandleOffer(mount string, offer webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	stream, ok := wm.relay.GetStream(mount)
	if !ok {
		return nil, fmt.Errorf("stream not found")
	}

	// WebRTC requires Opus (usually in Ogg container for us)
	ct := strings.ToLower(stream.ContentType)
	if !strings.Contains(ct, "ogg") && !strings.Contains(ct, "opus") {
		return nil, fmt.Errorf("WebRTC requires Opus stream (got %s)", stream.ContentType)
	}

	peerConnection, err := wm.api.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	})
	if err != nil {
		return nil, err
	}

	// Create a track for Opus audio
	audioTrack, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus}, "audio", "tinyice")
	if err != nil {
		return nil, err
	}

	if _, err = peerConnection.AddTrack(audioTrack); err != nil {
		return nil, err
	}

	// Set the remote SessionDescription
	if err = peerConnection.SetRemoteDescription(offer); err != nil {
		return nil, err
	}

	// Create answer
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		return nil, err
	}

	// Sets the LocalDescription and starts gathering ICE candidates
	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)
	if err = peerConnection.SetLocalDescription(answer); err != nil {
		return nil, err
	}
	<-gatherComplete

	// Start feeding the track. Register OnConnectionStateChange once
	// here (pion replaces handlers, so registering inside the goroutine
	// caused leaks under WHEP). Cancelling pumpCtx unblocks the pump.
	pumpCtx, pumpCancel := context.WithCancel(context.Background())
	peerConnection.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateClosed ||
			state == webrtc.PeerConnectionStateFailed ||
			state == webrtc.PeerConnectionStateDisconnected {
			pumpCancel()
			// Release UDP sockets / DTLS state. Same FD-creep
			// concern as the WHEP path.
			_ = peerConnection.Close()
		}
	})
	go wm.streamToTrack(pumpCtx, peerConnection, audioTrack, stream)

	return peerConnection.LocalDescription(), nil
}

type relayWriter struct {
	relay  *Relay
	stream *Stream
}

func (rw *relayWriter) Write(p []byte) (n int, err error) {
	logger.L.Debugw("relayWriter: Broadcasting", "bytes", len(p), "mount", rw.stream.MountName)
	rw.stream.Broadcast(p, rw.relay)
	return len(p), nil
}

func (wm *WebRTCManager) HandleSourceOffer(mount string, offer webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	peerConnection, err := wm.api.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	})
	if err != nil {
		return nil, err
	}

	// Per-source-ingest context, cancelled when the PC tears down.
	// The OnTrack ReadRTP loop selects on it so a closed PC unwinds
	// the pump goroutine immediately rather than waiting for pion's
	// internal track teardown.
	srcCtx, srcCancel := context.WithCancel(context.Background())

	// When a source reconnects (same mount, new peer connection), we
	// must wait for the previous source's pump goroutine to actually
	// EXIT before letting the new one start writing — otherwise both
	// run briefly and interleave bytes into the shared Stream.Buffer,
	// producing torn Ogg pages until pion finishes closing the prior
	// track. existingDone is closed by the prior OnTrack on exit.
	wm.mu.Lock()
	existingPC, _ := wm.sources[mount]
	existingDone := wm.sourceDone[mount]
	doneCh := make(chan struct{})
	if wm.sourceDone == nil {
		wm.sourceDone = make(map[string]chan struct{})
	}
	wm.sources[mount] = peerConnection
	wm.sourceDone[mount] = doneCh
	wm.mu.Unlock()
	if existingPC != nil {
		_ = existingPC.Close()
	}
	if existingDone != nil {
		// Bounded wait: pion's track.ReadRTP returns promptly on
		// PC.Close. 3 s is generous; if it hasn't unwound by then
		// we proceed anyway and accept the brief overlap rather
		// than blocking the new source forever.
		select {
		case <-existingDone:
		case <-time.After(3 * time.Second):
			logger.L.Warnw("WebRTC Source: previous pump didn't exit in time", "mount", mount)
		}
	}

	peerConnection.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		logger.L.Infow("WebRTC Source: Connection state changed", "mount", mount, "state", state.String())
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed || state == webrtc.PeerConnectionStateDisconnected {
			srcCancel()
			wm.mu.Lock()
			if wm.sources[mount] == peerConnection {
				delete(wm.sources, mount)
				delete(wm.sourceDone, mount)
			}
			wm.mu.Unlock()
			// Release UDP sockets / DTLS state on terminal states.
			// pion's PC keeps internal goroutines alive until Close;
			// without this the FD count creeps up across reconnects.
			_ = peerConnection.Close()
		}
	})

	// pion invokes OnTrack once per remote track, each on its own
	// goroutine that nothing recovers. An offer carrying audio+video (a
	// browser with getUserMedia({audio, video}), or a WHIP tool that adds
	// a video track) therefore ran this closure twice: two pumps wrote
	// interleaved Ogg pages into the same Stream.Buffer, and when the
	// publisher left, the second deferred close(doneCh) panicked with
	// "close of closed channel" and took the whole process down. One
	// track owns the pump; the rest are declined; doneCh closes once.
	var pumpClaimed atomic.Bool
	var doneOnce sync.Once
	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		defer func() {
			if r := recover(); r != nil {
				logger.L.Errorw("WebRTC Source: track pump panicked", "mount", mount, "panic", fmt.Sprintf("%v", r))
			}
		}()
		codec := track.Codec().MimeType
		if !strings.EqualFold(codec, webrtc.MimeTypeOpus) {
			logger.L.Warnw("WebRTC Source: ignoring non-Opus track", "track", track.ID(), "codec", codec, "mount", mount)
			return
		}
		if !pumpClaimed.CompareAndSwap(false, true) {
			logger.L.Warnw("WebRTC Source: ignoring additional audio track; the first one feeds the mount", "track", track.ID(), "mount", mount)
			return
		}
		logger.L.Infow("WebRTC Source: Received track", "track", track.ID(), "mount", mount)
		// doneCh handshake with a successor source — close on exit so
		// the next HandleSourceOffer can stop waiting for us.
		defer doneOnce.Do(func() { close(doneCh) })

		stream := wm.relay.GetOrCreateStream(mount)
		stream.mu.Lock()
		stream.ContentType = "audio/ogg"
		stream.IsOggStream = true
		stream.SourceIP = "webrtc-source"
		stream.mu.Unlock()
		// Fresh producer session: wipes Ogg page-tracking state from any
		// previous source on this mount and snaps existing listeners to
		// the live edge.
		stream.BeginSession()

		// Capture headers written by oggwriter.NewWith
		var headerBuf bytes.Buffer
		multi := io.MultiWriter(&headerBuf, &relayWriter{wm.relay, stream})

		writer, err := oggwriter.NewWith(multi, 48000, 2)
		if err != nil {
			logger.L.Errorf("Failed to create Ogg writer for WebRTC source: %v", err)
			return
		}
		defer writer.Close()

		// Save captured headers immediately
		stream.mu.Lock()
		h := make([]byte, headerBuf.Len())
		copy(h, headerBuf.Bytes())
		stream.OggHead = h
		logger.L.Infow("WebRTC Source: Captured Ogg/Opus headers", "bytes", len(h), "offset", stream.OggHeaderOffset)
		stream.mu.Unlock()

		for {
			select {
			case <-srcCtx.Done():
				return
			default:
			}
			rtpPacket, _, err := track.ReadRTP()
			if err != nil {
				if err != io.EOF {
					logger.L.Errorf("Error reading RTP packet from source: %v", err)
				}
				return
			}
			if err := writer.WriteRTP(rtpPacket); err != nil {
				logger.L.Errorf("Error writing RTP to Ogg muxer: %v", err)
				return
			}
		}
	})

	if err = peerConnection.SetRemoteDescription(offer); err != nil {
		return nil, err
	}

	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		return nil, err
	}

	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)
	if err = peerConnection.SetLocalDescription(answer); err != nil {
		return nil, err
	}
	<-gatherComplete

	return peerConnection.LocalDescription(), nil
}

func (wm *WebRTCManager) streamToTrack(ctx context.Context, pc *webrtc.PeerConnection, track *webrtc.TrackLocalStaticSample, stream *Stream) {
	// ctx is shared with the matching streamVideoToTrack goroutine and
	// is cancelled by the single OnConnectionStateChange registered in
	// HandleWHEPOffer. Don't register another one here — pion replaces
	// the prior handler and the loser leaks.

	id := fmt.Sprintf("webrtc-%d", time.Now().UnixNano())
	// For WebRTC we start at the current head (no burst)
	offset, signal := stream.Subscribe(id, 0)
	defer stream.Unsubscribe(id)

	// Seek to next "OggS" magic
	syncBuf := make([]byte, 16384)
	foundSync := false
	var searched int64 = 0
	for !foundSync && searched < 512*1024 {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-signal:
			// Stream.Close / DisconnectListeners close the signal
			// channel. A closed channel is always ready, so without
			// this check the loop spun at 100% of a core forever on a
			// mount that went away before the viewer found its first
			// page. Mirrors StreamReader in io.go.
			if !ok {
				return
			}
			n, next, _ := stream.Buffer.ReadAt(offset, syncBuf)
			if n == 0 {
				continue
			}
			// Use a robust sliding window to find "OggS"
			for i := 0; i <= n-4; i++ {
				if syncBuf[i] == 'O' && syncBuf[i+1] == 'g' && syncBuf[i+2] == 'g' && syncBuf[i+3] == 'S' {
					offset += int64(i)
					foundSync = true
					break
				}
			}
			if !foundSync {
				// Keep the last 3 bytes in case "OggS" is split between reads
				offset = next - 3
				searched += int64(n)
			}
		}
	}

	if !foundSync {
		logger.L.Errorw("WebRTC: Could not find Ogg sync in first 512KB. Is it an Opus stream?", "mount", stream.MountName)
		return
	}

	reader := NewStreamReader(stream.Buffer, offset, signal, ctx, id).WithOggSync(stream)

	// We wrap the reader to prepend the OggHead if available.
	// This ensures NewOpusReader always sees the ID/Tag headers.
	// Use GetOggHead to read under the stream mutex — direct field
	// access races with StoreOggHead's locked write and could yield
	// a torn slice header.
	var finalReader io.Reader = reader
	if oggHead := stream.GetOggHead(); oggHead != nil {
		finalReader = io.MultiReader(bytes.NewReader(oggHead), reader)
	}

	opusReader, err := ogg.NewOpusReader(finalReader)
	if err != nil {
		logger.L.Errorf("Failed to initialize Ogg/Opus reader for WebRTC: %v", err)
		return
	}

	pacer := &SimplePacer{}
	logger.L.Infof("WebRTC: Beginning packet transmission for %s", stream.MountName)
	sentCount := 0

	// Default Opus duration is 20ms. If we detect drift, the pacer will self-correct.
	const defaultDuration = 20 * time.Millisecond

	for {
		packet, err := opusReader.ReadAudioPacket()
		if err != nil {
			if err != io.EOF && !errors.Is(err, context.Canceled) {
				logger.L.Errorf("Error reading Opus packet: %v", err)
			}
			return
		}

		// WebRTC expects 48kHz Opus samples.
		pacer.Pace(defaultDuration)
		if err := track.WriteSample(media.Sample{
			Data:     packet.Data,
			Duration: defaultDuration,
		}); err != nil {
			return
		}
		sentCount++
		if sentCount%100 == 0 {
			logger.L.Debugw("WebRTC: Sent 100 packets", "mount", stream.MountName)
		}
	}
}

// HandleWHEPOffer accepts a raw SDP offer from a browser (WHEP egress)
// and returns a raw SDP answer. Unlike HandleOffer (which only attaches
// an Opus track from the legacy /webrtc/offer path), this picks both
// audio and video tracks when the mount has them. The returned string
// is the SDP answer only — the HTTP layer wraps it with the WHEP
// headers (Location, 201 status).
func (wm *WebRTCManager) HandleWHEPOffer(mount, sdpOffer string) (string, error) {
	stream, ok := wm.relay.GetStream(mount)
	if !ok {
		return "", fmt.Errorf("stream not found")
	}

	pc, err := wm.api.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	})
	if err != nil {
		return "", err
	}

	// Audio: only attach when the source is Opus. Browsers don't decode
	// MP3 over WebRTC, so mounts without Opus get video-only playback.
	var audioTrack *webrtc.TrackLocalStaticSample
	ct := strings.ToLower(stream.ContentType)
	if strings.Contains(ct, "ogg") || strings.Contains(ct, "opus") {
		audioTrack, err = webrtc.NewTrackLocalStaticSample(
			webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
			"audio", "tinyice-audio")
		if err != nil {
			pc.Close()
			return "", err
		}
		if _, err = pc.AddTrack(audioTrack); err != nil {
			pc.Close()
			return "", err
		}
	}

	// Video: attach when a /video sibling stream exists with H.264.
	var videoTrack *webrtc.TrackLocalStaticSample
	var videoStream *Stream
	if vs, ok := wm.relay.GetStream(mount + "/video"); ok {
		videoStream = vs
		videoTrack, err = webrtc.NewTrackLocalStaticSample(
			webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
			"video", "tinyice-video")
		if err != nil {
			pc.Close()
			return "", err
		}
		if _, err = pc.AddTrack(videoTrack); err != nil {
			pc.Close()
			return "", err
		}
	}

	if audioTrack == nil && videoTrack == nil {
		pc.Close()
		return "", fmt.Errorf("mount has no Opus audio or H.264 video to egress")
	}

	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdpOffer}
	if err = pc.SetRemoteDescription(offer); err != nil {
		pc.Close()
		return "", err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		pc.Close()
		return "", err
	}
	gather := webrtc.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(answer); err != nil {
		pc.Close()
		return "", err
	}
	<-gather

	// Register OnConnectionStateChange ONCE per peer connection.
	// pion replaces the previous handler on each call, so when both
	// streamToTrack and streamVideoToTrack registered their own,
	// only the second won and the first goroutine never saw the
	// PC close — leaking one Stream subscription + goroutine per
	// WHEP listener disconnect. Cancel a shared context here that
	// both pump goroutines select on.
	pumpCtx, pumpCancel := context.WithCancel(context.Background())
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateClosed ||
			state == webrtc.PeerConnectionStateFailed ||
			state == webrtc.PeerConnectionStateDisconnected {
			pumpCancel()
			// Explicitly close the PC so pion releases its UDP
			// sockets / DTLS state machine. Without this, the FD
			// count creeps up over the course of many WHEP
			// listener disconnects.
			_ = pc.Close()
		}
	})

	if audioTrack != nil {
		go wm.streamToTrack(pumpCtx, pc, audioTrack, stream)
	}
	if videoTrack != nil {
		go wm.streamVideoToTrack(pumpCtx, pc, videoTrack, videoStream)
	}

	return pc.LocalDescription().SDP, nil
}

// streamVideoToTrack pulls per-frame Annex-B H.264 NALUs from the stream's
// FrameHub and hands them to the WebRTC sample track. TrackLocalStaticSample
// runs RTP packetization (FU-A fragmentation for NAL units larger than
// MTU) internally; we just need to feed it one full access unit per
// WriteSample call with a realistic Duration.
func (wm *WebRTCManager) streamVideoToTrack(ctx context.Context, pc *webrtc.PeerConnection, track *webrtc.TrackLocalStaticSample, stream *Stream) {
	if stream == nil || stream.Frames == nil {
		return
	}
	// ctx comes from HandleWHEPOffer's shared pump context — see
	// streamToTrack for why we don't register OnConnectionStateChange here.

	frames := stream.Frames.Subscribe(ctx)
	var lastDTS int64 = -1
	// Wait for the first keyframe so the decoder has a valid IDR +
	// SPS / PPS to start from; without this Chrome shows a black
	// frame until the next GOP cycles around.
	gotKeyframe := false

	for {
		select {
		case <-ctx.Done():
			return
		case f, ok := <-frames:
			if !ok {
				return
			}
			if !gotKeyframe {
				if !f.Keyframe {
					continue
				}
				gotKeyframe = true
			}
			dur := 33 * time.Millisecond // 30 fps sane default
			if lastDTS >= 0 {
				// Frame.DTS is in 90 kHz ticks. Accept anything from
				// 5 fps (18 000 ticks) down to 240 fps (375 ticks);
				// outside that window the source has stalled or sent
				// a bogus timestamp and falling back to the default
				// keeps RTP timestamps monotonic.
				delta := f.DTS - lastDTS
				if delta >= 375 && delta <= 18000 {
					dur = time.Duration(delta) * time.Second / 90000
				}
			}
			lastDTS = f.DTS
			if err := track.WriteSample(media.Sample{Data: f.Data, Duration: dur}); err != nil {
				return
			}
		}
	}
}

func (wm *WebRTCManager) DisconnectSource(mount string) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	pc, ok := wm.sources[mount]
	if !ok {
		return fmt.Errorf("no WebRTC source for mount %s", mount)
	}
	delete(wm.sources, mount)
	return pc.Close()
}
