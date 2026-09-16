package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
	"github.com/DatanoiseTV/tinyice/relay"
)

// ---------------------------------------------------------------------------
// JSON helpers
// ---------------------------------------------------------------------------

func jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ---------------------------------------------------------------------------
// Audit helper
// ---------------------------------------------------------------------------

func (s *Server) Audit(r *http.Request, action, resourceType, resourceID, detail string) {
	if !s.Config.AuditEnabled {
		return
	}
	username := "system"
	if user, ok := s.checkAuth(r); ok {
		username = user.Username
	}
	// clientIP, not RemoteAddr: behind a reverse proxy every audit row
	// would otherwise read as the proxy's address, which is what
	// trusted_proxies exists to prevent.
	s.Relay.History.RecordAudit(username, action, resourceType, resourceID, detail, s.clientIP(r))
}

func (s *Server) apiGetAuditLog(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	page := 1
	limit := 25
	if v := r.URL.Query().Get("page"); v != "" {
		fmt.Sscanf(v, "%d", &page)
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		fmt.Sscanf(v, "%d", &limit)
	}
	if limit > 100 {
		limit = 100
	}
	if page < 1 {
		page = 1
	}
	category := r.URL.Query().Get("category")

	entries, total := s.Relay.History.GetAuditLog(page, limit, category)
	jsonResponse(w, map[string]interface{}{
		"entries": entries,
		"total":   total,
		"page":    page,
		"limit":   limit,
	})
}

// ---------------------------------------------------------------------------
// Streams
// ---------------------------------------------------------------------------

func (s *Server) apiGetStreams(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	allStreams := s.Relay.Snapshot()
	// Build a set of mounts that currently have a live /video sub-mount
	// so we can report has_video per parent mount (the frontend flips
	// the player between <audio> and <video> based on this).
	videoMounts := make(map[string]bool)
	for _, st := range allStreams {
		if strings.HasSuffix(st.MountName, "/video") {
			videoMounts[strings.TrimSuffix(st.MountName, "/video")] = true
		}
	}

	type streamInfo struct {
		Mount       string  `json:"mount"`
		ContentType string  `json:"content_type"`
		Bitrate     string  `json:"bitrate"`
		Listeners   int     `json:"listeners"`
		SourceIP    string  `json:"source_ip"`
		Visible     bool    `json:"visible"`
		Enabled     bool    `json:"enabled"`
		Health      float64 `json:"health"`
		Uptime      string  `json:"uptime"`
		CurrentSong string  `json:"current_song"`
		Name        string  `json:"name"`
		HasVideo    bool    `json:"has_video"`
		VideoWidth  int     `json:"video_width,omitempty"`
		VideoHeight int     `json:"video_height,omitempty"`
		VideoFPS    float64 `json:"video_fps,omitempty"`
		VideoGOP    float64 `json:"video_gop,omitempty"`
		VideoKbps   int     `json:"video_kbps,omitempty"`
	}

	var result []streamInfo
	seen := make(map[string]bool)
	for _, st := range allStreams {
		// Don't surface the /video sub-mount as its own top-level entry;
		// it's an implementation detail of the parent audio mount.
		if strings.HasSuffix(st.MountName, "/video") {
			continue
		}
		if s.hasAccess(user, st.MountName) {
			seen[st.MountName] = true
			info := streamInfo{
				Mount:       st.MountName,
				ContentType: st.ContentType,
				Bitrate:     st.Bitrate,
				Listeners:   st.ListenersCount,
				SourceIP:    st.SourceIP,
				Visible:     st.Visible,
				Enabled:     st.Enabled,
				Health:      st.Health,
				Uptime:      st.Uptime,
				CurrentSong: st.CurrentSong,
				Name:        st.Name,
				HasVideo:    videoMounts[st.MountName],
			}
			// Pull video metrics from the /video sibling (that's where
			// ingest records them) so the parent row in the UI carries
			// the numbers the operator expects to see alongside its
			// listener / uptime counts.
			if info.HasVideo {
				if vs, ok := s.Relay.GetStream(st.MountName + "/video"); ok {
					vm := vs.VideoMetricsSnapshot()
					if vm.Width > 0 {
						info.VideoWidth = vm.Width
						info.VideoHeight = vm.Height
						info.VideoFPS = vm.FPS
						info.VideoGOP = vm.GOPSeconds
						info.VideoKbps = vm.BitrateKbps
					}
				}
			}
			result = append(result, info)
		}
	}

	// Add configured mounts that are not currently active (offline)
	for mount := range s.Config.Mounts {
		if !seen[mount] && s.hasAccess(user, mount) {
			seen[mount] = true
			disabled := s.Config.DisabledMounts[mount]
			visible := s.Config.VisibleMounts[mount]
			result = append(result, streamInfo{
				Mount:   mount,
				Visible: visible,
				Enabled: !disabled,
			})
		}
	}
	for _, u := range s.Config.Users {
		for mount := range u.Mounts {
			if !seen[mount] && s.hasAccess(user, mount) {
				seen[mount] = true
				disabled := s.Config.DisabledMounts[mount]
				visible := s.Config.VisibleMounts[mount]
				result = append(result, streamInfo{
					Mount:   mount,
					Visible: visible,
					Enabled: !disabled,
				})
			}
		}
	}

	if result == nil {
		result = []streamInfo{}
	}
	jsonResponse(w, result)
}

func (s *Server) apiCreateStream(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		Mount    string `json:"mount"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if body.Mount == "" || body.Password == "" {
		jsonError(w, "Mount and password are required", http.StatusBadRequest)
		return
	}
	if body.Mount[0] != '/' {
		body.Mount = "/" + body.Mount
	}

	// Check access or existence
	if !s.hasAccess(user, body.Mount) && s.mountTaken(body.Mount) {
		jsonError(w, "Mount taken", http.StatusConflict)
		return
	}

	hashed, err := config.HashPassword(body.Password)
	if err != nil {
		jsonError(w, "Failed to hash password: "+err.Error(), http.StatusBadRequest)
		return
	}
	if user.Role == config.RoleSuperAdmin {
		s.Config.LockMaps()
		s.Config.Mounts[body.Mount] = hashed
		s.Config.UnlockMaps()
	} else {
		s.Config.LockMaps()
		user.Mounts[body.Mount] = hashed
		s.Config.UnlockMaps()
	}
	s.Config.SaveConfig()
	jsonResponse(w, map[string]string{"status": "created", "mount": body.Mount})
	s.Audit(r, "mount_created", "stream", body.Mount, "")
}

// apiUpdateStream edits an existing stream mount. The mount path itself is
// immutable (renaming would require migrating per-user ownership); password,
// enabled (DisabledMounts) and visibility (VisibleMounts) can all be changed
// independently. Any omitted field is left untouched.
func (s *Server) apiUpdateStream(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		Mount    string  `json:"mount"`
		Password *string `json:"password,omitempty"`
		Enabled  *bool   `json:"enabled,omitempty"`
		Visible  *bool   `json:"visible,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if body.Mount == "" {
		jsonError(w, "Mount is required", http.StatusBadRequest)
		return
	}
	if body.Mount[0] != '/' {
		body.Mount = "/" + body.Mount
	}
	if !s.hasAccess(user, body.Mount) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Find the current password location (global vs per-user map).
	_, inGlobal := s.Config.Mounts[body.Mount]
	var owningUser *config.User
	for _, u := range s.Config.Users {
		if _, ok := u.Mounts[body.Mount]; ok {
			owningUser = u
			break
		}
	}
	if !inGlobal && owningUser == nil {
		jsonError(w, "Mount not found", http.StatusNotFound)
		return
	}

	if body.Password != nil && *body.Password != "" {
		hashed, err := config.HashPassword(*body.Password)
		if err != nil {
			jsonError(w, "Failed to hash password", http.StatusInternalServerError)
			return
		}
		if inGlobal {
			s.Config.LockMaps()
			s.Config.Mounts[body.Mount] = hashed
			s.Config.UnlockMaps()
		} else {
			owningUser.Mounts[body.Mount] = hashed
		}
	}

	if body.Enabled != nil {
		if s.Config.DisabledMounts == nil {
			s.Config.DisabledMounts = make(map[string]bool)
		}
		if *body.Enabled {
			s.Config.LockMaps()
			delete(s.Config.DisabledMounts, body.Mount)
			s.Config.UnlockMaps()
		} else {
			s.Config.LockMaps()
			s.Config.DisabledMounts[body.Mount] = true
			s.Config.UnlockMaps()
			// Kick the live source if it's currently connected so the new
			// disabled state takes effect immediately. DisconnectListeners
			// alone left the encoder streaming — it just recreated the
			// mount and carried on, so "disabled" disabled nothing.
			s.Relay.RemoveStream(body.Mount)
		}
	}

	if body.Visible != nil {
		if s.Config.VisibleMounts == nil {
			s.Config.VisibleMounts = make(map[string]bool)
		}
		if *body.Visible {
			s.Config.LockMaps()
			s.Config.VisibleMounts[body.Mount] = true
			s.Config.UnlockMaps()
		} else {
			s.Config.LockMaps()
			delete(s.Config.VisibleMounts, body.Mount)
			s.Config.UnlockMaps()
		}
		if st, ok := s.Relay.GetStream(body.Mount); ok {
			st.SetVisible(*body.Visible)
		}
	}

	s.Config.SaveConfig()
	jsonResponse(w, map[string]string{"status": "updated", "mount": body.Mount})
	s.Audit(r, "mount_updated", "stream", body.Mount, "")
}

