package relay

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/DatanoiseTV/tinyice/logger"
	kazzopus "github.com/kazzmir/opus-go/opus"
)

// Pure-Go Ogg+Opus decoder that tolerates RFC 3533 chained logical
// streams. Each new logical bitstream (BOS page with a fresh serial
// number) carries its own OpusHead/OpusTags pair; the previous
// kazzmir/opus-go parser pinned the serial on first sight and
// returned ErrSerialMismatch on the next page, freezing the pump
// goroutine and silently killing every transcoded mount when the
// upstream rotated the bitstream (which robodj does between tracks).
//
// This decoder uses pion/opus for the raw codec and a small Ogg page
// reader for the container.

// --- low-level Ogg page reader ---------------------------------------------

type oggPage struct {
	headerType byte
	serial     uint32
	granule    uint64
	sequence   uint32
	segments   []byte // segment lacing table (1..255 entries)
	body       []byte // concatenated segment payload
}

func (p *oggPage) bos() bool       { return p.headerType&0x02 != 0 }
func (p *oggPage) continued() bool { return p.headerType&0x01 != 0 }

type oggPageReader struct {
	br *bufio.Reader
	// Reusable header buffer to avoid an allocation per page.
	hdr [27]byte
}

func newOggPageReader(r io.Reader) *oggPageReader {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReaderSize(r, 64*1024)
	}
	return &oggPageReader{br: br}
}

// readPage reads the next Ogg page. It resyncs to "OggS" on garbage but
// does not validate the page CRC — the source is a live stream that we
// trust at the byte level, and a corrupted page is better surfaced as a
// codec decode error than as a hard EOF.
func (pr *oggPageReader) readPage() (*oggPage, error) {
	if err := pr.sync(); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(pr.br, pr.hdr[:]); err != nil {
		return nil, fmt.Errorf("ogg: short header: %w", err)
	}
	if !bytes.Equal(pr.hdr[0:4], []byte("OggS")) {
		return nil, fmt.Errorf("ogg: lost sync at header start")
	}
	if pr.hdr[4] != 0 {
		return nil, fmt.Errorf("ogg: unsupported version %d", pr.hdr[4])
	}
	pg := &oggPage{
		headerType: pr.hdr[5],
		granule:    binary.LittleEndian.Uint64(pr.hdr[6:14]),
		serial:     binary.LittleEndian.Uint32(pr.hdr[14:18]),
		sequence:   binary.LittleEndian.Uint32(pr.hdr[18:22]),
	}
	nSeg := int(pr.hdr[26])
	if nSeg == 0 {
		return pg, nil
	}
	pg.segments = make([]byte, nSeg)
	if _, err := io.ReadFull(pr.br, pg.segments); err != nil {
		return nil, fmt.Errorf("ogg: short segment table: %w", err)
	}
	bodyLen := 0
	for _, s := range pg.segments {
		bodyLen += int(s)
	}
	pg.body = make([]byte, bodyLen)
	if _, err := io.ReadFull(pr.br, pg.body); err != nil {
		return nil, fmt.Errorf("ogg: short page body: %w", err)
	}
	return pg, nil
}

// sync scans forward in br until it finds "OggS" without consuming the
// magic itself. Bounded so a non-Ogg stream errors out instead of
// looping forever.
func (pr *oggPageReader) sync() error {
	const maxScan = 1 << 20 // 1 MiB
	scanned := 0
	for {
		peek, err := pr.br.Peek(4)
		if err != nil {
			return err
		}
		if bytes.Equal(peek, []byte("OggS")) {
			return nil
		}
		if _, err := pr.br.ReadByte(); err != nil {
			return err
		}
		scanned++
		if scanned > maxScan {
			return errors.New("ogg: lost sync (no OggS magic in 1MiB)")
		}
	}
}

// --- chained Opus decoder ---------------------------------------------------

