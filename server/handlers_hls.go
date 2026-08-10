package server

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DatanoiseTV/tinyice/logger"
	"github.com/DatanoiseTV/tinyice/relay"
)

func (s *Server) handleHLSPlaylist(w http.ResponseWriter, r *http.Request) {
	// Path: /{mount}/playlist.m3u8 — extract mount
	path := r.URL.Path
	mount := strings.TrimSuffix(path, "/playlist.m3u8")
	if mount == "" {
		http.NotFound(w, r)
		return
	}

	// Lazily register an HLS output on first request. This keeps us from
	// segmenting mounts nobody ever asks for, and lets ingest paths
	// (RTMP, SRT, Icecast SOURCE) just broadcast bytes without caring
	// about HLS. If the mount has a sibling /video sub-mount, the
	// registered output automatically runs in A/V mode.
	hls := s.getHLSOutput(mount)
	if hls == nil {
		hls = s.RegisterHLS(mount)
	}
	if hls == nil {
		http.NotFound(w, r)
		return
	}

	// Record the viewer so the UI can show "N viewers" on video
	// mounts. hls.js polls the playlist every TARGETDURATION/2, so
	// each viewer keeps refreshing the same IP entry inside the
	// 30 s viewer-TTL window.
	if stream, ok := s.Relay.GetStream(mount); ok {
		stream.RecordViewer(s.clientIP(r), time.Now())
	}

	playlist := hls.Playlist()

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write([]byte(playlist))
}

