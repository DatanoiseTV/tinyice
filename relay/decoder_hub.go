package relay

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DatanoiseTV/tinyice/logger"
)

// readerDecoder adapts an io.Reader producing PCM bytes into the
// PCMDecoder interface that NewLinearResampler / EncodeMP3 / EncodeOpus
// expect — those callers want a SampleRate() method alongside Read.
// The shim is here (next to the hub) because the hub is the only
// producer of "raw PCM with a known sample rate but no decoder
// object behind it".
type readerDecoder struct {
	r  io.Reader
	sr int
}

func (d *readerDecoder) Read(p []byte) (int, error) { return d.r.Read(p) }
func (d *readerDecoder) SampleRate() int            { return d.sr }

// pcmMountSuffix is appended to an input mount to name the internal
// shared-decoder PCM stream, e.g. "/electronica" -> "/electronica/_pcm".
// The internal stream is invisible to listeners and the public API; it
// only fans S16LE stereo PCM from one decoder to N transcoder
// encoders.
const pcmMountSuffix = "/_pcm"

// pcmStream holds the side-channel info we attach to a PCM-fanout
// Stream beyond what Stream itself carries: native sample rate +
// pump lifecycle. Tracked in DecoderHub.streams.
type pcmStream struct {
	mu sync.Mutex

	// stream is the internal Stream holding PCM bytes.
	stream *Stream

	// sampleRate is the native rate produced by the source decoder
	// (48000 for Opus, 44100 for most MP3 / Vorbis sources, etc.).
	sampleRate int

	// ready is closed once the decoder has been opened and the
	// sample rate is known. Subscribers wait on this before reading.
	ready chan struct{}

	// initErr captures a fatal start-up error so subscribers don't
	// block forever waiting for ready.
	initErr error

	// cancel stops the pump goroutine. Set when the pump starts.
	cancel context.CancelFunc

	// refcount tracks how many transcoders currently hold this
	// shared decoder. When it drops to zero the pump is cancelled
	// and the entry is removed.
	refcount int
}

// DecoderHub owns the shared-decoder pumps keyed by input mount. The
// TranscoderManager owns one DecoderHub. It deliberately holds no
// per-Source state — everything it needs comes from the relay.
type DecoderHub struct {
	mu      sync.Mutex
	relay   *Relay
	streams map[string]*pcmStream // key: input mount
}

// NewDecoderHub builds a DecoderHub bound to one relay.
func NewDecoderHub(r *Relay) *DecoderHub {
	return &DecoderHub{
		relay:   r,
		streams: map[string]*pcmStream{},
	}
}

// Acquire returns a PCM reader (S16LE stereo) for the given input
// mount, the native sample rate, and a release function. The first
// caller for a given mount triggers the pump goroutine that reads
// the input, decodes, and broadcasts PCM bytes; subsequent callers
// just attach a new subscriber to the existing PCM stream.
//
// ctx bounds the subscriber's read loop, NOT the pump. The pump
// outlives any single transcoder so the next subscriber can attach
// without re-paying the decoder warm-up cost.
//
// Caller MUST call release() when its transcoder loop exits, even
// on error; otherwise the pump never shuts down.
func (h *DecoderHub) Acquire(
	ctx context.Context, inputMount, subID string,
) (io.Reader, int, func(), error) {
	h.mu.Lock()
	ps, ok := h.streams[inputMount]
	if !ok {
		ps = &pcmStream{ready: make(chan struct{})}
		h.streams[inputMount] = ps
		ps.refcount = 1
		h.mu.Unlock()
		// Start the pump outside the hub lock to avoid blocking
		// other Acquire calls while we open the input + decoder.
		go h.runPump(inputMount, ps)
	} else {
		ps.refcount++
		h.mu.Unlock()
	}

	// Wait for the pump to publish its sample rate (or fail).
	select {
	case <-ps.ready:
		// fall through
	case <-ctx.Done():
		h.release(inputMount)
		return nil, 0, func() {}, ctx.Err()
	}
	if ps.initErr != nil {
		h.release(inputMount)
		return nil, 0, func() {}, ps.initErr
	}

	// Subscribe to the internal PCM stream. SubscribeInternal so the
	// subscriber doesn't show up in dashboard listener counts.
	pcmS := ps.stream
	offset, signal := pcmS.SubscribeInternal(subID, 0)
	reader := NewStreamReader(pcmS.Buffer, offset, signal, ctx, subID)

	released := false
	release := func() {
		if released {
			return
		}
		released = true
		pcmS.Unsubscribe(subID)
		h.release(inputMount)
	}
	return reader, ps.sampleRate, release, nil
}

