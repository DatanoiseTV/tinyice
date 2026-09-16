package server

import (
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
