package relay

import (
	"fmt"

	"github.com/DatanoiseTV/tinyice/logger"
)

// recoverIngest is deferred at the entry of ingest paths that run on
// goroutines the net/http server does not own — SRT and RTMP publishers
// are driven by their library's accept loops, MPD by ours. net/http
// recovers panics in its own handler goroutines; nothing recovers these,
// so a single malformed packet from one publisher would otherwise end
// the process for every mount and every listener. The connection is the
// right isolation boundary: log, and let the caller's deferred Close run.
func recoverIngest(what string, remote interface{}) {
	if r := recover(); r != nil {
		logger.L.Errorw("ingest handler panicked; closing that connection only",
			"path", what, "remote", fmt.Sprintf("%v", remote), "panic", fmt.Sprintf("%v", r))
	}
}

// recoverIngestErr is recoverIngest for callbacks that report failure by
// returning an error: the recovered panic becomes that error, so the
// library drops the offending connection instead of feeding it the next
// packet.
func recoverIngestErr(what string, remote interface{}, err *error) {
	if r := recover(); r != nil {
		logger.L.Errorw("ingest handler panicked; closing that connection only",
			"path", what, "remote", fmt.Sprintf("%v", remote), "panic", fmt.Sprintf("%v", r))
		*err = fmt.Errorf("%s ingest panicked: %v", what, r)
	}
}
