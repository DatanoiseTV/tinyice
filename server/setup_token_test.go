package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/DatanoiseTV/tinyice/config"
)

// A server that still needs setup but holds no setup token (the state a
// restart mid-setup used to produce) must refuse to complete setup for an
// empty client token. crypto/subtle.ConstantTimeCompare("", "") == 1, so
// the plain comparison let anyone become superadmin.
func TestSetupCompleteRefusesEmptyToken(t *testing.T) {
	s := &Server{
		Config: &config.Config{
			ConfigPath: filepath.Join(t.TempDir(), "tinyice.json"),
			Users:      map[string]*config.User{},
			Mounts:     map[string]string{},
		},
		setupToken: "",
		sessions:   make(map[string]*session),
	}
	r := httptest.NewRequest(http.MethodPost, "/setup/complete",
		bytes.NewBufferString(`{"token":"","username":"evil","password":"pw12345678"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleSetupComplete(w, r)

	if w.Code == http.StatusOK || s.Config.SetupComplete {
		t.Fatalf("setup completed with an empty token (status %d, SetupComplete=%v)", w.Code, s.Config.SetupComplete)
	}
	if _, made := s.Config.Users["evil"]; made {
		t.Fatal("an admin user was created with an empty setup token")
	}
}
