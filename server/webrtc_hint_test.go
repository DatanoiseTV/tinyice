package server

import (
	"errors"
	"strings"
	"testing"
)

// pion's failure when it cannot enumerate interfaces names neither
// systemd nor the directive that caused it, and tinyice shipped a unit
// in packaging/ and contrib/ whose RestrictAddressFamilies omitted
// AF_NETLINK — so every operator who installed the deb or rpm hit a
// cryptic message with nothing to search for.
func TestWebRTCErrorHintExplainsTheNetlinkFailure(t *testing.T) {
	err := errors.New("failed to create network: route ip+net: netlinkrib: address family not supported by protocol")
	got := webrtcErrorHint(err)

	if !strings.Contains(got, err.Error()) {
		t.Error("the hint dropped the original error text")
	}
	for _, want := range []string{"AF_NETLINK", "RestrictAddressFamilies", "systemd"} {
		if !strings.Contains(got, want) {
			t.Errorf("hint does not mention %q, so the operator has nothing to act on:\n%s", want, got)
		}
	}
}

// Unrelated failures must pass through untouched; a hint appended to
// every error would be noise that buries the real cause.
func TestWebRTCErrorHintLeavesOtherErrorsAlone(t *testing.T) {
	for _, msg := range []string{
		"mount is required",
		"invalid SDP",
		"connection refused",
	} {
		if got := webrtcErrorHint(errors.New(msg)); got != msg {
			t.Errorf("error %q was rewritten to %q", msg, got)
		}
	}
}
