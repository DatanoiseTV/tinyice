package server

import (
	"testing"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
)

func init() {
	logger.Init("error", false, "")
}

func TestIPWhitelistAndBanning(t *testing.T) {
	cfg := &config.Config{
		BannedIPs:      []string{"1.2.3.4", "10.0.0.0/24"},
		WhitelistedIPs: []string{"1.2.3.4", "192.168.1.1"},
	}
	s := &Server{
		Config:       cfg,
		authAttempts: make(map[string]*authAttempt),
		scanAttempts: make(map[string]*scanAttempt),
	}

	// 1.2.3.4 is both banned and whitelisted. Whitelist should win.
	if s.isBanned("1.2.3.4:1234") {
		t.Errorf("1.2.3.4 should NOT be banned as it is whitelisted")
	}

	// 10.0.0.5 is banned (range) and NOT whitelisted.
	if !s.isBanned("10.0.0.5:1234") {
		t.Errorf("10.0.0.5 should be banned")
	}

	// 192.168.1.1 is whitelisted.
	if !s.isWhitelisted("192.168.1.1:1234") {
		t.Errorf("192.168.1.1 should be whitelisted")
	}

	// 127.0.0.1 should be always whitelisted
	if !s.isWhitelisted("127.0.0.1:1234") {
		t.Errorf("127.0.0.1 should be always whitelisted")
	}
	if !s.isWhitelisted("[::1]:1234") {
		t.Errorf("::1 should be always whitelisted")
	}

	// Verify scan attempt lockout behavior
	ip := "8.8.8.8"
	path := "/wp-admin"

	// Record 9 attempts on SAME path
	for i := 0; i < 9; i++ {
		s.recordScanAttempt(ip, path)
	}

	if s.isBanned(ip) {
		t.Errorf("IP %s should not be banned yet after 9 attempts", ip)
	}

	// 10th attempt should ban it
	s.recordScanAttempt(ip, path)
	if !s.isBanned(ip) {
		t.Errorf("IP %s should be banned after 10 attempts on SAME path (due to fix)", ip)
	}

	// Verify whitelisted IP is NOT banned even after many attempts
	wip := "192.168.1.1"
	for i := 0; i < 20; i++ {
		s.recordScanAttempt(wip, path)
	}
	if s.isBanned(wip) {
		t.Errorf("Whitelisted IP %s should NOT be banned even after many attempts", wip)
	}
}
