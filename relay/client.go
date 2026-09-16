package relay

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/DatanoiseTV/tinyice/logger"
)

type backoff struct {
	base    time.Duration
	max     time.Duration
	attempt int
}

func (b *backoff) next() time.Duration {
	exp := math.Pow(2, float64(b.attempt))
	delay := time.Duration(float64(b.base) * exp)
	if delay > b.max {
		delay = b.max
	}
	jitter := time.Duration(float64(delay) * (0.5 + rand.Float64()*0.5))
	b.attempt++
	return jitter
}

func (b *backoff) reset() {
	b.attempt = 0
}

type RelayState int

const (
	RelayConnecting   RelayState = iota
	RelayConnected
	RelayReconnecting
	RelayFailed
)

type RelayInstance struct {
	URL            string
	Mount          string
	Password       string
	BurstSize      int
	Visible        bool
	cancel         context.CancelFunc
	mu             sync.Mutex
	Active         bool
	State          RelayState
	LastConnected  time.Time
	LastError      string
	ReconnectCount int
}

type RelayManager struct {
	instances map[string]*RelayInstance
	mu        sync.RWMutex
	relay     *Relay
	client    *http.Client
}

func NewRelayManager(r *Relay) *RelayManager {
	return &RelayManager{
		instances: make(map[string]*RelayInstance),
		relay:     r,
		// Timeout 0: a pull streams indefinitely and is bounded by the
		// idle watchdog in performPull instead. The dialer carries the
		// outbound address policy, so a redirect or a hostname that
		// resolves into the private network cannot turn a relay into an
		// exfiltration channel for an internal service.
		client: NewOutboundHTTPClient(0),
	}
}

func (rm *RelayManager) StartRelay(url, mount, password string, burstSize int, visible bool) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	// Stop existing if any
	if inst, ok := rm.instances[mount]; ok {
		inst.Stop()
	}

	ctx, cancel := context.WithCancel(context.Background())
	inst := &RelayInstance{
		URL:       url,
		Mount:     mount,
		Password:  password,
		BurstSize: burstSize,
		Visible:   visible,
		cancel:    cancel,
		Active:    true,
	}
	rm.instances[mount] = inst

	go rm.runRelay(ctx, inst)
}

func (rm *RelayManager) StopRelay(mount string) {

	rm.mu.Lock()

	defer rm.mu.Unlock()

	if inst, ok := rm.instances[mount]; ok {

		inst.Stop()

		delete(rm.instances, mount)

	}

}

func (rm *RelayManager) StopAll() {

	rm.mu.Lock()

	defer rm.mu.Unlock()

	for mount, inst := range rm.instances {

		inst.Stop()

		delete(rm.instances, mount)

	}

}

func (inst *RelayInstance) Stop() {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if inst.cancel != nil {
		inst.cancel()
		inst.Active = false
	}
}

