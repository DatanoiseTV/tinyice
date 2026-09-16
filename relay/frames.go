package relay

import (
	"context"
	"sync"
)

// FrameKind distinguishes audio and video frames on the same Stream's frame
// channel so consumers don't need a separate subscription per media type.
type FrameKind uint8

const (
	FrameAudio FrameKind = iota
	FrameVideo
)

// Frame is a single audio or video access unit with its presentation
// timestamp on the 90 kHz MPEG clock. The HLS output path uses these to
// emit one PES per frame with the correct PTS, instead of the previous
// "one PES per segment with an invented PTS" shortcut.
//
// For H.264, Data is Annex-B bytes of one access unit (SPS+PPS+IDR or a
// single non-IDR slice). For AAC it's one ADTS frame. For MP3 it's one
// MPEG audio frame.
type Frame struct {
	Kind     FrameKind
	PTS      int64 // presentation timestamp, 90 kHz units
	DTS      int64 // decode timestamp, 90 kHz units (== PTS for audio and B-frame-less video)
	Data     []byte
	Keyframe bool // video only; ignored for audio
}

// FrameHub broadcasts Frames from a producer (RTMP/SRT ingest) to any
// number of consumers (HLS segmenter) on a per-Stream basis. Each
// subscriber gets its own buffered channel; slow consumers drop frames
// rather than block the producer.
type FrameHub struct {
	mu   sync.Mutex
	subs map[int]*frameSub
	next int
}

// frameSub is one subscriber. done is closed whenever the subscription
// ends — by Close, or by the ctx watcher — so the watcher goroutine can
// exit as soon as either happens instead of only on ctx.
type frameSub struct {
	ch   chan Frame
	done chan struct{}
}

// NewFrameHub returns an empty hub.
func NewFrameHub() *FrameHub {
	return &FrameHub{subs: make(map[int]*frameSub)}
}

// Publish sends f to every subscriber. Non-blocking: if a subscriber's
// channel is full we drop the frame for that subscriber so the producer
// never stalls. Video glitches beat ingest stalls.
func (h *FrameHub) Publish(f Frame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, sub := range h.subs {
		select {
		case sub.ch <- f:
		default:
			// subscriber can't keep up; drop this frame for them
		}
	}
}

// Subscribe returns a channel that yields frames until ctx is cancelled or
// the hub is closed. Buffer size is deliberately generous (512) so bursty
// ingest doesn't lose frames before the consumer catches up.
// The watcher goroutine also waits on the subscription's own done
// channel: it used to wait on ctx alone, so every subscription torn down
// by Close left a goroutine parked until the caller's context ended. The
// HLS framed loop resubscribes with the same long-lived output context
// on every source flap, which leaked two parked goroutines per flap for
// the life of the output.
func (h *FrameHub) Subscribe(ctx context.Context) <-chan Frame {
	sub := &frameSub{ch: make(chan Frame, 512), done: make(chan struct{})}
	h.mu.Lock()
	id := h.next
	h.next++
	h.subs[id] = sub
	h.mu.Unlock()
	go func() {
		select {
		case <-ctx.Done():
		case <-sub.done:
			return
		}
		h.mu.Lock()
		if cur, ok := h.subs[id]; ok && cur == sub {
			delete(h.subs, id)
			close(cur.done)
			close(cur.ch)
		}
		h.mu.Unlock()
	}()
	return sub.ch
}

// Close tears down every subscriber channel. Used when a Stream is
// removed. Idempotent.
func (h *FrameHub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, sub := range h.subs {
		delete(h.subs, id)
		close(sub.done)
		close(sub.ch)
	}
}