func (s *Server) apiDeleteStream(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	mount := r.URL.Query().Get("mount")
	if mount == "" {
		jsonError(w, "Mount is required", http.StatusBadRequest)
		return
	}
	if !s.hasAccess(user, mount) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	s.Config.LockMaps()
	delete(s.Config.Mounts, mount)
	s.Config.UnlockMaps()
	s.Config.LockMaps()
	delete(s.Config.DisabledMounts, mount)
	s.Config.UnlockMaps()
	s.Config.LockMaps()
	delete(s.Config.VisibleMounts, mount)
	s.Config.UnlockMaps()
	s.Config.LockMaps()
	delete(user.Mounts, mount)
	s.Config.UnlockMaps()
	s.Relay.RemoveStream(mount)
	s.Config.SaveConfig()
	jsonResponse(w, map[string]string{"status": "deleted"})
	s.Audit(r, "mount_deleted", "stream", mount, "")
}

func (s *Server) apiKickStream(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		Mount string `json:"mount"`
		Type  string `json:"type"` // "source" or "listeners"
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if body.Mount == "" {
		jsonError(w, "Mount is required", http.StatusBadRequest)
		return
	}
	if !s.hasAccess(user, body.Mount) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	switch body.Type {
	case "listeners":
		if st, ok := s.Relay.GetStream(body.Mount); ok {
			st.DisconnectListeners()
		}
	default: // "source" or empty — kick the whole stream
		s.Relay.RemoveStream(body.Mount)
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}

// ---------------------------------------------------------------------------
// AutoDJ
// ---------------------------------------------------------------------------

func (s *Server) apiGetAutoDJ(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	type autoDJInfo struct {
		Name           string               `json:"name"`
		Mount          string               `json:"mount"`
		State          int                  `json:"state"`
		CurrentSong    string               `json:"current_song"`
		StartTime      int64                `json:"start_time"`
		Position       float64              `json:"position"`
		Duration       float64              `json:"duration"`
		CurrentID      int                  `json:"current_id"`
		PlaylistPos    int                  `json:"playlist_pos"`
		PlaylistLen    int                  `json:"playlist_len"`
		Shuffle        bool                 `json:"shuffle"`
		Loop           bool                 `json:"loop"`
		InjectMetadata bool                 `json:"inject_metadata"`
		Visible        bool                 `json:"visible"`
		MusicDir       string               `json:"music_dir"`
		Format         string               `json:"format"`
		Bitrate        int                  `json:"bitrate"`
		Enabled        bool                 `json:"enabled"`
		MPDEnabled     bool                 `json:"mpd_enabled"`
		MPDPort        string               `json:"mpd_port"`
		LastPlaylist          string               `json:"last_playlist"`
		SongCommand           string               `json:"song_command"`
		SongCommandTimeout    int                   `json:"song_command_timeout"`
		OnPlayCommand         string               `json:"on_play_command"`
		OnPlayCommandTimeout  int                   `json:"on_play_command_timeout"`
		Queue                 []relay.PlaylistItem  `json:"queue"`
	}

	var result []autoDJInfo
	streamers := s.StreamerM.GetStreamers()

	// Build map of streamer by mount for quick lookup
	streamerMap := make(map[string]*relay.Streamer)
	for _, st := range streamers {
		streamerMap[st.OutputMount] = st
	}

	for _, adj := range s.Config.AutoDJs {
		if !s.hasAccess(user, adj.Mount) {
			continue
		}
		info := autoDJInfo{
			Name:           adj.Name,
			Mount:          adj.Mount,
			Format:         adj.Format,
			Bitrate:        adj.Bitrate,
			Enabled:        adj.Enabled,
			MusicDir:       adj.MusicDir,
			MPDEnabled:     adj.MPDEnabled,
			MPDPort:        adj.MPDPort,
			LastPlaylist:       adj.LastPlaylist,
			SongCommand:          adj.SongCommand,
			SongCommandTimeout:   adj.SongCommandTimeout,
			OnPlayCommand:        adj.OnPlayCommand,
			OnPlayCommandTimeout: adj.OnPlayCommandTimeout,
			Loop:                 adj.Loop,
			InjectMetadata:     adj.InjectMetadata,
			Visible:            adj.Visible,
		}
		if st, ok := streamerMap[adj.Mount]; ok {
			stats := st.GetStats()
			info.State = int(stats.State)
			info.CurrentSong = stats.CurrentSong
			info.StartTime = stats.StartTime.Unix()
			info.Position = trackPosition(stats)
			info.Duration = stats.Duration.Seconds()
			info.CurrentID = stats.CurrentID
			info.PlaylistPos = stats.CurrentPos
			info.PlaylistLen = stats.PlaylistLen
			info.Shuffle = stats.Shuffle
			info.Loop = stats.Loop
			info.InjectMetadata = stats.InjectMetadata
			info.Visible = stats.Visible
			info.Queue = st.GetQueueInfo()
		}
		if info.Queue == nil {
			info.Queue = []relay.PlaylistItem{}
		}
		result = append(result, info)
	}
	if result == nil {
		result = []autoDJInfo{}
	}
	jsonResponse(w, result)
}

func (s *Server) apiCreateAutoDJ(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	// AutoDJ accepts SongCommand / OnPlayCommand strings that get exec'd
	// via `sh -c` in the streamer's lifetime context. Allowing any
	// authenticated DJ to register those is privilege escalation — they
	// can run arbitrary shell as the tinyice service user. Match the
	// relay / transcoder endpoints, which already require superadmin.
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		Name           string `json:"name"`
		Mount          string `json:"mount"`
		MusicDir       string `json:"music_dir"`
		Format         string `json:"format"`
		Bitrate        int    `json:"bitrate"`
		Loop           bool   `json:"loop"`
		InjectMetadata bool   `json:"inject_metadata"`
		MPDEnabled     bool   `json:"mpd_enabled"`
		MPDPort        string `json:"mpd_port"`
		MPDPassword    string `json:"mpd_password"`
		Visible              bool   `json:"visible"`
		SongCommand          string `json:"song_command"`
		SongCommandTimeout   int    `json:"song_command_timeout"`
		OnPlayCommand        string `json:"on_play_command"`
		OnPlayCommandTimeout int    `json:"on_play_command_timeout"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if body.Name == "" || body.Mount == "" {
		jsonError(w, "Name and mount are required", http.StatusBadRequest)
		return
	}
	if body.MusicDir == "" && body.SongCommand == "" {
		jsonError(w, "Either music_dir or song_command is required", http.StatusBadRequest)
		return
	}
	if body.Mount[0] != '/' {
		body.Mount = "/" + body.Mount
	}
	if body.Format == "" {
		body.Format = "mp3"
	}
	if body.Bitrate == 0 {
		body.Bitrate = 128
	}

	absMusicDir := ""
	if body.MusicDir != "" {
		absMusicDir, _ = filepath.Abs(body.MusicDir)
	}

	adj := &config.AutoDJConfig{
		Name:           body.Name,
		Mount:          body.Mount,
		MusicDir:       absMusicDir,
		Format:         body.Format,
		Bitrate:        body.Bitrate,
		Enabled:        true,
		Loop:           body.Loop,
		InjectMetadata: body.InjectMetadata,
		MPDEnabled:     body.MPDEnabled,
		MPDPort:        body.MPDPort,
		MPDPassword:          body.MPDPassword,
		Visible:              body.Visible,
		SongCommand:          body.SongCommand,
		SongCommandTimeout:   body.SongCommandTimeout,
		OnPlayCommand:        body.OnPlayCommand,
		OnPlayCommandTimeout: body.OnPlayCommandTimeout,
	}

	s.Config.AutoDJs = append(s.Config.AutoDJs, adj)
	s.Config.SaveConfig()

	streamer, err := s.StreamerM.StartStreamer(adj.Name, adj.Mount, adj.MusicDir, adj.Loop, adj.Format, adj.Bitrate, adj.InjectMetadata, nil, adj.MPDEnabled, adj.MPDPort, adj.MPDPassword, adj.Visible, "", adj.SongCommand, adj.SongCommandTimeout, adj.OnPlayCommand, adj.OnPlayCommandTimeout)
	if err != nil {
		jsonError(w, fmt.Sprintf("Failed to start AutoDJ: %v", err), http.StatusInternalServerError)
		return
	}
	if adj.InjectMetadata {
		if st, ok := s.Relay.GetStream(adj.Mount); ok {
			st.SetVisible(adj.Visible)
		}
	}
	if adj.MusicDir != "" {
		streamer.ScanMusicDir()
	}
	streamer.Play()
	jsonResponse(w, map[string]string{"status": "created", "mount": adj.Mount})
	s.Audit(r, "autodj_created", "autodj", body.Mount, body.Name)
}

// apiUpdateAutoDJ edits an existing AutoDJ in place, keyed by its current
// mount (passed as the "mount" query parameter, since the body may change
// the mount). The running streamer is stopped and, if still enabled,
// restarted against the new configuration. Existing playlist/queue state is
// preserved across the restart.
func (s *Server) apiUpdateAutoDJ(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, authed := s.checkAuth(r)
	if !authed {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	// Same reasoning as apiCreateAutoDJ — updating song_command /
	// on_play_command is shell-execution privilege. The mount-access
	// check below only verifies the user owns this mount; that doesn't
	// imply they should be able to register arbitrary shell.
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	originalMount := r.URL.Query().Get("mount")
	if originalMount == "" {
		jsonError(w, "Query parameter 'mount' is required", http.StatusBadRequest)
		return
	}
	if !s.hasAccess(user, originalMount) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		Name               string `json:"name"`
		Mount              string `json:"mount"`
		MusicDir           string `json:"music_dir"`
		Format             string `json:"format"`
		Bitrate            int    `json:"bitrate"`
		// Pointers where a missing field must mean "leave it alone".
		// The admin edit form doesn't submit these, and decoding an
		// absent bool as false silently switched off the AutoDJ's MPD
		// server and cleared its visibility on every edit.
		Loop               *bool   `json:"loop"`
		InjectMetadata     *bool   `json:"inject_metadata"`
		MPDEnabled         *bool   `json:"mpd_enabled"`
		MPDPort            *string `json:"mpd_port"`
		MPDPassword        string  `json:"mpd_password"`
		Visible              *bool `json:"visible"`
		SongCommand          string `json:"song_command"`
		SongCommandTimeout   int    `json:"song_command_timeout"`
		OnPlayCommand        string `json:"on_play_command"`
		OnPlayCommandTimeout int    `json:"on_play_command_timeout"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	var target *config.AutoDJConfig
	for _, adj := range s.Config.AutoDJs {
		if adj.Mount == originalMount {
			target = adj
			break
		}
	}
	if target == nil {
		jsonError(w, "AutoDJ not found", http.StatusNotFound)
		return
	}

	newMount := body.Mount
	if newMount == "" {
		newMount = originalMount
	}
	if newMount[0] != '/' {
		newMount = "/" + newMount
	}
	if body.MusicDir == "" && body.SongCommand == "" {
		jsonError(w, "Either music_dir or song_command is required", http.StatusBadRequest)
		return
	}

	absMusicDir := ""
	if body.MusicDir != "" {
		absMusicDir, _ = filepath.Abs(body.MusicDir)
	}

	// Snapshot the previous configuration so a failed restart can be
	// rolled back rather than leaving no AutoDJ at all.
	prev := *target

	// Stop the old instance before mutating config.
	s.StreamerM.DeleteStreamer(originalMount)

	if body.Name != "" {
		target.Name = body.Name
	}
	target.Mount = newMount
	target.MusicDir = absMusicDir
	if body.Format != "" {
		target.Format = body.Format
	}
	if body.Bitrate != 0 {
		target.Bitrate = body.Bitrate
	}
	if body.Loop != nil {
		target.Loop = *body.Loop
	}
	if body.InjectMetadata != nil {
		target.InjectMetadata = *body.InjectMetadata
	}
	if body.MPDEnabled != nil {
		target.MPDEnabled = *body.MPDEnabled
	}
	if body.MPDPort != nil {
		target.MPDPort = *body.MPDPort
	}
	if body.MPDPassword != "" {
		target.MPDPassword = body.MPDPassword
	}
	if body.Visible != nil {
		target.Visible = *body.Visible
	}
	target.SongCommand = body.SongCommand
	target.SongCommandTimeout = body.SongCommandTimeout
	target.OnPlayCommand = body.OnPlayCommand
	target.OnPlayCommandTimeout = body.OnPlayCommandTimeout

	// Start BEFORE persisting: the old streamer is already torn down, so
	// if the new configuration can't start (a taken MPD port, a bad
	// music dir) we must be able to put the previous one back. Saving
	// first left the config describing an AutoDJ that doesn't exist.
	streamer, err := s.StreamerM.StartStreamer(
		target.Name, target.Mount, target.MusicDir, target.Loop, target.Format, target.Bitrate,
		target.InjectMetadata, target.Playlist, target.MPDEnabled, target.MPDPort, target.MPDPassword,
		target.Visible, target.LastPlaylist, target.SongCommand, target.SongCommandTimeout,
		target.OnPlayCommand, target.OnPlayCommandTimeout,
	)
	if err != nil {
		*target = prev
		if restored, rerr := s.StreamerM.StartStreamer(
			target.Name, target.Mount, target.MusicDir, target.Loop, target.Format, target.Bitrate,
			target.InjectMetadata, target.Playlist, target.MPDEnabled, target.MPDPort, target.MPDPassword,
			target.Visible, target.LastPlaylist, target.SongCommand, target.SongCommandTimeout,
			target.OnPlayCommand, target.OnPlayCommandTimeout,
		); rerr == nil {
			restored.Play()
			logger.L.Warnw("AutoDJ update failed; previous configuration restored",
				"mount", target.Mount, "error", err)
		} else {
			logger.L.Errorw("AutoDJ update failed and the previous configuration could not be restored",
				"mount", target.Mount, "error", err, "restore_error", rerr)
		}
		jsonError(w, fmt.Sprintf("Failed to restart AutoDJ: %v", err), http.StatusInternalServerError)
		return
	}
	s.Config.SaveConfig()
	if target.MusicDir != "" {
		streamer.ScanMusicDir()
	}
	streamer.Play()
	jsonResponse(w, map[string]string{"status": "updated", "mount": target.Mount})
	s.Audit(r, "autodj_updated", "autodj", target.Mount, target.Name)
}

func (s *Server) apiDeleteAutoDJ(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	mount := r.URL.Query().Get("mount")
	if mount == "" {
		jsonError(w, "Mount is required", http.StatusBadRequest)
		return
	}

	newADJs := []*config.AutoDJConfig{}
	found := false
	for _, adj := range s.Config.AutoDJs {
		if adj.Mount != mount {
			newADJs = append(newADJs, adj)
		} else {
			s.StreamerM.DeleteStreamer(mount)
			found = true
		}
	}
	if !found {
		jsonError(w, "AutoDJ not found", http.StatusNotFound)
		return
	}
	s.Config.AutoDJs = newADJs
	s.Config.SaveConfig()
	jsonResponse(w, map[string]string{"status": "deleted"})
	s.Audit(r, "autodj_deleted", "autodj", mount, "")
}

func (s *Server) apiAutoDJPlay(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	mount, ok := s.requireMountAccess(w, r, "")
	if !ok {
		return
	}
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}
	streamer.Play()
	jsonResponse(w, map[string]string{"status": "playing"})
}

