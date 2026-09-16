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

// BlockedOutboundIP reports why an address must not be the target of an
// outbound request the operator configured (webhook, YP directory, relay
// pull, GeoIP download), or nil if it is acceptable.
//
// Checking the URL string alone is not enough: the hostname's resolution
// and any redirect the remote side returns are both outside our control,
// so the same policy has to be enforced again at connect time. See
// OutboundDialer.
func BlockedOutboundIP(ip net.IP) error {
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
