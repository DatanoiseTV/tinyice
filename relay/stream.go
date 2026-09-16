package relay

import (
	"bytes"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Stream represents a single mount point (e.g., /stream, /live, /radio).
//
// A Stream is the fundamental unit of audio distribution in TinyIce. Each stream:
//   - Has its own circular buffer for audio data
//   - Manages multiple concurrent listeners
//   - Tracks statistics and metadata
//   - Supports different audio formats (MP3, Ogg/Opus, etc.)
//
// Lifecycle:
//   - Created when a source connects or via admin interface
//   - Buffer accumulates audio data from the source
//   - Listeners subscribe and receive data from the buffer
//   - Destroyed when source disconnects (or manually removed)
//
// Thread Safety:
//   - All fields are protected by the mu RWMutex
//   - Atomic operations are used for performance-critical counters
//   - Safe for concurrent access from multiple goroutines
//
// Performance Considerations:
//   - Each stream has a 2MB circular buffer by default
//   - Listener signal channels are buffered (size 1) to prevent blocking
//   - Ogg page tracking enables proper synchronization for Opus streams
//
// Example Usage:
//
//	stream := relay.GetOrCreateStream("/live")
//	stream.Broadcast(audioData, relayInstance)
//	offset, signal := stream.Subscribe("listener-id", 32*1024)
type Stream struct {
	// Basic stream information
	MountName   string // Mount point path (e.g., "/stream", "/live")
	ContentType string // MIME type (e.g., "audio/mpeg", "audio/ogg")
	Description string // Stream description for directory listings
	Genre       string // Music genre
	URL         string // Associated website URL
	Name        string // Display name
	Bitrate     string // Bitrate in kbps (e.g., "128", "192")

	// Timing and source information
	Started          time.Time // When the stream was created
	SourceIP         string    // IP address of the source client
	sourceCloser     func()    // terminates the source connection (see KickSource)
	LastDataReceived time.Time // Last time data was received from source

	// Stream state and visibility
	Enabled      bool // Whether the stream is accepting connections
	Public       bool // Whether to advertise in public directories
	Visible      bool // Whether to show in admin UI
	IsTranscoded bool // True if this stream is an output of a transcoder

	// Format-specific optimizations
	IsOggStream bool // Pre-calculated for speed (true for Ogg/Opus streams)

	// Audio data and listener management
	CurrentSong string // Currently playing song title

	// Statistics (atomic for performance)
	BytesIn      int64 // Total bytes received from source
	BytesOut     int64 // Total bytes sent to listeners
	BytesDropped int64 // Track total bytes dropped due to slow listeners

	// Ogg/Opus specific state for proper synchronization
	// These fields enable new listeners to start at proper page boundaries
	OggHead         []byte  // Store Ogg headers for Opus/Ogg streams
	OggHeaderOffset int64   // Absolute buffer offset where headers end
	// VideoHeaders is the Annex-B SPS + PPS bytes for an H.264 video
	// stream. Listeners that tune in mid-GOP need these injected before
	// the first IDR — otherwise they get "non-existing PPS referenced"
	// / "no frame" errors from ffmpeg until the next keyframe cycles.
	VideoHeaders []byte

	// Frames fans out per-frame (audio + video) records with PTS on the
	// MPEG 90 kHz clock to consumers like the HLS segmenter. It's used
	// alongside the byte-level Buffer: raw Icecast-style listeners read
	// bytes, frame-level consumers subscribe to the hub. nil on streams
	// that never see a frame-level producer (e.g. relay pull).
	Frames *FrameHub

	// MinListenerOffset is a hard lower bound for listener reads of the
	// byte Buffer. Used when the source changes its codec config mid-
	// stream (e.g. OBS reconfigures and a new AVCDecoderConfigurationRecord
	// arrives) — any bytes written before that point were encoded with
	// parameters the new SPS/PPS no longer describes, so the decoder can
	// only crash on them. Subscribe() bumps a new listener's start
	// offset up to this value.
	MinListenerOffset int64
	LastPageOffset  int64   // Absolute offset of the last valid Ogg page start
	PageOffsets     []int64 // Circular list of last ~100 page starts
	PageIndex       int     // Index for managing PageOffsets circular list

	// FlushGen is incremented every time something invalidates the
	// in-buffer audio for currently-subscribed listeners (e.g. a
	// transcoder encoder restart after the upstream source flapped).
	// Listeners observe the counter on every signal iteration and snap
	// their offset to MinListenerOffset (set to the current Head at
	// flush time) so they don't replay stale bytes from before the gap.
	flushGen atomic.Uint64

	// Core streaming infrastructure
	Buffer    *CircularBuffer          // Audio data buffer (typically 2MB)
	listeners         map[string]chan struct{} // Signal channels for connected listeners
	internalListeners map[string]struct{}      // subset of listener ids that should NOT count toward ListenersCount (e.g. transcoders subscribing to their input)
	mu     sync.RWMutex             // Mutex protecting all fields
	closed int32                    // Atomic flag: 1 = stream closed

	// Video metrics sliding window, protected by mu. Refreshed by
	// RecordVideoSample on every frame and exposed via
	// VideoMetricsSnapshot for the dashboard.
	videoWidth        int
	videoHeight       int
	videoFrameStamps  []time.Time
	videoFrameBytes   []int
	videoLastKeyframe time.Time
	videoGOPs         []float64

	// HLS / WHEP viewer tracking. Indexed by client IP; entries
	// expire after viewerTTL since the last poll/keepalive. The
	// raw Icecast listener counter on listeners[] doesn't include
	// browser-side video viewers because they fetch segments
	// rather than holding a long-lived listener connection.
	viewers []viewerEntry
}

// IsOgg returns true if the stream is Ogg-based (Ogg/Vorbis, Ogg/Opus, etc).
//
// This method performs a case-insensitive check of the ContentType field to determine
// if the stream uses Ogg container format. This is important for proper handling of
// Opus streams which require special synchronization.
//
// Returns:
//
//	true if the stream content type contains "ogg" or "opus"
//	false otherwise
//
// Performance:
//   - O(1) operation (simple string contains check)
//   - Thread-safe (reads immutable field)
//
// Example:
//
//	if stream.IsOgg() {
//	    // Handle Ogg/Opus specific logic
//	}
func (s *Stream) IsOgg() bool {
	// ContentType is written under s.mu by every ingest (source connect,
	// AutoDJ track start, WebRTC OnTrack); read it the same way. No
	// caller holds s.mu when calling this (audited), so the RLock here
	// is not recursive.
	s.mu.RLock()
	ct := strings.ToLower(s.ContentType)
	s.mu.RUnlock()
	return strings.Contains(ct, "ogg") || strings.Contains(ct, "opus")
}

// SetSourceIP records the source address under the stream mutex so readers
// (Snapshot, listener handlers, status handlers) see a consistent value.
// Use TryClaimSource for the Icecast SOURCE / RTMP / SRT paths where two
// concurrent clients could race for the same mount; SetSourceIP is fine
// for paths that own their stream by construction (relay pull, WebRTC
// ingest, web-audio publisher) and just want a label for the dashboard.
func (s *Stream) SetSourceIP(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SourceIP = ip
}

// TryClaimSource atomically takes source ownership of this stream if it
// has none, returning true. If the mount already has an active source
// (SourceIP non-empty) it returns false WITHOUT changing state — the
// caller MUST reject the new connection. Pair with ReleaseSource in a
// defer.
//
// Without this, two concurrent SOURCE clients hitting the same mount
// both pass auth, both call SetSourceIP, and both spawn read loops that
// interleave bytes into the same ring buffer. Listeners then see two
// MP3 / Ogg streams shredded together byte-by-byte and every decoder
// errors out on missing frame headers. Observed in production with
// robodj retrying a connect 3× within 25 ms on every restart.
func (s *Stream) TryClaimSource(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.SourceIP != "" {
		return false
	}
	s.SourceIP = ip
	return true
}

// SetSourceCloser registers how to terminate the current source's
// connection. RemoveStream and the admin "kick source" action only ever
// dropped the Stream object and the listeners: the encoder's connection
// stayed open, kept reading, and a fresh Stream was created under it on
// the next write, so a kicked source carried on broadcasting. The ingest
// handler registers a closer when it takes ownership and clears it on
// exit.
func (s *Stream) SetSourceCloser(fn func()) {
	s.mu.Lock()
	s.sourceCloser = fn
	s.mu.Unlock()
}

// KickSource terminates the source connection if one is registered.
// Safe to call with no source.
func (s *Stream) KickSource() {
	s.mu.Lock()
	fn := s.sourceCloser
	s.sourceCloser = nil
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// ClaimSourceIfFree claims the mount for label unless a DIFFERENT source
// already holds it. Returns false when someone else owns the mount, so an
// in-process producer (the AutoDJ) can decline to write rather than
// interleaving its bytes with a live encoder's into the same buffer.
func (s *Stream) ClaimSourceIfFree(label string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.SourceIP != "" && s.SourceIP != label {
		return false
	}
	s.SourceIP = label
	return true
}

// ReleaseSource clears the source claim so the next TryClaimSource on
// this stream succeeds. Idempotent.
func (s *Stream) ReleaseSource() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SourceIP = ""
}

// GetSourceIP returns the current source's address under the stream
// mutex, mirroring SetSourceIP. Empty string means no live source.
func (s *Stream) GetSourceIP() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.SourceIP
}

