package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
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

	// Verify scan attempt lockout behavior: a legit listener hammering a
	// single offline mount path should *never* trigger the ban — only a
	// scanner touching many distinct paths should.
	ip := "8.8.8.8"

	for i := 0; i < 200; i++ {
		s.recordScanAttempt(ip, "/live")
	}
	if s.isBanned(ip) {
		t.Errorf("IP %s should NOT be banned for repeated 404s on the same path", ip)
	}

	// 25 distinct paths is the scanner threshold.
	scanner := "8.8.4.4"
	for i := 0; i < 24; i++ {
		s.recordScanAttempt(scanner, fmt.Sprintf("/probe-%d", i))
	}
	if s.isBanned(scanner) {
		t.Errorf("IP %s should not be banned yet after 24 distinct paths", scanner)
	}
	s.recordScanAttempt(scanner, "/probe-24")
	if !s.isBanned(scanner) {
		t.Errorf("IP %s should be banned after 25 distinct paths", scanner)
	}

	// Verify whitelisted IP is NOT banned even after many attempts
	wip := "192.168.1.1"
	for i := 0; i < 50; i++ {
		s.recordScanAttempt(wip, fmt.Sprintf("/wp-%d", i))
	}
	if s.isBanned(wip) {
		t.Errorf("Whitelisted IP %s should NOT be banned even after many attempts", wip)
	}
}

// TestClientIPTrustedProxy pins the behaviour trusted_proxies promises:
// X-Forwarded-For is honoured only when the direct peer is a configured
// proxy, and the configured entry may be written in any equivalent form.
func TestClientIPTrustedProxy(t *testing.T) {
	s := &Server{Config: &config.Config{
		TrustedProxies: []string{"10.0.0.1", "192.168.5.0/24", "2001:db8::1"},
	}}

	req := func(remote string, headers map[string]string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = remote
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		return r
	}

	cases := []struct {
		name    string
		remote  string
		headers map[string]string
		want    string
	}{
		{
			name:    "trusted proxy, single XFF entry",
			remote:  "10.0.0.1:54321",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.7"},
			want:    "203.0.113.7",
		},
		{
			name:    "trusted proxy, XFF chain uses left-most",
			remote:  "10.0.0.1:54321",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.7, 10.0.0.1"},
			want:    "203.0.113.7",
		},
		{
			name:    "trusted proxy, XFF entry carries a port",
			remote:  "10.0.0.1:54321",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.7:9999"},
			want:    "203.0.113.7",
		},
		{
			name:    "trusted proxy, X-Real-IP fallback",
			remote:  "10.0.0.1:54321",
			headers: map[string]string{"X-Real-IP": "203.0.113.9"},
			want:    "203.0.113.9",
		},
		{
			name:    "trusted proxy via CIDR",
			remote:  "192.168.5.44:1",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.7"},
			want:    "203.0.113.7",
		},
		{
			// A dual-stack listener reports an IPv4 proxy as an
			// IPv4-mapped IPv6 address; the config says "10.0.0.1".
			name:    "trusted proxy as IPv4-mapped IPv6",
			remote:  "[::ffff:10.0.0.1]:443",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.7"},
			want:    "203.0.113.7",
		},
		{
			name:    "trusted IPv6 proxy written non-canonically",
			remote:  "[2001:0db8:0000::1]:443",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.7"},
			want:    "203.0.113.7",
		},
		{
			name:    "untrusted peer cannot spoof XFF",
			remote:  "198.51.100.4:1234",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.7"},
			want:    "198.51.100.4",
		},
		{
			name:   "no proxy headers falls back to peer",
			remote: "10.0.0.1:54321",
			want:   "10.0.0.1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.clientIP(req(tc.remote, tc.headers)); got != tc.want {
				t.Errorf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestClientIPNoTrustedProxies makes sure the default (empty) configuration
// never honours a client-supplied forwarding header.
func TestClientIPNoTrustedProxies(t *testing.T) {
	s := &Server{Config: &config.Config{}}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "198.51.100.4:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.7")
	r.Header.Set("X-Real-IP", "203.0.113.8")
	if got := s.clientIP(r); got != "198.51.100.4" {
		t.Errorf("clientIP = %q, want the peer address", got)
	}
}
