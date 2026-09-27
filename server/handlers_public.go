package server

import (
	"net/http"
	"strings"
)

func (s *Server) handlePlayer(w http.ResponseWriter, r *http.Request) {
	mount := strings.TrimPrefix(r.URL.Path, "/player")
	if mount == "" || mount == "/" {
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		return
	}

	stream, ok := s.Relay.GetStream(mount)
	if !ok {
		fallback, hasFallback := s.Config.FallbackMounts[mount]
		if hasFallback {
			stream, ok = s.Relay.GetStream(fallback)
		}
	}

	if !ok {
		http.NotFound(w, r)
		return
	}

	snap := stream.Snapshot()
	pageData := s.BasePageData("")
	pageData["mount"] = mount
	pageData["title"] = snap.CurrentSong
	pageData["artist"] = snap.Name
	pageData["format"] = snap.ContentType
	pageData["bitrate"] = snap.Bitrate
	pageData["listeners"] = snap.ListenersCount
	pageData["hasWebRTC"] = true
	// hasVideo is true iff a live /video sub-mount exists; the frontend
	// uses it to decide between <audio> and <video> and between the raw
	// mount URL and the HLS playlist.
	_, hasVideo := s.Relay.GetStream(mount + "/video")
	pageData["hasVideo"] = hasVideo
	// hasHLS tells the player whether an HLS playlist for this mount can
	// exist at all. It cannot when the audio codec is outside what the
	// MPEG-TS muxer can declare (Opus, FLAC, Vorbis) — which is every
	// WebRTC publisher. Without this the player attached hls.js to a
	// playlist the server refuses to build and the browser reported
	// MEDIA_ERR_SRC_NOT_SUPPORTED; with it the player goes straight to
	// WHEP, which carries Opus + H.264 natively.
	pageData["hasHLS"] = stream.HLSMuxableAudio()
	s.shell.Render(w, "player", snap.Name+" — "+s.Config.PageTitle, pageData)
}

func (s *Server) handleWebRTCPlayer(w http.ResponseWriter, r *http.Request) {
	mount := strings.TrimPrefix(r.URL.Path, "/player-webrtc")
	if mount == "" || mount == "/" {
		http.Redirect(w, r, "/explore", http.StatusSeeOther)
		return
	}
	// Redirect to unified player with WebRTC mode
	http.Redirect(w, r, "/player"+mount+"?mode=webrtc", http.StatusMovedPermanently)
}

func (s *Server) handleEmbed(w http.ResponseWriter, r *http.Request) {
	mount := strings.TrimPrefix(r.URL.Path, "/embed")
	if mount == "" || mount == "/" {
		http.NotFound(w, r)
		return
	}

	stream, ok := s.Relay.GetStream(mount)
	if !ok {
		fallback, hasFallback := s.Config.FallbackMounts[mount]
		if hasFallback {
			stream, ok = s.Relay.GetStream(fallback)
		}
	}

	if !ok {
		http.NotFound(w, r)
		return
	}

	snap := stream.Snapshot()
	w.Header().Set("X-Frame-Options", "ALLOWALL")
	pageData := s.BasePageData("")
	pageData["mount"] = mount
	pageData["title"] = snap.CurrentSong
	pageData["artist"] = snap.Name
	pageData["format"] = snap.ContentType
	pageData["bitrate"] = snap.Bitrate
	pageData["listeners"] = snap.ListenersCount
	s.shell.Render(w, "embed", s.Config.PageTitle, pageData)
}

func (s *Server) handleExplore(w http.ResponseWriter, r *http.Request) {
	pageData := s.BasePageData("")
	pageData["streams"] = s.visibleStreamList()
	s.shell.Render(w, "explore", "Explore — "+s.Config.PageTitle, pageData)
}

func (s *Server) handleDevelopers(w http.ResponseWriter, r *http.Request) {
	pageData := s.BasePageData("")
	s.shell.Render(w, "developers", "Developers — "+s.Config.PageTitle, pageData)
}