// StoreVideoHeaders records the Annex-B SPS and PPS bytes for an H.264
// stream. The listener handler prepends these (and seeks to the most
// recent keyframe) so mpv / ffmpeg / a browser's `<video>` decoder can
// start without waiting for the next IDR.
func (s *Stream) StoreVideoHeaders(headers []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.VideoHeaders = headers
}

// GetMinListenerOffset returns MinListenerOffset under the stream mutex.
func (s *Stream) GetMinListenerOffset() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.MinListenerOffset
}

// CheckpointAtHead marks the current Buffer.Head as the new minimum
// valid offset for listener reads, and clears any previously recorded
// keyframe offsets (they pointed at bytes encoded under the old config).
// Called by ingest paths when they observe a codec-parameter change
// mid-stream — OBS restarting its encoder, resolution switch, etc.
func (s *Stream) CheckpointAtHead() {
	if s.Buffer == nil {
		return
	}
	head := s.Buffer.HeadOffset()
	s.mu.Lock()
	s.MinListenerOffset = head
	s.mu.Unlock()
	s.Buffer.ResetKeyframes()
}

// FlushAtHead behaves like CheckpointAtHead but also bumps the flush
// generation and wakes every subscribed listener so they observe the
// new minimum offset on their next read iteration. Use when the bytes
// already in the buffer are no longer relevant to listeners and we
// want them to jump forward to the live edge — e.g. the upstream
// source flapped and the encoder is about to resume writing a fresh
// run of audio after a multi-second gap.
func (s *Stream) FlushAtHead() {
	if s.Buffer == nil {
		return
	}
	head := s.Buffer.HeadOffset()
	s.mu.Lock()
	s.MinListenerOffset = head
	chans := make([]chan struct{}, 0, len(s.listeners))
	for _, ch := range s.listeners {
		chans = append(chans, ch)
	}
	s.mu.Unlock()
	s.Buffer.ResetKeyframes()
	s.flushGen.Add(1)
	// Non-blocking wake of every listener so they observe the bump
	// without waiting for the next Broadcast. recover() inside the
	// closure contains a send-on-closed panic if a listener tears
	// down at exactly this instant.
	for _, ch := range chans {
		func(ch chan struct{}) {
			defer func() { _ = recover() }()
			select {
			case ch <- struct{}{}:
			default:
			}
		}(ch)
	}
}

