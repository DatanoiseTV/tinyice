package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/relay"
	"github.com/pion/webrtc/v4"
)

type streamEventInfo struct {
	Mount        string  `json:"mount"`
	Name         string  `json:"name"`
	Listeners    int     `json:"listeners"`
	// Viewers counts HLS / WHEP browser playback sessions over the
	// last 30 s — those clients fetch segments / hold a peer
	// connection rather than holding the long-lived listener
	// connection that Listeners tracks. The player renders this
	// instead of Listeners on video mounts.
	Viewers      int     `json:"viewers"`
	Bitrate      string  `json:"bitrate"`
	Uptime       string  `json:"uptime"`
	ContentType  string  `json:"type"`
	SourceIP     string  `json:"ip"`
	BytesIn      int64   `json:"bytes_in"`
	BytesOut     int64   `json:"bytes_out"`
	BytesDropped int64   `json:"bytes_dropped"`
	CurrentSong  string  `json:"song"`
	Health       float64 `json:"health"`
	IsTranscoded bool    `json:"is_transcoded"`

	// SourceMount / SourceContentType / SourceBitrate carry the
	// upstream's format on transcoded outputs so the dashboard can
	// render "audio/ogg 160k → audio/mpeg 320k" rather than just the
	// output format. Empty on non-transcoded streams.
	SourceMount       string `json:"source_mount,omitempty"`
	SourceContentType string `json:"source_type,omitempty"`
	SourceBitrate     string `json:"source_bitrate,omitempty"`

	// Video-only metrics. Zero on audio mounts; the frontend hides
	// the video-stats strip when Width == 0.
	VideoWidth    int     `json:"video_width,omitempty"`
	VideoHeight   int     `json:"video_height,omitempty"`
	VideoFPS      float64 `json:"video_fps,omitempty"`
	VideoGOP      float64 `json:"video_gop,omitempty"`
	VideoKbps     int     `json:"video_kbps,omitempty"`
}

type relayEventInfo struct {
	URL     string `json:"url"`
	Mount   string `json:"mount"`
	Active  bool   `json:"active"`
	Enabled bool   `json:"enabled"`
}

type streamerEventInfo struct {
	Name        string               `json:"name"`
	Mount       string               `json:"mount"`
	State       int                  `json:"state"`
	CurrentSong string               `json:"song"`
	StartTime   int64                `json:"start_time"`
	Duration    float64              `json:"duration"`
	PlaylistPos int                  `json:"playlist_pos"`
	PlaylistLen int                  `json:"playlist_len"`
	Shuffle     bool                 `json:"shuffle"`
	Loop        bool                 `json:"loop"`
	Queue       []relay.PlaylistItem `json:"queue"`
	Playlist    []relay.PlaylistItem `json:"playlist"`
}

