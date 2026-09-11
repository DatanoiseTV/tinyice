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

// The legacy admin_user/admin_password lift must only bootstrap an empty
// user table. Lifting whenever the name was absent resurrected a deleted
// superadmin on every restart, with its original password.
func TestLegacyAdminIsNotResurrected(t *testing.T) {
	c := &Config{AdminUser: "root", AdminPassword: "$2a$10$x",
		Users: map[string]*User{"other": {Username: "other", Role: RoleSuperAdmin}}}
	c.handleMigrations()
	if _, back := c.Users["root"]; back {
		t.Fatal("deleted legacy admin was recreated from admin_user/admin_password")
	}
	empty := &Config{AdminUser: "root", AdminPassword: "$2a$10$x", Users: map[string]*User{}}
	empty.handleMigrations()
	if _, ok := empty.Users["root"]; !ok {
		t.Fatal("legacy admin must still bootstrap an empty user table")
	}
}
