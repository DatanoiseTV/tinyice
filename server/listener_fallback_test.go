package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/relay"
)

// A mount with a fallback configured, both currently down: a fresh
// listener must get a prompt 404, not an open request with no response.
// The old loop bounced primary -> fallback -> primary forever, wrote no
// headers, and only checked s.done — so it never noticed the client had
// gone either, leaking a goroutine and a ticker per player reconnect for
// the whole outage.
func TestListenerBothMountsDownGets404Promptly(t *testing.T) {
	s := &Server{
		Config: &config.Config{
			FallbackMounts: map[string]string{"/live": "/backup"},
			Mounts:         map[string]string{"/live": "x", "/backup": "x"},
		},
		Relay:        relay.NewRelay(false, nil),
		authAttempts: make(map[string]*authAttempt),
		scanAttempts: make(map[string]*scanAttempt),
		done:         make(chan struct{}),
	}

	r := httptest.NewRequest(http.MethodGet, "/live", nil)
	r.RemoteAddr = "198.51.100.3:1"
	w := httptest.NewRecorder()

	finished := make(chan struct{})
	go func() { s.handleListener(w, r); close(finished) }()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("handleListener never returned for a fresh listener with both mounts down")
	}
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

// And whatever the state, a listener that hangs up must release its
// handler goroutine — the loop has to watch r.Context().
func TestListenerReturnsWhenClientDisconnects(t *testing.T) {
	s := &Server{
		Config: &config.Config{
			FallbackMounts: map[string]string{"/live": "/backup"},
			Mounts:         map[string]string{"/live": "x", "/backup": "x"},
		},
		Relay:        relay.NewRelay(false, nil),
		authAttempts: make(map[string]*authAttempt),
		scanAttempts: make(map[string]*scanAttempt),
		done:         make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest(http.MethodGet, "/live", nil).WithContext(ctx)
	r.RemoteAddr = "198.51.100.3:1"
	w := httptest.NewRecorder()

	finished := make(chan struct{})
	go func() { s.handleListener(w, r); close(finished) }()
	time.Sleep(200 * time.Millisecond)
	cancel() // client went away
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("handleListener kept running after the client disconnected")
	}
}
