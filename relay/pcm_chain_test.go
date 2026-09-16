package relay

import (
	"context"
	"io"
	"testing"
)

// The encoders subtract paused time from their wall-clock pacing by type-
// asserting the reader they were handed to pauseAware. The Opus path
// applied its 48 kHz resampler on top of the pause gate, which hid the
// gate behind a wrapper: pausing an Opus AutoDJ on a file that wasn't
// already 48 kHz left the encoder believing it was behind schedule, and
// on resume it dumped the rest of the track into the ring buffer as fast
// as the CPU allowed. The gate must be outermost in every combination.
func TestPCMChainAlwaysExposesThePauseGate(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		format string
		rate   int
		volume float64
	}{
		{"opus at 48k", "opus", 48000, 1},
		{"opus at 44.1k", "opus", 44100, 1},
		{"opus at 44.1k with gain", "opus", 44100, 0.5},
		{"opus at 22.05k with gain", "opus", 22050, 0.25},
		{"mp3 at 44.1k", "mp3", 44100, 1},
		{"mp3 at 44.1k with gain", "mp3", 44100, 0.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Streamer{Format: tc.format, Volume: tc.volume}
			chain := s.buildPCMChain(ctx, io.LimitReader(zeroReader{}, 1<<20), tc.rate)
			if _, ok := chain.(pauseAware); !ok {
				t.Fatalf("%T does not implement pauseAware: the encoder cannot subtract paused time", chain)
			}
		})
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}