type chainedOpusDecoder struct {
	pr  *oggPageReader
	dec *kazzopus.Decoder

	// Per-logical-stream state.
	currentSerial uint32
	haveSerial    bool
	channels      int
	preSkip       int // remaining preskip samples (per channel) to drop
	headerStage   int // 0=expect OpusHead next, 1=expect OpusTags packet, 2=audio

	// Packet assembly state across pages within one logical stream.
	pending     []byte // partial packet (continued across pages)
	havePending bool

	// PCM output staging.
	pcm []int16 // scratch decode buffer, sized for 120 ms @ 48 kHz * 2 ch
	out []byte  // ready-to-Read S16LE stereo bytes

	// Telemetry. Counters surface decoder health without spamming logs on
	// every per-packet glitch — we log a summary at most once every 5 s.
	decodeErrors atomic.Uint64
	chainRotates atomic.Uint64
	lastReport   time.Time
}

func newChainedOpusDecoder(r io.Reader) (PCMDecoder, error) {
	d := &chainedOpusDecoder{
		pr:          newOggPageReader(r),
		pcm:         make([]int16, 5760*2),
		channels:    2,
		headerStage: 0,
	}
	// Drive the reader until the first OpusHead is parsed so callers
	// see a meaningful error if the stream isn't Ogg-Opus.
	for d.headerStage == 0 {
		if err := d.readOnePage(); err != nil {
			return nil, fmt.Errorf("decode opus: %w", err)
		}
	}
	return d, nil
}

func (d *chainedOpusDecoder) SampleRate() int { return 48000 }

func (d *chainedOpusDecoder) Read(p []byte) (int, error) {
	for len(d.out) == 0 {
		if err := d.readOnePage(); err != nil {
			return 0, err
		}
	}
	n := copy(p, d.out)
	d.out = d.out[n:]
	return n, nil
}

// readOnePage reads one Ogg page and processes its packets according to
// the current logical-stream state. Audio packets append to d.out.
func (d *chainedOpusDecoder) readOnePage() error {
	page, err := d.pr.readPage()
	if err != nil {
		return err
	}

	// A new logical stream begins. Per RFC 7845 §3, the BOS page
	// contains OpusHead alone; the next page(s) carry OpusTags.
	if page.bos() {
		if err := d.parseOpusHead(page); err != nil {
			return err
		}
		if d.haveSerial && page.serial != d.currentSerial {
			d.chainRotates.Add(1)
		}
		d.currentSerial = page.serial
		d.haveSerial = true
		d.headerStage = 1
		d.pending = d.pending[:0]
		d.havePending = false
		return nil
	}

	// Ignore pages from other logical streams. Standard chained
	// streams are sequential (one EOS → next BOS) so this is mostly
	// defensive against interleaved-streams sources.
	if d.haveSerial && page.serial != d.currentSerial {
		return nil
	}

	// Assemble packets via lacing rules: a packet is the concatenation
	// of consecutive segments; a segment of length <255 terminates the
	// packet. A continuation flag on the page indicates that the first
	// segment continues a packet from the previous page.
	if !page.continued() {
		// A non-continued page resets any partial packet (per spec the
		// previous packet would have been completed by a <255 segment).
		d.pending = d.pending[:0]
		d.havePending = false
	} else if !d.havePending {
		// Continuation flag set but no prior packet — corrupted page
		// boundary, skip it rather than abort the stream.
		return nil
	}

	off := 0
	for _, segLen := range page.segments {
		end := off + int(segLen)
		if end > len(page.body) {
			return errors.New("ogg: segment underrun")
		}
		d.pending = append(d.pending, page.body[off:end]...)
		d.havePending = true
		off = end
		if segLen < 255 {
			// Packet boundary.
			pkt := d.pending
			d.pending = make([]byte, 0, 1024)
			d.havePending = false
			if err := d.handlePacket(pkt); err != nil {
				return err
			}
		}
	}
	return nil
}