// FlushGen returns the current flush generation counter. Listener
// handlers read this once at subscribe time and re-read on every
// signal iteration; a change means FlushAtHead was called and the
// listener should snap to MinListenerOffset.
func (s *Stream) FlushGen() uint64 {
	return s.flushGen.Load()
}

// BeginSession marks the start of a fresh producer session on this stream
// (transcoder restart after a source flap, WebRTC source reconnect, AutoDJ
// re-arming after a stop, ...). It is the Ogg-aware superset of FlushAtHead:
//
//   - bytes already in the buffer are declared stale (MinListenerOffset
//     snaps to Buffer.Head, flushGen bumps, every listener is woken so
//     they jump to the live edge);
//   - the Ogg page-tracking state is wiped (PageOffsets, LastPageOffset,
//     PageIndex, OggHead, OggHeaderOffset all reset). Without this wipe a
//     new subscriber that arrived between the restart and the next
//     StoreOggHead would align to a tracked page offset from the previous
//     Ogg serial and decode garbage; with it, Subscribe falls through to
//     the live edge and the listener picks up the next BOS page cleanly.
//
// Producers that re-initialise their encoder (new Ogg serial, new ID/Tag
// headers) should call this once, before writing the new run.
func (s *Stream) BeginSession() {
	if s.Buffer == nil {
		return
	}
	head := s.Buffer.HeadOffset()
	s.mu.Lock()
	s.MinListenerOffset = head
	s.OggHead = nil
	s.OggHeaderOffset = head
	s.LastPageOffset = 0
	s.PageIndex = 0
	for i := range s.PageOffsets {
		s.PageOffsets[i] = 0
	}
	chans := make([]chan struct{}, 0, len(s.listeners))
	for _, ch := range s.listeners {
		chans = append(chans, ch)
	}
	s.mu.Unlock()
	s.Buffer.ResetKeyframes()
	s.flushGen.Add(1)
	for _, ch := range chans {
		func(ch chan struct{}) {
			defer func() { _ = recover() }()
			select {
			case ch <- struct{}{}:
			default:
			}
		}(ch)
	}
}

