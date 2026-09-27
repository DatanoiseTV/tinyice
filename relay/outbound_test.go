package relay

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBlockedOutboundIP(t *testing.T) {
	for _, s := range []string{
		"127.0.0.1", "::1", "::ffff:127.0.0.1",
		"10.1.2.3", "172.16.0.1", "192.168.1.1", "fd00::1",
		"169.254.169.254", // cloud metadata
		"100.64.0.1",      // RFC 6598 carrier-grade NAT
		"0.0.0.0", "0.1.2.3",
		"224.0.0.1", "ff02::1",
	} {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("%s is not parseable", s)
		}
		if err := BlockedOutboundIP(ip); err == nil {
			t.Errorf("%s was accepted as an outbound target", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "93.184.216.34", "2606:4700:4700::1111"} {
		if err := BlockedOutboundIP(net.ParseIP(s)); err != nil {
			t.Errorf("%s was refused: %v", s, err)
		}
	}
}

// The URL check runs once, on a string the operator typed. Both DNS and
// any redirect the remote returns are outside our control, so the policy
// has to be applied again to the address actually being connected to —
// which is what the dialer does, on every hop.
func TestOutboundClientRefusesALoopbackTarget(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := NewOutboundHTTPClient(0).Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("the client connected to a loopback address")
	}
	if !strings.Contains(err.Error(), "non-routable") {
		t.Errorf("error = %v, want it to name the address policy", err)
	}
	if reached {
		t.Error("the request reached the server")
	}
}

// A redirect is just another connection, so the same dialer sees it.
func TestOutboundClientRefusesARedirectToLoopback(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer target.Close()

	// Dial the redirector with a permissive client so only the second hop
	// is subject to the policy.
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	client := NewOutboundHTTPClient(0)
	// Allow the first hop explicitly by dialling it directly; the policy
	// then applies to the redirect target.
	transport := client.Transport.(*http.Transport).Clone()
	base := transport.DialContext
	first := true
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if first {
			first = false
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}
		return base(ctx, network, addr)
	}
	client.Transport = transport

	resp, err := client.Get(redirector.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("the redirect to %s was followed", target.URL)
	}
	if !strings.Contains(err.Error(), "non-routable") {
		t.Errorf("error = %v, want it to name the address policy", err)
	}
}

// IPv6 transition addresses embed an IPv4 address that none of the
// stdlib predicates look at: IsLoopback knows only ::1, IsPrivate only
// fc00::/7, IsLinkLocalUnicast only fe80::/10, and net.IP.To4 decodes
// IPv4-mapped alone. So 64:ff9b::a9fe:a9fe — NAT64 for the cloud
// metadata endpoint — passed the outbound policy, as did 6to4 and
// Teredo forms of any private address, and the deprecated-but-routed
// IPv4-compatible ::127.0.0.1 (GHSA-7r38-rfcc-8x22).
func TestBlockedOutboundIPDecodesIPv6TransitionAddresses(t *testing.T) {
	for _, tc := range []struct{ addr, what string }{
		{"64:ff9b::a9fe:a9fe", "NAT64 to 169.254.169.254 (cloud metadata)"},
		{"64:ff9b::7f00:1", "NAT64 to 127.0.0.1"},
		{"64:ff9b::a00:1", "NAT64 to 10.0.0.1"},
		{"64:ff9b:1::a00:1", "NAT64 local-use prefix to 10.0.0.1"},
		{"2002:c0a8:0101::", "6to4 to 192.168.1.1"},
		{"2002:7f00:1::", "6to4 to 127.0.0.1"},
		{"2002:a9fe:a9fe::", "6to4 to 169.254.169.254"},
		{"2001:0::f5ff:fffe", "Teredo to 10.0.0.1"},
		{"2001:0::80ff:fffe", "Teredo to 127.0.0.1"},
		{"2001:0::5601:5601", "Teredo to 169.254.169.254"},
		{"::127.0.0.1", "IPv4-compatible loopback"},
		{"::10.0.0.1", "IPv4-compatible private"},
	} {
		ip := net.ParseIP(tc.addr)
		if ip == nil {
			t.Fatalf("%s is not parseable", tc.addr)
		}
		if err := BlockedOutboundIP(ip); err == nil {
			t.Errorf("%s (%s) was accepted as an outbound target", tc.addr, tc.what)
		}
	}
}

// The decode must judge the embedded address on the same policy rather
// than banning the transition prefixes outright — a blanket ban would
// refuse a legitimate publisher reachable over 6to4 or Teredo.
func TestTransitionAddressesCarryingPublicIPv4StayAllowed(t *testing.T) {
	for _, tc := range []struct{ addr, what string }{
		{"2002:0808:0808::", "6to4 to 8.8.8.8"},
		{"64:ff9b::0808:0808", "NAT64 to 8.8.8.8"},
		{"2001:0:4136:e378:8000:63bf:3fff:fdd2", "Teredo to 192.0.2.45"},
		{"2606:4700:4700::1111", "plain public IPv6"},
	} {
		if err := BlockedOutboundIP(net.ParseIP(tc.addr)); err != nil {
			t.Errorf("%s (%s) was refused: %v", tc.addr, tc.what, err)
		}
	}
}