func (s *Server) handleHLSSegment(w http.ResponseWriter, r *http.Request) {
	// Path: /{mount}/segment-{n}.ts — extract mount and sequence
	path := r.URL.Path

	// Find the last path component
	lastSlash := strings.LastIndex(path, "/")
	if lastSlash == -1 {
		http.NotFound(w, r)
		return
	}

	mount := path[:lastSlash]
	segPart := path[lastSlash+1:] // "segment-N.ts"

	if !strings.HasPrefix(segPart, "segment-") || !strings.HasSuffix(segPart, ".ts") {
		http.NotFound(w, r)
		return
	}

	seqStr := strings.TrimPrefix(segPart, "segment-")
	seqStr = strings.TrimSuffix(seqStr, ".ts")
	seq, err := strconv.Atoi(seqStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	hls := s.getHLSOutput(mount)
	if hls == nil {
		hls = s.RegisterHLS(mount)
	}
	if hls == nil {
		http.NotFound(w, r)
		return
	}

	segment := hls.Ring().Get(seq)
	if segment == nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Accept-Ranges", "bytes")
	// Use http.ServeContent so partial-content (Range) requests get a
	// proper 206 response with Content-Range. Mobile HLS players poke
	// Range to test the server before doing real range fetches; if we
	// return 200 with the whole body they assume range support is
	// broken and fall back to less efficient strategies (or in iOS
	// Safari's case, refuse to load the segment at all on some
	// versions). Mod-time is segment creation time so caching
	// behaviour is sensible.
	http.ServeContent(w, r, "", segment.CreatedAt, bytes.NewReader(segment.Data))
}

// handleWHEP implements a minimal WHEP (WebRTC-HTTP Egress Protocol)
// endpoint for viewer-side WebRTC playback.
//
//	POST /{mount}/whep
//	Content-Type: application/sdp
//	<SDP offer>
//
// Server returns 201 Created with Content-Type: application/sdp and the
// SDP answer in the body. The client uses that answer to finalise the
// peer connection. Termination is a DELETE on the Location URL (we
// issue "/{mount}/whep" as the Location for simplicity; disconnect is
// currently driven by the WebRTC peer connection state).
func (s *Server) handleWHEP(w http.ResponseWriter, r *http.Request) {
	mount := strings.TrimSuffix(r.URL.Path, "/whep")
	if mount == "" || !strings.HasPrefix(mount, "/") {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ct := r.Header.Get("Content-Type")
	if !strings.Contains(strings.ToLower(ct), "application/sdp") {
		http.Error(w, "expected application/sdp", http.StatusUnsupportedMediaType)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64*1024))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	answer, err := s.WebRTCM.HandleWHEPOffer(mount, string(body))
	if err != nil {
		logger.L.Warnw("WHEP: offer rejected", "mount", mount, "err", err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if stream, ok := s.Relay.GetStream(mount); ok {
		stream.RecordViewer(s.clientIP(r), time.Now())
	}
	w.Header().Set("Content-Type", "application/sdp")
	w.Header().Set("Location", r.URL.Path)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusCreated)
	w.Write([]byte(answer))
}

// handleHLSMaster serves a master (multivariant) HLS playlist that lists
// every member of the configured VariantGroup for this mount. Players
// can then choose a rendition based on bandwidth / display size.
//
// Path: /{primary}/master.m3u8
func (s *Server) handleHLSMaster(w http.ResponseWriter, r *http.Request) {
	primary := strings.TrimSuffix(r.URL.Path, "/master.m3u8")
	if primary == "" || !strings.HasPrefix(primary, "/") {
		http.NotFound(w, r)
		return
	}
	members, ok := s.Config.VariantGroups[primary]
	if !ok || len(members) == 0 {
		http.NotFound(w, r)
		return
	}

	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:4\n")
	for _, m := range members {
		// Ensure per-variant HLS output exists so the referenced
		// playlist URL responds immediately instead of racing the
		// player's first request.
		if _, exists := s.Relay.GetStream(m); exists {
			if s.getHLSOutput(m) == nil {
				s.RegisterHLS(m)
			}
		}
		bandwidth := 0
		resolution := ""
		if vs, ok := s.Relay.GetStream(m + "/video"); ok {
			vm := vs.VideoMetricsSnapshot()
			if vm.BitrateKbps > 0 {
				bandwidth = vm.BitrateKbps * 1000
			}
			if vm.Width > 0 {
				resolution = fmt.Sprintf("%dx%d", vm.Width, vm.Height)
			}
		}
		// If we can't observe a live bitrate yet, synthesize one from
		// the mount name so the player still gets a plausible BANDWIDTH
		// hint to sort by. Default 2 Mbps, bumped for common suffixes.
		if bandwidth == 0 {
			bandwidth = 2_000_000
			switch {
			case strings.Contains(m, "2160"), strings.Contains(m, "4k"):
				bandwidth = 12_000_000
			case strings.Contains(m, "1080"):
				bandwidth = 5_000_000
			case strings.Contains(m, "720"):
				bandwidth = 2_800_000
			case strings.Contains(m, "480"):
				bandwidth = 1_200_000
			case strings.Contains(m, "360"):
				bandwidth = 600_000
			}
		}
		if resolution != "" {
			fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%s\n", bandwidth, resolution)
		} else {
			fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d\n", bandwidth)
		}
		fmt.Fprintf(&b, "%s/playlist.m3u8\n", m)
	}

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write([]byte(b.String()))
}

// handlePoster serves and accepts per-mount poster JPEGs. The upload path
// is intentionally simple: any authenticated-or-anonymous listener can
// POST a JPEG sampled client-side from the video element, and the most
// recent upload is cached in memory per mount. The GET path returns the
// cached JPEG or 404 — the landing / explore UIs fall back to a brand
// placeholder on miss.
//
// Path: /{mount}/poster.jpg
func (s *Server) handlePoster(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	mount := strings.TrimSuffix(path, "/poster.jpg")
	if mount == "" || !strings.HasPrefix(mount, "/") {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet, http.MethodHead:
		s.posterMu.RLock()
		data := s.posters[mount]
		s.posterMu.RUnlock()
		if len(data) == 0 {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "public, max-age=10")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == http.MethodHead {
			return
		}
		w.Write(data)
	case http.MethodPost, http.MethodPut:
		if _, ok := s.Relay.GetStream(mount); !ok {
			http.NotFound(w, r)
			return
		}
		// Cap at 1 MiB — client uploads a 480p JPEG, anything larger is
		// almost certainly misuse or a decode failure.
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		data, err := io.ReadAll(r.Body)
		if err != nil || len(data) < 128 {
			http.Error(w, "bad poster", http.StatusBadRequest)
			return
		}
		// Minimal JPEG sniff: SOI marker FF D8 FF.
		if !(data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF) {
			http.Error(w, "not a JPEG", http.StatusUnsupportedMediaType)
			return
		}
		s.posterMu.Lock()
		s.posters[mount] = data
		s.posterMu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// getHLSOutput finds the HLS output for a given mount.
func (s *Server) getHLSOutput(mount string) *relay.HLSOutput {
	s.hlsMu.RLock()
	defer s.hlsMu.RUnlock()
	return s.hlsOutputs[mount]
}

// RegisterHLS creates and starts an HLS output for a stream mount. If a
// sibling "/video" sub-mount exists for this mount (created by the RTMP or
// SRT ingest when an H.264 track is present), the output is started with
// both tracks and the resulting segments interleave audio + video.
//
// Idempotent and race-safe: two concurrent first-listener requests for the
// same mount won't each spawn a segmentLoop goroutine. The previous
// implementation created+started the HLSOutput before taking the map
// lock, so a second goroutine arriving between getHLSOutput's "not
// found" return and Register's map write would also build+start its
// own output, then atomically overwrite the first one in the map —
// leaking the first's goroutine + its segment ring forever.
func (s *Server) RegisterHLS(mount string) *relay.HLSOutput {
	// Fast-path under read lock.
	s.hlsMu.RLock()
	if existing, ok := s.hlsOutputs[mount]; ok {
		s.hlsMu.RUnlock()
		return existing
	}
	s.hlsMu.RUnlock()

	stream, ok := s.Relay.GetStream(mount)
	if !ok {
		return nil
	}

	config := relay.DefaultHLSConfig()
	hls := relay.NewHLSOutput(mount, config).WithRelay(s.Relay)

	// Determine codec from content type
	codec := "mp3"
	if stream.IsOgg() {
		codec = "opus"
	}

	tracks := []*relay.Track{relay.NewAudioTrack(stream, codec)}
	if vs, ok := s.Relay.GetStream(mount + "/video"); ok {
		tracks = append(tracks, relay.NewTrackFromStream(relay.MediaVideo, "h264", vs))
	}

	// Take the write lock BEFORE Start so a parallel Register that
	// arrived between our fast-path check and now sees the map entry
	// rather than racing us into a duplicate goroutine. Re-check the
	// map under the write lock; if a peer beat us, throw away the
	// not-yet-started HLSOutput (no goroutine to leak — Start hasn't
	// been called) and return theirs.
	s.hlsMu.Lock()
	if existing, ok := s.hlsOutputs[mount]; ok {
		s.hlsMu.Unlock()
		return existing
	}
	hls.Start(s.hlsCtx, tracks)
	s.hlsOutputs[mount] = hls
	s.hlsMu.Unlock()

	logger.L.Infow("HLS: Registered output", "mount", mount, "has_video", len(tracks) > 1)
	return hls
}

// UnregisterHLS stops and removes an HLS output.
func (s *Server) UnregisterHLS(mount string) {
	s.hlsMu.Lock()
	if hls, ok := s.hlsOutputs[mount]; ok {
		hls.Stop()
		delete(s.hlsOutputs, mount)
	}
	s.hlsMu.Unlock()
}