// release decrements refcount; when it hits zero the pump is
// cancelled and the entry is removed so the next Acquire starts a
// fresh decoder.
func (h *DecoderHub) release(inputMount string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ps, ok := h.streams[inputMount]
	if !ok {
		return
	}
	ps.refcount--
	if ps.refcount > 0 {
		return
	}
	if ps.cancel != nil {
		ps.cancel()
	}
	delete(h.streams, inputMount)
}

// runPump opens the input + decoder for one inputMount, then pumps
// PCM into the internal /_pcm stream's buffer. Exits when:
//   - the per-pump context is cancelled (release brings refcount to 0)
//   - the input source disconnects and the decoder hits EOF
//   - the decoder errors mid-stream
//
// On exit the pump signals ready (with initErr if it never reached
// the read loop) and closes the internal PCM stream so subscribers
// see EOF.
func (h *DecoderHub) runPump(inputMount string, ps *pcmStream) {
	pumpCtx, cancel := context.WithCancel(context.Background())
	ps.cancel = cancel
	defer func() {
		// Make sure ready is signalled in every exit path so
		// Acquire callers don't block forever on a failed start.
		select {
		case <-ps.ready:
		default:
			close(ps.ready)
		}
		// Kick the PCM-stream subscribers so transcoders unblock
		// immediately on source disconnect. Before this, when the
		// pump exited (e.g. on `unexpected EOF` after the source
		// dropped), the orphaned PCM stream's listeners stayed
		// subscribed and StreamReader.Read blocked on the dead
		// signal channel. Transcoders only resumed when the
		// HealthMonitor's 2-minute auto-remove finally swept the
		// stale stream — a 1-2 minute gap between source-back and
		// transcoder-back, even though the source had already
		// reconnected. Removing the PCM stream here closes its
		// listener channels (Stream.Close kicks them), the
		// transcoders' encode loops error out, performTranscode
		// returns, the deferred releaseDecoder fires, and the next
		// retry tick (~5 s later) creates a fresh pump. Resync
		// time goes from ~2 min to ~5 s.
		if ps.stream != nil {
			h.relay.RemoveStream(ps.stream.MountName)
		}
	}()

	// 1. Wait for the input stream to exist. Up to 30 s — if the
	//    operator started the transcoders before the source, we
	//    wait briefly; otherwise we mark initErr and exit so
	//    subscribers retry instead of hanging.
	var input *Stream
	{
		// time.After is fine here even though it can leak the timer
		// briefly on early exit — pumpCtx.Done() and the success path
		// both let us drop our reference, and the runtime collects
		// the timer once nothing references its channel. The previous
		// helper spawned a dedicated goroutine that ALWAYS ran for
		// 30 s regardless, which was a small but unbounded drip.
		deadline := time.After(30 * time.Second)
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
	wait:
		for {
			if s, ok := h.relay.GetStream(inputMount); ok {
				input = s
				break wait
			}
			select {
			case <-pumpCtx.Done():
				ps.initErr = pumpCtx.Err()
				return
			case <-deadline:
				ps.initErr = fmt.Errorf("decoder hub: input mount %q not found", inputMount)
				return
			case <-tick.C:
			}
		}
	}

	// 2. Subscribe to the input. Same burst we used in
	//    performTranscode (256 KiB) so strict Opus / FLAC decoders
	//    have enough warm-up bytes.
	subID := fmt.Sprintf("decoder-hub-%s", strings.TrimPrefix(inputMount, "/"))
	offset, signal := input.SubscribeInternal(subID, 256*1024)
	defer input.Unsubscribe(subID)

	input.mu.RLock()
	isOgg := input.IsOggStream || strings.Contains(strings.ToLower(input.ContentType), "ogg") ||
		strings.Contains(strings.ToLower(input.ContentType), "opus") ||
		strings.Contains(strings.ToLower(input.ContentType), "vorbis") ||
		strings.Contains(strings.ToLower(input.ContentType), "flac")
	var headBytes []byte
	if len(input.OggHead) > 0 {
		headBytes = append(headBytes, input.OggHead...)
	}
	input.mu.RUnlock()

	var reader io.Reader
	if isOgg {
		// HeadOffset, not a bare Buffer.Head read: the source goroutine
		// writes Head under the buffer mutex, so reading the field
		// directly here is a data race against every Broadcast.
		aligned := input.Buffer.FindNextPageBoundaryLocked(offset)
		if aligned < input.Buffer.HeadOffset() {
			offset = aligned
		}
		live := NewStreamReader(input.Buffer, offset, signal, pumpCtx, subID).WithOggSync(input)
		if len(headBytes) > 0 {
			reader = io.MultiReader(bytes.NewReader(headBytes), live)
		} else {
			logger.L.Warnw("decoder hub: input has no captured Ogg headers; decoder may fail until source reconnects",
				"input", inputMount)
			reader = live
		}
	} else {
		reader = NewStreamReader(input.Buffer, offset, signal, pumpCtx, subID).WithOggSync(input)
	}

	decoder, err := OpenDecoder(reader)
	if err != nil {
		ps.initErr = fmt.Errorf("decoder hub: open: %w", err)
		return
	}

	// 3. Spin up the internal PCM stream. Use the relay's
	//    GetOrCreateStream so the lifecycle matches the rest of
	//    the system (snapshot, kick-all, etc.).
	pcmMount := inputMount + pcmMountSuffix
	pcmS := h.relay.GetOrCreateStream(pcmMount)
	pcmS.mu.Lock()
	pcmS.Name = "PCM hub for " + inputMount
	pcmS.ContentType = "audio/raw-s16le"
	pcmS.IsTranscoded = true
	pcmS.Visible = false // don't surface to public APIs / dashboards
	pcmS.Public = false
	pcmS.mu.Unlock()
	ps.stream = pcmS
	ps.sampleRate = decoder.SampleRate()

	// 4. Signal Acquire callers that the rate is published and they
	//    can subscribe.
	close(ps.ready)
	logger.L.Infow("decoder hub: pump up",
		"input", inputMount, "rate", ps.sampleRate)

	// 5. Pump loop. ReadFull a chunk of PCM, broadcast to the PCM
	//    stream. Block on EOF / errors and exit cleanly.
	//
	//    Stalled-pump watchdog: defensive against genuine source-side
	//    stalls (network blip, dead-but-not-closed TCP, a buggy
	//    decoder that hangs without erroring). The chained-Ogg case
	//    that originally motivated this is now handled inside the
	//    Opus decoder itself (see decode_opus_chained.go), but we
	//    keep the watchdog as a belt-and-braces safety net so that
	//    a stuck pump always gets recycled before the 120 s
	//    HealthMonitor kill window.
	const chunkBytes = 8192 // 2048 stereo s16 samples = ~21ms @ 48kHz / ~23ms @ 44.1kHz
	const pumpStallTimeout = 30 * time.Second
	var lastWrite atomic.Int64
	lastWrite.Store(time.Now().UnixNano())
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-pumpCtx.Done():
				return
			case now := <-t.C:
				if now.UnixNano()-lastWrite.Load() > int64(pumpStallTimeout) {
					logger.L.Warnw("decoder hub: pump stalled, forcing restart",
						"input", inputMount, "idle", pumpStallTimeout.String())
					cancel()
					return
				}
			}
		}
	}()

	buf := make([]byte, chunkBytes)
	for {
		select {
		case <-pumpCtx.Done():
			logger.L.Infow("decoder hub: pump cancelled", "input", inputMount)
			return
		default:
		}
		n, err := io.ReadFull(decoder, buf)
		if err != nil {
			logger.L.Infow("decoder hub: pump exiting", "input", inputMount, "reason", err.Error())
			return
		}
		pcmS.Broadcast(buf[:n], h.relay)
		lastWrite.Store(time.Now().UnixNano())
	}
}