// parseOpusHead reads the OpusHead identification packet from a BOS page
// and (re-)initialises the Opus decoder. RFC 7845 §5.1.
func (d *chainedOpusDecoder) parseOpusHead(page *oggPage) error {
	if len(page.body) < 19 {
		return errors.New("opus: BOS page too small for OpusHead")
	}
	if !bytes.Equal(page.body[0:8], []byte("OpusHead")) {
		return errors.New("opus: BOS page is not OpusHead")
	}
	version := page.body[8]
	if version>>4 != 0 {
		return fmt.Errorf("opus: unsupported OpusHead major version %d", version>>4)
	}
	ch := int(page.body[9])
	if ch != 1 && ch != 2 {
		return fmt.Errorf("opus: unsupported channel count %d", ch)
	}
	preSkip := int(binary.LittleEndian.Uint16(page.body[10:12]))

	// kazzmir/opus-go is libopus transpiled to pure Go via ccgo. The
	// codec is the RFC 6716 reference, which is far more lenient with
	// real-world Opus packets than hand-written reimplementations and
	// produces sample-accurate output. We discard kazzmir's PacketReader
	// (which has the chained-stream bug that motivated this file) and
	// drive the codec directly with the packets our oggPageReader
	// produces.
	if d.dec != nil {
		_ = d.dec.Close()
	}
	dec, err := kazzopus.NewDecoder(48000, ch)
	if err != nil {
		return fmt.Errorf("opus: init decoder: %w", err)
	}
	d.dec = dec
	d.channels = ch
	d.preSkip = preSkip
	return nil
}

func (d *chainedOpusDecoder) handlePacket(pkt []byte) error {
	switch d.headerStage {
	case 1:
		// OpusTags. We don't read tags, just consume the packet.
		d.headerStage = 2
		return nil
	case 2:
		return d.decodeAudio(pkt)
	}
	return nil
}

func (d *chainedOpusDecoder) decodeAudio(pkt []byte) error {
	if len(pkt) == 0 || d.dec == nil {
		return nil
	}
	// frameSize is the max per-channel samples we want; 5760 == 120 ms
	// at 48 kHz, the Opus packet duration upper bound (RFC 6716).
	n, err := d.dec.Decode(pkt, d.pcm, 5760, false)
	if err != nil {
		// Skip a single bad packet instead of tearing down the stream
		// — chained-stream transitions sometimes leave one ragged
		// packet at the edge. Track the rate so a real decoder problem
		// shows up in logs.
		d.decodeErrors.Add(1)
		d.maybeReport(err)
		return nil
	}
	if n <= 0 {
		return nil
	}
	// Drop preskip samples per channel from the start of this logical
	// stream's audio. RFC 7845 §4.2.
	if d.preSkip > 0 {
		drop := d.preSkip
		if drop > n {
			drop = n
		}
		d.preSkip -= drop
		n -= drop
		if n == 0 {
			return nil
		}
		// Shift the remaining samples to the front of d.pcm.
		copy(d.pcm, d.pcm[drop*d.channels:(drop+n)*d.channels])
	}
	d.out = int16ToStereoS16LE(d.pcm[:n*d.channels], d.channels, n, d.out)
	return nil
}

// maybeReport emits a single warn line at most once per 5 seconds, so a
// burst of decode errors at a chain boundary surfaces in logs without
// drowning everything else. The most recent error message is included
// so an operator can tell whether pion/opus is rejecting a specific
// packet shape vs. random byte-level corruption.
func (d *chainedOpusDecoder) maybeReport(lastErr error) {
	now := time.Now()
	if !d.lastReport.IsZero() && now.Sub(d.lastReport) < 5*time.Second {
		return
	}
	d.lastReport = now
	logger.L.Warnw("decode opus: packet errors",
		"errors", d.decodeErrors.Load(),
		"chain_rotations", d.chainRotates.Load(),
		"last_err", lastErr.Error(),
	)
}
