package relay

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/hajimehoshi/go-mp3"
	"github.com/jfreymuth/oggvorbis"
	"github.com/kazzmir/opus-go/ogg"
	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
)

// PCMDecoder produces interleaved signed 16-bit little-endian stereo PCM
// via the embedded io.Reader, at the rate reported by SampleRate().
//
// Channels are always 2 (mono inputs are duplicated, >2 channel inputs are
// downmixed to the first two channels) so downstream encoders don't need to
// special-case the layout.
type PCMDecoder interface {
	io.Reader
	SampleRate() int
}

// OpenDecoder inspects the first few bytes of r and returns a PCMDecoder
// that streams S16LE stereo PCM. It supports MP3, Ogg Opus, Ogg Vorbis, and
// native FLAC.
func OpenDecoder(r io.Reader) (PCMDecoder, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	// Peek a generous amount so the BOS page body (which contains the codec
	// identification packet) is always present even for longer Vorbis /
	// FLAC-in-Ogg setup packets.
	peek, err := br.Peek(4096)
	if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
		return nil, fmt.Errorf("decode: peek failed: %w", err)
	}
	if len(peek) < 4 {
		return nil, fmt.Errorf("decode: stream too short")
	}

	switch {
	case peek[0] == 'I' && peek[1] == 'D' && peek[2] == '3':
		return newMP3Decoder(br)
	case peek[0] == 0xFF && peek[1]&0xE0 == 0xE0:
		return newMP3Decoder(br)
	case bytes.Equal(peek[:4], []byte("fLaC")):
		return newFLACDecoder(br)
	case bytes.Equal(peek[:4], []byte("OggS")):
		return openOggDecoder(br, peek)
	case bytes.Equal(peek[:4], []byte("RIFF")) && len(peek) >= 12 && bytes.Equal(peek[8:12], []byte("WAVE")):
		return newWavDecoder(br)
	}
	return nil, fmt.Errorf("decode: unknown audio format (header: %x)", peek[:4])
}

// openOggDecoder dispatches based on the identification packet carried in the
// BOS page of an Ogg stream.
func openOggDecoder(br io.Reader, peek []byte) (PCMDecoder, error) {
	// If the first page in the peek isn't a BOS page, we're reading
	// mid-stream (typical for the transcoder subscribing to a live relay
	// with no captured OggHead). No amount of codec sniffing will yield the
	// right identification packet — return a specific error so the caller
	// can log a helpful hint.
	if len(peek) >= 6 && peek[5]&0x02 == 0 {
		return nil, fmt.Errorf("decode: Ogg stream started mid-stream (no BOS page in first %d bytes — source needs to reconnect so BOS/Tags pages can be captured)", len(peek))
	}

	body := firstOggPageBody(peek)
	switch {
	case len(body) >= 8 && bytes.Equal(body[:8], []byte("OpusHead")):
		return newOggOpusDecoder(br)
	case len(body) >= 7 && body[0] == 0x01 && bytes.Equal(body[1:7], []byte("vorbis")):
		return newOggVorbisDecoder(br)
	case len(body) >= 5 && body[0] == 0x7F && bytes.Equal(body[1:5], []byte("FLAC")):
		return newOggFLACDecoder(br)
	}
	// Fall back to a textual search — some encoders place the codec magic at
	// a non-standard offset in the first page. This keeps compatibility broad
	// without needing to teach the parser about every Ogg codec.
	switch {
	case bytes.Contains(peek, []byte("OpusHead")):
		return newOggOpusDecoder(br)
	case bytes.Contains(peek, []byte("\x01vorbis")):
		return newOggVorbisDecoder(br)
	case bytes.Contains(peek, []byte{0x7F, 'F', 'L', 'A', 'C'}):
		return newOggFLACDecoder(br)
	}
	// Report up to 32 bytes of the actual body so the operator can tell which
	// Ogg codec we don't yet handle (e.g. Speex, Theora).
	preview := body
	if len(preview) > 32 {
		preview = preview[:32]
	}
	return nil, fmt.Errorf("decode: unknown Ogg codec (BOS body: %x)", preview)
}

