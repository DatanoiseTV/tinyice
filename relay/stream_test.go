package relay

import (
	"sync"
	"testing"
	"time"
)

func TestSubscribeOggEmptyPageOffsetsFallsBackToBurst(t *testing.T) {
	r := NewRelay(false, nil)
	s := r.GetOrCreateStream("/ogg-test")
	s.ContentType = "audio/ogg"
	s.IsOggStream = true
	s.Buffer.Write(make([]byte, 4096))

	offset, _ := s.Subscribe("listener-1", 2048)

	if offset >= s.Buffer.Head {
		t.Fatalf("expected burst offset < Head(%d), got %d", s.Buffer.Head, offset)
	}
	expectedMin := s.Buffer.Head - 2048
	if offset < expectedMin {
		t.Fatalf("expected offset >= %d, got %d", expectedMin, offset)
	}
}

func TestBroadcastAfterCloseNoPanic(t *testing.T) {
	r := NewRelay(false, nil)
	s := r.GetOrCreateStream("/test")
	s.Subscribe("listener-1", 0)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			s.Broadcast([]byte("data"), r)
		}
	}()
	go func() {
		defer wg.Done()
		s.Close()
	}()
	wg.Wait()
}

func TestBroadcastUnsubscribeRaceNoPanic(t *testing.T) {
	r := NewRelay(false, nil)
	s := r.GetOrCreateStream("/race")

	const listeners = 32
	ids := make([]string, listeners)
	for i := range ids {
		ids[i] = "l-" + string(rune('a'+i))
		s.Subscribe(ids[i], 0)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 5000; i++ {
			s.Broadcast([]byte("x"), r)
		}
	}()
	go func() {
		defer wg.Done()
		for _, id := range ids {
			s.Unsubscribe(id)
		}
	}()
	wg.Wait()
}

func TestSubscribeAfterCloseRejects(t *testing.T) {
	r := NewRelay(false, nil)
	s := r.GetOrCreateStream("/test")
	s.Close()

	_, _, ok := s.SubscribeSafe("listener-after-close", 1024)
	if ok {
		t.Fatal("expected SubscribeSafe to return false after Close")
	}
}

// TestSubscribeSkipsBurstWhenSourceIdle is the regression test for the
// stale-burst replay bug: when the producer has been silent for >2s, the
// bytes in the buffer are stale and a new subscriber must start at Head
// rather than being bursted with that old audio.
func TestSubscribeSkipsBurstWhenSourceIdle(t *testing.T) {
	r := NewRelay(false, nil)
	s := r.GetOrCreateStream("/idle")

	s.Broadcast(make([]byte, 8192), r)
	// Backdate LastDataReceived past the freshness window.
	s.mu.Lock()
	s.LastDataReceived = time.Now().Add(-10 * time.Second)
	s.mu.Unlock()

	head := s.Buffer.Head
	start, _ := s.Subscribe("listener-idle", 4096)
	if start != head {
		t.Fatalf("idle-source Subscribe should start at Head=%d, got %d (would replay %d stale bytes)",
			head, start, head-start)
	}
}

// TestSubscribeBurstsWhenSourceFresh confirms the normal burst path still
// works on an active stream — the freshness gate must not break instant
// playback for the common case.
func TestSubscribeBurstsWhenSourceFresh(t *testing.T) {
	r := NewRelay(false, nil)
	s := r.GetOrCreateStream("/fresh")

	// Broadcast sets LastDataReceived to time.Now() itself.
	s.Broadcast(make([]byte, 8192), r)

	head := s.Buffer.Head
	start, _ := s.Subscribe("listener-fresh", 4096)
	if start >= head {
		t.Fatalf("fresh-source Subscribe should burst, start=%d head=%d", start, head)
	}
	if head-start != 4096 {
		t.Fatalf("expected 4096-byte burst, got %d", head-start)
	}
}