// VideoInfo returns the flags that the HTTP listener path needs to
// decide whether a stream is video and what headers to prepend, under
// the stream mutex. Callers outside the relay package can't read the
// unexported mu directly.
func (s *Stream) VideoInfo() (isH264 bool, headers []byte) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	isH264 = strings.Contains(strings.ToLower(s.ContentType), "h264")
	if len(s.VideoHeaders) > 0 {
		headers = append(headers, s.VideoHeaders...)
	}
	return
}

// VideoMetrics is a point-in-time snapshot of codec metrics for a video
// track. Zeroed fields mean "unknown" (either the stream isn't video or
// we haven't sampled yet). Consumers should treat all fields as
// informational — they refresh every ~1 s from the ingest path.
type VideoMetrics struct {
	Width        int     // luma pixels, 0 when SPS unavailable
	Height       int     // luma pixels
	FPS          float64 // rolling 1 s frame count
	GOPSeconds   float64 // observed keyframe interval (avg of last few)
	BitrateKbps  int     // rolling 1 s byte rate, × 8 / 1000
	LastKeyframe time.Time
}

// RecordVideoSample feeds a single frame's observations into the stream's
// metrics window. width/height are 0 when unchanged. bytes is the frame's
// Annex-B byte count. keyframe is true for IDRs. The stream keeps a
// 1-second sliding sum of frames and bytes, plus a short history of
// keyframe deltas for the GOP calculation — all protected by mu.
func (s *Stream) RecordVideoSample(width, height, bytes int, keyframe bool, ts time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if width > 0 {
		s.videoWidth = width
	}
	if height > 0 {
		s.videoHeight = height
	}
	// Trim samples older than 1 s, then append.
	cutoff := ts.Add(-time.Second)
	for i, f := range s.videoFrameStamps {
		if !f.Before(cutoff) {
			s.videoFrameStamps = s.videoFrameStamps[i:]
			s.videoFrameBytes = s.videoFrameBytes[i:]
			break
		}
		if i == len(s.videoFrameStamps)-1 {
			s.videoFrameStamps = s.videoFrameStamps[:0]
			s.videoFrameBytes = s.videoFrameBytes[:0]
		}
	}
	s.videoFrameStamps = append(s.videoFrameStamps, ts)
	s.videoFrameBytes = append(s.videoFrameBytes, bytes)

	if keyframe {
		if !s.videoLastKeyframe.IsZero() {
			d := ts.Sub(s.videoLastKeyframe).Seconds()
			if d > 0 && d < 60 {
				s.videoGOPs = append(s.videoGOPs, d)
				if len(s.videoGOPs) > 8 {
					s.videoGOPs = s.videoGOPs[len(s.videoGOPs)-8:]
				}
			}
		}
		s.videoLastKeyframe = ts
	}
}

// VideoMetricsSnapshot returns the current video metrics under the
// stream mutex. Safe to call from any goroutine.
func (s *Stream) VideoMetricsSnapshot() VideoMetrics {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m := VideoMetrics{
		Width:        s.videoWidth,
		Height:       s.videoHeight,
		LastKeyframe: s.videoLastKeyframe,
	}
	if n := len(s.videoFrameStamps); n > 1 {
		span := s.videoFrameStamps[n-1].Sub(s.videoFrameStamps[0]).Seconds()
		if span > 0 {
			m.FPS = float64(n-1) / span
			var total int
			for _, b := range s.videoFrameBytes {
				total += b
			}
			m.BitrateKbps = int(float64(total) * 8 / 1000 / span)
		}
	}
	if n := len(s.videoGOPs); n > 0 {
		var sum float64
		for _, d := range s.videoGOPs {
			sum += d
		}
		m.GOPSeconds = sum / float64(n)
	}
	return m
}

// RecordViewer marks an HLS / WHEP viewer as active for `mount` from `ip`
// at `now`. Entries older than viewerTTL are evicted on each call.
// ViewerCount returns the unique-IP count over that same window — this
// is what we surface as "viewers" in the UI for video mounts (which
// don't go through the byte-streaming listener handler).
const viewerTTL = 30 * time.Second