func (rm *RelayManager) runRelay(ctx context.Context, inst *RelayInstance) {
	bo := &backoff{base: 1 * time.Second, max: 60 * time.Second}
	for {
		select {
		case <-ctx.Done():
			return
		default:
			inst.mu.Lock()
			inst.State = RelayConnecting
			inst.mu.Unlock()

			rm.performPull(ctx, inst)

			inst.mu.Lock()
			wasConnected := inst.State == RelayConnected
			inst.State = RelayReconnecting
			inst.ReconnectCount++
			inst.mu.Unlock()

			if wasConnected {
				bo.reset()
			}

			delay := bo.next()
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
	}
}

// relayIdleTimeout bounds how long a connected pull may go without
// delivering a byte before it is torn down and retried. A var so tests
// can shorten it.
var relayIdleTimeout = 60 * time.Second

func (rm *RelayManager) performPull(ctx context.Context, inst *RelayInstance) {
	logger.L.Infow("Attempting to pull relay stream", "url", inst.URL)

	// The idle watchdog set up below has to cancel the context the
	// REQUEST was made with, or cancelling it does nothing to a parked
	// body.Read. It used to derive a second context after the request was
	// already in flight, so a silently half-closed upstream parked the
	// pull goroutine forever — the exact stall the watchdog exists for.
	pullCtx, pullCancel := context.WithCancel(ctx)
	defer pullCancel()

	req, err := http.NewRequestWithContext(pullCtx, "GET", inst.URL, nil)
	if err != nil {
		inst.mu.Lock()
		inst.LastError = fmt.Sprintf("request creation failed: %v", err)
		inst.mu.Unlock()
		return
	}

	req.Header.Set("User-Agent", "TinyIce/0.1.0")
	req.Header.Set("Icy-MetaData", "1")
	req.Header.Set("Accept", "*/*")

	if inst.Password != "" {
		req.SetBasicAuth("source", inst.Password)
	}

	resp, err := rm.client.Do(req)
	if err != nil {
		logger.L.Errorf("Relay connection failed: %v", err)
		inst.mu.Lock()
		inst.LastError = fmt.Sprintf("connection failed: %v", err)
		inst.mu.Unlock()
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.L.Errorw("Relay server returned non-200", "status", resp.Status)
		return
	}

	logger.L.Infow("Relay stream connected and pulling", "mount", inst.Mount)

	inst.mu.Lock()
	inst.State = RelayConnected
	inst.LastConnected = time.Now()
	inst.LastError = ""
	inst.mu.Unlock()

	stream := rm.relay.GetOrCreateStream(inst.Mount)
	stream.SetSourceIP("relay-pull")

	// Metadata
	name := resp.Header.Get("Ice-Name")
	if name == "" {
		name = resp.Header.Get("Icy-Name")
	}
	desc := resp.Header.Get("Ice-Description")
	if desc == "" {
		desc = resp.Header.Get("Icy-Description")
	}
	genre := resp.Header.Get("Ice-Genre")
	if genre == "" {
		genre = resp.Header.Get("Icy-Genre")
	}
	bitrate := resp.Header.Get("Ice-Bitrate")
	if bitrate == "" {
		bitrate = resp.Header.Get("Icy-Br")
	}

	// Check for ICY metadata interval
	var metaInt int
	fmt.Sscanf(resp.Header.Get("Icy-Metaint"), "%d", &metaInt)

	stream.UpdateMetadata(name, desc, genre, resp.Header.Get("Ice-Url"), bitrate, resp.Header.Get("Content-Type"), false, inst.Visible)

	// Wrap the response body in an idle-watchdog reader. The
	// http.Client only has a ResponseHeaderTimeout — once the upstream
	// returns headers, body.Read parks indefinitely if the remote half-
	// closes silently (NAT idle drop, common on long-lived icecast
	// pulls). The watchdog fires the request context if no bytes arrive
	// within the idle window, which propagates an i/o cancellation up
	// to body.Read so the loop exits and runRelay can reconnect.
	watchdog := time.AfterFunc(relayIdleTimeout, func() {
		logger.L.Warnw("Relay pull went silent past idle window — reconnecting",
			"mount", inst.Mount, "url", inst.URL, "idle_window", relayIdleTimeout.String())
		pullCancel()
	})
	defer watchdog.Stop()
	body := &idleResetReader{r: resp.Body, timer: watchdog, reset: relayIdleTimeout}

	// In-stream metadata parsing
	if metaInt > 0 {
		rm.pullWithMetadata(pullCtx, body, stream, metaInt)
	} else {
		rm.pullSimple(pullCtx, body, stream)
	}
}

// idleResetReader resets a timer on every byte received. Used to bound
// silent stalls on a long-lived HTTP body where the kernel TCP layer
// hasn't noticed the peer is gone (e.g. NAT idle drop, half-open).
type idleResetReader struct {
	r     io.Reader
	timer *time.Timer
	reset time.Duration
}

func (i *idleResetReader) Read(p []byte) (int, error) {
	n, err := i.r.Read(p)
	if n > 0 {
		i.timer.Reset(i.reset)
	}
	return n, err
}

func (rm *RelayManager) pullSimple(ctx context.Context, body io.Reader, stream *Stream) {
	buf := make([]byte, 16384)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			n, err := body.Read(buf)
			if n > 0 {
				data := buf[:n]
				stream.Broadcast(data, rm.relay)

				// Sniff for Opus metadata (Vorbis comments) in Ogg pages
				// Look for "OpusTags" magic
				if idx := bytes.Index(data, []byte("OpusTags")); idx != -1 {
					// Found tags! Extract title if possible
					// Skip "OpusTags" (8 bytes)
					tagsData := data[idx+8:]
					if len(tagsData) > 8 {
						// Simple sniffer for "TITLE="
						tagsStr := string(tagsData)
						if strings.Contains(tagsStr, "TITLE=") {
							title := strings.Split(tagsStr, "TITLE=")[1]
							// Titles in Ogg are often null-terminated or limited by length
							// For simplicity, we just take a reasonable chunk and trim
							if len(title) > 100 {
								title = title[:100]
							}
							// Clean up
							title = strings.Map(func(r rune) rune {
								if r < 32 || r > 126 {
									return -1
								}
								return r
							}, title)
							if title != "" {
								stream.SetCurrentSong(title, rm.relay)
							}
						}
					}
				}
			}
			if err != nil {
				return
			}
		}
	}
}

func (rm *RelayManager) pullWithMetadata(ctx context.Context, body io.Reader, stream *Stream, metaInt int) {
	audioBuf := make([]byte, metaInt)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// 1. Read Audio Data
			_, err := io.ReadFull(body, audioBuf)
			if err != nil {
				return
			}
			stream.Broadcast(audioBuf, rm.relay)

			// 2. Read Metadata Length Byte
			var metaLenByte [1]byte
			_, err = io.ReadFull(body, metaLenByte[:])
			if err != nil {
				return
			}

			metaLen := int(metaLenByte[0]) * 16
			if metaLen > 0 {
				// 3. Read Metadata String
				metaBuf := make([]byte, metaLen)
				_, err = io.ReadFull(body, metaBuf)
				if err != nil {
					return
				}

				// Parse StreamTitle='Artist - Title';
				metaStr := string(metaBuf)
				if strings.Contains(metaStr, "StreamTitle='") {
					title := strings.Split(metaStr, "StreamTitle='")[1]
					title = strings.Split(title, "';")[0]
					if title != "" {
						stream.SetCurrentSong(title, rm.relay)
					}
				}
			}
		}
	}
}
