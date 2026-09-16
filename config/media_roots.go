package config

import (
	"os"
	"path/filepath"
	"sort"
)

// BrowsableRoots returns the directories the AutoDJ directory browser may
// list, as absolute, symlink-resolved paths. Nothing outside them is
// reachable, so this is the whole sandbox.
//
// An explicit media_roots wins. When the key is absent the defaults make
// the browser useful on an install that already works — the process
// working directory, plus the music directory of every configured AutoDJ
// — without exposing anything the operator has not already pointed the
// server at. A fresh install has no AutoDJs, so an operator who keeps
// their library elsewhere sets media_roots once.
//
// Entries that don't exist, or aren't directories, are dropped: a root
// that isn't there can only produce confusing errors deeper in the UI.
func (c *Config) BrowsableRoots() []string {
	candidates := c.MediaRoots
	if len(candidates) == 0 {
		if cwd, err := os.Getwd(); err == nil {
			candidates = append(candidates, cwd)
		}
		for _, dj := range c.AutoDJs {
			if dj != nil && dj.MusicDir != "" {
				candidates = append(candidates, dj.MusicDir)
			}
		}
	}

	seen := make(map[string]bool, len(candidates))
	roots := make([]string, 0, len(candidates))
	for _, p := range candidates {
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		if seen[abs] {
			continue
		}
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			continue
		}
		seen[abs] = true
		roots = append(roots, abs)
	}
	sort.Strings(roots)
	return roots
}