type viewerEntry struct {
	ip   string
	last time.Time
}

func (s *Stream) RecordViewer(ip string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := now.Add(-viewerTTL)
	kept := s.viewers[:0]
	found := false
	for _, v := range s.viewers {
		if v.last.Before(cutoff) {
			continue
		}
		if v.ip == ip {
			v.last = now
			found = true
		}
		kept = append(kept, v)
	}
	if !found {
		kept = append(kept, viewerEntry{ip: ip, last: now})
	}
	s.viewers = kept
}

func (s *Stream) ViewerCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cutoff := time.Now().Add(-viewerTTL)
	n := 0
	for _, v := range s.viewers {
		if !v.last.Before(cutoff) {
			n++
		}
	}
	return n
}

// StoreOggHead atomically records the Ogg header bytes and the buffer offset
// at which audio data begins. Late-joining listeners prepend OggHead to their
// output and skip buffer content before OggHeaderOffset so they get a complete
// set of codec setup pages.
func (s *Stream) StoreOggHead(head []byte, audioStartOffset int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.OggHead = head
	s.OggHeaderOffset = audioStartOffset
	// Mark the stream as Ogg so the listener path applies Ogg-specific sync.
	s.IsOggStream = true
}

// GetOggHead returns a snapshot of the captured Ogg header bytes (or nil).
// Callers MUST use this rather than reading s.OggHead directly: StoreOggHead
// writes the slice header (ptr+len+cap) under s.mu, and a concurrent
// unsynchronised read can observe a torn slice header (updated len with
// stale ptr) per the Go memory model. The returned slice is safe to write
// to the wire — we don't share the backing array.
func (s *Stream) GetOggHead() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.OggHead
}

// GetLastDataReceived returns the timestamp of the last byte received from
// the source, under the stream mutex. time.Time is a 24-byte struct on
// amd64 — an unsynchronised read against Broadcast's
// `s.LastDataReceived = time.Now()` write can tear the value across the
// monotonic + wall + ext fields and yield a meaningless time. Callers
// outside the relay package can't see s.mu directly, so use this.
func (s *Stream) GetLastDataReceived() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.LastDataReceived
}

// Close closes all listeners on the stream and cleans up resources.
//
// This method should be called when a stream is being removed or when the source
// disconnects. It ensures all listener goroutines can exit gracefully by closing
// their signal channels.
//
// Behavior:
//   - Locks the stream mutex for the entire operation
//   - Closes all listener signal channels
//   - Removes all listeners from the listeners map
//   - Does NOT remove the stream from the Relay
//
// Thread Safety:
//   - Safe to call from any goroutine
//   - Uses exclusive lock (Lock) to prevent concurrent modifications
//
// Example:
//
//	stream.Close()
//	relay.RemoveStream("/live")
func (s *Stream) Close() {
	atomic.StoreInt32(&s.closed, 1)
	// Tear down the frame hub so HLS / other frame subscribers exit
	// their select loops cleanly.
	if s.Frames != nil {
		s.Frames.Close()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ch := range s.listeners {
		close(ch)
		delete(s.listeners, id)
	}
}

// DisconnectListeners specifically kicks all listeners without closing the stream
func (s *Stream) DisconnectListeners() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ch := range s.listeners {
		close(ch)
		delete(s.listeners, id)
	}
}

