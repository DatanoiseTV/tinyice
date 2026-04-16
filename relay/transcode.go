package relay

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
	shine "github.com/braheezy/shine-mp3/pkg/mp3"
	"github.com/hajimehoshi/go-mp3"
	"github.com/kazzmir/opus-go/ogg"
	"github.com/kazzmir/opus-go/opus"
)

type TranscoderInstance struct {
	Config *config.TranscoderConfig
	cancel context.CancelFunc
	active bool
	mu     sync.Mutex

	// Stats
	FramesProcessed int64
	BytesEncoded    int64
	StartTime       time.Time
}

type TranscoderManager struct {
	instances map[string]*TranscoderInstance // key is OutputMount
	mu        sync.RWMutex
	relay     *Relay
}

func NewTranscoderManager(r *Relay) *TranscoderManager {
	return &TranscoderManager{
		instances: make(map[string]*TranscoderInstance),
		relay:     r,
	}
}

func (tm *TranscoderManager) StartTranscoder(cfg *config.TranscoderConfig) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if inst, ok := tm.instances[cfg.OutputMount]; ok {
		inst.Stop()
	}

	ctx, cancel := context.WithCancel(context.Background())
	inst := &TranscoderInstance{
		Config:    cfg,
		cancel:    cancel,
		active:    true,
		StartTime: time.Now(),
	}
	tm.instances[cfg.OutputMount] = inst

	go tm.runTranscoder(ctx, inst)
}

func (tm *TranscoderManager) StopTranscoder(outputMount string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if inst, ok := tm.instances[outputMount]; ok {
		inst.Stop()
		delete(tm.instances, outputMount)
	}
}

func (tm *TranscoderManager) StopAll() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	for _, inst := range tm.instances {
		inst.Stop()
	}
	tm.instances = make(map[string]*TranscoderInstance)
}

func (inst *TranscoderInstance) Stop() {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if inst.cancel != nil {
		inst.cancel()
		inst.active = false
	}
}

func (tm *TranscoderManager) runTranscoder(ctx context.Context, inst *TranscoderInstance) {
	logger.L.Infow("Starting transcoder",
		"name", inst.Config.Name,
		"input", inst.Config.InputMount,
		"output", inst.Config.OutputMount,
		"format", inst.Config.Format,
		"bitrate", inst.Config.Bitrate,
	)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			tm.safePerformTranscode(ctx, inst)
			// Wait before retry if input stream wasn't found
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

// safePerformTranscode wraps performTranscode with a panic recovery. Some
// third-party decoders (e.g. go-mp3) can panic on malformed or non-MP3 input
// instead of returning an error. Recovering here prevents a bad input stream
// from taking down the whole server and lets the retry loop try again.
func (tm *TranscoderManager) safePerformTranscode(ctx context.Context, inst *TranscoderInstance) {
	defer func() {
		if r := recover(); r != nil {
			logger.L.Errorw("Transcoder: recovered from panic",
				"name", inst.Config.Name,
				"input", inst.Config.InputMount,
				"output", inst.Config.OutputMount,
				"panic", fmt.Sprintf("%v", r),
			)
		}
	}()
	tm.performTranscode(ctx, inst)
}

func (tm *TranscoderManager) performTranscode(ctx context.Context, inst *TranscoderInstance) {
	var input *Stream
	var ok bool

	// 1. Wait for input stream to become available
	for {
		input, ok = tm.relay.GetStream(inst.Config.InputMount)
		if ok {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(1 * time.Second):
			// Keep waiting
		}
	}

	logger.L.Infow("Transcoder: Input stream found, initializing...", "name", inst.Config.Name, "input", inst.Config.InputMount)

	// 2. Subscribe to input
	id := fmt.Sprintf("transcoder-%s", inst.Config.Name)
	// We use a small burst to ensure the decoder gets enough data to start
	offset, signal := input.Subscribe(id, 32*1024)
	defer input.Unsubscribe(id)

	reader := NewStreamReader(input.Buffer, offset, signal, ctx, id).WithOggSync(input)

	// 3. Decode
	decoder, err := mp3.NewDecoder(reader)
	if err != nil {
		logger.L.Errorw("Transcoder: Failed to initialize decoder", "name", inst.Config.Name, "input", inst.Config.InputMount, "error", err)
		return
	}

	// 4. Create Output Stream
	output := tm.relay.GetOrCreateStream(inst.Config.OutputMount)
	output.Name = fmt.Sprintf("%s (%s %dK)", input.Name, inst.Config.Format, inst.Config.Bitrate)
	output.Bitrate = fmt.Sprintf("%d", inst.Config.Bitrate)
	output.IsTranscoded = true
	output.Visible = tm.relay.GetStreamVisibility(inst.Config.InputMount) // Follow input visibility

	if inst.Config.Format == "mp3" {
		output.ContentType = "audio/mpeg"
		EncodeMP3(ctx, tm.relay, output, decoder, inst.Config.Bitrate, &inst.BytesEncoded, false, 44100)
	} else if inst.Config.Format == "opus" {
		output.ContentType = "audio/ogg"
		EncodeOpus(ctx, tm.relay, output, decoder, inst.Config.Bitrate, &inst.BytesEncoded, false)
	}
}

func EncodeMP3(ctx context.Context, relay *Relay, output *Stream, decoder io.Reader, bitrate int, stats *int64, pace bool, sampleRate int) {
	if sampleRate <= 0 {
		sampleRate = 44100 // Fallback
	}
	// Shine MP3 initialization
	encoder := shine.NewEncoder(sampleRate, 2)

	// Output buffer - shine Write uses int16 samples
	pcmBuf := make([]byte, 4608) // 1152 samples * 2 bytes * 2 channels
	samples := make([]int16, 2304)

	startTime := time.Now()
	totalSamples := int64(0)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			n, err := io.ReadFull(decoder, pcmBuf)
			if err != nil {
				return
			}

			// Convert PCM bytes to int16 for Shine
			for i := 0; i < n/2; i++ {
				samples[i] = int16(pcmBuf[i*2]) | int16(pcmBuf[i*2+1])<<8
			}

			// Encode and broadcast
			// Shine writes directly to an io.Writer
			// We can wrap our broadcast in an io.Writer
			writer := &streamWriter{stream: output, relay: relay, stats: stats}
			err = encoder.Write(writer, samples[:n/2])
			if err != nil {
				return
			}

			if pace {
				totalSamples += int64(n / 4) // 2 channels, 2 bytes per sample
				elapsed := time.Since(startTime)
				expected := time.Duration(totalSamples) * time.Second / time.Duration(sampleRate)
				if expected > elapsed {
					time.Sleep(expected - elapsed)
				}
			}
		}
	}
}

