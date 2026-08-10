package server

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
	"github.com/DatanoiseTV/tinyice/relay"
)

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	logger.L.Debugw("Root handler request", "method", r.Method, "path", r.URL.Path)
	if r.Method == "PUT" || r.Method == "SOURCE" {
		s.handleSource(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, ".m3u8") || strings.HasSuffix(r.URL.Path, ".m3u") {
		s.handlePlaylist(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, ".pls") {
		s.handlePLS(w, r)
		return
	}
	// Known app paths — serve the appropriate page, not a stream listener
	path := r.URL.Path
	if strings.HasPrefix(path, "/admin") {
		// All /admin/* paths serve the admin SPA shell
		s.handleAdmin(w, r)
		return
	}

	// Only treat as stream listener if it's not a known app route
	appPrefixes := []string{"/login", "/logout", "/setup", "/auth/", "/api/", "/explore", "/developers", "/assets/", "/events", "/player", "/embed/", "/webrtc", "/kiosk"}
	isAppRoute := path == "/" || path == "/favicon.ico"
	for _, prefix := range appPrefixes {
		if strings.HasPrefix(path, prefix) || path == prefix {
			isAppRoute = true
			break
		}
	}

	if r.Method == "GET" && !isAppRoute {
		s.handleListener(w, r)
		return
	}
	s.handleStatus(w, r)
}

func (s *Server) handlePLS(w http.ResponseWriter, r *http.Request) {
	mount := strings.TrimSuffix(r.URL.Path, ".pls")
	st, ok := s.Relay.GetStream(mount)
	if !ok {
		http.NotFound(w, r)
		return
	}

	baseURL := s.Config.BaseURL
	if baseURL == "" {
		proto := "http://"
		if s.Config.UseHTTPS || r.Header.Get("X-Forwarded-Proto") == "https" {
			proto = "https://"
		}
		baseURL = proto + r.Host
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	w.Header().Set("Content-Type", "audio/x-scpls")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.pls\"", st.Name))
	fmt.Fprintf(w, "[playlist]\nNumberOfEntries=1\nFile1=%s%s\nTitle1=%s\nLength1=-1\nVersion=2\n", baseURL, mount, st.Name)
}

func (s *Server) handlePlaylist(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	ext := ".m3u8"
	if strings.HasSuffix(path, ".m3u") {
		ext = ".m3u"
	}
	mount := strings.TrimSuffix(path, ext)

	st, ok := s.Relay.GetStream(mount)
	if !ok {
		http.NotFound(w, r)
		return
	}

	baseURL := s.Config.BaseURL
	if baseURL == "" {
		proto := "http://"
		if s.Config.UseHTTPS || r.Header.Get("X-Forwarded-Proto") == "https" {
			proto = "https://"
		}
		baseURL = proto + r.Host
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	w.Header().Set("Content-Type", "audio/x-mpegurl")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s%s\"", st.Name, ext))
	fmt.Fprintf(w, "#EXTM3U\n#EXTINF:-1,%s\n%s%s\n", st.Name, baseURL, mount)
}