// Broadcast sends data to all listeners via the shared circular buffer.
//
// This is the core method that distributes audio data from sources to all connected
// listeners. It handles writing to the circular buffer, updating statistics, tracking
// Ogg page boundaries (for Opus streams), and notifying listeners of new data.
//
// Parameters:
//
//	data  - The audio data to broadcast to all listeners
//	relay - The relay instance for updating global statistics
//
// Behavior:
//   - Updates LastDataReceived timestamp
//   - Updates bytes-in statistics (both stream and global)
//   - For Ogg streams: tracks "OggS" page boundaries for synchronization
//   - Writes data to the circular buffer
//   - Signals all listeners via their channels (non-blocking)
//
// Performance:
//   - Locks the stream mutex for the entire operation
//   - Uses atomic operations for statistics to avoid lock contention
//   - Non-blocking listener notification (select with default case)
//   - O(n) where n is number of listeners (for signaling)
//
// Thread Safety:
//   - Safe to call from any goroutine
//   - Uses exclusive lock (Lock) to protect stream state
//
// Example:
//
//	stream.Broadcast(audioChunk, relayInstance)
func (s *Stream) Broadcast(data []byte, relay *Relay) {
	if atomic.LoadInt32(&s.closed) == 1 {
		return
	}

	// 1. Write phase — hold the write lock only for the bookkeeping
	//    mutations (timestamp, page-offset table, buffer write). The
	//    listener-signal fan-out is the expensive part and only
	//    needs concurrent-safe channel sends, so we don't keep the
	//    write lock for it.
	//
	//    Rationale: with N listeners on a busy mount, holding the
	//    WRITE lock during the channel-send loop made every other
	//    goroutine touching the stream — listener Reads, SSE
	//    Snapshot, GeoTracker.Add/Remove — queue behind it. At ~50
	//    broadcasts/sec across multiple mounts the lock saturated
	//    and unrelated HTTPS handlers (login, /healthz, kiosk SSE)
	//    visibly hung. Channel sends are inherently safe across
	//    goroutines so we capture the listener slice under RLock
	//    and signal outside any lock.
	s.mu.Lock()
	s.LastDataReceived = time.Now()
	atomic.AddInt64(&relay.BytesIn, int64(len(data)))
	atomic.AddInt64(&s.BytesIn, int64(len(data)))
	if s.IsOggStream {
		// Track Ogg page boundaries for alignment so new Opus /
		// Vorbis listeners start at a proper page edge. bytes.IndexByte
		// skips ahead between candidate 'O' bytes — for a 4-8 KiB
		// Broadcast call with few O bytes (Opus payload) this is
		// ~16x fewer comparisons than a byte-by-byte scan.
		//
		// We MUST validate the byte that follows "OggS" (the Ogg
		// stream-structure version) before recording an offset.
		// 0x4F 0x67 0x67 0x53 also occurs inside Opus packet
		// payloads — without the version-byte check those false
		// positives end up in PageOffsets and cause new listeners
		// to align mid-packet, which manifests as glitched /
		// "shuffled" audio. Real Ogg pages always have version 0
		// at byte+4. If the magic lands in the last 4 bytes of
		// this chunk we can't peek at the version yet; skip it,
		// the next Broadcast call will see the full header.
		i := 0
		for i <= len(data)-5 {
			j := bytes.IndexByte(data[i:len(data)-4], 'O')
			if j < 0 {
				break
			}
			i += j
			if data[i+1] == 'g' && data[i+2] == 'g' && data[i+3] == 'S' && data[i+4] == 0 {
				offset := s.Buffer.Head + int64(i)
				s.LastPageOffset = offset
				s.PageOffsets[s.PageIndex%len(s.PageOffsets)] = offset
				s.PageIndex++
				i += 5
			} else {
				i++
			}
		}
	}
	s.Buffer.Write(data)
	s.mu.Unlock()

	// 2. Signal phase — copy the listener channel slice under
	//    RLock then drop the lock before sending. Subscribe /
	//    Unsubscribe holds the write lock to mutate the map; an
	//    Unsubscribe racing with a Broadcast can leave us holding a
	//    reference to a channel that has been closed in the meantime.
	//    A select-default does NOT protect against that — sending on
	//    a closed channel panics regardless of select. The recover
	//    inside the per-channel anon-func contains the panic to one
	//    listener so a subscriber tearing down at the wrong moment
	//    can't crash the source / encoder goroutine that called us.
	s.mu.RLock()
	chans := make([]chan struct{}, 0, len(s.listeners))
	for _, ch := range s.listeners {
		chans = append(chans, ch)
	}
	s.mu.RUnlock()
	for _, ch := range chans {
		func(ch chan struct{}) {
			defer func() { _ = recover() }()
			select {
			case ch <- struct{}{}:
				// signalled
			default:
				// already pending; slow listener, skip
			}
		}(ch)
	}
}