// firstOggPageBody returns the concatenated segment data of the first Ogg
// page found in data. Returns nil if data doesn't start with a complete page.
func firstOggPageBody(data []byte) []byte {
	if len(data) < 27 || !bytes.Equal(data[:4], []byte("OggS")) {
		return nil
	}
	numSegments := int(data[26])
	headerLen := 27 + numSegments
	if len(data) < headerLen {
		return nil
	}
	bodyLen := 0
	for i := 0; i < numSegments; i++ {
		bodyLen += int(data[27+i])
	}
	if len(data) < headerLen+bodyLen {
		// Truncated peek — return whatever body we have so the caller can
		// still try magic matching.
		return data[headerLen:]
	}
	return data[headerLen : headerLen+bodyLen]
}

// --- MP3 --------------------------------------------------------------------

type mp3Decoder struct {
	*mp3.Decoder
}

func newMP3Decoder(r io.Reader) (PCMDecoder, error) {
	d, err := mp3.NewDecoder(r)
	if err != nil {
		return nil, fmt.Errorf("decode mp3: %w", err)
	}
	return &mp3Decoder{d}, nil
}

func (m *mp3Decoder) SampleRate() int { return m.Decoder.SampleRate() }

// --- Ogg Vorbis -------------------------------------------------------------

type vorbisDecoder struct {
	r        *oggvorbis.Reader
	pcmF     []float32
	pending  []byte
	channels int
}

func newOggVorbisDecoder(r io.Reader) (PCMDecoder, error) {
	vr, err := oggvorbis.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("decode vorbis: %w", err)
	}
	if vr.Channels() < 1 {
		return nil, fmt.Errorf("decode vorbis: invalid channel count %d", vr.Channels())
	}
	return &vorbisDecoder{
		r:        vr,
		pcmF:     make([]float32, 4096*vr.Channels()),
		channels: vr.Channels(),
	}, nil
}

func (v *vorbisDecoder) SampleRate() int { return v.r.SampleRate() }

func (v *vorbisDecoder) Read(p []byte) (int, error) {
	for len(v.pending) == 0 {
		n, err := v.r.Read(v.pcmF)
		if n == 0 {
			if err == nil {
				err = io.EOF
			}
			return 0, err
		}
		frames := n / v.channels
		v.pending = float32ToStereoS16LE(v.pcmF[:n], v.channels, frames, v.pending[:0])
	}
	n := copy(p, v.pending)
	v.pending = v.pending[n:]
	return n, nil
}

// --- Ogg Opus ---------------------------------------------------------------
//
// newOggOpusDecoder forwards to a chained-stream-aware decoder that uses
// pion/opus for the raw codec and a small Ogg page reader that does not
// pin the bitstream serial. See decode_opus_chained.go.

func newOggOpusDecoder(r io.Reader) (PCMDecoder, error) {
	return newChainedOpusDecoder(r)
}

// --- FLAC -------------------------------------------------------------------

type flacDecoder struct {
	stream   *flac.Stream
	pending  []byte
	channels int
	bitDepth int
}

func newFLACDecoder(r io.Reader) (PCMDecoder, error) {
	s, err := flac.New(r)
	if err != nil {
		return nil, fmt.Errorf("decode flac: %w", err)
	}
	if s.Info == nil {
		return nil, fmt.Errorf("decode flac: missing StreamInfo")
	}
	return &flacDecoder{
		stream:   s,
		channels: int(s.Info.NChannels),
		bitDepth: int(s.Info.BitsPerSample),
	}, nil
}