func EncodeOpus(ctx context.Context, relay *Relay, output *Stream, decoder io.Reader, bitrate int, stats *int64, pace bool) {
	// 48kHz is standard for Opus
	const sampleRate = 48000
	const channels = 2
	const frameMS = 20
	const frameSize = sampleRate * frameMS / 1000

	enc, err := opus.NewEncoder(sampleRate, channels, opus.ApplicationAudio)
	if err != nil {
		logger.L.Errorf("Failed to create Opus encoder: %v", err)
		return
	}
	defer enc.Close()

	if bitrate > 0 {
		enc.SetBitrate(bitrate * 1000)
	}

	// Ogg encapsulation
	writer := &streamWriter{stream: output, relay: relay, stats: stats, capture: true}
	serial := uint32(time.Now().UnixNano())
	pw := ogg.NewPacketWriter(writer, serial)

	// 1. ID Header
	head := ogg.OpusHead{
		Version:         1,
		Channels:        uint8(channels),
		InputSampleRate: uint32(sampleRate),
	}
	headPacket, _ := ogg.BuildOpusHeadPacket(head)
	pw.WritePacket(headPacket, 0, true, false)
	pw.Flush()

	// 2. Tags Header
	tags := ogg.OpusTags{Vendor: "tinyice-opus"}
	tagsPacket, _ := ogg.BuildOpusTagsPacket(tags)
	pw.WritePacket(tagsPacket, 0, false, false)
	pw.Flush()

	// Store for mid-stream listeners
	output.OggHead = writer.headerBuf.Bytes()
	output.OggHeaderOffset = output.Buffer.Head
	writer.capture = false // Stop capturing headers

	pcmBuf := make([]byte, frameSize*channels*2)
	pcmSamples := make([]int16, frameSize*channels)
	opusPacket := make([]byte, 4000) // Max opus packet size

	var granulePos uint64
	var sentCount int64 = 0
	startTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		default:
			_, rerr := io.ReadFull(decoder, pcmBuf)
			if rerr != nil {
				return
			}

			for i := 0; i < len(pcmSamples); i++ {
				pcmSamples[i] = int16(pcmBuf[i*2]) | int16(pcmBuf[i*2+1])<<8
			}

			en, eerr := enc.Encode(pcmSamples, frameSize, opusPacket)
			if eerr != nil {
				logger.L.Errorf("Opus encode error: %v", eerr)
				return
			}

			granulePos += uint64(frameSize)
			if err := pw.WritePacket(opusPacket[:en], granulePos, false, false); err != nil {
				return
			}
			pw.Flush()

			if pace {
				sentCount++
				elapsed := time.Since(startTime)
				expected := time.Duration(sentCount*frameMS) * time.Millisecond
				if expected > elapsed {
					time.Sleep(expected - elapsed)
				}
			}
		}
	}
}

type streamWriter struct {
	stream    *Stream
	relay     *Relay
	stats     *int64
	headerBuf bytes.Buffer
	capture   bool
}

func (w *streamWriter) Write(p []byte) (n int, err error) {
	if w.capture {
		w.headerBuf.Write(p)
	}
	w.stream.Broadcast(p, w.relay)
	if w.stats != nil {
		atomic.AddInt64(w.stats, int64(len(p)))
	}
	return len(p), nil
}

func (tm *TranscoderManager) GetInstance(outputMount string) *TranscoderInstance {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.instances[outputMount]
}

type TranscoderStats struct {
	Name            string `json:"name"`
	Input           string `json:"input"`
	Output          string `json:"output"`
	Format          string `json:"format"`
	Bitrate         int    `json:"bitrate"`
	Active          bool   `json:"active"`
	FramesProcessed int64  `json:"frames"`
	BytesEncoded    int64  `json:"bytes"`
	Uptime          string `json:"uptime"`
}