func (s *Server) apiAutoDJPause(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	mount, ok := s.requireMountAccess(w, r, "")
	if !ok {
		return
	}
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}
	streamer.Pause()
	jsonResponse(w, map[string]string{"status": "paused"})
}

func (s *Server) apiAutoDJNext(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	mount, ok := s.requireMountAccess(w, r, "")
	if !ok {
		return
	}
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}
	streamer.Next()
	jsonResponse(w, map[string]string{"status": "skipped"})
}

// apiAutoDJPrev rewinds the AutoDJ playlist by one track. Paired with the
// UI's previous-track button, which was 404ing before this endpoint
// existed.
func (s *Server) apiAutoDJPrev(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	mount, ok := s.requireMountAccess(w, r, "")
	if !ok {
		return
	}
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}
	streamer.Previous()
	jsonResponse(w, map[string]string{"status": "rewound"})
}

// apiAutoDJVolume sets playback volume for an AutoDJ mount. The body is
// {"volume": v, "unit": "percent"|"fraction"}; with no unit the value is
// guessed by range (> 1 means percent) for legacy callers.
func (s *Server) apiAutoDJVolume(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	mount, ok := s.requireMountAccess(w, r, "")
	if !ok {
		return
	}
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}
	var body struct {
		Volume float64 `json:"volume"`
		// Unit disambiguates the range. Without it the endpoint guessed
		// "> 1 means percent", which makes every value in 0..1 ambiguous:
		// the Studio's slider at 1% sent 1, the guess read that as the
		// fraction 1.0, and the knob jumped to full volume.
		Unit string `json:"unit"` // "percent" | "fraction"
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	v := body.Volume
	switch strings.ToLower(body.Unit) {
	case "percent":
		v = v / 100.0
	case "fraction":
		// already 0..1
	default:
		// Legacy callers sent either range with no unit; keep guessing
		// for them, but the UI now says which it means.
		if v > 1.0 {
			v = v / 100.0
		}
	}
	streamer.SetVolume(v)
	jsonResponse(w, map[string]interface{}{"status": "ok", "volume": streamer.GetVolume()})
}