// newOggFLACDecoder demuxes FLAC-in-Ogg by reading Ogg pages and reconstituting
// the native FLAC byte stream (stripping the "\x7FFLAC\x01\x00\x??\x??"
// mapping header on the first page), then hands that stream to the native
// FLAC decoder.
func newOggFLACDecoder(r io.Reader) (PCMDecoder, error) {
	pr := ogg.NewPageReader(r)
	pr.VerifyCRC = false
	pipeR, pipeW := io.Pipe()
	go func() {
		var closeErr error
		defer func() { pipeW.CloseWithError(closeErr) }()
		first := true
		for {
			page, err := pr.ReadPage()
			if err != nil {
				closeErr = err
				return
			}
			body := page.SegmentData
			if first {
				first = false
				// Strip "\x7F FLAC <mapping-major> <mapping-minor> <num-packets>"
				// 1 + 4 + 1 + 1 + 2 = 9 bytes. The native FLAC stream starts
				// at the "fLaC" signature that follows.
				if len(body) > 9 && body[0] == 0x7F &&
					bytes.Equal(body[1:5], []byte("FLAC")) {
					body = body[9:]
				}
			}
			if len(body) == 0 {
				continue
			}
			if _, werr := pipeW.Write(body); werr != nil {
				closeErr = werr
				return
			}
		}
	}()
	return newFLACDecoder(pipeR)
}

func (f *flacDecoder) SampleRate() int { return int(f.stream.Info.SampleRate) }

func (f *flacDecoder) Read(p []byte) (int, error) {
	for len(f.pending) == 0 {
		fr, err := f.stream.ParseNext()
		if err != nil {
			return 0, err
		}
		if fr == nil || len(fr.Subframes) == 0 {
			continue
		}
		f.pending = flacFrameToStereoS16LE(fr, f.channels, f.bitDepth, f.pending[:0])
	}
	n := copy(p, f.pending)
	f.pending = f.pending[n:]
	return n, nil
}

// --- WAV --------------------------------------------------------------------

type wavDecoder struct {
	r             io.Reader
	sampleRate    int
	channels      int
	bitsPerSample int
	isFloat       bool
	pending       []byte
	inBuf         []byte
}

func newWavDecoder(r io.Reader) (PCMDecoder, error) {
	var head [12]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return nil, fmt.Errorf("decode wav: read RIFF header: %w", err)
	}
	if !bytes.Equal(head[0:4], []byte("RIFF")) || !bytes.Equal(head[8:12], []byte("WAVE")) {
		return nil, fmt.Errorf("decode wav: not a RIFF/WAVE file")
	}

	var (
		fmtFound                  bool
		sampleRate, channels, bps int
		isFloat                   bool
	)
	for {
		var hdr [8]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return nil, fmt.Errorf("decode wav: read chunk header: %w", err)
		}
		id := string(hdr[0:4])
		size := binary.LittleEndian.Uint32(hdr[4:8])
		switch id {
		case "fmt ":
			if size < 16 || size > 1<<20 {
				return nil, fmt.Errorf("decode wav: implausible fmt chunk size %d", size)
			}
			body := make([]byte, size)
			if _, err := io.ReadFull(r, body); err != nil {
				return nil, fmt.Errorf("decode wav: read fmt: %w", err)
			}
			audioFormat := binary.LittleEndian.Uint16(body[0:2])
			channels = int(binary.LittleEndian.Uint16(body[2:4]))
			sampleRate = int(binary.LittleEndian.Uint32(body[4:8]))
			bps = int(binary.LittleEndian.Uint16(body[14:16]))
			switch audioFormat {
			case 1: // PCM
				isFloat = false
			case 3: // IEEE float
				isFloat = true
			case 0xFFFE: // extensible
				if size < 40 {
					return nil, fmt.Errorf("decode wav: extensible fmt too small (%d)", size)
				}
				subFmt := binary.LittleEndian.Uint16(body[24:26])
				isFloat = subFmt == 3
				if subFmt != 1 && subFmt != 3 {
					return nil, fmt.Errorf("decode wav: unsupported extensible sub-format %d", subFmt)
				}
			default:
				return nil, fmt.Errorf("decode wav: unsupported audio format %d (only PCM and IEEE float supported)", audioFormat)
			}
			fmtFound = true
		case "data":
			if !fmtFound {
				return nil, fmt.Errorf("decode wav: data chunk before fmt chunk")
			}
			if channels < 1 || bps < 8 || bps > 32 || sampleRate < 1 {
				return nil, fmt.Errorf("decode wav: invalid params channels=%d bps=%d sr=%d", channels, bps, sampleRate)
			}
			return &wavDecoder{
				r:             r,
				sampleRate:    sampleRate,
				channels:      channels,
				bitsPerSample: bps,
				isFloat:       isFloat,
			}, nil
		default:
			// Skip unknown chunk (and its padding byte if odd size).
			if _, err := io.CopyN(io.Discard, r, int64(size)); err != nil {
				return nil, fmt.Errorf("decode wav: skip chunk %q: %w", id, err)
			}
			if size%2 != 0 {
				var pad [1]byte
				_, _ = io.ReadFull(r, pad[:])
			}
		}
	}
}

