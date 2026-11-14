package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DatanoiseTV/tinyice/config"
)

func putSettings(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	s.apiUpdateSettings(w, autoDJRequest(http.MethodPut, "/api/settings", body))
	return w
}

func getMaxListeners(t *testing.T, s *Server) float64 {
	t.Helper()
	w := httptest.NewRecorder()
	s.apiGetSettings(w, autoDJRequest(http.MethodGet, "/api/settings", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("GET settings = %d: %s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	v, ok := out["max_listeners"].(float64)
	if !ok {
		t.Fatalf("max_listeners missing or not a number: %v", out["max_listeners"])
	}
	return v
}

// The limit the operator chose must be what the API reports, what is stored on
// disk, and what a restarted server loads: custom values and unlimited (0).
func TestSettingsMaxListenersPersist(t *testing.T) {
	for _, want := range []int{250, 1, 0} {
		s := newAutoDJTestServer(t)
		w := putSettings(t, s, `{"max_listeners": `+itoa(want)+`}`)
		if w.Code != http.StatusOK {
			t.Fatalf("PUT %d = %d: %s", want, w.Code, w.Body.String())
		}
		if got := getMaxListeners(t, s); int(got) != want {
			t.Fatalf("GET after PUT %d = %v", want, got)
		}
		reloaded, err := config.LoadConfig(s.Config.ConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		if got := reloaded.MaxListenersForAPI(); got != want {
			t.Fatalf("after restart: %d, want %d", got, want)
		}
	}
}

// Invalid input must be refused with 400 and change nothing, including other
// fields sent in the same request.
func TestSettingsMaxListenersRejectsInvalid(t *testing.T) {
	bad := []string{`-1`, `1.5`, `1000001`, `"100"`, `null`, `true`}
	for _, v := range bad {
		s := newAutoDJTestServer(t)
		s.Config.MaxListeners = 77
		s.Config.Location = "before"
		w := putSettings(t, s, `{"location": "after", "max_listeners": `+v+`}`)
		if w.Code != http.StatusBadRequest {
			t.Errorf("max_listeners=%s: status %d, want 400", v, w.Code)
			continue
		}
		if s.Config.MaxListeners != 77 || s.Config.Location != "before" {
			t.Errorf("max_listeners=%s half-applied: max=%d location=%q", v, s.Config.MaxListeners, s.Config.Location)
		}
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
