package relay

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// PipelineManager manages pipelines, wrapping the existing Relay for backward compatibility.
type PipelineManager struct {
	relay     *Relay
	pipelines map[string]*Pipeline // key is mount
	mu        sync.RWMutex
}

// NewPipelineManager creates a PipelineManager wrapping an existing Relay.
func NewPipelineManager(r *Relay) *PipelineManager {
	return &PipelineManager{
		relay:     r,
		pipelines: make(map[string]*Pipeline),
	}
}

// Relay returns the underlying Relay for backward compatibility.
func (pm *PipelineManager) Relay() *Relay {
	return pm.relay
}

// GetOrCreatePipeline returns an existing pipeline or creates a new one.
// For backward compat, also creates the underlying Stream via Relay.
func (pm *PipelineManager) GetOrCreatePipeline(mount string) *Pipeline {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if p, ok := pm.pipelines[mount]; ok {
		return p
	}

	// Create underlying stream via Relay (backward compat)
	stream := pm.relay.GetOrCreateStream(mount)

	// Wrap in pipeline with an audio track. The codec hint we assign now
	// is provisional — a freshly-created Stream has no content-type yet,
	// so IsOgg() always returns false and we'd mislabel Opus sources as
	// MP3. Consumers that care (HLS / WebRTC output) should call
	// track.ResolveCodec() once data has started flowing.
	p := NewPipeline(mount)
	codec := "mp3"
	if stream.IsOgg() {
		codec = "opus"
	}
	p.AddTrack(NewAudioTrack(stream, codec))

	pm.pipelines[mount] = p
	return p
}

// GetPipeline returns a pipeline by mount, or nil if not found.
func (pm *PipelineManager) GetPipeline(mount string) (*Pipeline, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	p, ok := pm.pipelines[mount]
	return p, ok
}

// RemovePipeline removes a pipeline and its underlying stream.
func (pm *PipelineManager) RemovePipeline(mount string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if p, ok := pm.pipelines[mount]; ok {
		// Stop outputs
		for _, o := range p.Outputs {
			o.Stop()
		}
		delete(pm.pipelines, mount)
	}

	// Also remove from underlying relay
	pm.relay.RemoveStream(mount)
}

// GetOrCreateStream provides backward compatibility with the Relay interface.
// Returns the Stream from the pipeline's audio track.
func (pm *PipelineManager) GetOrCreateStream(mount string) *Stream {
	p := pm.GetOrCreatePipeline(mount)
	if t := p.GetAudioTrack(); t != nil {
		return t.Stream
	}
	// Shouldn't happen, but fallback
	return pm.relay.GetOrCreateStream(mount)
}

// GetStream provides backward compatibility with the Relay interface.
func (pm *PipelineManager) GetStream(mount string) (*Stream, bool) {
	return pm.relay.GetStream(mount)
}

// Snapshot returns stats for all pipelines.
func (pm *PipelineManager) Snapshot() []PipelineStats {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	stats := make([]PipelineStats, 0, len(pm.pipelines))
	for _, p := range pm.pipelines {
		stats = append(stats, p.Stats())
	}

	sort.Slice(stats, func(i, j int) bool {
		return stats[i].Created.After(stats[j].Created)
	})
	return stats
}

// PipelineCount returns the number of active pipelines.
func (pm *PipelineManager) PipelineCount() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return len(pm.pipelines)
}

// PipelineStats is a snapshot of a pipeline's state.
type PipelineStats struct {
	Mount     string
	TenantID  string
	Protocol  string // source protocol
	Tracks    []TrackStats
	Listeners int
	Created   time.Time
	Health    PipelineHealth
}

// TrackStats is a snapshot of a track's state.
type TrackStats struct {
	Type     string // "audio" or "video"
	Codec    string
	Bitrate  int
	BytesIn  int64
	BytesOut int64
}

// Stats returns a point-in-time snapshot of the pipeline.
func (p *Pipeline) Stats() PipelineStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	ps := PipelineStats{
		Mount:     p.Mount,
		TenantID:  p.TenantID,
		Listeners: 0,
		Created:   p.Created,
		Health:    p.Health,
	}

	if p.Source != nil {
		ps.Protocol = p.Source.Protocol()
	}

	for _, t := range p.Tracks {
		ts := TrackStats{
			Type:  t.Type.String(),
			Codec: t.Codec,
		}
		if t.Stream != nil {
			// BytesIn / BytesOut are mutated by Broadcast / serveStreamData
			// via atomic.AddInt64 — read them with atomic.LoadInt64 to
			// match. Direct field access would be a data race the race
			// detector would flag, and on weakly-ordered hardware could
			// occasionally see torn values.
			ts.BytesIn = atomic.LoadInt64(&t.Stream.BytesIn)
			ts.BytesOut = atomic.LoadInt64(&t.Stream.BytesOut)
			ps.Listeners += t.Stream.ListenersCount()
		}
		ts.Bitrate = t.Metadata.Bitrate
		ps.Tracks = append(ps.Tracks, ts)
	}

	return ps
}