// TestBroadcastIgnoresFalseOggMagic is the regression test for the false-
// positive Ogg page detection bug. A "OggS" byte sequence followed by a
// non-zero byte (a real Opus payload pattern) must NOT be recorded into
// PageOffsets; a real header (version byte 0) must.
func TestBroadcastIgnoresFalseOggMagic(t *testing.T) {
	r := NewRelay(false, nil)
	s := r.GetOrCreateStream("/ogg-validation")
	s.ContentType = "audio/ogg"
	s.IsOggStream = true

	// False positive: "OggS" + 0xFF (version != 0). Must not be tracked.
	payload := make([]byte, 64)
	payload[10] = 'O'
	payload[11] = 'g'
	payload[12] = 'g'
	payload[13] = 'S'
	payload[14] = 0xFF
	s.Broadcast(payload, r)

	for i, po := range s.PageOffsets {
		if po != 0 {
			t.Fatalf("PageOffsets[%d]=%d — a false-positive OggS was tracked", i, po)
		}
	}

	// Real header: "OggS" + 0x00. Must be tracked.
	real := make([]byte, 32)
	real[5] = 'O'
	real[6] = 'g'
	real[7] = 'g'
	real[8] = 'S'
	real[9] = 0x00
	expected := s.Buffer.Head + 5
	s.Broadcast(real, r)

	if s.LastPageOffset != expected {
		t.Fatalf("LastPageOffset=%d, expected %d", s.LastPageOffset, expected)
	}
}

// TestBeginSessionWipesOggState is the regression test for the transcoder-
// restart bug: a new producer session must clear PageOffsets, LastPageOffset,
// OggHead, OggHeaderOffset so new subscribers can't align to stale bytes
// from a previous Ogg serial. MinListenerOffset must also snap to Head so
// existing listeners jump forward on their next flushGen check.
func TestBeginSessionWipesOggState(t *testing.T) {
	r := NewRelay(false, nil)
	s := r.GetOrCreateStream("/session")
	s.ContentType = "audio/ogg"
	s.IsOggStream = true

	s.StoreOggHead([]byte{1, 2, 3, 4}, 100)
	s.mu.Lock()
	s.LastPageOffset = 999
	s.PageOffsets[0] = 999
	s.PageIndex = 7
	s.mu.Unlock()

	beforeGen := s.FlushGen()
	s.BeginSession()

	if got := s.GetOggHead(); got != nil {
		t.Fatalf("OggHead should be nil after BeginSession, got %v", got)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.LastPageOffset != 0 {
		t.Fatalf("LastPageOffset should be reset, got %d", s.LastPageOffset)
	}
	if s.PageOffsets[0] != 0 {
		t.Fatalf("PageOffsets[0] should be reset, got %d", s.PageOffsets[0])
	}
	if s.PageIndex != 0 {
		t.Fatalf("PageIndex should be reset, got %d", s.PageIndex)
	}
	if s.MinListenerOffset != s.Buffer.Head {
		t.Fatalf("MinListenerOffset=%d should equal Head=%d", s.MinListenerOffset, s.Buffer.Head)
	}
	if s.flushGen.Load() <= beforeGen {
		t.Fatalf("flushGen should have advanced (was %d, now %d)", beforeGen, s.flushGen.Load())
	}
}

// A subscriber that asks for no burst wants the live edge. The Ogg
// alignment used to walk backwards to the oldest tracked page whenever it
// couldn't find one at or after the requested start — which, for the live
// edge, is always — so WebRTC/WHEP viewers began seconds behind live and
// never caught up.
func TestSubscribeZeroBurstStartsAtLiveEdge(t *testing.T) {
	r := NewRelay(false, nil)
	s := r.GetOrCreateStream("/ogg-live")
	s.mu.Lock()
	s.IsOggStream = true
	s.ContentType = "audio/ogg"
	s.mu.Unlock()

	// Fill the buffer with several tracked Ogg pages.
	page := append([]byte("OggS"), make([]byte, 400)...)
	for i := 0; i < 40; i++ {
		s.Broadcast(page, r)
	}
	head := s.Buffer.Head

	offset, _ := s.Subscribe("whep-viewer", 0)
	if offset != head {
		t.Errorf("zero-burst subscribe started at %d, want the live edge %d (%d bytes behind)",
			offset, head, head-offset)
	}
}

// Subscribing to an already-closed stream used to register a listener
// that nothing would ever signal or close — the reader parked forever.
// Close() has already walked the map by then and never runs again.
func TestSubscribeOnClosedStreamReturnsClosedChannel(t *testing.T) {
	r := NewRelay(false, nil)
	s := r.GetOrCreateStream("/closed")
	s.Close()

	_, ch := s.Subscribe("late", 4096)
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("signal channel delivered a value on a closed stream")
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber on a closed stream parked forever")
	}
}