func (s *Server) collectStatsPayload(user *config.User) ([]byte, error) {
	bi, bo := s.Relay.GetMetrics()
	allStreams := s.Relay.Snapshot()
	tl := 0
	var info []streamEventInfo
	tr, ts := 0, 0
	for _, st := range allStreams {
		if s.hasAccess(user, st.MountName) {
			lc := st.ListenersCount
			tl += lc
			entry := streamEventInfo{
				Mount: st.MountName, Name: st.Name, Listeners: lc, Bitrate: st.Bitrate,
				Uptime: st.Uptime, ContentType: st.ContentType, SourceIP: st.SourceIP,
				BytesIn: st.BytesIn, BytesOut: st.BytesOut, BytesDropped: st.BytesDropped,
				CurrentSong: st.CurrentSong, Health: st.Health, IsTranscoded: st.IsTranscoded,
			}
			// For transcoded outputs, surface the source mount + its
			// content-type + bitrate so the dashboard can render
			// "<src-format> → <out-format>". Look up via the
			// transcoder manager: each TranscoderInstance.Config
			// remembers InputMount; the live source stream gives us
			// the real content-type + bitrate it's producing.
			if st.IsTranscoded {
				if inst := s.TranscoderM.GetInstance(st.MountName); inst != nil && inst.Config != nil {
					entry.SourceMount = inst.Config.InputMount
					if src, ok := s.Relay.GetStream(inst.Config.InputMount); ok {
						// Direct field reads — same accept-the-race
						// profile the rest of this file uses for
						// display-only metadata.
						entry.SourceContentType = src.ContentType
						entry.SourceBitrate = src.Bitrate
					}
				}
			}
			// Attach video metrics when the live Stream has them.
			// Zero width means either "audio mount" or "we haven't
			// sampled yet" — both are invisible to the UI.
			if liveStream, ok := s.Relay.GetStream(st.MountName); ok {
				vm := liveStream.VideoMetricsSnapshot()
				if vm.Width > 0 {
					entry.VideoWidth = vm.Width
					entry.VideoHeight = vm.Height
					entry.VideoFPS = vm.FPS
					entry.VideoGOP = vm.GOPSeconds
					entry.VideoKbps = vm.BitrateKbps
				}
				// Combine browser viewers from the parent mount
				// (HLS playlist + WHEP) with the /video sibling.
				entry.Viewers = liveStream.ViewerCount()
				if vs, ok := s.Relay.GetStream(st.MountName + "/video"); ok {
					if c := vs.ViewerCount(); c > entry.Viewers {
						entry.Viewers = c
					}
				}
			}
			info = append(info, entry)
			if st.SourceIP == "relay-pull" {
				tr++
			} else {
				ts++
			}
		}
	}
	if user.Role != config.RoleSuperAdmin {
		var ubi, ubo int64
		for _, st := range info {
			ubi += st.BytesIn
			ubo += st.BytesOut
		}
		bi, bo = ubi, ubo
	}

	relays := make([]relayEventInfo, len(s.Config.Relays))
	for i, rc := range s.Config.Relays {
		relays[i] = relayEventInfo{URL: rc.URL, Mount: rc.Mount, Active: false, Enabled: rc.Enabled}
		if st, ok := s.Relay.GetStream(rc.Mount); ok && st.SourceIP == "relay-pull" {
			relays[i].Active = true
		}
	}

	activeStreamers := s.StreamerM.GetStreamers()
	streamers := make([]streamerEventInfo, 0, len(activeStreamers))
	for _, st := range activeStreamers {
		if s.hasAccess(user, st.OutputMount) {
			stats := st.GetStats()
			streamers = append(streamers, streamerEventInfo{
				Name:        stats.Name,
				Mount:       stats.Mount,
				State:       int(stats.State),
				CurrentSong: stats.CurrentSong,
				StartTime:   stats.StartTime.Unix(),
				Duration:    stats.Duration.Seconds(),
				PlaylistLen: stats.PlaylistLen,
				Shuffle:     stats.Shuffle,
				Loop:        stats.Loop,
				Queue:       st.GetQueueInfo(),
				Playlist:    st.GetPlaylistInfo(),
			})
		}
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	totalDropped := int64(0)
	for _, st := range allStreams {
		totalDropped += st.BytesDropped
	}

	// Live geo snapshot piggybacks on every stats SSE tick so the
	// dashboard map updates with the same cadence as the listener
	// counters and traffic chart — no separate /admin/geo poll
	// needed. Snapshot is in-memory + cheap; ~one map walk over a
	// (small) set of (city, mount) tuples.
	geoCities := s.GeoTracker.Snapshot("")

	return json.Marshal(map[string]interface{}{
		"bytes_in":        bi,
		"bytes_out":       bo,
		"total_listeners": tl,
		"total_sources":   len(info),
		"total_relays":    tr,
		"total_streamers": ts,
		"streams":         info,
		"relays":          relays,
		"streamers":       streamers,
		"visible_mounts":  s.Config.VisibleMounts,
		"sys_ram":         m.Sys,
		"heap_alloc":      m.HeapAlloc,
		"stack_sys":       m.StackSys,
		"num_gc":          m.NumGC,
		"goroutines":      runtime.NumGoroutine(),
		"total_dropped":   totalDropped,
		"server_uptime":   time.Since(s.startTime).Round(time.Second).String(),
		"geo":             geoCities,
	})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	bi, bo := s.Relay.GetMetrics()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"bytes_in": bi, "bytes_out": bo})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	// Track previous byte counters to compute bandwidth as a rate. The UI
	// formats bandwidth_in/out as "bytes per second"; sending cumulative
	// totals made the dashboard display the session's entire ingest as MB/s.
	var lastBytesIn, lastBytesOut int64
	lastTime := time.Now()

	send := func() error {
		payload, err := s.collectStatsPayload(user)
		if err != nil {
			return err
		}
		// Send as unnamed event for legacy clients
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return err
		}

		// Also send named events for the new Preact frontend
		var full map[string]interface{}
		json.Unmarshal(payload, &full)

		curIn, _ := full["bytes_in"].(float64)
		curOut, _ := full["bytes_out"].(float64)
		now := time.Now()
		elapsed := now.Sub(lastTime).Seconds()
		var rateIn, rateOut int64
		if elapsed > 0 {
			if lastTime.IsZero() || lastBytesIn == 0 && lastBytesOut == 0 {
				// First tick: no prior sample, report 0 instead of a huge burst.
				rateIn, rateOut = 0, 0
			} else {
				rateIn = int64(float64(int64(curIn)-lastBytesIn) / elapsed)
				rateOut = int64(float64(int64(curOut)-lastBytesOut) / elapsed)
				if rateIn < 0 {
					rateIn = 0
				}
				if rateOut < 0 {
					rateOut = 0
				}
			}
		}
		lastBytesIn = int64(curIn)
		lastBytesOut = int64(curOut)
		lastTime = now

		// stats event
		statsJSON, _ := json.Marshal(map[string]interface{}{
			"listeners":     full["total_listeners"],
			"streams":       full["total_sources"],
			"bandwidth_in":  rateIn,
			"bandwidth_out": rateOut,
			"bandwidth":     rateOut, // backwards compat
			"bytes_in":      int64(curIn),
			"bytes_out":     int64(curOut),
			"uptime":        int(time.Since(s.startTime).Seconds()),
			"goroutines":    full["goroutines"],
			"memory":        full["heap_alloc"],
			"gc":            full["num_gc"],
		})
		fmt.Fprintf(w, "event: stats\ndata: %s\n\n", statsJSON)

		// stream events
		if streams, ok := full["streams"].([]interface{}); ok {
			for _, st := range streams {
				stMap := st.(map[string]interface{})
				streamJSON, _ := json.Marshal(map[string]interface{}{
					"mount":        stMap["mount"],
					"format":       stMap["type"],
					"bitrate":      stMap["bitrate"],
					"listeners":    stMap["listeners"],
					"viewers":      stMap["viewers"],
					"health":       stMap["health"],
					"title":        stMap["song"],
					"artist":       "",
					"video_width":  stMap["video_width"],
					"video_height": stMap["video_height"],
					"video_fps":    stMap["video_fps"],
					"video_gop":    stMap["video_gop"],
					"video_kbps":   stMap["video_kbps"],
				})
				fmt.Fprintf(w, "event: stream\ndata: %s\n\n", streamJSON)
			}
		}

		// autodj events
		if streamers, ok := full["streamers"].([]interface{}); ok {
			for _, st := range streamers {
				stJSON, _ := json.Marshal(st)
				fmt.Fprintf(w, "event: autodj\ndata: %s\n\n", stJSON)
			}
		}

		// geo event — full city list per tick. Cheap (the snapshot
		// is in-memory) and lets the dashboard map auto-refresh
		// without a separate /admin/geo poll. We send it on every
		// SSE tick so the operator sees connect/disconnect events
		// immediately.
		if geo, ok := full["geo"]; ok {
			geoJSON, _ := json.Marshal(geo)
			fmt.Fprintf(w, "event: geo\ndata: %s\n\n", geoJSON)
		}

		flusher.Flush()
		return nil
	}

	if err := send(); err != nil {
		return
	}
	for {
		select {
		case <-s.done:
			return
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if err := send(); err != nil {
				return
			}
		}
	}
}

