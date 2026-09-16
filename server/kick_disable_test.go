package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/relay"
)

// Kicking a source used to drop the Stream object only: the encoder
// stayed connected, kept reading, and its next write recreated the mount,
// so the kick had no lasting effect. RemoveStream must terminate the
// source connection.
func TestRemoveStreamKicksTheSource(t *testing.T) {
	r := relay.NewRelay(false, nil)
	st := r.GetOrCreateStream("/kickme")
	var closed atomic.Bool
	st.SetSourceCloser(func() { closed.Store(true) })

	r.RemoveStream("/kickme")

	if !closed.Load() {
		t.Fatal("RemoveStream did not terminate the source connection")
	}
}

// A disabled mount must refuse listeners, not just new sources.
func TestDisabledMountRefusesListeners(t *testing.T) {
	s := &Server{
		Config: &config.Config{
			DisabledMounts: map[string]bool{"/off": true},
			Mounts:         map[string]string{"/off": "x"},
		},
		Relay:        relay.NewRelay(false, nil),
		authAttempts: make(map[string]*authAttempt),
		scanAttempts: make(map[string]*scanAttempt),
		done:         make(chan struct{}),
	}
	s.Relay.GetOrCreateStream("/off")

	r := httptest.NewRequest(http.MethodGet, "/off", nil)
	r.RemoteAddr = "198.51.100.7:1"
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { s.handleListener(w, r); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleListener did not return for a disabled mount")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

// HLS outputs were never unregistered — every mount ever requested kept
// its segment ring (up to 90 MPEG-TS segments) and its goroutine for the
// life of the process. The janitor drops outputs with no recent request
// and no live stream, and must NOT drop one whose source is merely
// flapping (the segment loop deliberately survives that).
func TestHLSJanitorDropsOnlyIdleOutputs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Server{
		Config:        &config.Config{},
		Relay:         relay.NewRelay(false, nil),
		hlsOutputs:    make(map[string]*relay.HLSOutput),
		hlsLastAccess: make(map[string]time.Time),
		hlsCtx:        ctx,
		done:          make(chan struct{}),
	}
	for _, m := range []string{"/idle", "/watched", "/live"} {
		st := s.Relay.GetOrCreateStream(m)
		st.SetContentTypeForTest("audio/mpeg")
		if s.RegisterHLS(m) == nil {
			t.Fatalf("could not register HLS for %s", m)
		}
	}
	// /idle: requested long ago, stream gone.
	s.hlsLastAccess["/idle"] = time.Now().Add(-2 * hlsIdleTimeout)
	s.Relay.RemoveStream("/idle")
	// /watched: requested just now, stream gone (source flapping).
	s.hlsLastAccess["/watched"] = time.Now()
	s.Relay.RemoveStream("/watched")
	// /live: requested long ago but still producing.
	s.hlsLastAccess["/live"] = time.Now().Add(-2 * hlsIdleTimeout)

	// One janitor pass, inline (the task's body).
	now := time.Now()
	var stale []string
	s.hlsMu.RLock()
	for mount := range s.hlsOutputs {
		last, seen := s.hlsLastAccess[mount]
		if !seen || now.Sub(last) < hlsIdleTimeout {
			continue
		}
		if _, live := s.Relay.GetStream(mount); live {
			continue
		}
		stale = append(stale, mount)
	}
	s.hlsMu.RUnlock()
	for _, m := range stale {
		s.UnregisterHLS(m)
	}

	if s.getHLSOutput("/idle") != nil {
		t.Error("idle, sourceless output was not unregistered")
	}
	if s.getHLSOutput("/watched") == nil {
		t.Error("output with a recent viewer was dropped during a source flap")
	}
	if s.getHLSOutput("/live") == nil {
		t.Error("output with a live source was dropped")
	}
}