func (s *Server) handleSource(w http.ResponseWriter, r *http.Request) {
	clientIP := s.clientIP(r)
	if s.isBanned(clientIP) {
		logger.L.Warnw("Banned IP source connection", "ip", clientIP)
		return
	}
	mount := r.URL.Path
	requiredPass, found := s.getSourcePassword(mount)
	if !found {
		requiredPass = s.Config.DefaultSourcePassword
	}

	if s.Config.DisabledMounts[mount] {
		logger.L.Warnw("Disabled mount connection", "mount", mount)
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	_, p, ok := r.BasicAuth()
	if !ok || !config.CheckPasswordHash(p, requiredPass) {
		u, _, _ := r.BasicAuth()
		if u == "" {
			u = "unknown"
		}
		s.logAuthFailed(u, clientIP, "source password mismatch")
		w.Header().Set("WWW-Authenticate", `Basic realm="Icecast"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	s.logAuth().Infow("Source auth successful", "mount", mount, "ip", clientIP)

	if s.Relay.History != nil {
		s.Relay.History.RecordUA(r.Header.Get("User-Agent"), "source")
	}

	// Single-source enforcement. Without this, robodj-class clients
	// that retry on connect (we've observed 3 SOURCE attempts within
	// 25 ms after a restart) all get accepted concurrently. Two write
	// loops then interleave bytes into the same ring buffer and every
	// downstream MP3 / Ogg decoder errors out on missing frame
	// headers. Claim BEFORE the hijack so we can return a clean 403
	// for the loser; release in defer so the next reconnect (after
	// the current source drops) succeeds.
	stream := s.Relay.GetOrCreateStream(mount)
	if !stream.TryClaimSource(clientIP) {
		existing := stream.GetSourceIP()
		logger.L.Warnw("Source rejected: mount already has an active source",
			"mount", mount, "new_ip", clientIP, "existing_ip", existing)
		http.Error(w, "Mount already has an active source", http.StatusForbidden)
		return
	}
	defer stream.ReleaseSource()

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking unsupported", http.StatusInternalServerError)
		return
	}
	conn, bufrw, err := hj.Hijack()
	if err != nil {
		logger.L.Errorf("Hijack failed: %v", err)
		return
	}
	defer conn.Close()
	// Per-read deadline on the source. Without it, a source that
	// silently drops (network blip, encoder crash without FIN)
	// holds the handler goroutine in bufrw.Read forever; the stream
	// stays mounted, transcoders keep their input subscription, and
	// the next reconnect from the same encoder leaves zombies. The
	// deadline is refreshed after every successful read so a
	// continuously-streaming source is never penalised — only
	// genuinely silent connections die.
	const sourceReadTimeout = 60 * time.Second

	bufrw.WriteString("HTTP/1.0 200 OK\r\nServer: Icecast 2.4.4\r\nConnection: Keep-Alive\r\n\r\n")
	bufrw.Flush()

	logger.L.Infow("Source connected", "mount", mount, "ip", clientIP, "ua", r.Header.Get("User-Agent"))
	s.dispatchWebhook("source_connect", map[string]interface{}{
		"mount": mount,
		"ip":    clientIP,
		"ua":    r.Header.Get("User-Agent"),
		"name":  r.Header.Get("Ice-Name"),
	})

	s.updateSourceMetadata(stream, mount, r)

	// Late-joining listeners on Ogg-based mounts (Vorbis / Opus / FLAC-in-Ogg)
	// need the BOS + comment/setup pages prepended so they can initialise the
	// codec. Capture those pages while forwarding the byte stream.
	captureHeaders := isOggContentType(r.Header.Get("Content-Type")) || stream.IsOgg()
	var headerBuf []byte
	captureStartOffset := stream.Buffer.HeadOffset()

	buf := make([]byte, 8192)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(sourceReadTimeout))
		n, err := bufrw.Read(buf)
		if n > 0 {
			stream.Broadcast(buf[:n], s.Relay)
			if captureHeaders {
				headerBuf = append(headerBuf, buf[:n]...)
				endPos, needMore, abort := relay.FindOggHeaderEnd(headerBuf)
				if abort {
					captureHeaders = false
					headerBuf = nil
				} else if !needMore {
					headers := make([]byte, endPos)
					copy(headers, headerBuf[:endPos])
					stream.StoreOggHead(headers, captureStartOffset+int64(endPos))
					logger.L.Infow("Source: Captured Ogg headers",
						"mount", mount,
						"bytes", len(headers),
						"offset", captureStartOffset+int64(endPos),
					)
					captureHeaders = false
					headerBuf = nil
				}
			}
		}
		if err != nil {
			break
		}
	}
	logger.L.Infow("Source disconnected", "mount", mount)
	s.dispatchWebhook("source_disconnect", map[string]interface{}{
		"mount": mount,
	})
	s.Relay.RemoveStream(mount)
}

// looksLikeUnknownMount reports whether a 404 for this path should count
// toward the vuln-scanner lockout. Anything that is configured as a stream
// (even if no source is currently connected) is a legitimate listener URL;
// browser prefetch paths with static file extensions are also ignored.
func (s *Server) looksLikeUnknownMount(mount string) bool {
	// Known configured mount — always legitimate.
	if _, ok := s.Config.Mounts[mount]; ok {
		return false
	}
	if _, ok := s.Config.AdvancedMounts[mount]; ok {
		return false
	}
	if _, ok := s.Config.VisibleMounts[mount]; ok {
		return false
	}
	if _, ok := s.Config.FallbackMounts[mount]; ok {
		return false
	}
	for _, u := range s.Config.Users {
		if _, ok := u.Mounts[mount]; ok {
			return false
		}
	}
	// Browser prefetch paths — not a stream attempt.
	lower := strings.ToLower(mount)
	if strings.HasPrefix(lower, "/.well-known/") {
		return false
	}
	staticSuffixes := []string{
		".ico", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp",
		".css", ".js", ".map", ".json", ".xml", ".txt", ".webmanifest",
		".woff", ".woff2", ".ttf",
	}
	for _, ext := range staticSuffixes {
		if strings.HasSuffix(lower, ext) {
			return false
		}
	}
	return true
}

// isOggContentType returns true if the given HTTP Content-Type indicates an
// Ogg-container stream (Vorbis, Opus, FLAC-in-Ogg, etc.).
func isOggContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if ct == "" {
		return false
	}
	// Strip any "; charset=..." suffix.
	if idx := strings.Index(ct, ";"); idx >= 0 {
		ct = strings.TrimSpace(ct[:idx])
	}
	switch ct {
	case "application/ogg", "audio/ogg", "audio/opus", "audio/vorbis":
		return true
	}
	// Also recognise vendor-specific shapes like audio/ogg; codecs=opus.
	if strings.HasPrefix(ct, "audio/ogg") || strings.HasPrefix(ct, "application/ogg") {
		return true
	}
	return false
}

func (s *Server) updateSourceMetadata(stream *relay.Stream, mount string, r *http.Request) {
	bitrate := r.Header.Get("Ice-Bitrate")
	if bitrate == "" || bitrate == "N/A" {
		audioInfo := r.Header.Get("Ice-Audio-Info")
		if audioInfo != "" {
			parts := strings.Split(audioInfo, ";")
			for _, part := range parts {
				if strings.HasPrefix(strings.TrimSpace(part), "bitrate=") {
					bitrate = strings.TrimPrefix(strings.TrimSpace(part), "bitrate=")
					break
				}
			}
		}
	}
	isPublic := r.Header.Get("Ice-Public") == "1"
	isVisible := s.Config.VisibleMounts[mount]

	if !isAlreadyMP3(r.Header.Get("Content-Type")) {
		s.TranscoderM.EnsureAutoMP3Transcoders(mount, s.Config.AutoTranscodeMP3Bitrates, s.Config.Transcoders)
	}

	// Per-mount NameOverride from AdvancedMounts: rebrand a stream
	// without poking the encoder. Empty / unset = use the source's
	// Ice-Name as before.
	iceName := r.Header.Get("Ice-Name")
	if adv, ok := s.Config.AdvancedMounts[mount]; ok && adv != nil && adv.NameOverride != "" {
		iceName = adv.NameOverride
	}

	if stream.UpdateMetadata(iceName, r.Header.Get("Ice-Description"), r.Header.Get("Ice-Genre"), r.Header.Get("Ice-Url"), bitrate, r.Header.Get("Content-Type"), isPublic, isVisible) {
		s.dispatchWebhook("metadata_update", map[string]interface{}{
			"mount":        mount,
			"name":         stream.Name,
			"description":  stream.Description,
			"genre":        stream.Genre,
			"current_song": stream.CurrentSong,
		})
	}
}

func (s *Server) handleListener(w http.ResponseWriter, r *http.Request) {
	clientIP := s.clientIP(r)
	if s.isBanned(clientIP) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	originalMount := r.URL.Path
	mount := originalMount

	if s.Relay.History != nil {
		s.Relay.History.RecordUA(r.Header.Get("User-Agent"), "listener")
	}

	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if s.Config.LowLatencyMode {
		w.Header().Set("X-Accel-Buffering", "no")
	}

	flusher, _ := w.(http.Flusher)
	// The subscriber id must stay unique per CONNECTION, so it keeps the
	// raw RemoteAddr (which carries the peer port); only the logged/geo
	// identity is resolved through the trusted-proxy chain.
	id := r.RemoteAddr + "-" + fmt.Sprintf("%d", time.Now().UnixNano())
	logger.L.Infow("Listener connected", "mount", mount, "ip", clientIP, "ua", r.Header.Get("User-Agent"))
	defer logger.L.Infow("Listener disconnected", "mount", mount, "ip", clientIP)

	recoveryTicker := time.NewTicker(10 * time.Second)
	defer recoveryTicker.Stop()

	var primaryFirstSeen time.Time
	const fallbackHysteresis = 30 * time.Second

	for {
		select {
		case <-s.done:
			return
		default:
		}

		if mount != originalMount {
			if _, ok := s.Relay.GetStream(originalMount); ok {
				if primaryFirstSeen.IsZero() {
					primaryFirstSeen = time.Now()
				}
				if time.Since(primaryFirstSeen) >= fallbackHysteresis {
					logger.L.Infow("Primary stream stable, recovering from fallback",
						"mount", originalMount,
						"stable_for", time.Since(primaryFirstSeen),
					)
					mount = originalMount
					primaryFirstSeen = time.Time{}
				}
			} else {
				primaryFirstSeen = time.Time{}
			}
		}

		stream, ok := s.Relay.GetStream(mount)
		if !ok {
			fallback, hasFallback := s.Config.FallbackMounts[mount]
			if hasFallback && fallback != mount {
				logger.L.Infow("Primary stream down, falling back", "from", mount, "to", fallback)
				mount = fallback
				continue
			}
			if mount != originalMount {
				mount = originalMount
				time.Sleep(1 * time.Second)
				continue
			}
			// Only count as a scan attempt when the mount is something
			// we've never known — a configured mount that's currently
			// offline is a legitimate listener, not an attacker probing
			// paths. Similarly, obvious non-stream prefetch paths
			// (favicon.ico, manifest.json, *.png …) don't deserve a ban.
			if s.looksLikeUnknownMount(mount) {
				s.recordScanAttempt(clientIP, mount)
			}
			http.NotFound(w, r)
			return
		}

		metaint := 0
		if r.Header.Get("Icy-MetaData") == "1" && !stream.IsOgg() {
			metaint = 16000
			w.Header().Set("icy-metaint", "16000")
		}
		// ICY metadata headers — populated for all listeners regardless of
		// metaint negotiation so non-shoutcast clients (browsers, players)
		// can still surface the station name in their UI. Field reads are
		// single-string copies — same race profile the rest of this file
		// already accepts.
		icyName := stream.Name
		if icyName == "" { icyName = s.Config.PageTitle }
		icyGenre := stream.Genre
		icyURL := stream.URL
		icyDesc := stream.Description
		icyBR := stream.Bitrate
		icyPub := stream.Public
		w.Header().Set("icy-name", icyName)
		if icyGenre != "" { w.Header().Set("icy-genre", icyGenre) }
		if icyURL   != "" { w.Header().Set("icy-url", icyURL) }
		if icyDesc  != "" { w.Header().Set("icy-description", icyDesc) }
		if icyBR    != "" { w.Header().Set("icy-br", icyBR) }
		if icyPub        { w.Header().Set("icy-pub", "1") } else { w.Header().Set("icy-pub", "0") }

		if s.Config.MaxListeners > 0 && stream.ListenersCount() >= s.Config.MaxListeners {
			http.Error(w, "Server Full", http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", stream.ContentType)
		if mount != originalMount {
			w.Header().Set("X-Stream-Status", "fallback")
		} else {
			w.Header().Set("X-Stream-Status", "primary")
		}
		if flusher != nil {
			flusher.Flush()
		}

		if !s.serveStreamData(w, r, stream, id, originalMount, mount, recoveryTicker, metaint) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *Server) serveStreamData(w http.ResponseWriter, r *http.Request, stream *relay.Stream, id, originalMount, currentMount string, recoveryTicker *time.Ticker, metaint int) bool {
	// Burst size defaults to 512 KiB but can be overridden per mount via
	// AdvancedMounts.BurstSize (the "Advanced Mount Settings" UI field).
	// At typical listener bitrates (128–320 kbps) this puts 10–30 seconds
	// of audio into the client's cache on connect, so a ~5 s network stall
	// no longer drains the player's buffer and forces a reconnect.
	burst := 512 * 1024
	if adv, ok := s.Config.AdvancedMounts[currentMount]; ok && adv != nil && adv.BurstSize > 0 {
		burst = adv.BurstSize
	}
	offset, signal := stream.Subscribe(id, burst)
	defer stream.Unsubscribe(id)

	// Geo-track this listener for the dashboard map. The full
	// (city, country, lat, lon) tuple is captured here (synchronous
	// mmdb lookup, sub-millisecond) so the deferred Remove uses the
	// SAME tuple we registered with — avoids races where the lookup
	// might briefly differ across calls (DB swap mid-listen) and
	// double-counts.
	listenerGeo := s.GeoTracker.Add(s.clientIP(r), currentMount)
	defer s.GeoTracker.Remove(listenerGeo, currentMount)

	// For Ogg streams (Opus / Vorbis / FLAC-in-Ogg) route all output through
	// a per-listener Ogg page rewriter. It regenerates the bitstream serial,
	// resets the page sequence, and rebases the granule so the listener sees
	// a clean timeline starting near zero — without this, replaying the
	// cached BOS/Tags pages in front of live audio causes a multi-minute
	// granule jump and strict decoders fill the gap with silence / robotic
	// concealment.
	var out io.Writer = w
	var oggRewriter *relay.OggPageRewriter
	if stream.IsOggStream {
		oggRewriter = relay.NewOggPageRewriter(w)
		out = oggRewriter
	}

	if oggHead := stream.GetOggHead(); oggHead != nil {
		if _, err := out.Write(oggHead); err != nil {
			return false
		}
		logger.L.Debugf("Ogg Listener %s: Sending stored headers (%d bytes), then starting burst at %d", id, len(oggHead), offset)
	}

	// Respect the stream's minimum valid offset — set whenever the
	// source reconfigures its codec mid-stream. Without this a listener
	// that subscribes right after an OBS reconfig replays pre-reconfig
	// bytes (old SPS/PPS) and the decoder dies as soon as the new
	// parameters activate.
	if min := stream.GetMinListenerOffset(); min > offset {
		offset = min
	}

	// For raw H.264 video listeners, seek back to the most recent
	// keyframe (so playback doesn't start on a P-frame whose reference
	// frames aren't in the listener's buffer) and prepend the cached
	// SPS/PPS Annex-B headers. Without this the decoder spends the
	// first GOP spamming "non-existing PPS" / "mmco: unref short
	// failure" errors until the next keyframe cycles through.
	isH264, videoHeaders := stream.VideoInfo()
	if isH264 {
		if kf := stream.Buffer.LatestKeyframe(); kf >= 0 && kf > offset {
			offset = kf
		}
		if len(videoHeaders) > 0 {
			if _, err := out.Write(videoHeaders); err != nil {
				return false
			}
			logger.L.Debugf("H264 Listener %s: Sent SPS/PPS (%d bytes), starting at keyframe %d", id, len(videoHeaders), offset)
		}
	}

	// Per-write deadline. Without it, a Write to a slow / dead /
	// half-disconnected listener blocks forever — the handler
	// goroutine sits in TCP-write-blocked state, the listener slot
	// stays registered on the stream, and the conn ends up in
	// CLOSE_WAIT. With many of those accumulating, the runtime ran
	// out of usable handler slots and NEW HTTPS requests started
	// timing out wholesale (recurring "i/o timeout" symptom).
	//
	// 30 s is conservative — typical TCP send to a healthy listener
	// returns in <1 ms; 30 s only fires when the kernel send buffer
	// is full and not draining (i.e. the receiver is gone). On
	// deadline expiry Write returns an error → we exit the handler →
	// Unsubscribe runs → the conn cleans up.
	rc := http.NewResponseController(w)
	const writeTimeout = 30 * time.Second

	// 64 KiB read chunk: we flush after draining the buffer on each signal,
	// so a larger chunk means fewer syscalls / TCP writes per second without
	// adding latency. 4 KiB was causing tiny-segment writes whenever the
	// source broadcast fast.
	buf := make([]byte, 64*1024)
	flusher, _ := w.(http.Flusher)

	bytesSentSinceMeta := 0
	lastSong := ""

	consecutiveSkips := 0
	maxConsecutiveSkips := 5

	// Flush-generation tracker. If the stream owner bumps the flush
	// generation (e.g. transcoder encoder restart after a source
	// flap), we snap our offset forward to MinListenerOffset so the
	// listener doesn't replay the stale buffer contents from before
	// the gap. Read once at subscribe time so we only react to bumps
	// that happen AFTER we started reading.
	lastFlushGen := stream.FlushGen()

	for {
		select {
		case <-s.done:
			return false
		case <-r.Context().Done():
			return false
		case <-recoveryTicker.C:
			if currentMount != originalMount {
				if _, ok := s.Relay.GetStream(originalMount); ok {
					return true
				}
			}
		case _, ok := <-signal:
			if !ok {
				return true
			}
			if g := stream.FlushGen(); g != lastFlushGen {
				lastFlushGen = g
				if min := stream.GetMinListenerOffset(); min > offset {
					offset = min
					// Reset the ICY meta-interval counter so the
					// 16000-byte boundary stays aligned to the new
					// post-flush byte stream.
					bytesSentSinceMeta = 0
				}
			}
			for {
				readLimit := len(buf)
				if metaint > 0 {
					remaining := metaint - bytesSentSinceMeta
					if remaining < readLimit {
						readLimit = remaining
					}
				}

				n, next, skipped := stream.Buffer.ReadAt(offset, buf[:readLimit])
				if skipped && stream.IsOggStream {
					consecutiveSkips++
					if consecutiveSkips >= maxConsecutiveSkips {
						logger.L.Warnw("Slow listener disconnected (ogg sync skip)",
							"id", id, "mount", currentMount,
							"consecutive_skips", consecutiveSkips,
						)
						return false
					}
					offset = stream.Buffer.FindNextPageBoundaryLocked(next)
					continue
				}
				if n == 0 {
					break
				}
				if skipped {
					atomic.AddInt64(&stream.BytesDropped, next-offset)
					consecutiveSkips++
					if consecutiveSkips >= maxConsecutiveSkips {
						logger.L.Warnw("Slow listener disconnected",
							"id", id, "mount", currentMount,
							"consecutive_skips", consecutiveSkips,
						)
						return false
					}
				} else {
					consecutiveSkips = 0
				}
				offset = next

				_ = rc.SetWriteDeadline(time.Now().Add(writeTimeout))
				if _, err := out.Write(buf[:n]); err != nil {
					return false
				}

				if metaint > 0 {
					bytesSentSinceMeta += n
					if bytesSentSinceMeta >= metaint {
						currentSong := stream.GetCurrentSong()
						meta := ""
						if currentSong != lastSong {
							meta = fmt.Sprintf("StreamTitle='%s';", currentSong)
							lastSong = currentSong
						}

						l := (len(meta) + 15) / 16
						res := make([]byte, 1+l*16)
						res[0] = byte(l)
						copy(res[1:], meta)

						_ = rc.SetWriteDeadline(time.Now().Add(writeTimeout))
						if _, err := w.Write(res); err != nil {
							return false
						}
						bytesSentSinceMeta = 0
					}
				}

				atomic.AddInt64(&s.Relay.BytesOut, int64(n))
				atomic.AddInt64(&stream.BytesOut, int64(n))
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	// Build stream list for the landing page
	allStreams := s.Relay.Snapshot()
	videoMounts := make(map[string]bool)
	for _, st := range allStreams {
		if strings.HasSuffix(st.MountName, "/video") {
			videoMounts[strings.TrimSuffix(st.MountName, "/video")] = true
		}
	}
	var streamList []map[string]interface{}
	for _, st := range allStreams {
		if st.Visible {
			streamList = append(streamList, map[string]interface{}{
				"mount":     st.MountName,
				"title":     st.CurrentSong,
				"artist":    st.Name,
				"format":    st.ContentType,
				"bitrate":   st.Bitrate,
				"listeners": st.ListenersCount,
				"live":      st.SourceIP != "",
				"has_video": videoMounts[st.MountName],
			})
		}
	}

	pageData := s.BasePageData("")
	pageData["streams"] = streamList
	s.shell.Render(w, "landing", s.Config.PageTitle, pageData)
}


// isAlreadyMP3 returns true if the Content-Type header advertises an MP3
// stream — an auto MP3 transcoder of an mp3 source is wasted CPU.
func isAlreadyMP3(contentType string) bool {
	ct := strings.ToLower(contentType)
	return ct == "audio/mpeg" || ct == "audio/mp3" || strings.HasPrefix(ct, "audio/mpeg;")
}
