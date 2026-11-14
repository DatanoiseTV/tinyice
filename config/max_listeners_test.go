package config

import (
	"os"
	"path/filepath"
	"testing"
)

// roundTrip saves cfg and loads it back from disk, which is what a restart does.
func roundTrip(t *testing.T, cfg *Config) *Config {
	t.Helper()
	cfg.ConfigPath = filepath.Join(t.TempDir(), "tinyice.json")
	if err := cfg.SaveConfig(); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadConfig(cfg.ConfigPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return got
}

func TestMaxListenersSurviveRestart(t *testing.T) {
	cases := []struct {
		name   string
		apiSet int
	}{
		{"custom value", 250},
		{"smallest limit", 1},
		{"ceiling", MaxListenersCeiling},
		// 0 = unlimited. Before the fix this came back as 100.
		{"unlimited", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &Config{}
			cfg.SetMaxListenersFromAPI(c.apiSet)
			got := roundTrip(t, cfg)
			if got.MaxListenersForAPI() != c.apiSet {
				t.Fatalf("after restart API value = %d, want %d (stored %d)", got.MaxListenersForAPI(), c.apiSet, got.MaxListeners)
			}
		})
	}
}

func TestUnlimitedIsNotEnforced(t *testing.T) {
	cfg := &Config{}
	cfg.SetMaxListenersFromAPI(0)
	got := roundTrip(t, cfg)
	// The enforcement site is `MaxListeners > 0 && count >= MaxListeners`.
	if got.MaxListeners > 0 {
		t.Fatalf("unlimited must not enforce a limit, stored %d", got.MaxListeners)
	}
}

func TestLegacyZeroStillMeansDefault(t *testing.T) {
	// Older versions wrote 0 for "never configured" (first-run file) and the
	// loader turned it into the default. That must not become "unlimited".
	p := filepath.Join(t.TempDir(), "tinyice.json")
	if err := os.WriteFile(p, []byte(`{"port":"8000","max_listeners":0}`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxListeners != DefaultMaxListeners {
		t.Fatalf("legacy 0 loaded as %d, want %d", got.MaxListeners, DefaultMaxListeners)
	}
	// And a file with the key missing entirely gets the default too.
	if err := os.WriteFile(p, []byte(`{"port":"8000"}`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxListeners != DefaultMaxListeners {
		t.Fatalf("missing key loaded as %d, want %d", got.MaxListeners, DefaultMaxListeners)
	}
}
