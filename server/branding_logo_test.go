package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DatanoiseTV/tinyice/config"
)

// /branding/logo hands Config.LogoPath to http.ServeFile with no auth.
// LogoPath used to be settable, verbatim, by any authenticated user, so
// pointing it at the config file published every secret in it. The path
// must be refused at write time AND at serve time.
func TestLogoPathCannotEscapeBrandingDir(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })

	secret := filepath.Join(dir, "tinyice.json")
	os.WriteFile(secret, []byte(`{"admin_password":"hunter2"}`), 0600)
	os.MkdirAll("branding", 0755)
	os.WriteFile(filepath.Join("branding", "logo.png"), []byte("PNG"), 0644)

	s := &Server{Config: &config.Config{ConfigPath: secret}}

	serve := func(p string) *httptest.ResponseRecorder {
		s.Config.LogoPath = p
		w := httptest.NewRecorder()
		s.handleServeLogo(w, httptest.NewRequest(http.MethodGet, "/branding/logo", nil))
		return w
	}

	for _, bad := range []string{"tinyice.json", secret, "../tinyice.json", "branding/../tinyice.json", "branding", "/etc/passwd"} {
		if w := serve(bad); w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "hunter2") {
			t.Errorf("LogoPath=%q served status %d body %q; want 404 with no secret", bad, w.Code, w.Body.String())
		}
	}
	if w := serve(filepath.Join("branding", "logo.png")); w.Code != http.StatusOK || w.Body.String() != "PNG" {
		t.Errorf("legitimate uploaded logo not served: %d %q", w.Code, w.Body.String())
	}

	// And the write side refuses anything outside branding/.
	for _, bad := range []string{"tinyice.json", "../x", "branding"} {
		if logoPathAllowed(bad) {
			t.Errorf("logoPathAllowed(%q) = true", bad)
		}
	}
	if !logoPathAllowed("") || !logoPathAllowed("branding/logo.png") {
		t.Error("empty and branding/logo.png must be allowed")
	}
}
