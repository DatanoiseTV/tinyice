package relay

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/kazzmir/opus-go/ogg"
)

// buildPage constructs a well-formed Ogg page with the given header flags and
// a body of `bodyLen` zero bytes. The caller's granule / serial / seq are
// written raw. CRC is computed properly.
func buildPage(headerType byte, granule uint64, serial, seq uint32, bodyLen int) []byte {
	numSegs := (bodyLen + 254) / 255
	if bodyLen == 0 {
		numSegs = 1
	}
	page := make([]byte, 27+numSegs+bodyLen)
	copy(page[0:4], "OggS")
	page[4] = 0          // version
	page[5] = headerType // header type flag
	binary.LittleEndian.PutUint64(page[6:14], granule)
	binary.LittleEndian.PutUint32(page[14:18], serial)
	binary.LittleEndian.PutUint32(page[18:22], seq)
	page[26] = byte(numSegs)
	remaining := bodyLen
	for i := 0; i < numSegs; i++ {
		if remaining >= 255 {
			page[27+i] = 255
			remaining -= 255
		} else {
			page[27+i] = byte(remaining)
			remaining = 0
		}
	}
	// Zero CRC field, then compute.
	page[22], page[23], page[24], page[25] = 0, 0, 0, 0
	binary.LittleEndian.PutUint32(page[22:26], oggCRC(page))
	return page
}

func parsePages(data []byte) []map[string]uint64 {
	var pages []map[string]uint64
	i := 0
	for i < len(data) {
		if i+27 > len(data) || string(data[i:i+4]) != "OggS" {
			break
		}
		numSegs := int(data[i+26])
		hdrLen := 27 + numSegs
		bodyLen := 0
		for j := 0; j < numSegs; j++ {
			bodyLen += int(data[i+27+j])
		}
		pageLen := hdrLen + bodyLen
		if i+pageLen > len(data) {
			break
		}
		pages = append(pages, map[string]uint64{
			"type":    uint64(data[i+5]),
			"granule": binary.LittleEndian.Uint64(data[i+6 : i+14]),
			"serial":  uint64(binary.LittleEndian.Uint32(data[i+14 : i+18])),
			"seq":     uint64(binary.LittleEndian.Uint32(data[i+18 : i+22])),
			"crc":     uint64(binary.LittleEndian.Uint32(data[i+22 : i+26])),
			"len":     uint64(pageLen),
		})
		i += pageLen
	}
	return pages
}

func TestOggPageRewriter_HeadersThenAudio(t *testing.T) {
	// Simulate the real-world layout: a cached BOS + Tags with granule 0,
	// then live audio pages with a large granule from a long-running source.
	bos := buildPage(0x02, 0, 42, 0, 19)  // BOS
	tags := buildPage(0x00, 0, 42, 1, 64) // Tags (still granule 0)
	aud1 := buildPage(0x00, 15_800_000, 42, 1247, 200)
	aud2 := buildPage(0x00, 15_800_960, 42, 1248, 200)

	var out bytes.Buffer
	rw := NewOggPageRewriter(&out)

	// OggHead (BOS + Tags)
	if _, err := rw.Write(append(bos, tags...)); err != nil {
		t.Fatalf("write headers: %v", err)
	}
	// Live audio
	if _, err := rw.Write(append(aud1, aud2...)); err != nil {
		t.Fatalf("write audio: %v", err)
	}

	pages := parsePages(out.Bytes())
	if len(pages) != 4 {
		t.Fatalf("expected 4 pages, got %d", len(pages))
	}
	for i, p := range pages {
		if p["seq"] != uint64(i) {
			t.Errorf("page %d: seq=%d, want %d", i, p["seq"], i)
		}
		if p["serial"] != pages[0]["serial"] {
			t.Errorf("page %d: serial diverged from first page", i)
		}
	}
	// BOS and Tags must keep granule 0.
	if pages[0]["granule"] != 0 || pages[1]["granule"] != 0 {
		t.Errorf("BOS/Tags granule not preserved at 0: %v, %v", pages[0]["granule"], pages[1]["granule"])
	}
	// First audio page anchored at 960 samples; second should advance by the
	// same delta as the source (960 samples between aud1 and aud2).
	if pages[2]["granule"] != 960 {
		t.Errorf("first audio granule=%d, want 960", pages[2]["granule"])
	}
	if pages[3]["granule"] != 1920 {
		t.Errorf("second audio granule=%d, want 1920", pages[3]["granule"])
	}
	// The serial should have been replaced.
	if pages[0]["serial"] == 42 {
		t.Errorf("serial not rewritten (still 42)")
	}
}