func (s *Server) apiAutoDJShuffle(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	mount, ok := s.requireMountAccess(w, r, "")
	if !ok {
		return
	}
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}
	streamer.ToggleShuffle()
	stats := streamer.GetStats()
	jsonResponse(w, map[string]interface{}{"status": "ok", "shuffle": stats.Shuffle})
}

func (s *Server) apiAutoDJLoop(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	mount, ok := s.requireMountAccess(w, r, "")
	if !ok {
		return
	}
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}
	streamer.ToggleLoop()
	stats := streamer.GetStats()

	for _, adj := range s.Config.AutoDJs {
		if adj.Mount == mount {
			adj.Loop = stats.Loop
			s.Config.SaveConfig()
			break
		}
	}
	jsonResponse(w, map[string]interface{}{"status": "ok", "loop": stats.Loop})
}

// ---------------------------------------------------------------------------
// Playlist
// ---------------------------------------------------------------------------

func (s *Server) apiGetPlaylist(w http.ResponseWriter, r *http.Request) {
	mount, ok := s.requireMountAccess(w, r, "")
	if !ok {
		return
	}
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}
	jsonResponse(w, streamer.GetPlaylistInfo())
}

func (s *Server) apiAddToPlaylist(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, authed := s.checkAuth(r)
	if !authed {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		Mount string   `json:"mount"`
		Files []string `json:"files"`
		Path  string   `json:"path"`
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Support frontend format: path (single) or paths (array) alongside files
	if body.Path != "" {
		body.Files = append(body.Files, body.Path)
	}
	if len(body.Paths) > 0 {
		body.Files = append(body.Files, body.Paths...)
	}

	// Fall back to query param for mount if not in body
	mount := body.Mount
	if mount == "" {
		mount = r.URL.Query().Get("mount")
	}
	if mount == "" {
		jsonError(w, "Mount is required", http.StatusBadRequest)
		return
	}
	if !s.hasAccess(user, mount) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}

	musicDir := streamer.GetMusicDir()
	for _, file := range body.Files {
		// If path is relative (not absolute), resolve it relative to music dir
		if !filepath.IsAbs(file) {
			file = filepath.Join(musicDir, file)
		}
		fullPath, err := s.validatePathInMusicDir(musicDir, file)
		if err != nil {
			logger.L.Warnw("Security: Blocked playlist addition", "path", file, "mount", mount, "error", err)
			continue
		}
		info, err := os.Stat(fullPath)
		if err != nil {
			continue
		}
		if info.IsDir() {
			filepath.Walk(fullPath, func(p string, i os.FileInfo, e error) error {
				if e != nil {
					return nil
				}
				ext := strings.ToLower(filepath.Ext(p))
				if !i.IsDir() && (ext == ".mp3" || ext == ".ogg" || ext == ".opus" || ext == ".flac" || ext == ".wav") {
					streamer.AddToPlaylist(p)
				}
				return nil
			})
		} else {
			streamer.AddToPlaylist(fullPath)
		}
	}

	// Persist
	playlistCopy := streamer.GetPlaylist()
	lastPl := streamer.GetStats().LastPlaylist
	if lastPl == "" {
		lastPl = streamer.Name + ".pls"
		streamer.SetLastPlaylist(lastPl)
	}
	streamer.SavePlaylist()
	for _, adj := range s.Config.AutoDJs {
		if adj.Mount == mount {
			adj.Playlist = playlistCopy
			adj.LastPlaylist = lastPl
			s.Config.SaveConfig()
			break
		}
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (s *Server) apiRemoveFromPlaylist(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, authed := s.checkAuth(r)
	if !authed {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	mount := r.URL.Query().Get("mount")

	var body struct {
		Mount string `json:"mount"`
		ID    int    `json:"id"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if mount == "" {
		mount = body.Mount
	}
	if mount == "" {
		jsonError(w, "Mount is required", http.StatusBadRequest)
		return
	}
	if !s.hasAccess(user, mount) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	id := body.ID
	if id == 0 {
		fmt.Sscanf(r.URL.Query().Get("id"), "%d", &id)
	}

	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}
	// By ID, not index: the UI sends back the PlaylistItem.ID it was
	// given, and treating that as an index removed the wrong track as
	// soon as IDs and positions diverged (i.e. after any removal).
	if !streamer.RemoveFromPlaylistByID(id) {
		jsonError(w, "Playlist entry not found", http.StatusNotFound)
		return
	}

	playlistCopy := streamer.GetPlaylist()
	for _, adj := range s.Config.AutoDJs {
		if adj.Mount == mount {
			adj.Playlist = playlistCopy
			s.Config.SaveConfig()
			break
		}
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (s *Server) apiClearPlaylist(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, authed := s.checkAuth(r)
	if !authed {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		Mount string `json:"mount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		mount := r.URL.Query().Get("mount")
		if mount == "" {
			jsonError(w, "Mount is required", http.StatusBadRequest)
			return
		}
		body.Mount = mount
	}
	if !s.hasAccess(user, body.Mount) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	streamer := s.StreamerM.GetStreamer(body.Mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}
	streamer.ClearPlaylist()

	for _, adj := range s.Config.AutoDJs {
		if adj.Mount == body.Mount {
			adj.Playlist = []string{}
			s.Config.SaveConfig()
			break
		}
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (s *Server) apiReorderPlaylist(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, authed := s.checkAuth(r)
	if !authed {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		Mount string `json:"mount"`
		From  int    `json:"from"`
		To    int    `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if body.Mount == "" {
		jsonError(w, "Mount is required", http.StatusBadRequest)
		return
	}
	if !s.hasAccess(user, body.Mount) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	streamer := s.StreamerM.GetStreamer(body.Mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}
	streamer.MovePlaylistItem(body.From, body.To)

	playlistCopy := streamer.GetPlaylist()
	for _, adj := range s.Config.AutoDJs {
		if adj.Mount == body.Mount {
			adj.Playlist = playlistCopy
			s.Config.SaveConfig()
			break
		}
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}

// ---------------------------------------------------------------------------
// Queue
// ---------------------------------------------------------------------------

func (s *Server) apiGetQueue(w http.ResponseWriter, r *http.Request) {
	mount, ok := s.requireMountAccess(w, r, "")
	if !ok {
		return
	}
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}
	jsonResponse(w, streamer.GetQueueInfo())
}

func (s *Server) apiAddToQueue(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, authed := s.checkAuth(r)
	if !authed {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		Mount string `json:"mount"`
		Path  string `json:"path"`
		// ID queues an existing playlist entry (what the Studio's
		// "play next" button sends); Path queues an arbitrary file
		// from the music directory.
		ID    int  `json:"id"`
		Front bool `json:"front"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	// The path-based routes (/api/autodj/{mount}/queue) inject the mount
	// as a query parameter, so requiring it in the body made every call
	// from the Studio fail with "Mount is required".
	mount := body.Mount
	if mount == "" {
		mount = r.URL.Query().Get("mount")
	}
	if mount == "" {
		jsonError(w, "Mount is required", http.StatusBadRequest)
		return
	}
	if !s.hasAccess(user, mount) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}

	fullPath := ""
	if body.Path != "" {
		p, err := s.validatePathInMusicDir(streamer.GetMusicDir(), body.Path)
		if err != nil {
			jsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
		fullPath = p
	} else if body.ID != 0 {
		p, ok := streamer.PathForPlaylistID(body.ID)
		if !ok {
			jsonError(w, "Playlist entry not found", http.StatusNotFound)
			return
		}
		fullPath = p
	} else {
		jsonError(w, "Either path or id is required", http.StatusBadRequest)
		return
	}

	// The /playlist/playnext route signals front-of-queue via the query
	// string, since it has no control over the caller's JSON body.
	if r.URL.Query().Get("front") == "true" {
		body.Front = true
	}
	if body.Front {
		streamer.PushToQueueFront(fullPath)
	} else {
		streamer.PushToQueue(fullPath)
	}
	jsonResponse(w, map[string]string{"status": "ok"})
}

// ---------------------------------------------------------------------------
// Library / Files
// ---------------------------------------------------------------------------

func (s *Server) apiGetFiles(w http.ResponseWriter, r *http.Request) {
	mount, ok := s.requireMountAccess(w, r, "")
	if !ok {
		return
	}
	subDir := r.URL.Query().Get("path")
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		jsonError(w, "Streamer not found", http.StatusNotFound)
		return
	}

	musicDir := streamer.GetMusicDir()
	fullPath, err := s.validatePathInMusicDir(musicDir, filepath.Join(musicDir, subDir))
	if err != nil {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type fileEntry struct {
		Name    string `json:"name"`
		Title   string `json:"title"`
		IsDir   bool   `json:"is_dir"`
		Path    string `json:"path"`
		AbsPath string `json:"abs_path"`
		IsPLS   bool   `json:"is_pls"`
	}
	var res []fileEntry

	// Show playlist files at root level
	if subDir == "" {
		if plsEntries, err := os.ReadDir("playlists"); err == nil {
			for _, f := range plsEntries {
				if !f.IsDir() && strings.HasSuffix(f.Name(), ".pls") {
					abs, _ := filepath.Abs(filepath.Join("playlists", f.Name()))
					res = append(res, fileEntry{
						Name:    f.Name(),
						Title:   "Playlist: " + f.Name(),
						IsDir:   false,
						Path:    f.Name(),
						AbsPath: abs,
						IsPLS:   true,
					})
				}
			}
		}
	}

	supportedExts := map[string]bool{".mp3": true, ".ogg": true, ".opus": true, ".flac": true, ".wav": true}
	for _, f := range entries {
		ext := strings.ToLower(filepath.Ext(f.Name()))
		if f.IsDir() || supportedExts[ext] {
			title := f.Name()
			full := filepath.Join(fullPath, f.Name())
			if !f.IsDir() {
				title = streamer.GetSongTitle(full)
			}
			abs, _ := filepath.Abs(full)
			res = append(res, fileEntry{
				Name:    f.Name(),
				Title:   title,
				IsDir:   f.IsDir(),
				Path:    filepath.Join(subDir, f.Name()),
				AbsPath: abs,
				IsPLS:   false,
			})
		}
	}
	if res == nil {
		res = []fileEntry{}
	}
	jsonResponse(w, res)
}

// ---------------------------------------------------------------------------
// Relays
// ---------------------------------------------------------------------------

func (s *Server) apiGetRelays(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	type relayInfo struct {
		URL       string `json:"url"`
		Mount     string `json:"mount"`
		BurstSize int    `json:"burst_size"`
		Enabled   bool   `json:"enabled"`
		Active    bool   `json:"active"`
	}

	var result []relayInfo
	for _, rc := range s.Config.Relays {
		active := false
		if st, ok := s.Relay.GetStream(rc.Mount); ok && st.SourceIP == "relay-pull" {
			active = true
		}
		result = append(result, relayInfo{
			URL:       rc.URL,
			Mount:     rc.Mount,
			BurstSize: rc.BurstSize,
			Enabled:   rc.Enabled,
			Active:    active,
		})
	}
	if result == nil {
		result = []relayInfo{}
	}
	jsonResponse(w, result)
}

func (s *Server) apiCreateRelay(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		URL       string `json:"url"`
		Mount     string `json:"mount"`
		Password  string `json:"password"`
		BurstSize int    `json:"burst_size"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if body.URL == "" || body.Mount == "" {
		jsonError(w, "URL and mount are required", http.StatusBadRequest)
		return
	}
	if err := validateOutboundURL(body.URL); err != nil {
		jsonError(w, "Upstream URL rejected: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.Mount[0] != '/' {
		body.Mount = "/" + body.Mount
	}
	if body.BurstSize == 0 {
		body.BurstSize = 20
	}

	// Update existing or create new. An empty password on an update preserves
	// the stored one so the Edit UI can omit the password field.
	found := false
	effectivePassword := body.Password
	for _, rc := range s.Config.Relays {
		if rc.Mount == body.Mount {
			rc.URL = body.URL
			if body.Password != "" {
				rc.Password = body.Password
			}
			rc.BurstSize = body.BurstSize
			effectivePassword = rc.Password
			found = true
			break
		}
	}
	if !found {
		s.Config.Relays = append(s.Config.Relays, &config.RelayConfig{
			URL: body.URL, Mount: body.Mount, Password: body.Password, BurstSize: body.BurstSize, Enabled: true,
		})
	}
	s.Config.SaveConfig()
	// Restart the relay so new URL / burst / password take effect immediately.
	s.RelayM.StopRelay(body.Mount)
	s.RelayM.StartRelay(body.URL, body.Mount, effectivePassword, body.BurstSize, s.Config.VisibleMounts[body.Mount])
	action := "relay_created"
	if found {
		action = "relay_updated"
	}
	jsonResponse(w, map[string]string{"status": "ok"})
	s.Audit(r, action, "relay", body.Mount, body.URL)
}

func (s *Server) apiDeleteRelay(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	mount := r.URL.Query().Get("mount")
	if mount == "" {
		jsonError(w, "Mount is required", http.StatusBadRequest)
		return
	}

	newRelays := []*config.RelayConfig{}
	found := false
	for _, rc := range s.Config.Relays {
		if rc.Mount != mount {
			newRelays = append(newRelays, rc)
		} else {
			found = true
		}
	}
	if !found {
		jsonError(w, "Relay not found", http.StatusNotFound)
		return
	}
	s.Config.Relays = newRelays
	s.Config.SaveConfig()
	s.RelayM.StopRelay(mount)
	jsonResponse(w, map[string]string{"status": "deleted"})
	s.Audit(r, "relay_deleted", "relay", mount, "")
}

func (s *Server) apiToggleRelay(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		Mount string `json:"mount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	for _, rc := range s.Config.Relays {
		if rc.Mount == body.Mount {
			rc.Enabled = !rc.Enabled
			if rc.Enabled {
				s.RelayM.StartRelay(rc.URL, rc.Mount, rc.Password, rc.BurstSize, s.Config.VisibleMounts[body.Mount])
			} else {
				s.RelayM.StopRelay(body.Mount)
			}
			s.Config.SaveConfig()
			jsonResponse(w, map[string]interface{}{"status": "ok", "enabled": rc.Enabled})
			return
		}
	}
	jsonError(w, "Relay not found", http.StatusNotFound)
}

// ---------------------------------------------------------------------------
// Transcoders
// ---------------------------------------------------------------------------

func (s *Server) apiGetTranscoders(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	type transcoderRow struct {
		Name            string `json:"name"`
		Input           string `json:"input"`
		Output          string `json:"output"`
		Format          string `json:"format"`
		Bitrate         int    `json:"bitrate"`
		Enabled         bool   `json:"enabled"`
		Active          bool   `json:"active"`
		FramesProcessed int64  `json:"frames_processed"`
		BytesEncoded    int64  `json:"bytes_encoded"`
		Uptime          string `json:"uptime"`
		Visibility      string `json:"visibility,omitempty"`
		SampleRate      int    `json:"sample_rate,omitempty"`
		Channels        int    `json:"channels,omitempty"`
		OpusApplication string `json:"opus_application,omitempty"`
		OpusVBR         *bool  `json:"opus_vbr,omitempty"`
		OpusComplexity  int    `json:"opus_complexity,omitempty"`
		OpusFrameSizeMS int    `json:"opus_frame_size_ms,omitempty"`
	}

	rows := make([]transcoderRow, 0, len(s.Config.Transcoders))
	for _, tc := range s.Config.Transcoders {
		inst := s.TranscoderM.GetInstance(tc.OutputMount)
		uptime := "OFF"
		var frames, bytes int64
		active := false
		if inst != nil {
			active = true
			uptime = time.Since(inst.StartTime).Round(time.Second).String()
			frames = atomic.LoadInt64(&inst.FramesProcessed)
			bytes = atomic.LoadInt64(&inst.BytesEncoded)
		}
		rows = append(rows, transcoderRow{
			Name:            tc.Name,
			Input:           tc.InputMount,
			Output:          tc.OutputMount,
			Format:          tc.Format,
			Bitrate:         tc.Bitrate,
			Enabled:         tc.Enabled,
			Active:          active,
			FramesProcessed: frames,
			BytesEncoded:    bytes,
			Uptime:          uptime,
			Visibility:      tc.Visibility,
			SampleRate:      tc.SampleRate,
			Channels:        tc.Channels,
			OpusApplication: tc.OpusApplication,
			OpusVBR:         tc.OpusVBR,
			OpusComplexity:  tc.OpusComplexity,
			OpusFrameSizeMS: tc.OpusFrameSizeMS,
		})
	}
	jsonResponse(w, rows)
}

type transcoderBody struct {
	Name            string  `json:"name"`
	InputMount      string  `json:"input_mount"`
	OutputMount     string  `json:"output_mount"`
	Format          string  `json:"format"`
	Bitrate         int     `json:"bitrate"`
	Enabled         *bool   `json:"enabled,omitempty"`
	Visibility      *string `json:"visibility,omitempty"`
	SampleRate      int     `json:"sample_rate,omitempty"`
	Channels        int     `json:"channels,omitempty"`
	OpusApplication string  `json:"opus_application,omitempty"`
	OpusVBR         *bool   `json:"opus_vbr,omitempty"`
	OpusComplexity  int     `json:"opus_complexity,omitempty"`
	OpusFrameSizeMS int     `json:"opus_frame_size_ms,omitempty"`
}

func (s *Server) apiCreateTranscoder(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var body transcoderBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if body.Name == "" || body.InputMount == "" || body.OutputMount == "" {
		jsonError(w, "Name, input_mount, and output_mount are required", http.StatusBadRequest)
		return
	}

	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	visibility := ""
	if body.Visibility != nil {
		visibility = normalizeTranscoderVisibility(*body.Visibility)
	}
	tc := &config.TranscoderConfig{
		Name:            body.Name,
		InputMount:      body.InputMount,
		OutputMount:     body.OutputMount,
		Format:          body.Format,
		Bitrate:         body.Bitrate,
		Enabled:         enabled,
		Visibility:      visibility,
		SampleRate:      body.SampleRate,
		Channels:        body.Channels,
		OpusApplication: body.OpusApplication,
		OpusVBR:         body.OpusVBR,
		OpusComplexity:  body.OpusComplexity,
		OpusFrameSizeMS: body.OpusFrameSizeMS,
	}
	s.Config.Transcoders = append(s.Config.Transcoders, tc)
	s.applyTranscoderVisibilityToMap(tc)
	s.Config.SaveConfig()
	if tc.Enabled {
		s.TranscoderM.StartTranscoder(tc)
	}
	jsonResponse(w, map[string]string{"status": "created"})
	s.Audit(r, "transcoder_created", "transcoder", body.OutputMount, body.Name)
}

// apiUpdateTranscoder edits an existing transcoder in place, keyed by its
// original Name (passed as ?name=). The running instance is stopped and, if
// still enabled, restarted with the new settings.
func (s *Server) apiUpdateTranscoder(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	originalName := r.URL.Query().Get("name")
	if originalName == "" {
		jsonError(w, "Query parameter 'name' is required", http.StatusBadRequest)
		return
	}

	var body transcoderBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	var target *config.TranscoderConfig
	for _, tc := range s.Config.Transcoders {
		if tc.Name == originalName {
			target = tc
			break
		}
	}
	if target == nil {
		jsonError(w, "Transcoder not found", http.StatusNotFound)
		return
	}

	// Stop the running instance before mutating config — StartTranscoder is
	// keyed by OutputMount so changing the output needs a clean stop first.
	s.TranscoderM.StopTranscoder(target.OutputMount)

	if body.Name != "" {
		target.Name = body.Name
	}
	if body.InputMount != "" {
		target.InputMount = body.InputMount
	}
	if body.OutputMount != "" {
		target.OutputMount = body.OutputMount
	}
	if body.Format != "" {
		target.Format = body.Format
	}
	if body.Bitrate != 0 {
		target.Bitrate = body.Bitrate
	}
	if body.Enabled != nil {
		target.Enabled = *body.Enabled
	}
	if body.Visibility != nil {
		target.Visibility = normalizeTranscoderVisibility(*body.Visibility)
	}
	target.SampleRate = body.SampleRate
	target.Channels = body.Channels
	target.OpusApplication = body.OpusApplication
	target.OpusVBR = body.OpusVBR
	target.OpusComplexity = body.OpusComplexity
	target.OpusFrameSizeMS = body.OpusFrameSizeMS

	s.applyTranscoderVisibilityToMap(target)
	s.Config.SaveConfig()
	if target.Enabled {
		s.TranscoderM.StartTranscoder(target)
	}
	jsonResponse(w, map[string]string{"status": "updated", "name": target.Name})
	s.Audit(r, "transcoder_updated", "transcoder", target.OutputMount, target.Name)
}

func (s *Server) apiDeleteTranscoder(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" {
		jsonError(w, "Name is required", http.StatusBadRequest)
		return
	}

	newTCs := []*config.TranscoderConfig{}
	found := false
	for _, tc := range s.Config.Transcoders {
		if tc.Name != name {
			newTCs = append(newTCs, tc)
		} else {
			s.TranscoderM.StopTranscoder(tc.OutputMount)
			if tc.Visibility != "" {
				s.Config.LockMaps()
				delete(s.Config.VisibleMounts, tc.OutputMount)
				s.Config.UnlockMaps()
			}
			found = true
		}
	}
	if !found {
		jsonError(w, "Transcoder not found", http.StatusNotFound)
		return
	}
	s.Config.Transcoders = newTCs
	s.Config.SaveConfig()
	jsonResponse(w, map[string]string{"status": "deleted"})
	s.Audit(r, "transcoder_deleted", "transcoder", name, "")
}

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

func (s *Server) apiGetUsers(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	type userInfo struct {
		Username string   `json:"username"`
		Role     string   `json:"role"`
		Mounts   []string `json:"mounts"`
	}
	var result []userInfo
	for _, u := range s.Config.Users {
		mounts := make([]string, 0, len(u.Mounts))
		for m := range u.Mounts {
			mounts = append(mounts, m)
		}
		result = append(result, userInfo{Username: u.Username, Role: u.Role, Mounts: mounts})
	}
	if result == nil {
		result = []userInfo{}
	}
	jsonResponse(w, result)
}

func (s *Server) apiCreateUser(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if body.Username == "" || body.Password == "" {
		jsonError(w, "Username and password are required", http.StatusBadRequest)
		return
	}
	if body.Role == "" {
		body.Role = config.RoleAdmin
	}

	hp, err := config.HashPassword(body.Password)
	if err != nil {
		jsonError(w, "Failed to hash password: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.Config.LockMaps()
	s.Config.Users[body.Username] = &config.User{
		Username: body.Username,
		Password: hp,
		Role:     body.Role,
		Mounts:   make(map[string]string),
	}
	s.Config.UnlockMaps()
	s.Config.SaveConfig()
	jsonResponse(w, map[string]string{"status": "created", "username": body.Username})
	s.Audit(r, "user_created", "user", body.Username, body.Role)
}

func (s *Server) apiUpdateUser(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		Username string `json:"username"`
		Password string `json:"password,omitempty"`
		Role     string `json:"role,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if body.Username == "" {
		jsonError(w, "Username is required", http.StatusBadRequest)
		return
	}

	u, exists := s.Config.Users[body.Username]
	if !exists {
		jsonError(w, "User not found", http.StatusNotFound)
		return
	}
	if body.Password != "" {
		hp, err := config.HashPassword(body.Password)
		if err != nil {
			jsonError(w, "Failed to hash password: "+err.Error(), http.StatusBadRequest)
			return
		}
		u.Password = hp
	}
	if body.Role != "" {
		u.Role = body.Role
	}
	s.Config.SaveConfig()
	jsonResponse(w, map[string]string{"status": "updated"})
	s.Audit(r, "user_updated", "user", body.Username, "")
}

func (s *Server) apiDeleteUser(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	username := r.URL.Query().Get("username")
	if username == "" {
		jsonError(w, "Username is required", http.StatusBadRequest)
		return
	}
	if username == user.Username {
		jsonError(w, "Cannot delete yourself", http.StatusBadRequest)
		return
	}
	if _, exists := s.Config.Users[username]; !exists {
		jsonError(w, "User not found", http.StatusNotFound)
		return
	}
	s.Config.LockMaps()
	delete(s.Config.Users, username)
	s.Config.UnlockMaps()
	// Invalidate any live sessions belonging to the deleted user; otherwise
	// their cookie keeps working until it expires even though the account
	// no longer exists.
	s.sessionsMu.Lock()
	for sid, sess := range s.sessions {
		if sess.User != nil && sess.User.Username == username {
			delete(s.sessions, sid)
		}
	}
	s.sessionsMu.Unlock()
	s.Config.SaveConfig()
	jsonResponse(w, map[string]string{"status": "deleted"})
	s.Audit(r, "user_deleted", "user", username, "")
}

// ---------------------------------------------------------------------------
// Security — Bans & Whitelist
// ---------------------------------------------------------------------------

func (s *Server) apiGetBans(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	jsonResponse(w, s.Config.BannedIPs)
}

func (s *Server) apiAddBan(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		IP string `json:"ip"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IP == "" {
		jsonError(w, "IP is required", http.StatusBadRequest)
		return
	}
	s.Config.AddBannedIP(body.IP)
	s.Config.SaveConfig()
	jsonResponse(w, map[string]string{"status": "added", "ip": body.IP})
	s.Audit(r, "ip_banned", "security", body.IP, "")
}

func (s *Server) apiRemoveBan(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	ip := r.URL.Query().Get("ip")
	if ip == "" {
		jsonError(w, "IP is required", http.StatusBadRequest)
		return
	}
	s.Config.RemoveBannedIP(ip)
	s.Config.SaveConfig()
	jsonResponse(w, map[string]string{"status": "removed", "ip": ip})
	s.Audit(r, "ip_unbanned", "security", ip, "")
}

func (s *Server) apiGetWhitelist(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	jsonResponse(w, s.Config.WhitelistedIPs)
}

func (s *Server) apiAddWhitelist(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		IP string `json:"ip"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IP == "" {
		jsonError(w, "IP is required", http.StatusBadRequest)
		return
	}
	s.Config.AddWhitelistedIP(body.IP)
	s.Config.SaveConfig()
	jsonResponse(w, map[string]string{"status": "added", "ip": body.IP})
	s.Audit(r, "ip_whitelisted", "security", body.IP, "")
}

func (s *Server) apiRemoveWhitelist(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	ip := r.URL.Query().Get("ip")
	if ip == "" {
		jsonError(w, "IP is required", http.StatusBadRequest)
		return
	}
	s.Config.RemoveWhitelistedIP(ip)
	s.Config.SaveConfig()
	jsonResponse(w, map[string]string{"status": "removed", "ip": ip})
	s.Audit(r, "ip_unwhitelisted", "security", ip, "")
}

// ---------------------------------------------------------------------------
// Branding
// ---------------------------------------------------------------------------

func (s *Server) apiGetBranding(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	jsonResponse(w, map[string]interface{}{
		"page_title":       s.Config.PageTitle,
		"page_subtitle":    s.Config.PageSubtitle,
		"accent_color":     s.Config.AccentColor,
		"logo_path":        s.Config.LogoPath,
		"landing_markdown": s.Config.LandingMarkdown,
	})
}

// logoPathAllowed reports whether p is something /branding/logo may serve:
// empty (no logo) or a regular file inside the branding directory that
// apiUploadLogo writes to. Any authenticated user used to be able to set
// LogoPath to an arbitrary string, and handleServeLogo hands whatever it
// holds to http.ServeFile with no auth — so "tinyice.json" made the whole
// config (relay/MPD/SMTP passwords, OIDC secret, token hashes) public.
func logoPathAllowed(p string) bool {
	if p == "" {
		return true
	}
	base, err := filepath.Abs("branding")
	if err != nil {
		return false
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if resolvedBase, err := filepath.EvalSymlinks(base); err == nil {
		base = resolvedBase
	}
	rel, err := filepath.Rel(base, abs)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	info, err := os.Stat(abs)
	return err == nil && info.Mode().IsRegular()
}

func (s *Server) apiUpdateBranding(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	// Branding is server-wide; only a superadmin may change it, matching
	// apiUpdateSettings. Previously any DJ-role account could.
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var body struct {
		PageTitle       *string `json:"page_title"`
		PageSubtitle    *string `json:"page_subtitle"`
		AccentColor     *string `json:"accent_color"`
		LogoPath        *string `json:"logo_path"`
		LandingMarkdown *string `json:"landing_markdown"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if body.PageTitle != nil {
		s.Config.PageTitle = *body.PageTitle
	}
	if body.PageSubtitle != nil {
		s.Config.PageSubtitle = *body.PageSubtitle
	}
	if body.AccentColor != nil {
		s.Config.AccentColor = *body.AccentColor
	}
	if body.LogoPath != nil {
		if !logoPathAllowed(*body.LogoPath) {
			jsonError(w, "logo_path must be empty or a file uploaded via /api/branding/logo", http.StatusBadRequest)
			return
		}
		s.Config.LogoPath = *body.LogoPath
	}
	if body.LandingMarkdown != nil {
		s.Config.LandingMarkdown = *body.LandingMarkdown
	}

	s.Config.SaveConfig()
	jsonResponse(w, map[string]string{"status": "updated"})
	s.Audit(r, "branding_updated", "branding", "", "")
}

// ---------------------------------------------------------------------------
// Logo upload + serve
// ---------------------------------------------------------------------------

func (s *Server) apiUploadLogo(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	if err := r.ParseMultipartForm(2 << 20); err != nil {
		jsonError(w, "File too large (max 2MB)", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("logo")
	if err != nil {
		jsonError(w, "No file uploaded", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// The file is served from this origin by /branding/logo. Trusting the
	// upload's extension let any account store logo.html or logo.svg with
	// a script in it — stored XSS against whoever opens the site. Only
	// raster image types, and the bytes must actually decode as one.
	ext := strings.ToLower(filepath.Ext(header.Filename))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
	default:
		jsonError(w, "logo must be a PNG, JPEG, GIF or WebP image", http.StatusBadRequest)
		return
	}
	sniff := make([]byte, 512)
	n, _ := io.ReadFull(file, sniff)
	if ct := http.DetectContentType(sniff[:n]); !strings.HasPrefix(ct, "image/") || ct == "image/svg+xml" {
		jsonError(w, "logo does not look like an image", http.StatusBadRequest)
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		jsonError(w, "Failed to read upload", http.StatusBadRequest)
		return
	}
	destPath := filepath.Join("branding", "logo"+ext)

	os.MkdirAll("branding", 0755)
	dst, err := os.Create(destPath)
	if err != nil {
		jsonError(w, "Failed to save logo", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := dst.ReadFrom(file); err != nil {
		jsonError(w, "Failed to write logo", http.StatusInternalServerError)
		return
	}

	s.Config.LogoPath = destPath
	s.Config.SaveConfig()
	jsonResponse(w, map[string]string{"status": "ok", "path": destPath})
	s.Audit(r, "logo_uploaded", "branding", destPath, "")
}

func (s *Server) handleServeLogo(w http.ResponseWriter, r *http.Request) {
	// Re-checked at serve time as well as at write time, so a LogoPath
	// that predates the validation (or was hand-edited into the config)
	// still can't expose an arbitrary file. ServeFile would also happily
	// render a directory listing for a directory path.
	p := s.Config.LogoPath
	if p == "" || !logoPathAllowed(p) {
		http.NotFound(w, r)
		return
	}
	// Belt and braces for a pre-existing logo file: never let the browser
	// interpret this as a document.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	http.ServeFile(w, r, p)
}

// ---------------------------------------------------------------------------
// API Tokens
// ---------------------------------------------------------------------------

func (s *Server) apiGetTokens(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	type tokenInfo struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Username   string `json:"username"`
		CreatedAt  string `json:"created_at"`
		LastUsedAt string `json:"last_used_at"`
		LastUsedIP string `json:"last_used_ip"`
		ExpiresAt  string `json:"expires_at"`
		Prefix     string `json:"prefix"`
	}

	var result []tokenInfo
	for _, tok := range s.Config.APITokens {
		if user.Role != config.RoleSuperAdmin && tok.Username != user.Username {
			continue
		}
		prefix := "ti_XXXX..."
		if len(tok.TokenHash) >= 8 {
			prefix = "ti_" + tok.TokenHash[:4] + "..."
		}
		result = append(result, tokenInfo{
			ID:         tok.ID,
			Name:       tok.Name,
			Username:   tok.Username,
			CreatedAt:  tok.CreatedAt,
			LastUsedAt: tok.LastUsedAt,
			LastUsedIP: tok.LastUsedIP,
			ExpiresAt:  tok.ExpiresAt,
			Prefix:     prefix,
		})
	}
	if result == nil {
		result = []tokenInfo{}
	}
	jsonResponse(w, result)
}

func (s *Server) apiCreateToken(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	// Token management needs an interactive session. A request that
	// authenticated with a Bearer token could otherwise mint further
	// tokens — including non-expiring ones from an expiring one — so a
	// leaked short-lived token became permanent access.
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		jsonError(w, "API tokens cannot create tokens; use a logged-in session", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		Name      string `json:"name"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if body.Name == "" {
		jsonError(w, "Name is required", http.StatusBadRequest)
		return
	}

	raw, err := generateToken()
	if err != nil {
		jsonError(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	hash := hashToken(raw)
	id := hash[:16]

	tok := &config.APIToken{
		ID:        id,
		Name:      body.Name,
		TokenHash: hash,
		Username:  user.Username,
		Role:      user.Role,
		CreatedAt: time.Now().Format(time.RFC3339),
		ExpiresAt: body.ExpiresAt,
	}

	s.Config.APITokens = append(s.Config.APITokens, tok)
	s.Config.SaveConfig()

	jsonResponse(w, map[string]string{
		"id":    id,
		"token": raw,
		"name":  body.Name,
	})
	s.Audit(r, "token_created", "auth", body.Name, "")
}

func (s *Server) apiDeleteToken(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		jsonError(w, "ID is required", http.StatusBadRequest)
		return
	}

	newTokens := make([]*config.APIToken, 0, len(s.Config.APITokens))
	found := false
	for _, tok := range s.Config.APITokens {
		if tok.ID == id {
			if user.Role != config.RoleSuperAdmin && tok.Username != user.Username {
				jsonError(w, "Forbidden", http.StatusForbidden)
				return
			}
			found = true
			continue
		}
		newTokens = append(newTokens, tok)
	}
	if !found {
		jsonError(w, "Token not found", http.StatusNotFound)
		return
	}

	s.Config.APITokens = newTokens
	s.Config.SaveConfig()
	jsonResponse(w, map[string]string{"status": "deleted"})
	s.Audit(r, "token_revoked", "auth", id, "")
}

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

func (s *Server) apiGetSettings(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	jsonResponse(w, map[string]interface{}{
		"bind_host":         s.Config.BindHost,
		"port":              s.Config.Port,
		"hostname":          s.Config.HostName,
		"base_url":          s.Config.BaseURL,
		"location":          s.Config.Location,
		"admin_email":       s.Config.AdminEmail,
		"admin_user":        s.Config.AdminUser,
		"low_latency_mode":  s.Config.LowLatencyMode,
		"max_listeners":     s.Config.MaxListeners,
		"use_https":         s.Config.UseHTTPS,
		"auto_https":        s.Config.AutoHTTPS,
		"https_port":        s.Config.HTTPSPort,
		"acme_email":        s.Config.ACMEEmail,
		"domains":           s.Config.Domains,
		"directory_listing": s.Config.DirectoryListing,
		"directory_server":  s.Config.DirectoryServer,
		"audit_enabled":     s.Config.AuditEnabled,
	})
}

func (s *Server) apiUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var body map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if v, ok := body["hostname"]; ok {
		s.Config.HostName = fmt.Sprintf("%v", v)
	}
	if v, ok := body["base_url"]; ok {
		s.Config.BaseURL = fmt.Sprintf("%v", v)
	}
	if v, ok := body["location"]; ok {
		s.Config.Location = fmt.Sprintf("%v", v)
	}
	if v, ok := body["admin_email"]; ok {
		s.Config.AdminEmail = fmt.Sprintf("%v", v)
	}
	if v, ok := body["low_latency_mode"]; ok {
		if b, ok := v.(bool); ok {
			s.Config.LowLatencyMode = b
			s.Relay.LowLatency = b
		}
	}
	if v, ok := body["max_listeners"]; ok {
		if n, ok := v.(float64); ok {
			s.Config.MaxListeners = int(n)
		}
	}
	if v, ok := body["directory_listing"]; ok {
		if b, ok := v.(bool); ok {
			s.Config.DirectoryListing = b
		}
	}
	if v, ok := body["audit_enabled"]; ok {
		if b, ok := v.(bool); ok {
			s.Config.AuditEnabled = b
		}
	}

	s.Config.SaveConfig()
	jsonResponse(w, map[string]string{"status": "updated"})
	s.Audit(r, "settings_updated", "settings", "", "")
}

// ---------------------------------------------------------------------------
// Stats
// ---------------------------------------------------------------------------

func (s *Server) apiGetStats(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	bi, bo := s.Relay.GetMetrics()
	allStreams := s.Relay.Snapshot()
	totalListeners := 0
	totalDropped := int64(0)

	type streamStat struct {
		Mount       string  `json:"mount"`
		Name        string  `json:"name"`
		Listeners   int     `json:"listeners"`
		Bitrate     string  `json:"bitrate"`
		Uptime      string  `json:"uptime"`
		ContentType string  `json:"content_type"`
		SourceIP    string  `json:"source_ip"`
		BytesIn     int64   `json:"bytes_in"`
		BytesOut    int64   `json:"bytes_out"`
		CurrentSong string  `json:"current_song"`
		Health      float64 `json:"health"`
	}

	var streams []streamStat
	for _, st := range allStreams {
		if !s.hasAccess(user, st.MountName) {
			continue
		}
		totalListeners += st.ListenersCount
		totalDropped += st.BytesDropped
		streams = append(streams, streamStat{
			Mount:       st.MountName,
			Name:        st.Name,
			Listeners:   st.ListenersCount,
			Bitrate:     st.Bitrate,
			Uptime:      st.Uptime,
			ContentType: st.ContentType,
			SourceIP:    st.SourceIP,
			BytesIn:     st.BytesIn,
			BytesOut:    st.BytesOut,
			CurrentSong: st.CurrentSong,
			Health:      st.Health,
		})
	}
	if streams == nil {
		streams = []streamStat{}
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	jsonResponse(w, map[string]interface{}{
		"bytes_in":        bi,
		"bytes_out":       bo,
		"total_listeners": totalListeners,
		"total_streams":   len(streams),
		"total_dropped":   totalDropped,
		"streams":         streams,
		"server_uptime":   time.Since(s.startTime).Round(time.Second).String(),
		"goroutines":      runtime.NumGoroutine(),
		"sys_ram":         m.Sys,
		"heap_alloc":      m.HeapAlloc,
		"num_gc":          m.NumGC,
		"version":         s.Version,
		"commit":          s.Commit,
	})
}
