package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/relay"
)

// audioExts are the extensions the AutoDJ can actually play. The browser
// counts them per directory so an operator can tell a music library from
// its neighbours without opening every candidate.
var audioExts = map[string]bool{
	".mp3": true, ".ogg": true, ".opus": true, ".flac": true, ".wav": true,
}

const (
	// browseMaxEntries bounds one response. A directory with more
	// subdirectories than this is almost certainly not what the operator
	// is looking for, and the truncation is reported rather than silent.
	browseMaxEntries = 500
	// browseMaxCounted bounds how many child directories get a track
	// count, and browseCountBudget bounds the wall-clock spent on it.
	// Each count is one os.ReadDir, which on a network mount is not free.
	browseMaxCounted  = 100
	browseCountBudget = 250 * time.Millisecond
)

// confineToRoots resolves target and returns it only if it sits at or
// below one of roots. The per-root check is relay.ConfineToDir, so the
// browser inherits exactly the confinement the MPD path arguments use —
// symlinks resolved on both sides, so a link inside a root that points
// out of it is refused rather than followed.
func confineToRoots(roots []string, target string) (string, bool) {
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", false
	}
	for _, root := range roots {
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			continue
		}
		if full, err := relay.ConfineToDir(root, rel); err == nil {
			return full, true
		}
	}
	return "", false
}

// countChildren reports how many playable files and how many
// subdirectories sit directly in dir. It does not recurse: the operator
// is choosing one directory, and a recursive walk over a large library
// would be the expensive part of every listing.
//
// The directory count matters as much as the track count. A library's
// parent holds no tracks of its own, and reporting only "no tracks"
// against it reads as "nothing here" for exactly the folder the operator
// is looking at.
func countChildren(dir string) (tracks, dirs int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	for _, e := range entries {
		// Dotfiles are hidden from the listing, so counting them here
		// would report a number the operator cannot see.
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() {
			dirs++
			continue
		}
		if audioExts[strings.ToLower(filepath.Ext(e.Name()))] {
			tracks++
		}
	}
	return tracks, dirs
}

type browseEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// TrackCount and DirCount are -1 when counting was skipped (too many
	// siblings, or the time budget ran out) so the UI can say "unknown"
	// instead of showing an empty-looking directory as empty.
	TrackCount int `json:"track_count"`
	DirCount   int `json:"dir_count"`
}

type browseResponse struct {
	Roots []string `json:"roots"`
	// Path is "" at the top level, where Entries are the roots themselves.
	Path string `json:"path"`
	// Parent is "" when Path is a root: there is nowhere above to go.
	Parent     string        `json:"parent"`
	TrackCount int           `json:"track_count"`
	DirCount   int           `json:"dir_count"`
	Entries    []browseEntry `json:"entries"`
	Truncated  bool          `json:"truncated"`
}

// apiBrowseMediaDirs lists directories for the AutoDJ music-directory
// picker. Read-only, directories only, and confined to the configured
// media roots — see config.BrowsableRoots for what those are.
//
// Superadmin, matching apiCreateAutoDJ / apiUpdateAutoDJ: choosing a
// music directory is part of creating an AutoDJ, and that already
// requires superadmin because it can register shell commands.
func (s *Server) apiBrowseMediaDirs(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	roots := s.Config.BrowsableRoots()
	resp := browseResponse{Roots: roots, Entries: []browseEntry{}}

	requested := r.URL.Query().Get("path")
	if requested == "" {
		// Top level: the roots are the entries.
		deadline := time.Now().Add(browseCountBudget)
		for _, root := range roots {
			tracks, dirs := -1, -1
			if time.Now().Before(deadline) {
				tracks, dirs = countChildren(root)
			}
			resp.Entries = append(resp.Entries, browseEntry{
				Name: root, Path: root, TrackCount: tracks, DirCount: dirs,
			})
		}
		jsonResponse(w, resp)
		return
	}

	dir, allowed := confineToRoots(roots, requested)
	if !allowed {
		jsonError(w, "Path is outside every configured media root", http.StatusForbidden)
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		jsonError(w, "Cannot read directory", http.StatusNotFound)
		return
	}

	resp.Path = dir
	resp.TrackCount = 0
	// Parent stays "" when dir is itself a root, so the UI cannot walk
	// above the sandbox even visually.
	for _, root := range roots {
		if dir != root && strings.HasPrefix(dir, root+string(filepath.Separator)) {
			resp.Parent = filepath.Dir(dir)
			break
		}
	}

	deadline := time.Now().Add(browseCountBudget)
	counted := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !e.IsDir() {
			if audioExts[strings.ToLower(filepath.Ext(e.Name()))] {
				resp.TrackCount++
			}
			continue
		}
		resp.DirCount++
		if len(resp.Entries) >= browseMaxEntries {
			resp.Truncated = true
			continue
		}
		child := filepath.Join(dir, e.Name())
		tracks, dirs := -1, -1
		if counted < browseMaxCounted && time.Now().Before(deadline) {
			tracks, dirs = countChildren(child)
			counted++
		}
		resp.Entries = append(resp.Entries, browseEntry{
			Name: e.Name(), Path: child, TrackCount: tracks, DirCount: dirs,
		})
	}

	jsonResponse(w, resp)
}
