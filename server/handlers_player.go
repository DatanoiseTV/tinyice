package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
)

func (s *Server) handlePlayerClearQueue(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	streamer.ClearQueue()
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handlePlayerToggle(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	streamer.TogglePlay()

	http.Redirect(w, r, "/admin#tab-streamer", http.StatusSeeOther)
}

func (s *Server) handlePlayerScan(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	if err := streamer.ScanMusicDir(); err != nil {
		logger.L.Errorf("Failed to scan music directory: %v", err)
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handlePlayerSavePlaylist(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	if err := streamer.SavePlaylist(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handlePlayerClearPlaylist(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	streamer.ClearPlaylist()
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handlePlayerPlaylistInfo(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.URL.Query().Get("mount")
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(streamer.GetPlaylistInfo())
}

func (s *Server) handlePlayerLoadPlaylist(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	filename := r.FormValue("file")
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	playlistName := filepath.Base(filename)

	if err := streamer.LoadPlaylist(playlistName); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	playlistCopy := streamer.GetPlaylist()
	for _, adj := range s.Config.AutoDJs {
		if adj.Mount == mount {
			adj.Playlist = playlistCopy
			adj.LastPlaylist = playlistName
			s.Config.SaveConfig()
			break
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handlePlayerReorder(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	fromStr := r.FormValue("from")
	toStr := r.FormValue("to")

	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	var from, to int
	fmt.Sscanf(fromStr, "%d", &from)
	fmt.Sscanf(toStr, "%d", &to)

	streamer.MovePlaylistItem(from, to)

	playlistCopy := streamer.GetPlaylist()
	for _, adj := range s.Config.AutoDJs {
		if adj.Mount == mount {
			adj.Playlist = playlistCopy
			s.Config.SaveConfig()
			break
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handlePlayerQueue(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	path := r.FormValue("path")
	action := r.FormValue("action")

	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	if action == "add" {
		musicDir := streamer.GetMusicDir()
		fullPath, err := s.validatePathInMusicDir(musicDir, path)
		if err != nil {
			logger.L.Warnw("Security: Blocked queue addition", "path", path, "mount", mount, "error", err)
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		streamer.PushToQueue(fullPath)
		logger.L.Debugf("AutoDJ %s: Queued song %s", mount, fullPath)
	} else if action == "remove" {
		var index int
		fmt.Sscanf(r.FormValue("index"), "%d", &index)
		streamer.RemoveFromQueue(index)
	} else if action == "reorder" {
		var from, to int
		fmt.Sscanf(r.FormValue("from"), "%d", &from)
		fmt.Sscanf(r.FormValue("to"), "%d", &to)
		streamer.MoveQueueItem(from, to)
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handlePlayerShuffle(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	streamer.ToggleShuffle()
	http.Redirect(w, r, "/admin#tab-streamer", http.StatusSeeOther)
}

func (s *Server) handlePlayerLoop(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	streamer.ToggleLoop()
	loopState := streamer.GetStats().Loop

	for _, adj := range s.Config.AutoDJs {
		if adj.Mount == mount {
			adj.Loop = loopState
			s.Config.SaveConfig()
			break
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handlePlayerMetadata(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	streamer.ToggleInjectMetadata()
	metaState := streamer.GetStats().InjectMetadata

	for _, adj := range s.Config.AutoDJs {
		if adj.Mount == mount {
			adj.InjectMetadata = metaState
			s.Config.SaveConfig()
			break
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handlePlayerRestart(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	streamer.Restart()
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handlePlayerNext(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	streamer.Next()
	http.Redirect(w, r, "/admin#tab-streamer", http.StatusSeeOther)
}

func (s *Server) handlePlayerFiles(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	mount := r.URL.Query().Get("mount")
	subDir := r.URL.Query().Get("path")
	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	musicDir := streamer.GetMusicDir()

	fullPath, err := s.validatePathInMusicDir(musicDir, filepath.Join(musicDir, subDir))
	if err != nil {
		logger.L.Warnw("Security: Blocked file browser access", "subDir", subDir, "mount", mount, "error", err)
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	logger.L.Debugw("AutoDJ File Browser: Reading directory",
		"mount", mount,
		"musicDir", musicDir,
		"subDir", subDir,
		"fullPath", fullPath,
	)

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		logger.L.Errorf("AutoDJ File Browser: Failed to read directory %s: %v", fullPath, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
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

	for _, f := range entries {
		ext := strings.ToLower(filepath.Ext(f.Name()))
		if f.IsDir() || ext == ".mp3" {
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func (s *Server) handlePlayerPlaylistAction(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	action := r.FormValue("action")
	relPath := r.FormValue("file")

	logger.L.Debugw("handlePlayerPlaylistAction", "mount", mount, "action", action, "file", relPath)

	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		http.Error(w, "Streamer not found", http.StatusNotFound)
		return
	}

	if action == "add" {
		musicDir := streamer.GetMusicDir()
		fullPath, err := s.validatePathInMusicDir(musicDir, relPath)
		if err != nil {
			logger.L.Warnw("Security: Blocked playlist addition", "relPath", relPath, "mount", mount, "error", err)
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		logger.L.Infof("AutoDJ %s: Attempting to add %s", mount, fullPath)

		info, err := os.Stat(fullPath)
		if err != nil {
			logger.L.Errorf("AutoDJ %s: Failed to stat path %s: %v", mount, fullPath, err)
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}

		if info.IsDir() {
			logger.L.Infof("AutoDJ %s: Adding directory %s", mount, fullPath)
			err := filepath.Walk(fullPath, func(p string, i os.FileInfo, e error) error {
				if e != nil {
					logger.L.Warnw("AutoDJ error walking path", "mount", mount, "path", p, "error", e)
					return nil
				}
				if !i.IsDir() && strings.ToLower(filepath.Ext(p)) == ".mp3" {
					streamer.AddToPlaylist(p)
				}
				return nil
			})
			if err != nil {
				logger.L.Errorf("AutoDJ %s: Walk failed for %s: %v", mount, fullPath, err)
			}
		} else {
			logger.L.Infof("AutoDJ %s: Adding single file %s", mount, fullPath)
			streamer.AddToPlaylist(fullPath)
		}
	} else if action == "remove" {
		var idx int
		fmt.Sscanf(r.FormValue("index"), "%d", &idx)
		streamer.RemoveFromPlaylist(idx)
	}
	playlistCopy := streamer.GetPlaylist()

	lastPl := streamer.GetStats().LastPlaylist
	if action == "add" && lastPl == "" {
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

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleAddAutoDJ(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	name := r.FormValue("name")
	mount := r.FormValue("mount")
	musicDir := r.FormValue("music_dir")
	format := r.FormValue("format")
	bitrateStr := r.FormValue("bitrate")
	loop := r.FormValue("loop") == "on"
	injectMetadata := r.FormValue("inject_metadata") == "on"
	mpdEnabled := r.FormValue("mpd_enabled") == "on"
	mpdPort := r.FormValue("mpd_port")
	mpdPassword := r.FormValue("mpd_password")
	visible := r.FormValue("visible") == "on"

	songCommand := r.FormValue("song_command")
	if name == "" || mount == "" {
		http.Error(w, "Name and mount are required", http.StatusBadRequest)
		return
	}
	if musicDir == "" && songCommand == "" {
		http.Error(w, "Either music directory or song command is required", http.StatusBadRequest)
		return
	}

	bitrate := 128
	if bitrateStr != "" {
		fmt.Sscanf(bitrateStr, "%d", &bitrate)
	}
	if format == "" {
		format = "mp3"
	}

	absMusicDir := ""
	if musicDir != "" {
		absMusicDir, _ = filepath.Abs(musicDir)
	}

	if mount[0] != '/' {
		mount = "/" + mount
	}

	adj := &config.AutoDJConfig{
		Name:           name,
		Mount:          mount,
		MusicDir:       absMusicDir,
		Format:         format,
		Bitrate:        bitrate,
		Enabled:        true,
		Loop:           loop,
		InjectMetadata: injectMetadata,
		MPDEnabled:     mpdEnabled,
		MPDPort:        mpdPort,
		MPDPassword:    mpdPassword,
		Visible:        visible,
		SongCommand:    songCommand,
	}

	s.Config.AutoDJs = append(s.Config.AutoDJs, adj)
	s.Config.SaveConfig()

	streamer, err := s.StreamerM.StartStreamer(adj.Name, adj.Mount, adj.MusicDir, adj.Loop, adj.Format, adj.Bitrate, adj.InjectMetadata, nil, adj.MPDEnabled, adj.MPDPort, adj.MPDPassword, adj.Visible, "", adj.SongCommand, adj.SongCommandTimeout)
	if err == nil {
		if adj.InjectMetadata {
			if st, ok := s.Relay.GetStream(adj.Mount); ok {
				st.SetVisible(adj.Visible)
			}
		}
		if adj.MusicDir != "" {
			streamer.ScanMusicDir()
		}
		streamer.Play()
	}

	http.Redirect(w, r, "/admin#tab-streamer", http.StatusSeeOther)
}

func (s *Server) handleDeleteAutoDJ(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	newADJs := []*config.AutoDJConfig{}
	for _, adj := range s.Config.AutoDJs {
		if adj.Mount != mount {
			newADJs = append(newADJs, adj)
		} else {
			s.StreamerM.DeleteStreamer(mount)
		}
	}
	s.Config.AutoDJs = newADJs
	s.Config.SaveConfig()

	http.Redirect(w, r, "/admin#tab-streamer", http.StatusSeeOther)
}

func (s *Server) handleToggleAutoDJ(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	mount := r.FormValue("mount")
	found := false
	existing := s.StreamerM.GetStreamer(mount)

	for _, adj := range s.Config.AutoDJs {
		if adj.Mount == mount {
			if adj.Enabled && existing == nil {
			} else {
				adj.Enabled = !adj.Enabled
			}

			if adj.Enabled {
				if existing == nil {
					absMusicDir, _ := filepath.Abs(adj.MusicDir)
					streamer, err := s.StreamerM.StartStreamer(adj.Name, adj.Mount, absMusicDir, adj.Loop, adj.Format, adj.Bitrate, adj.InjectMetadata, adj.Playlist, adj.MPDEnabled, adj.MPDPort, adj.MPDPassword, adj.Visible, adj.LastPlaylist, adj.SongCommand, adj.SongCommandTimeout)
					if err == nil {
						if adj.InjectMetadata {
							if st, ok := s.Relay.GetStream(adj.Mount); ok {
								st.SetVisible(adj.Visible)
							}
						}
						if len(adj.Playlist) == 0 {
							streamer.ScanMusicDir()
						}
						streamer.Play()
					}
				} else {
					existing.Play()
				}
			} else {
				s.StreamerM.StopStreamer(mount)
			}
			found = true
			break
		}
	}

	if !found {
		if existing != nil {
			s.StreamerM.StopStreamer(mount)
		}
	}

	s.Config.SaveConfig()
	http.Redirect(w, r, "/admin#tab-streamer", http.StatusSeeOther)
}

func (s *Server) handleAutoDJStudio(w http.ResponseWriter, r *http.Request) {
	user, ok := s.checkAuth(r)
	if !ok {
		return
	}

	mount := r.URL.Query().Get("mount")
	logger.L.Infof("Studio: Requested mount: %s", mount)

	streamer := s.StreamerM.GetStreamer(mount)
	if streamer == nil {
		logger.L.Warnf("Studio: Streamer not found for mount: %s", mount)
		http.Redirect(w, r, "/admin#tab-streamer", http.StatusSeeOther)
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
	s.shell.Render(w, "admin", "Studio — "+s.Config.PageTitle, pageData)
}

func (s *Server) handleUpdateAutoDJ(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if _, ok := s.checkAuth(r); !ok {
		return
	}

	oldMount := r.FormValue("old_mount")
	name := r.FormValue("name")
	newMount := r.FormValue("mount")
	musicDir := r.FormValue("music_dir")
	format := r.FormValue("format")
	bitrateStr := r.FormValue("bitrate")
	loop := r.FormValue("loop") == "on"
	injectMetadata := r.FormValue("inject_metadata") == "on"
	mpdEnabled := r.FormValue("mpd_enabled") == "on"
	mpdPort := r.FormValue("mpd_port")
	mpdPassword := r.FormValue("mpd_password")
	visible := r.FormValue("visible") == "on"

	songCommand := r.FormValue("song_command")
	if name == "" || newMount == "" {
		http.Error(w, "Name and mount are required", http.StatusBadRequest)
		return
	}
	if musicDir == "" && songCommand == "" {
		http.Error(w, "Either music directory or song command is required", http.StatusBadRequest)
		return
	}

	if newMount[0] != '/' {
		newMount = "/" + newMount
	}

	bitrate := 128
	fmt.Sscanf(bitrateStr, "%d", &bitrate)

	absMusicDir := ""
	if musicDir != "" {
		absMusicDir, _ = filepath.Abs(musicDir)
	}

	for _, adj := range s.Config.AutoDJs {
		if adj.Mount == oldMount {
			adj.Name = name
			adj.Mount = newMount
			adj.MusicDir = absMusicDir
			adj.Format = format
			adj.Bitrate = bitrate
			adj.Loop = loop
			adj.InjectMetadata = injectMetadata
			adj.MPDEnabled = mpdEnabled
			adj.MPDPort = mpdPort
			adj.MPDPassword = mpdPassword
			adj.Visible = visible
			adj.SongCommand = songCommand

			s.StreamerM.DeleteStreamer(oldMount)
			streamer, err := s.StreamerM.StartStreamer(adj.Name, adj.Mount, absMusicDir, adj.Loop, adj.Format, adj.Bitrate, adj.InjectMetadata, adj.Playlist, adj.MPDEnabled, adj.MPDPort, adj.MPDPassword, adj.Visible, adj.LastPlaylist, adj.SongCommand, adj.SongCommandTimeout)
			if err == nil {
				if adj.Enabled {
					if adj.InjectMetadata {
						if st, ok := s.Relay.GetStream(adj.Mount); ok {
							st.SetVisible(adj.Visible)
						}
					}
					streamer.Play()
				}
			}
			break
		}
	}

	s.Config.SaveConfig()
	http.Redirect(w, r, "/admin#tab-streamer", http.StatusSeeOther)
}