func TestOggPageRewriter_ChunkedWrites(t *testing.T) {
	// Verify the rewriter handles pages split across multiple Write calls.
	bos := buildPage(0x02, 0, 1, 0, 19)
	aud := buildPage(0x00, 9_600_000, 1, 200, 48)

	var out bytes.Buffer
	rw := NewOggPageRewriter(&out)

	all := append(bos, aud...)
	// Feed 17 bytes at a time.
	for i := 0; i < len(all); i += 17 {
		end := i + 17
		if end > len(all) {
			end = len(all)
		}
		if _, err := rw.Write(all[i:end]); err != nil {
			t.Fatalf("chunked write: %v", err)
		}
	}
	pages := parsePages(out.Bytes())
	if len(pages) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(pages))
	}
	if pages[0]["granule"] != 0 {
		t.Errorf("BOS granule=%d, want 0", pages[0]["granule"])
	}
	if pages[1]["granule"] != 960 {
		t.Errorf("audio granule=%d, want 960 (anchored)", pages[1]["granule"])
	}
}

func TestOggCRC_ZeroInput(t *testing.T) {
	if oggCRC(nil) != 0 {
		t.Errorf("CRC(nil)=%d, want 0", oggCRC(nil))
	}
}

// TestOggCRC_InteropWithKazzmir exercises our CRC implementation through a
// real Ogg PageReader (with CRC verification on). Any bug in poly, bit order,
// init value, or masking would cause the reader to reject the page.
func TestOggCRC_InteropWithKazzmir(t *testing.T) {
	page := buildPage(0x02, 0, 0xBADCAFE, 0, 19)
	pr := ogg.NewPageReader(bytes.NewReader(page))
	pr.VerifyCRC = true
	got, err := pr.ReadPage()
	if err != nil {
		t.Fatalf("kazzmir rejected our page: %v", err)
	}
	if got.BitstreamSerial != 0xBADCAFE {
		t.Errorf("serial mismatch: got %x, want %x", got.BitstreamSerial, 0xBADCAFE)
	}
}

// TestOggPageRewriter_ChainedBOSResetsSeq exercises the chained-logical-stream
// case: after a stream has been running for a while, a new BOS page arrives.
// The rewriter must emit that BOS at seq=0 (not continue the counter) and
// use a fresh bitstream serial so decoders see a proper chained stream.
func TestOggPageRewriter_ChainedBOSResetsSeq(t *testing.T) {
	bos1 := buildPage(0x02, 0, 1, 0, 19)
	tags1 := buildPage(0x00, 0, 1, 1, 32)
	aud1 := buildPage(0x00, 960, 1, 2, 64)
	bos2 := buildPage(0x02, 0, 2, 0, 19) // new chained BOS, different raw serial
	aud2 := buildPage(0x00, 960, 2, 1, 64)

	var out bytes.Buffer
	rw := NewOggPageRewriter(&out)
	rw.Write(bos1)
	rw.Write(tags1)
	rw.Write(aud1)
	rw.Write(bos2)
	rw.Write(aud2)

	pages := parsePages(out.Bytes())
	if len(pages) != 5 {
		t.Fatalf("expected 5 pages, got %d", len(pages))
	}
	// First chain: seq 0, 1, 2 with one serial.
	firstSerial := pages[0]["serial"]
	for i := 0; i < 3; i++ {
		if pages[i]["seq"] != uint64(i) {
			t.Errorf("first chain page %d: seq=%d, want %d", i, pages[i]["seq"], i)
		}
		if pages[i]["serial"] != firstSerial {
			t.Errorf("first chain page %d: serial changed", i)
		}
	}
	// Second chain: seq should reset to 0; serial should be different.
	if pages[3]["seq"] != 0 {
		t.Errorf("chained BOS: seq=%d, want 0", pages[3]["seq"])
	}
	if pages[3]["serial"] == firstSerial {
		t.Errorf("chained BOS: serial did not change on new logical stream")
	}
	if pages[4]["seq"] != 1 {
		t.Errorf("page after chained BOS: seq=%d, want 1", pages[4]["seq"])
	}
}

// TestOggPageRewriter_OpusPreSkipAnchor verifies the first audio page granule
// equals PreSkip + 960 when the BOS carries an OpusHead packet.
func TestOggPageRewriter_OpusPreSkipAnchor(t *testing.T) {
	// Build a BOS page whose body is a minimal OpusHead with PreSkip = 312.
	opusHead := make([]byte, 19)
	copy(opusHead[0:8], "OpusHead")
	opusHead[8] = 1                                     // version
	opusHead[9] = 2                                     // channels
	binary.LittleEndian.PutUint16(opusHead[10:12], 312) // PreSkip
	binary.LittleEndian.PutUint32(opusHead[12:16], 48000)
	// 16:18 = OutputGain, 18 = ChannelMappingFamily
	bos := buildPageWithBody(0x02, 0, 42, 0, opusHead)

	// Audio page with a large raw granule (as if mid-stream).
	aud := buildPage(0x00, 15_000_000, 42, 500, 64)

	var out bytes.Buffer
	rw := NewOggPageRewriter(&out)
	rw.Write(bos)
	rw.Write(aud)

	pages := parsePages(out.Bytes())
	if len(pages) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(pages))
	}
	// First audio page should be anchored at PreSkip + 960 = 1272.
	const wantGranule uint64 = 312 + 960
	if pages[1]["granule"] != wantGranule {
		t.Errorf("first audio granule=%d, want %d (PreSkip=312 + 960)", pages[1]["granule"], wantGranule)
	}
}

