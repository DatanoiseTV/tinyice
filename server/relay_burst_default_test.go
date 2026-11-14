package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DatanoiseTV/tinyice/relay"
)

func postRelay(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	s.apiCreateRelay(w, autoDJRequest(http.MethodPost, "/api/relays", body))
	return w
}

// A client that leaves burst_size out used to store 20 bytes, which is "no
// burst" and made new listeners wait for the next frame. It now gets the same
// default the UI's recommended preset uses.
func TestCreateRelayDefaultsBurst(t *testing.T) {
	for _, body := range []string{
		`{"url":"https://198.51.100.20/stream","mount":"/r1"}`,
		`{"url":"https://198.51.100.20/stream","mount":"/r1","burst_size":0}`,
	} {
		s := newAutoDJTestServer(t)
		s.RelayM = relay.NewRelayManager(s.Relay)
		t.Cleanup(s.RelayM.StopAll)
		if w := postRelay(t, s, body); w.Code != http.StatusOK {
			t.Fatalf("%s -> %d: %s", body, w.Code, w.Body.String())
		}
		// The literal, not the constant: 65536 is the documented default and the
		// value of the UI's recommended "Balanced" preset. Comparing against the
		// constant would let a change to it pass unnoticed.
		if len(s.Config.Relays) != 1 || s.Config.Relays[0].BurstSize != 65536 {
			t.Fatalf("%s stored burst %d, want 65536", body, s.Config.Relays[0].BurstSize)
		}
	}
}

func TestCreateRelayKeepsExplicitBurstAndRejectsBadOnes(t *testing.T) {
	s := newAutoDJTestServer(t)
	s.RelayM = relay.NewRelayManager(s.Relay)
	t.Cleanup(s.RelayM.StopAll)
	if w := postRelay(t, s, `{"url":"https://198.51.100.20/stream","mount":"/r1","burst_size":131072}`); w.Code != http.StatusOK {
		t.Fatalf("explicit burst -> %d: %s", w.Code, w.Body.String())
	}
	if got := s.Config.Relays[0].BurstSize; got != 131072 {
		t.Fatalf("explicit burst changed to %d", got)
	}
	for _, v := range []string{`-1`, `8388609`} {
		if w := postRelay(t, s, `{"url":"https://198.51.100.20/stream","mount":"/r2","burst_size":`+v+`}`); w.Code != http.StatusBadRequest {
			t.Errorf("burst_size %s -> %d, want 400", v, w.Code)
		}
	}
	if len(s.Config.Relays) != 1 {
		t.Fatalf("a rejected burst still created a relay: %d relays", len(s.Config.Relays))
	}
}
