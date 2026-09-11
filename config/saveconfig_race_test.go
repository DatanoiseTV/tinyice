package config

import (
	"path/filepath"
	"sync"
	"testing"
)

// SaveConfig marshals the live struct while handlers mutate its maps, and
// it runs from a background timer as well as from handlers. Without a
// shared lock that is Go's fatal "concurrent map iteration and map write"
// — not a panic, the process just ends. Mutators go through LockMaps.
func TestSaveConfigConcurrentWithMapWrites(t *testing.T) {
	c := &Config{
		ConfigPath:    filepath.Join(t.TempDir(), "tinyice.json"),
		Users:         map[string]*User{},
		Mounts:        map[string]string{},
		VisibleMounts: map[string]bool{},
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			if err := c.SaveConfig(); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 3000; i++ {
			c.LockMaps()
			c.Users["u"] = &User{Username: "u", Mounts: map[string]string{"/m": "x"}}
			c.Mounts["/m"] = "x"
			c.VisibleMounts["/m"] = i%2 == 0
			delete(c.Users, "u")
			c.UnlockMaps()
		}
	}()
	wg.Wait()
}