// buildPageWithBody constructs a single-segment-table page carrying the
// given body bytes verbatim.
func buildPageWithBody(headerType byte, granule uint64, serial, seq uint32, body []byte) []byte {
	page := make([]byte, 27+1+len(body))
	copy(page[0:4], "OggS")
	page[4] = 0
	page[5] = headerType
	binary.LittleEndian.PutUint64(page[6:14], granule)
	binary.LittleEndian.PutUint32(page[14:18], serial)
	binary.LittleEndian.PutUint32(page[18:22], seq)
	page[26] = 1
	page[27] = byte(len(body))
	copy(page[28:], body)
	page[22], page[23], page[24], page[25] = 0, 0, 0, 0
	binary.LittleEndian.PutUint32(page[22:26], oggCRC(page))
	return page
}

// TestOggPageRewriter_RejectsFakeOggS simulates the real-world failure mode:
// a real page is preceded by payload bytes that happen to contain the four
// ASCII characters "OggS" inside audio data. Before CRC verification the
// rewriter would parse that as a page header, emit garbage, and desync for
// the rest of the connection. After CRC verification the fake marker is
// skipped and the real page is emitted cleanly.
func TestOggPageRewriter_RejectsFakeOggS(t *testing.T) {
	real := buildPage(0x00, 9_600_000, 5, 100, 64)
	// Build a buffer: [fake "OggS" + plausible-looking nonsense] + [real page].
	var stream []byte
	stream = append(stream, "OggS"...)
	// Plausible-looking header bytes that WILL fail CRC.
	stream = append(stream, 0, 0) // version, type
	var gp [8]byte
	binary.LittleEndian.PutUint64(gp[:], 12345)
	stream = append(stream, gp[:]...)
	stream = append(stream, 1, 2, 3, 4)    // serial
	stream = append(stream, 5, 6, 7, 8)    // seq
	stream = append(stream, 9, 10, 11, 12) // fake CRC — definitely wrong
	stream = append(stream, 1)             // num_segments
	stream = append(stream, 8)             // one segment, 8 bytes
	stream = append(stream, "garbage!"...)
	stream = append(stream, real...)

	var out bytes.Buffer
	rw := NewOggPageRewriter(&out)
	if _, err := rw.Write(stream); err != nil {
		t.Fatalf("write: %v", err)
	}
	pages := parsePages(out.Bytes())
	if len(pages) != 1 {
		t.Fatalf("expected exactly 1 valid page, got %d", len(pages))
	}
	if pages[0]["granule"] != 960 {
		t.Errorf("surviving page granule=%d, want 960 (rebased)", pages[0]["granule"])
	}
}

// TestOggPageRewriter_CRCRoundTrip makes sure the CRC the rewriter writes
// back onto a page matches what the Ogg spec expects: CRC over the entire
// page with the CRC field zeroed.
func TestOggPageRewriter_CRCRoundTrip(t *testing.T) {
	bos := buildPage(0x02, 0, 7, 0, 19)
	aud := buildPage(0x00, 9_600_000, 7, 100, 32)

	var out bytes.Buffer
	rw := NewOggPageRewriter(&out)
	if _, err := rw.Write(append(bos, aud...)); err != nil {
		t.Fatalf("write: %v", err)
	}

	data := out.Bytes()
	// Walk each emitted page, zero the CRC, recompute, and compare.
	i := 0
	for i < len(data) {
		numSegs := int(data[i+26])
		hdrLen := 27 + numSegs
		bodyLen := 0
		for j := 0; j < numSegs; j++ {
			bodyLen += int(data[i+27+j])
		}
		pageLen := hdrLen + bodyLen
		claimed := binary.LittleEndian.Uint32(data[i+22 : i+26])
		tmp := append([]byte(nil), data[i:i+pageLen]...)
		tmp[22], tmp[23], tmp[24], tmp[25] = 0, 0, 0, 0
		expect := oggCRC(tmp)
		if claimed != expect {
			t.Errorf("page at offset %d: CRC=%08x, recomputed=%08x", i, claimed, expect)
		}
		i += pageLen
	}
}