// Subscribe adds a listener and returns its starting offset and a signal channel.
//
// This method registers a new listener for the stream and determines the optimal
// starting position in the buffer. For Ogg/Opus streams, it ensures the listener
// starts at a proper Ogg page boundary to avoid corruption.
//
// Parameters:
//
//	id        - Unique identifier for this listener
//	burstSize - Number of bytes to rewind from current position for instant playback
//
// Returns:
//
//	int64          - Absolute offset in buffer to start reading from
//	chan struct{}  - Signal channel that will be closed when stream ends or listener is removed
//
// Behavior:
//   - Creates a buffered signal channel (size 1) for the listener
//   - Calculates starting position as (current head - burstSize)
//   - For Ogg streams: aligns to nearest valid Ogg page boundary
//   - Ensures position is within valid buffer range
//   - Adds listener to the listeners map
//
// Ogg Synchronization:
//
//	The method implements sophisticated Ogg page boundary detection to ensure
//	Opus listeners start at valid page boundaries. This prevents audio corruption
//	and ensures proper decoding from the first packet.
//
// Thread Safety:
//   - Safe to call from any goroutine
//   - Uses exclusive lock (Lock) to protect stream state
//
// Performance:
//   - O(n) where n is number of tracked page offsets (typically ~100)
//   - Lock held for entire operation
//
// Example:
//
//	offset, signal := stream.Subscribe("listener-123", 32*1024)
//	reader := NewStreamReader(stream.Buffer, offset, signal, ctx, "listener-123")
// SubscribeInternal is like Subscribe but flags the listener as internal
// (e.g. a transcoder reading the input mount). Internal listeners are
// excluded from ListenersCount and from the listener-count history
// so the dashboard reflects real human listeners.
func (s *Stream) SubscribeInternal(id string, burstSize int) (int64, chan struct{}) {
	offset, sig := s.Subscribe(id, burstSize)
	s.mu.Lock()
	if s.internalListeners == nil { s.internalListeners = make(map[string]struct{}) }
	s.internalListeners[id] = struct{}{}
	s.mu.Unlock()
	return offset, sig
}

func (s *Stream) Subscribe(id string, burstSize int) (int64, chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Create buffered signal channel for this listener
	ch := make(chan struct{}, 1)
	if atomic.LoadInt32(&s.closed) == 1 {
		// The stream is already closed: Close() has walked the listener
		// map and will never do so again, so registering here would leave
		// a subscriber parked on a channel nobody ever closes or signals.
		// Hand back a closed channel so the reader sees EOF immediately,
		// which is what every caller does with a closed signal.
		close(ch)
		return s.Buffer.Head, ch
	}
	s.listeners[id] = ch

	// Freshness gate: if the producer has been silent for >2s, the bytes
	// currently in the buffer are stale (from a previous song / a prior
	// source / a flap). Starting the listener at Head-burstSize would
	// replay several seconds of that old audio before the source resumed.
	// Start at the live edge instead; the listener gets immediate audio
	// as soon as the next Broadcast lands. 2s is large enough to cover
	// normal between-frame jitter but small enough that pauses between
	// songs / brief flaps don't replay.
	if !s.LastDataReceived.IsZero() && time.Since(s.LastDataReceived) > 2*time.Second {
		return s.Buffer.Head, ch
	}

	// Start at current head minus burst size for instant playback
	// This gives the listener immediate audio data instead of waiting for new data
	start := s.Buffer.Head - int64(burstSize)
	if start < 0 {
		start = 0
	}

	// A caller asking for no burst wants the LIVE EDGE (WebRTC and WHEP
	// viewers do this; they run their own page sync from there). The
	// alignment below walks BACKWARDS to the oldest tracked page when it
	// can't find one at or after `start`, which for start == Head is
	// always — so a burst-free subscriber was handed the oldest audio in
	// the buffer and stayed seconds behind live for the whole session.
	if burstSize <= 0 {
		return s.Buffer.Head, ch
	}

	// For Ogg/Opus, align to the oldest known page boundary within the valid buffer range
	// This is crucial for proper Opus decoding - listeners MUST start at page boundaries
	if s.IsOggStream {
		validStart := s.Buffer.Head - s.Buffer.Size
		if validStart < 0 {
			validStart = 0
		}

		// If we have an OggHead persistent storage, we want to start reading
		// from the Buffer AFTER the initial headers to avoid duplicates.
		if s.OggHeaderOffset > start {
			start = s.OggHeaderOffset
		}

		if start < validStart {
			start = validStart
		}

		// Prefer the oldest tracked page boundary that is still >= start and
		// within the valid buffer range — this maximises the burst delivered
		// to the listener. Falling back to LastPageOffset (newest page) used
		// to cap the burst at near-zero whenever PageOffsets didn't happen
		// to cover far enough back, which manifested as "client underrun
		// every few seconds on reconnect".
		bestAlign := int64(-1)
		oldestValid := int64(-1)
		for _, po := range s.PageOffsets {
			if po < validStart || po == 0 {
				continue
			}
			if oldestValid < 0 || po < oldestValid {
				oldestValid = po
			}
			if po >= start && (bestAlign < 0 || po < bestAlign) {
				bestAlign = po
			}
		}
		switch {
		case bestAlign >= 0:
			start = bestAlign
		case oldestValid >= 0:
			// Intended start is older than any tracked page — use the
			// oldest we have, which is still newer than the buffer tail.
			start = oldestValid
		case s.LastPageOffset >= validStart && s.LastPageOffset > 0:
			// Absolute last resort: at least align on the most recent page.
			start = s.LastPageOffset
		default:
			// Nothing tracked yet — fall back to burst-based offset.
			start = s.Buffer.Head - int64(burstSize)
			if start < validStart {
				start = validStart
			}
			if start < 0 {
				start = 0
			}
		}
	}

	// Ensure we don't go back further than the buffer allows
	if s.Buffer.Head-start > s.Buffer.Size {
		start = s.Buffer.Head - s.Buffer.Size
	}

	return start, ch
}