func (s *Server) handlePublicEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	flusher, _ := w.(http.Flusher)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	type PublicStreamInfo struct {
		Mount       string  `json:"mount"`
		Name        string  `json:"name"`
		Listeners   int     `json:"listeners"`
		Viewers     int     `json:"viewers,omitempty"`
		Bitrate     string  `json:"bitrate"`
		Uptime      string  `json:"uptime"`
		Genre       string  `json:"genre"`
		Description string  `json:"description"`
		CurrentSong string  `json:"song"`
		HasVideo    bool    `json:"has_video,omitempty"`
		VideoWidth  int     `json:"video_width,omitempty"`
		VideoHeight int     `json:"video_height,omitempty"`
		VideoFPS    float64 `json:"video_fps,omitempty"`
		VideoGOP    float64 `json:"video_gop,omitempty"`
		VideoKbps   int     `json:"video_kbps,omitempty"`
	}
	send := func() error {
		allStreams := s.Relay.Snapshot()
		videoMounts := make(map[string]bool)
		for _, st := range allStreams {
			if strings.HasSuffix(st.MountName, "/video") {
				videoMounts[strings.TrimSuffix(st.MountName, "/video")] = true
			}
		}
		var info []PublicStreamInfo
		// We split visible-only from emitted-to-events: the unnamed
		// `data:` payload (legacy landing card consumers) only sees
		// visible mounts, while the per-stream named "stream" events
		// (player consumers) see everything so a player tab can show
		// stats for an unlisted mount.
		var visible []PublicStreamInfo
		for _, st := range allStreams {
			if strings.HasSuffix(st.MountName, "/video") {
				continue // sub-mount, surfaced via the parent
			}
			entry := PublicStreamInfo{
				Mount: st.MountName, Name: st.Name, Listeners: st.ListenersCount,
				Bitrate: st.Bitrate, Uptime: st.Uptime, Genre: st.Genre,
				Description: st.Description, CurrentSong: st.CurrentSong,
				HasVideo: videoMounts[st.MountName],
			}
			if liveStream, ok := s.Relay.GetStream(st.MountName); ok {
				entry.Viewers = liveStream.ViewerCount()
				if entry.HasVideo {
					if vs, ok := s.Relay.GetStream(st.MountName + "/video"); ok {
						vm := vs.VideoMetricsSnapshot()
						if vm.Width > 0 {
							entry.VideoWidth = vm.Width
							entry.VideoHeight = vm.Height
							entry.VideoFPS = vm.FPS
							entry.VideoGOP = vm.GOPSeconds
							entry.VideoKbps = vm.BitrateKbps
						}
						if c := vs.ViewerCount(); c > entry.Viewers {
							entry.Viewers = c
						}
					}
				}
			}
			info = append(info, entry)
			if st.Visible {
				visible = append(visible, entry)
			}
		}
		payload, _ := json.Marshal(visible)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return err
		}
		// Per-stream named events so the player's `sse.on('stream', …)`
		// handler actually fires; the unnamed `data:` frame above is
		// kept for legacy listeners.
		for _, entry := range info {
			single, _ := json.Marshal(map[string]interface{}{
				"mount":        entry.Mount,
				"title":        entry.CurrentSong,
				"artist":       entry.Name,
				"format":       "",
				"bitrate":      entry.Bitrate,
				"listeners":    entry.Listeners,
				"viewers":      entry.Viewers,
				"video_width":  entry.VideoWidth,
				"video_height": entry.VideoHeight,
				"video_fps":    entry.VideoFPS,
				"video_gop":    entry.VideoGOP,
				"video_kbps":   entry.VideoKbps,
			})
			if _, err := fmt.Fprintf(w, "event: stream\ndata: %s\n\n", single); err != nil {
				return err
			}
		}
		flusher.Flush()
		return nil
	}
	if err := send(); err != nil {
		return
	}
	for {
		select {
		case <-s.done:
			return
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if err := send(); err != nil {
				return
			}
		}
	}
}

