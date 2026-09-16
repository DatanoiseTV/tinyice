package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The relay pull has an idle watchdog for upstreams that return headers
// and then go silent (a NAT idle drop half-closes the connection without
// the kernel noticing). The watchdog used to cancel a context derived
// AFTER the request was already in flight, so cancelling it did nothing
// to the parked body.Read and the pull goroutine leaked for the life of
// the process.
func TestRelayPullGivesUpOnASilentUpstream(t *testing.T) {
	old := relayIdleTimeout
	relayIdleTimeout = 250 * time.Millisecond
	t.Cleanup(func() { relayIdleTimeout = old })

	served := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(served)
		// Deliver nothing at all, and hold the response open until the
		// client gives up.
		<-r.Context().Done()
	}))
	defer srv.Close()

	rm := NewRelayManager(NewRelay(false, nil))
	inst := &RelayInstance{URL: srv.URL, Mount: "/pull"}

	done := make(chan struct{})
	go func() {
		rm.performPull(context.Background(), inst)
		close(done)
	}()

	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream never served the request")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("performPull never returned: the idle watchdog cannot interrupt a parked body.Read")
	}
}
