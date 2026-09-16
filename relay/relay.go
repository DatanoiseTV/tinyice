package relay

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Relay manages all active streams in the TinyIce server.
//
// The Relay is the central coordinator for all streaming activity. It maintains
// a collection of active streams (mount points), manages global statistics,
// and provides methods for stream creation, removal, and monitoring.
//
// Key responsibilities:
//   - Maintain mapping of mount points to Stream instances
//   - Track global bandwidth statistics
//   - Provide thread-safe access to streams
//   - Generate snapshots of all active streams
//
// Thread Safety:
// All methods are thread-safe and can be called concurrently from multiple
// goroutines. The Relay uses RWMutex for protecting the streams map.
//
// Example:
//
//	relay := NewRelay(true, historyManager)
//	stream := relay.GetOrCreateStream("/live")
//	stats := relay.Snapshot()
type Relay struct {
	Streams    map[string]*Stream // Active streams by mount point
	mu         sync.RWMutex       // Mutex protecting the streams map
	LowLatency bool               // Whether to optimize for low latency
	BytesIn    int64              // Global bytes received counter
	BytesOut   int64              // Global bytes sent counter
	History    *HistoryManager    // Optional history manager for statistics
}

func NewRelay(lowLatency bool, history *HistoryManager) *Relay {
	return &Relay{
		Streams:    make(map[string]*Stream),
		LowLatency: lowLatency,
		History:    history,
	}
}

// GetOrCreateStream returns an existing stream or creates a new one.
//
// This method provides thread-safe access to streams. If a stream with the given
// mount point already exists, it is returned. Otherwise, a new stream is created
// with default settings and added to the relay.
//
// Parameters:
//
//	mount - The mount point path (e.g., "/live", "/stream")
//
// Returns:
//
//	A pointer to the Stream instance (either existing or newly created)
//
// Behavior:
//   - Creates a new stream with 2MB circular buffer if mount doesn't exist
//   - Returns existing stream if mount already exists
//   - Initializes stream with default metadata
//   - Sets up Ogg page tracking (128 page offsets)
//
// Thread Safety:
//   - Uses exclusive lock (Lock) to protect streams map
//   - Safe to call from any goroutine
//
// Example:
//
//	stream := relay.GetOrCreateStream("/live")
//	stream.Broadcast(audioData, relay)
func (r *Relay) GetOrCreateStream(mount string) *Stream {
	return r.GetOrCreateStreamSized(mount, 512*1024)
}

// GetOrCreateStreamSized returns the existing stream for the mount (leaving
// its buffer untouched) or creates a new one with a buffer of `bufferSize`
// bytes. Use this for mounts that need larger buffers up front (e.g. the
// RTMP video sub-mount) so we never have to swap the buffer pointer under
// an already-subscribed listener.
func (r *Relay) GetOrCreateStreamSized(mount string, bufferSize int) *Stream {
	r.mu.Lock()
	defer r.mu.Unlock()

	if s, ok := r.Streams[mount]; ok {
		return s
	}
	if bufferSize <= 0 {
		bufferSize = 512 * 1024
	}

	s := &Stream{
		MountName:   mount,
		listeners:   make(map[string]chan struct{}),
		Buffer:      NewCircularBuffer(bufferSize),
		Started:     time.Now(),
		Name:        "Unnamed Stream",
		Description: "No description",
		Genre:       "N/A",
		Bitrate:     "N/A",
		ContentType: "audio/mpeg",
		IsOggStream: false,
		Enabled:     true,
		CurrentSong: "N/A",
		// Track last 2048 Ogg pages so Subscribe can find a page-boundary
		// alignment anywhere in the buffer — 128 covered barely 3–5 s at
		// typical Opus framing and truncated the actual burst delivered to
		// listeners ("underrun every 4–5 s on reconnect").
		PageOffsets: make([]int64, 2048),
		Frames:      NewFrameHub(),
	}
	r.Streams[mount] = s
	return s
}

// RemoveStream deletes a stream (e.g., when source disconnects)
func (r *Relay) RemoveStream(mount string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if s, ok := r.Streams[mount]; ok {
		// Terminate the source connection as well. Dropping the Stream
		// alone left the encoder connected and reading; its next write
		// simply recreated the mount, so an admin "kick source" did
		// nothing lasting.
		s.KickSource()
		s.Close()
		delete(r.Streams, mount)
	}
}

func (r *Relay) GetStreamVisibility(mount string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if s, ok := r.Streams[mount]; ok {
		return s.Visible
	}
	return false
}

// GetStream safely retrieves a stream
func (r *Relay) GetStream(mount string) (*Stream, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.Streams[mount]
	return s, ok
}

func (r *Relay) UpdateMetadata(mount, song string) {
	st := r.GetOrCreateStream(mount)
	st.SetCurrentSong(song, r)
}

// DisconnectAllListeners kicks all listeners from all active streams
func (r *Relay) DisconnectAllListeners() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.Streams {
		s.DisconnectListeners()
	}
}

// GetMetrics returns the current bandwidth usage since start
func (r *Relay) GetMetrics() (int64, int64) {
	return atomic.LoadInt64(&r.BytesIn), atomic.LoadInt64(&r.BytesOut)
}

// Snapshot returns a safe copy of all current streams.
//
// This method generates a point-in-time snapshot of all active streams, including
// their statistics, metadata, and current state. The snapshot is sorted by stream
// creation time with newest streams first.
//
// Returns:
//
//	A slice of StreamStats containing snapshots of all active streams
//
// Behavior:
//   - Takes a read lock (RLock) for thread-safe access
//   - Creates independent copies of all stream data
//   - Sorts results by creation time (newest first)
//   - Safe to use even if streams are modified concurrently
//
// Performance:
//   - O(n log n) due to sorting where n is number of streams
//   - Minimal locking - only holds read lock during data collection
//
// Example:
//
//	snapshot := relay.Snapshot()
//	for _, stats := range snapshot {
//	    fmt.Printf("Stream %s: %d listeners\n", stats.MountName, stats.ListenersCount)
//	}
func (r *Relay) Snapshot() []StreamStats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	streams := make([]StreamStats, 0, len(r.Streams))
	for _, s := range r.Streams {
		streams = append(streams, s.Snapshot())
	}

	// Sort by start time, newest first
	sort.Slice(streams, func(i, j int) bool {
		return streams[i].Started.After(streams[j].Started)
	})

	return streams
}
