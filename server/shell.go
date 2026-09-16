package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/DatanoiseTV/tinyice/logger"
)

// `all:` is required because Vite emits underscore-prefixed vendor
// chunks (e.g. _commonjsHelpers-*.js) when a dependency uses
// CommonJS modules; the default `//go:embed` directive silently
// skips files starting with `_` or `.`, so without the prefix those
// chunks would 404 at runtime even though they're present on disk.
//
//go:generate sh -c "cd frontend && npm install --silent && npm run build"
//go:embed all:frontend/dist
var frontendDistFS embed.FS

// ShellRenderer serves the new Preact-based frontend.
// It reads Vite's built HTML files and injects initial page data.
type ShellRenderer struct {
	distFS    fs.FS
	assetFS   fs.FS
	htmlCache map[string]string
}

// NewShellRenderer creates a ShellRenderer from the embedded frontend dist.
func NewShellRenderer() *ShellRenderer {
	distFS, err := fs.Sub(frontendDistFS, "frontend/dist")
	if err != nil {
		logger.L.Fatalf("Failed to access frontend dist: %v", err)
	}

	assetFS, err := fs.Sub(distFS, "assets")
	if err != nil {
		logger.L.Fatalf("Failed to access frontend assets: %v", err)
	}

	// Pre-read all HTML entry files
	cache := make(map[string]string)
	entries, _ := fs.ReadDir(distFS, "src/entries")
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".html") {
			data, err := fs.ReadFile(distFS, "src/entries/"+e.Name())
			if err != nil {
				logger.L.Warnf("Failed to read entry %s: %v", e.Name(), err)
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".html")
			cache[name] = string(data)
		}
	}

	logger.L.Infof("ShellRenderer loaded %d frontend entry pages", len(cache))

	return &ShellRenderer{distFS: distFS, assetFS: assetFS, htmlCache: cache}
}

// Render serves a Vite-built HTML page, injecting initial data as window.__TINYICE__.
func (sr *ShellRenderer) Render(w http.ResponseWriter, page string, title string, data any) {
	html, ok := sr.htmlCache[page]
	if !ok {
		logger.L.Errorf("No frontend entry for page: %s", page)
		http.Error(w, "Page not found", http.StatusNotFound)
		return
	}

	// Serialize initial data
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Inject __TINYICE__ data and title into the HTML
	dataScript := fmt.Sprintf(`<script>window.__TINYICE__=%s</script>`, jsonBytes)

	// Replace </head> to inject title, and <div id="app"> to inject data before it
	html = strings.Replace(html, "<title>TinyIce</title>", fmt.Sprintf("<title>%s</title>", title), 1)
	html = strings.Replace(html, `<div id="app"></div>`, dataScript+"\n"+`<div id="app"></div>`, 1)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Always re-validate the shell HTML — its `<script src=…>` tags
	// reference Vite-hashed asset names that change every build.
	// A cached shell pointing at a deleted hash is what causes the
	// classic "404 + text/plain MIME refused" stall after a deploy.
	// no-cache (NOT no-store) lets the browser re-use the body if
	// the ETag still matches; stale browsers re-validate cheaply.
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(html))
}

// AssetHandler returns an http.Handler that serves the frontend asset
// files (JS/CSS). Vite-hashed file names are content-addressed by
// design — once a hash exists it serves the same bytes forever — so
// it's safe to mark them immutable for a year.
func (sr *ShellRenderer) AssetHandler() http.Handler {
	fs := http.FileServer(http.FS(sr.assetFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		fs.ServeHTTP(w, r)
	})
}

// FileServer returns an http.Handler that serves all frontend dist files.
func (sr *ShellRenderer) FileServer() http.Handler {
	return http.FileServer(http.FS(sr.distFS))
}

// BrandingData returns the branding data structure for injection into page data.
func (s *Server) BrandingData() map[string]any {
	logoURL := interface{}(nil)
	if s.Config.LogoPath != "" {
		logoURL = "/branding/logo"
	}
	return map[string]any{
		"logoUrl":         logoURL,
		"accentColor":     s.Config.AccentColor,
		"landingMarkdown": s.Config.LandingMarkdown,
	}
}

// BasePageData returns the common data injected into all pages.
func (s *Server) BasePageData(csrfToken string) map[string]any {
	var oidcProviders []map[string]string
	for _, p := range s.Config.OIDCProviders {
		if p.Enabled {
			oidcProviders = append(oidcProviders, map[string]string{
				"id": p.ID, "name": p.Name, "icon": p.Icon,
			})
		}
	}

	passkeysEnabled := s.webAuthn != nil

	return map[string]any{
		"csrfToken":       csrfToken,
		"version":         s.Version,
		"pageTitle":       s.Config.PageTitle,
		"pageSubtitle":    s.Config.PageSubtitle,
		"branding":        s.BrandingData(),
		"passkeysEnabled": passkeysEnabled,
		"oidcProviders":   oidcProviders,
	}
}