func (s *Server) handleLegacyStats(w http.ResponseWriter, r *http.Request) {
	streams := s.Relay.Snapshot()
	type IcecastSource struct {
		AudioInfo         string      `json:"audio_info"`
		Bitrate           interface{} `json:"bitrate"`
		Genre             string      `json:"genre"`
		Listeners         int         `json:"listeners"`
		ListenURL         string      `json:"listenurl"`
		Mount             string      `json:"mount"`
		ServerDescription string      `json:"server_description"`
		ServerName        string      `json:"server_name"`
		ServerType        string      `json:"server_type"`
		StreamStart       string      `json:"stream_start"`
		Title             string      `json:"title"`
		Dummy             interface{} `json:"dummy"`
	}
	sources := make([]IcecastSource, len(streams))
	host := s.Config.HostName
	if !strings.Contains(host, ":") {
		host = net.JoinHostPort(host, s.Config.Port)
	}
	proto := "http://"
	if s.Config.UseHTTPS {
		proto = "https://"
	}
	for i, st := range streams {
		sources[i] = IcecastSource{AudioInfo: fmt.Sprintf("bitrate=%s", st.Bitrate), Bitrate: st.Bitrate, Genre: st.Genre, Listeners: st.ListenersCount, ListenURL: proto + host + st.MountName, Mount: st.MountName, ServerDescription: st.Description, ServerName: st.Name, ServerType: st.ContentType, StreamStart: st.Started.Format(time.RFC1123), Title: st.CurrentSong, Dummy: nil}
	}
	resp := map[string]interface{}{"icestats": map[string]interface{}{"admin": s.Config.AdminEmail, "host": s.Config.HostName, "location": s.Config.Location, "server_id": "Icecast 2.4.4 (TinyIce)", "server_start": time.Now().Format(time.RFC1123), "source": sources}}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	mount := r.URL.Query().Get("mount")
	history := s.Relay.History.Get(mount)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

func (s *Server) handleGetStats(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	allStreams := s.Relay.Snapshot()
	sort.Slice(allStreams, func(i, j int) bool {
		return allStreams[i].ListenersCount > allStreams[j].ListenersCount
	})

	topStreams := allStreams
	if len(topStreams) > 10 {
		topStreams = topStreams[:10]
	}

	topListenersUA := []relay.UAStat{}
	topSourcesUA := []relay.UAStat{}
	if s.Relay.History != nil {
		if uas := s.Relay.History.GetTopUAs("listener", 10); uas != nil {
			topListenersUA = uas
		}
		if uas := s.Relay.History.GetTopUAs("source", 10); uas != nil {
			topSourcesUA = uas
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"top_streams":      topStreams,
		"top_listeners_ua": topListenersUA,
		"top_sources_ua":   topSourcesUA,
	})
}

// handleInsights returns per-mount listener / bandwidth time-series data
// from the listener_histories table, optionally filtered to a smaller
// window and downsampled so the payload stays small enough to chart.
//
//	hours       — window size (default 24, max 24*365*5 = 5 years)
//	max_points  — server-side downsample target per mount (default 200,
//	              max 2000); buckets average listeners and sum bytes
//	              within each bucket so a 7-day view doesn't ship 10k
//	              rows per stream.
func (s *Server) handleInsights(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if s.Relay.History == nil {
		http.Error(w, "History disabled", http.StatusServiceUnavailable)
		return
	}

	hours := 24
	if v := r.URL.Query().Get("hours"); v != "" {
		// 5-year ceiling — generous for an "all-time" view without
		// letting a malformed query trigger a multi-decade SELECT.
		const maxHours = 24 * 365 * 5
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= maxHours {
			hours = n
		}
	}
	maxPoints := 200
	if v := r.URL.Query().Get("max_points"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 10 && n <= 2000 {
			maxPoints = n
		}
	}

	stats := s.Relay.History.GetAllHistoricalStats(time.Duration(hours) * time.Hour)
	for mount, series := range stats {
		stats[mount] = downsampleHistorical(series, maxPoints)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// handleGeo returns a per-country aggregated view of currently active
// listeners. Powers the dashboard map. Optional ?mount= narrows to
// one stream. Includes (lat, lon) centroid for each country so the
// frontend can plot bubbles without an extra round-trip.
//
// Attribution: GeoIP data sourced from db-ip.com / CC BY 4.0 — the
// dashboard surfaces this near the map.
func (s *Server) handleGeo(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	mount := strings.TrimSpace(r.URL.Query().Get("mount"))
	rows := s.GeoTracker.Snapshot(mount)
	total := 0
	for _, row := range rows {
		total += row.Listeners
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"countries":   rows,
		"total":       total,
		"attribution": "GeoIP data: db-ip.com / CC BY 4.0",
	})
}

// handleTraffic returns total inbound (sources) and outbound (listeners)
// bytes transferred over the standard reporting windows. Computed by
// summing per-sample deltas of listener_histories with stream-restart
// detection (see relay.HistoryManager.GetTrafficTotals).
func (s *Server) handleTraffic(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if s.Relay.History == nil {
		http.Error(w, "History disabled", http.StatusServiceUnavailable)
		return
	}
	resp := map[string]any{
		"day":      s.Relay.History.GetTrafficTotals(24 * time.Hour),
		"week":     s.Relay.History.GetTrafficTotals(7 * 24 * time.Hour),
		"month":    s.Relay.History.GetTrafficTotals(30 * 24 * time.Hour),
		"quarter":  s.Relay.History.GetTrafficTotals(90 * 24 * time.Hour),
		"year":     s.Relay.History.GetTrafficTotals(365 * 24 * time.Hour),
		"lifetime": s.Relay.History.GetTrafficTotals(0),
		// Kept for back-compat with the existing TrafficTotalsCard; same
		// value as `lifetime`.
		"all": s.Relay.History.GetTrafficTotals(0),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// downsampleHistorical buckets a sorted-ascending series into at most n
// points by averaging listeners and summing bytes_in / bytes_out within
// each bucket. Pass-through when the series already fits.
func downsampleHistorical(series []relay.HistoricalStat, n int) []relay.HistoricalStat {
	if len(series) <= n {
		return series
	}
	bucketSize := (len(series) + n - 1) / n
	out := make([]relay.HistoricalStat, 0, n)
	for i := 0; i < len(series); i += bucketSize {
		end := i + bucketSize
		if end > len(series) {
			end = len(series)
		}
		var listeners int
		var bi, bo int64
		for _, s := range series[i:end] {
			listeners += s.Listeners
			bi += s.BytesIn
			bo += s.BytesOut
		}
		cnt := end - i
		out = append(out, relay.HistoricalStat{
			Timestamp: series[i+cnt/2].Timestamp,
			Listeners: listeners / cnt,
			BytesIn:   bi,
			BytesOut:  bo,
		})
	}
	return out
}

func (s *Server) handleWebRTCOffer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	mount := r.URL.Query().Get("mount")
	if mount == "" {
		http.Error(w, "mount query param required", http.StatusBadRequest)
		return
	}

	var offer webrtc.SessionDescription
	if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	answer, err := s.WebRTCM.HandleOffer(mount, offer)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(answer)
}

func (s *Server) handleWebRTCSourceOffer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	mount := r.URL.Query().Get("mount")
	if mount == "" {
		http.Error(w, "mount query param required", http.StatusBadRequest)
		return
	}

	// Auth gate. The icecast SOURCE / RTMP / SRT ingest paths all
	// require the per-mount source password (or DefaultSourcePassword)
	// before accepting a publish; this endpoint MUST do the same.
	// Without it, anyone on the internet who can POST here can hijack
	// any mount's broadcast — pion happily ingests whatever audio they
	// send, and tinyice broadcasts it to the radio's listeners.
	//
	// Two credentials are accepted:
	//
	//  1. The per-mount source password, via Basic auth (matches
	//     handleSource) or a `password` query parameter — this is what
	//     scripted publishers use.
	//
	//  2. A logged-in admin session or API token that has access to the
	//     mount. The "Go Live" page is already behind admin auth, and
	//     asking the operator to re-type (or put in the URL) a password
	//     the server already knows is why browser broadcasting never
	//     worked. Session auth additionally requires the CSRF token, so
	//     a third-party page can't start a broadcast with the operator's
	//     cookie.
	host := s.clientIP(r)
	if err := s.checkAuthLimit(host); err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	supplied := ""
	if _, p, ok := r.BasicAuth(); ok {
		supplied = p
	} else if q := r.URL.Query().Get("password"); q != "" {
		supplied = q
	}

	authorized := false
	if supplied != "" {
		requiredPass, found := s.getSourcePassword(mount)
		if !found {
			requiredPass = s.Config.DefaultSourcePassword
		}
		authorized = requiredPass != "" && config.CheckPasswordHash(supplied, requiredPass)
	} else if user, ok := s.checkAuth(r); ok {
		// Only reached when no source password was offered, so the
		// Basic-auth branch of checkAuth can't burn a rate-limit slot
		// on a publisher that authenticated the other way.
		if !s.isCSRFSafe(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if !s.hasAccess(user, mount) {
			s.logAuthFailed(user.Username, host, "no access to mount "+mount)
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		authorized = true
	}

	if !authorized {
		s.recordAuthFailure(host)
		s.logAuthFailed("webrtc-source", host, "invalid source password")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	s.recordAuthSuccess(host)

	if s.Config.DisabledMounts[mount] {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var offer webrtc.SessionDescription
	if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	answer, err := s.WebRTCM.HandleSourceOffer(mount, offer)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(answer)
}

func (s *Server) handleGoLive(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	csrf := ""
	if cookie, err := r.Cookie("sid"); err == nil {
		s.sessionsMu.RLock()
		if sess, ok := s.sessions[cookie.Value]; ok {
			csrf = sess.CSRFToken
		}
		s.sessionsMu.RUnlock()
	}

	pageData := s.BasePageData(csrf)
	pageData["user"] = map[string]interface{}{
		"username": user.Username,
		"role":     user.Role,
	}
	s.shell.Render(w, "admin", "Go Live — "+s.Config.PageTitle, pageData)
}

func (s *Server) handleGoLiveChunk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Block cross-origin form POSTs that could trick a logged-in admin's
	// browser into broadcasting attacker-supplied bytes. The handler
	// reads raw audio off r.Body, so the JSON Content-Type fast-path
	// in isCSRFSafe doesn't apply automatically — caller must include
	// the CSRF token.
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	mount := r.URL.Query().Get("mount")
	if mount == "" {
		http.Error(w, "mount query param required", http.StatusBadRequest)
		return
	}

	user, ok := s.checkAuth(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	// Without this, any authenticated user could broadcast to any
	// mount — including mounts owned by other users.
	if !s.hasAccess(user, mount) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	stream := s.Relay.GetOrCreateStream(mount)
	stream.SetSourceIP("webaudio-http")
	stream.Broadcast(body, s.Relay)

	w.WriteHeader(http.StatusOK)
}
