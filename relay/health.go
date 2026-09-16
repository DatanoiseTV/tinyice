package relay

import (
	"context"
	"sync"
	"time"

	"github.com/DatanoiseTV/tinyice/logger"
)

type HealthStatus int

const (
	StatusHealthy HealthStatus = iota
	StatusDegraded
	StatusDead
)

func (h HealthStatus) String() string {
	switch h {
	case StatusHealthy:
		return "healthy"
	case StatusDegraded:
		return "degraded"
	case StatusDead:
		return "dead"
	default:
		return "unknown"
	}
}

func calculateHealthStatus(lastDataReceived time.Time) HealthStatus {
	if lastDataReceived.IsZero() {
		return StatusDead
	}
	silence := time.Since(lastDataReceived)
	if silence > 30*time.Second {
		return StatusDead
	}
	if silence > 5*time.Second {
		return StatusDegraded
	}
	return StatusHealthy
}

type StreamHealthEvent struct {
	Mount     string
	OldStatus HealthStatus
	NewStatus HealthStatus
	Timestamp time.Time
}

type HealthMonitor struct {
	relay          *Relay
	interval       time.Duration
	deadTimeout    time.Duration
	lastStatus     map[string]HealthStatus
	mu             sync.Mutex
	onEvent        func(StreamHealthEvent)
	autoRemoveDead bool
}

func NewHealthMonitor(r *Relay) *HealthMonitor {
	return &HealthMonitor{
		relay:          r,
		interval:       5 * time.Second,
		deadTimeout:    60 * time.Second,
		lastStatus:     make(map[string]HealthStatus),
		autoRemoveDead: false,
	}
}

func (hm *HealthMonitor) WithAutoRemove(timeout time.Duration) *HealthMonitor {
	hm.autoRemoveDead = true
	hm.deadTimeout = timeout
	return hm
}

func (hm *HealthMonitor) OnEvent(fn func(StreamHealthEvent)) {
	hm.onEvent = fn
}

func (hm *HealthMonitor) Start(ctx context.Context) {
	ticker := time.NewTicker(hm.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hm.check()
		}
	}
}

func (hm *HealthMonitor) check() {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	snapshots := hm.relay.Snapshot()
	activeStreams := make(map[string]bool)

	for _, ss := range snapshots {
		activeStreams[ss.MountName] = true

		stream, ok := hm.relay.GetStream(ss.MountName)
		if !ok {
			continue
		}

		stream.mu.RLock()
		lastData := stream.LastDataReceived
		started := stream.Started
		stream.mu.RUnlock()

		checkTime := lastData
		if checkTime.IsZero() {
			checkTime = started
		}

		newStatus := calculateHealthStatus(checkTime)
		oldStatus, known := hm.lastStatus[ss.MountName]
		if !known {
			oldStatus = StatusHealthy
		}

		if newStatus != oldStatus {
			hm.lastStatus[ss.MountName] = newStatus
			logger.L.Infow("Stream health changed",
				"mount", ss.MountName,
				"from", oldStatus.String(),
				"to", newStatus.String(),
			)
			if hm.onEvent != nil {
				hm.onEvent(StreamHealthEvent{
					Mount:     ss.MountName,
					OldStatus: oldStatus,
					NewStatus: newStatus,
					Timestamp: time.Now(),
				})
			}
		}

		if hm.autoRemoveDead && newStatus == StatusDead {
			silence := time.Since(checkTime)
			if silence > hm.deadTimeout {
				// Transcoded outputs are tied to a configured input
				// mount and an encoder goroutine that re-attaches on
				// every source flap. Removing them while the source
				// is briefly down forces listeners to 404 and turns a
				// 10-30 s source hiccup into an end-to-end outage:
				// the player reconnects, hits 404, gives up, and the
				// user hears "stream dead" until they manually retry.
				// Keep the mount registered; the encoder's
				// FlushAtHead on resume snaps listeners to the live
				// edge so they don't replay buffered audio from
				// before the gap.
				stream.mu.RLock()
				isTranscoded := stream.IsTranscoded
				stream.mu.RUnlock()
				if isTranscoded {
					continue
				}
				logger.L.Warnw("Auto-removing dead stream", "mount", ss.MountName, "silence", silence)
				hm.relay.RemoveStream(ss.MountName)
				delete(hm.lastStatus, ss.MountName)
			}
		}
	}

	for mount := range hm.lastStatus {
		if !activeStreams[mount] {
			delete(hm.lastStatus, mount)
		}
	}
}