func (w *wavDecoder) SampleRate() int { return w.sampleRate }

func (w *wavDecoder) Read(p []byte) (int, error) {
	for len(w.pending) == 0 {
		sampleBytes := w.bitsPerSample / 8
		frameBytes := w.channels * sampleBytes
		if frameBytes == 0 {
			return 0, fmt.Errorf("decode wav: invalid frame size")
		}
		batchFrames := 1024
		readBytes := batchFrames * frameBytes
		if cap(w.inBuf) < readBytes {
			w.inBuf = make([]byte, readBytes)
		}
		buf := w.inBuf[:readBytes]
		n, err := io.ReadFull(w.r, buf)
		if n == 0 {
			if err == nil {
				err = io.EOF
			}
			return 0, err
		}
		// Trim to whole frames.
		n -= n % frameBytes
		if n == 0 {
			return 0, io.ErrUnexpectedEOF
		}
		w.pending = wavBytesToStereoS16LE(buf[:n], w.channels, w.bitsPerSample, w.isFloat, w.pending[:0])
	}
	n := copy(p, w.pending)
	w.pending = w.pending[n:]
	return n, nil
}

// wavBytesToStereoS16LE converts interleaved WAV sample bytes into S16LE
// stereo. Mono inputs are duplicated, >2 channel inputs keep just the first
// two channels.
func wavBytesToStereoS16LE(src []byte, channels, bps int, isFloat bool, dst []byte) []byte {
	sampleBytes := bps / 8
	frameBytes := channels * sampleBytes
	frames := len(src) / frameBytes
	tmp := make([]byte, 0, frames*4)
	for i := 0; i < frames; i++ {
		off := i * frameBytes
		l := extractWavSample(src[off:off+sampleBytes], bps, isFloat)
		var r int32
		if channels >= 2 {
			r = extractWavSample(src[off+sampleBytes:off+2*sampleBytes], bps, isFloat)
		} else {
			r = l
		}
		li := clampInt32ToInt16(l)
		ri := clampInt32ToInt16(r)
		tmp = append(tmp, byte(li), byte(li>>8), byte(ri), byte(ri>>8))
	}
	return append(dst, tmp...)
}

// extractWavSample returns a single sample normalised into the int16 range
// (before clamping).
func extractWavSample(b []byte, bps int, isFloat bool) int32 {
	switch {
	case isFloat && bps == 32:
		bits := binary.LittleEndian.Uint32(b)
		f := math.Float32frombits(bits) * 32767.0
		if f > 32767 {
			f = 32767
		} else if f < -32768 {
			f = -32768
		}
		return int32(f)
	case !isFloat && bps == 16:
		return int32(int16(binary.LittleEndian.Uint16(b)))
	case !isFloat && bps == 24:
		v := int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16
		if v&0x800000 != 0 {
			v |= ^int32(0xFFFFFF)
		}
		return v >> 8 // down to 16-bit
	case !isFloat && bps == 32:
		v := int32(binary.LittleEndian.Uint32(b))
		return v >> 16
	case !isFloat && bps == 8:
		// 8-bit WAV is unsigned with a centre of 128.
		return (int32(b[0]) - 128) << 8
	default:
		return 0
	}
}

// --- conversion helpers -----------------------------------------------------

func clampInt32ToInt16(v int32) int16 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}

func clampFloatToInt16(v float32) int16 {
	s := v * 32767.0
	if s > 32767 {
		return 32767
	}
	if s < -32768 {
		return -32768
	}
	return int16(s)
}

