package relay

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// cgnatRange is RFC 6598 carrier-grade NAT space. net.IP.IsPrivate does
// not cover it, but it is routed internally on plenty of networks.
var cgnatRange = net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// nat64WellKnown is the RFC 6052 well-known NAT64 prefix; nat64LocalUse is
// the RFC 8215 local-use prefix. A network may also run NAT64 on its own
// prefix, which no static list can cover — see the note on embeddedIPv4.
var (
	nat64WellKnown = net.IPNet{IP: net.ParseIP("64:ff9b::"), Mask: net.CIDRMask(96, 128)}
	nat64LocalUse  = net.IPNet{IP: net.ParseIP("64:ff9b:1::"), Mask: net.CIDRMask(48, 128)}
	sixToFour      = net.IPNet{IP: net.ParseIP("2002::"), Mask: net.CIDRMask(16, 128)}
	teredo         = net.IPNet{IP: net.ParseIP("2001::"), Mask: net.CIDRMask(32, 128)}
)

// embeddedIPv4 extracts the IPv4 address carried inside an IPv6
// transition address, so the outbound policy can be applied to what the
// packet will actually reach.
//
// Covered: IPv4-mapped (::ffff:a.b.c.d), IPv4-compatible (::a.b.c.d),
// NAT64 (RFC 6052 well-known and RFC 8215 local-use prefixes), 6to4
// (2002::/16) and Teredo (2001:0::/32, where the client address is the
// last four bytes, bitwise inverted).
//
// Known limit, stated rather than hidden: NAT64 permits network-specific
// prefixes of several lengths, and those cannot be enumerated. An
// operator running NAT64 on a custom prefix still has a path to internal
// addresses through a configured webhook or relay URL. Blocking that
// would need the resolver's view of the network, which we do not have.
func embeddedIPv4(ip net.IP) (net.IP, bool) {
	if ip == nil {
		return nil, false
	}
	// An address that is already IPv4 (or IPv4-mapped, which To4
	// decodes) is judged directly by the caller, not here.
	if ip.To4() != nil {
		return nil, false
	}
	v6 := ip.To16()
	if v6 == nil {
		return nil, false
	}
	switch {
	case nat64WellKnown.Contains(v6), nat64LocalUse.Contains(v6):
		// RFC 6052 /96: the IPv4 address is the final four bytes.
		return net.IPv4(v6[12], v6[13], v6[14], v6[15]), true
	case sixToFour.Contains(v6):
		// RFC 3056: 2002:V4ADDR::/48.
		return net.IPv4(v6[2], v6[3], v6[4], v6[5]), true
	case teredo.Contains(v6):
		// RFC 4380: the client IPv4 is the last four bytes, inverted.
		return net.IPv4(^v6[12], ^v6[13], ^v6[14], ^v6[15]), true
	}
	// IPv4-compatible ::a.b.c.d (deprecated by RFC 4291 but still
	// parsed and routed). Exclude :: and ::1, which the unspecified and
	// loopback predicates already own.
	var zeros [12]byte
	if string(v6[:12]) == string(zeros[:]) && !ip.IsUnspecified() && !ip.IsLoopback() {
		return net.IPv4(v6[12], v6[13], v6[14], v6[15]), true
	}
	return nil, false
}

// BlockedOutboundIP reports why an address must not be the target of an
// outbound request the operator configured (webhook, YP directory, relay
// pull, GeoIP download), or nil if it is acceptable.
//
// Checking the URL string alone is not enough: the hostname's resolution
// and any redirect the remote side returns are both outside our control,
// so the same policy has to be enforced again at connect time. See
// OutboundDialer.
func BlockedOutboundIP(ip net.IP) error {
	// Decode IPv6 transition formats first. An address like
	// 64:ff9b::a9fe:a9fe (NAT64 for 169.254.169.254) satisfies none of
	// the stdlib predicates below — IsLoopback only knows ::1, IsPrivate
	// only fc00::/7, IsLinkLocalUnicast only fe80::/10 — so the embedded
	// IPv4 has to be pulled out and judged on its own. net.IP.To4 does
	// not do this: it decodes IPv4-mapped (::ffff:a.b.c.d) only.
	if v4, ok := embeddedIPv4(ip); ok {
		if err := BlockedOutboundIP(v4); err != nil {
			return fmt.Errorf("%w, reached via the IPv6 transition address %s", err, ip)
		}
	}
	switch {
	case ip.IsLoopback():
		return fmt.Errorf("loopback address (%s)", ip)
	case ip.IsPrivate():
		return fmt.Errorf("private address (%s)", ip)
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		// Covers the 169.254.169.254 cloud metadata endpoint.
		return fmt.Errorf("link-local address (%s)", ip)
	case ip.IsMulticast():
		return fmt.Errorf("multicast address (%s)", ip)
	case ip.IsUnspecified():
		return fmt.Errorf("unspecified address (%s)", ip)
	case ip.IsInterfaceLocalMulticast():
		return fmt.Errorf("interface-local address (%s)", ip)
	}
	if ip4 := ip.To4(); ip4 != nil {
		// 0.0.0.0/8 ("this network") reaches the local host on Linux.
		if ip4[0] == 0 {
			return fmt.Errorf("reserved address (%s)", ip)
		}
		if cgnatRange.Contains(ip4) {
			return fmt.Errorf("carrier-grade NAT address (%s)", ip)
		}
	}
	return nil
}

// OutboundDialer returns a dialer that refuses to complete a connection
// to an address BlockedOutboundIP rejects. The check runs in Control,
// after the name has been resolved and before connect(2), so it sees the
// address actually being dialled — there is no resolve-then-dial window
// to race, and it applies to every hop of a redirect chain because each
// one opens its own connection.
func OutboundDialer(timeout, keepAlive time.Duration) *net.Dialer {
	return &net.Dialer{
		Timeout:   timeout,
		KeepAlive: keepAlive,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("outbound: cannot parse %q: %w", address, err)
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("outbound: %q is not an IP address", host)
			}
			if err := BlockedOutboundIP(ip); err != nil {
				return fmt.Errorf("outbound request to a non-routable target refused: %w", err)
			}
			return nil
		},
	}
}

// NewOutboundHTTPClient builds an http.Client for operator-configured
// destinations. It carries the dial-time address policy and a total
// timeout; callers that stream a response body indefinitely should pass
// 0 and bound the read themselves.
func NewOutboundHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:           OutboundDialer(10*time.Second, 30*time.Second).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 15 * time.Second,
			IdleConnTimeout:       90 * time.Second,
		},
	}
}