// SubscribeSafe is like Subscribe but returns false if the stream is already closed.
// This prevents adding listeners to a closed stream.
func (s *Stream) SubscribeSafe(id string, burstSize int) (int64, chan struct{}, bool) {
	if atomic.LoadInt32(&s.closed) == 1 {
		return 0, nil, false
	}
	offset, ch := s.Subscribe(id, burstSize)
	return offset, ch, true
}

// Unsubscribe removes a listener.
//
// Every caller in this codebase invokes Unsubscribe via `defer` from
// the same goroutine that subscribed — i.e. a self-exit path. There is
// no caller that needs to wake the listener from another goroutine via
// this entry point. Closing the signal channel under the lock used to
// be done "for safety", but combined with the lock-released signal
// fan-out in Broadcast it created a real send-on-closed-channel panic
// in production (4 occurrences in r4dio's journal, 7 days). We don't
// close the channel here — only Stream.Close / DisconnectListeners
// (the external kick paths) close, and those are already serialised
// against Broadcast's snapshot via the stream mutex.
//
// The orphaned channel is GC'd as soon as the caller drops its
// reference; it carries no payload and never blocks any goroutine.
func (s *Stream) Unsubscribe(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.internalListeners != nil {
		delete(s.internalListeners, id)
	}
	delete(s.listeners, id)
}

// UpdateMetadata updates stream info
func (s *Stream) UpdateMetadata(name, desc, genre, url, bitrate, contentType string, public, visible bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	if name != "" && s.Name != name {
		s.Name = name
		changed = true
	}
	if desc != "" && s.Description != desc {
		s.Description = desc
		changed = true
	}
	if genre != "" && s.Genre != genre {
		s.Genre = genre
		changed = true
	}
	if url != "" && s.URL != url {
		s.URL = url
		changed = true
	}
	if bitrate != "" && s.Bitrate != bitrate {
		s.Bitrate = bitrate
		changed = true
	}
	if contentType != "" && s.ContentType != contentType {
		s.ContentType = contentType
		ct := strings.ToLower(contentType)
		s.IsOggStream = strings.Contains(ct, "ogg") || strings.Contains(ct, "opus")
		changed = true
	}
	if s.Public != public {
		s.Public = public
		changed = true
	}
	if s.Visible != visible {
		s.Visible = visible
		changed = true
	}
	return changed
}

// SetCurrentSong updates the current song info thread-safely.
//
// History.Add issues several SQLite/GORM queries; running them under
// s.mu would freeze every listener, broadcast, snapshot and metadata
// reader for the duration of those queries. Capture the diff under the
// lock, release it, then write to history outside.
func (s *Stream) SetCurrentSong(song string, relay *Relay) {
	s.mu.Lock()
	changed := s.CurrentSong != song
	if changed {
		s.CurrentSong = song
	}
	mount := s.MountName
	s.mu.Unlock()

	if changed && relay != nil && relay.History != nil {
		relay.History.Add(mount, song)
	}
}

// GetCurrentSong returns the current song info thread-safely
func (s *Stream) GetCurrentSong() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.CurrentSong
}

// SetVisible updates the visibility of the stream thread-safely
func (s *Stream) SetVisible(visible bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Visible = visible
}

// ListenersCount returns the number of active *real* listeners (i.e.
// internal subscribers like transcoders are excluded).
func (s *Stream) ListenersCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.listeners) - len(s.internalListeners)
}

// GetStartedTime returns the stream start time (implements TimeProvider interface)
func (s *Stream) GetStartedTime() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Started
}