// float32ToStereoS16LE converts an interleaved float32 block with `channels`
// channels and `frames` sample-frames into interleaved S16LE stereo, appending
// to dst.
func float32ToStereoS16LE(src []float32, channels, frames int, dst []byte) []byte {
	tmp := make([]byte, 0, frames*4)
	for i := 0; i < frames; i++ {
		var l, r float32
		switch channels {
		case 1:
			l = src[i]
			r = l
		default:
			l = src[i*channels]
			r = src[i*channels+1]
		}
		li := clampFloatToInt16(l)
		ri := clampFloatToInt16(r)
		tmp = append(tmp, byte(li), byte(li>>8), byte(ri), byte(ri>>8))
	}
	return append(dst, tmp...)
}

// int16ToStereoS16LE converts an interleaved int16 block with `channels`
// channels and `frames` sample-frames into interleaved S16LE stereo, appending
// to dst.
func int16ToStereoS16LE(src []int16, channels, frames int, dst []byte) []byte {
	tmp := make([]byte, 0, frames*4)
	for i := 0; i < frames; i++ {
		var l, r int16
		switch channels {
		case 1:
			l = src[i]
			r = l
		default:
			l = src[i*channels]
			r = src[i*channels+1]
		}
		tmp = append(tmp, byte(l), byte(l>>8), byte(r), byte(r>>8))
	}
	return append(dst, tmp...)
}

// flacFrameToStereoS16LE converts a FLAC frame's subframes (int32 at bitDepth)
// into interleaved S16LE stereo, appending to dst.
func flacFrameToStereoS16LE(fr *frame.Frame, channels, bitDepth int, dst []byte) []byte {
	if channels <= 0 || len(fr.Subframes) == 0 {
		return dst
	}
	shift := bitDepth - 16
	nSamples := fr.Subframes[0].NSamples
	tmp := make([]byte, 0, nSamples*4)
	left := fr.Subframes[0].Samples
	var right []int32
	if channels >= 2 && len(fr.Subframes) >= 2 {
		right = fr.Subframes[1].Samples
	} else {
		right = left
	}
	for i := 0; i < nSamples; i++ {
		var ls, rs int32
		if i < len(left) {
			ls = left[i]
		}
		if i < len(right) {
			rs = right[i]
		}
		if shift > 0 {
			ls >>= shift
			rs >>= shift
		} else if shift < 0 {
			ls <<= uint(-shift)
			rs <<= uint(-shift)
		}
		li := clampInt32ToInt16(ls)
		ri := clampInt32ToInt16(rs)
		tmp = append(tmp, byte(li), byte(li>>8), byte(ri), byte(ri>>8))
	}
	return append(dst, tmp...)
}

// --- Ogg header parsing (used by Icecast SOURCE handler) --------------------

// FindOggHeaderEnd walks Ogg pages in data and returns the byte offset where
// the first audio data page begins, i.e. the first page whose granule
// position is not zero and not 0xFFFFFFFFFFFFFFFF (the continuation marker).
//
// If data does not yet contain a complete non-header page, needMore=true and
// endPos is undefined.
//
// If the buffer is malformed (no OggS magic at offset 0), abort=true signals
// that capture should stop so a non-Ogg stream doesn't hang the caller.
func FindOggHeaderEnd(data []byte) (endPos int, needMore bool, abort bool) {
	offset := 0
	for {
		if offset+27 > len(data) {
			return 0, true, false
		}
		if !bytes.Equal(data[offset:offset+4], []byte("OggS")) {
			return 0, false, true
		}
		numSegments := int(data[offset+26])
		headerLen := 27 + numSegments
		if offset+headerLen > len(data) {
			return 0, true, false
		}
		bodyLen := 0
		for i := 0; i < numSegments; i++ {
			bodyLen += int(data[offset+27+i])
		}
		pageLen := headerLen + bodyLen
		if offset+pageLen > len(data) {
			return 0, true, false
		}
		granule := binary.LittleEndian.Uint64(data[offset+6:])
		if granule != 0 && granule != 0xFFFFFFFFFFFFFFFF {
			return offset, false, false
		}
		offset += pageLen
	}
}
