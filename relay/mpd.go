package relay

import (
	"bufio"
	"crypto/subtle"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DatanoiseTV/tinyice/logger"
)

// maxMPDLineLen caps how many bytes we'll read before declaring a single
// MPD command line malformed. The real protocol never uses lines this
// large; the bound is here so a client that holds the connection open and
// streams bytes with no newline can't force unbounded buffer growth.
const maxMPDLineLen = 64 * 1024

type MPDServer struct {
	Port     string
	Password string
	streamer *Streamer

	// mu guards listener, which Start/Stop swap as an AutoDJ is
	// disabled and re-enabled (ResumeStreamer).
	mu       sync.Mutex
	listener net.Listener
}

// readMPDLine reads up to and including the next '\n', or returns
// io.ErrShortBuffer once total bytes seen exceed maxLen. Replacement for
// bufio.Reader.ReadString('\n') which has no upper bound.
func readMPDLine(r *bufio.Reader, maxLen int) (string, error) {
	var sb strings.Builder
	for {
		chunk, err := r.ReadSlice('\n')
		sb.Write(chunk)
		if err == nil {
			return sb.String(), nil
		}
		if err == bufio.ErrBufferFull {
			if sb.Len() > maxLen {
				return "", io.ErrShortBuffer
			}
			continue
		}
		// EOF / real error
		if sb.Len() > 0 && err == io.EOF {
			return sb.String(), nil
		}
		return "", err
	}
}

func NewMPDServer(port, password string, s *Streamer) *MPDServer {
	return &MPDServer{
		Port:     port,
		Password: password,
		streamer: s,
	}
}

// Start binds the MPD port and serves connections until Stop. Calling it
// on an already-listening server is a no-op rather than an error, so a
// re-enable path can call it unconditionally.
func (m *MPDServer) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listener != nil {
		return nil
	}

	addr := m.Port
	if !strings.Contains(addr, ":") {
		addr = ":" + addr
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	m.listener = l
	logger.L.Infof("MPD Server listening on %s", addr)

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go m.handleConnection(conn)
		}
	}()

	return nil
}

func (m *MPDServer) Stop() {
	logger.L.Debugf("MPD: Stopping server on port %s", m.Port)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listener != nil {
		m.listener.Close()
		m.listener = nil
	}
}

func (m *MPDServer) handleConnection(conn net.Conn) {
	defer conn.Close()
	writer := bufio.NewWriter(conn)
	// Use a sized buffered reader and readMPDLine so a client that streams
	// megabytes without a newline can't grow our buffer until we OOM.
	reader := bufio.NewReaderSize(conn, 8192)

	resp := NewMPDResponse(writer)

	logger.L.Debugf("MPD: New connection from %v", conn.RemoteAddr())

	// Greeting
	resp.Greeting("0.23.5")
	writer.Flush()

	authenticated := m.Password == ""
	commandList := false
	commandListOK := false
	var commandListBuffer []string

	for {
		line, err := readMPDLine(reader, maxMPDLineLen)
		if err != nil {
			if err != io.EOF {
				logger.L.Errorf("MPD: Connection error: %v", err)
			} else {
				logger.L.Debug("MPD: Connection closed by client")
			}
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, " ", 2)
		cmd := strings.ToLower(parts[0])
		args := ""
		if len(parts) > 1 {
			args = parts[1]
		}

		logger.L.Debugf("MPD: Command: %s, Args: %s (Authenticated: %v)", cmd, args, authenticated)

		if !authenticated && cmd != "password" && cmd != "close" {
			logger.L.Debugf("MPD: Permission denied for command: %s", cmd)
			resp.ACK(5, 0, "", "permission denied")
			writer.Flush()
			continue
		}

		switch cmd {
		case "command_list_begin":
			commandList = true
			commandListOK = false
			commandListBuffer = []string{}
			continue
		case "command_list_ok_begin":
			commandList = true
			commandListOK = true
			commandListBuffer = []string{}
			continue
		case "command_list_end":
			if !commandList {
				resp.ACK(5, 0, "command_list_end", "not in command list")
				writer.Flush()
				continue
			}
			commandList = false
			// Execute all buffered commands
			allOK := true
			for _, bufferedLine := range commandListBuffer {
				parts := strings.SplitN(bufferedLine, " ", 2)
				bcmd := strings.ToLower(parts[0])
				bargs := ""
				if len(parts) > 1 {
					bargs = parts[1]
				}
				if !m.dispatchCommand(bcmd, bargs, resp) {
					// On error, the ACK is already sent by dispatchCommand or it should be!
					// Actually we need to send the correct [error@list_pos]
					// Let's refine ACK to be more generic
					allOK = false
					break
				}
				if commandListOK {
					resp.ListOK()
				}
			}
			if allOK {
				resp.OK()
			}
			writer.Flush()
			continue
		}

		if commandList {
			// Bound the command list — without this an authenticated
			// client could send `command_list_begin` followed by
			// millions of lines and never close the list, growing
			// commandListBuffer until the process OOMs. 10 000 is
			// well past any real client (typical MPD clients send a
			// few dozen commands per list).
			const maxCmdListLen = 10000
			if len(commandListBuffer) >= maxCmdListLen {
				resp.ACK(5, 0, "command_list", "command list too long")
				writer.Flush()
				return
			}
			commandListBuffer = append(commandListBuffer, line)
			continue
		}

		if m.dispatchCommand(cmd, args, resp) {
			resp.OK()
		}
		writer.Flush()
	}
}

func (m *MPDServer) dispatchCommand(cmd, args string, resp *MPDResponse) bool {
	switch cmd {
	case "password":
		// Constant-time compare — without this the comparison short-
		// circuits on the first wrong byte, leaking the prefix length
		// of m.Password to an attacker probing one byte at a time.
		// MPD passwords are short and plaintext (protocol-level) but
		// timing leaks are still fixable.
		if subtle.ConstantTimeCompare([]byte(args), []byte(m.Password)) == 1 {
			return true
		}
		resp.ACK(3, 0, "password", "incorrect password")
		return false
	case "status":
		m.handleStatus(resp)
	case "currentsong":
		m.handleCurrentSong(resp)
	case "play":
		m.streamer.Play()
	case "stop":
		m.streamer.Stop()
	case "pause":
		m.streamer.TogglePlay()
	case "next":
		m.streamer.Next()
	case "previous":
		// Not implemented
	case "ping":
		// OK
	case "idle":
		m.handleIdle(resp)
		return true
	case "update":
		m.streamer.ScanMusicDir()
		resp.Field("updating_db", 1)
	case "listall", "listallinfo":
		m.handleListAll(resp)
	case "lsinfo":
		m.handleLsInfo(args, resp)
	case "playlistinfo":
		m.handlePlaylistInfo(args, resp)
	case "playlistid":
		m.handlePlaylistId(args, resp)
	case "add":
		m.handleAdd(args, resp)
	case "addid":
		m.handleAddId(args, resp)
	case "delete":
		m.handleDelete(args, resp)
	case "deleteid":
		m.handleDeleteId(args, resp)
	case "clear":
		m.streamer.ClearPlaylist()
	case "move":
		m.handleMove(args, resp)
	case "moveid":
		m.handleMoveId(args, resp)
	case "shuffle":
		m.streamer.mu.Lock()
		// Basic shuffle of existing playlist
		rand.Shuffle(len(m.streamer.Playlist), func(i, j int) {
			m.streamer.Playlist[i], m.streamer.Playlist[j] = m.streamer.Playlist[j], m.streamer.Playlist[i]
		})
		m.streamer.mu.Unlock()
	case "listplaylists":
		m.handleListPlaylists(resp)
	case "load":
		m.handleLoad(args, resp)
	case "save":
		m.handleSave(args, resp)
	case "rm":
		m.handleRm(args, resp)
	case "outputs":
		resp.Field("outputid", 0)
		resp.Field("outputname", m.streamer.OutputMount)
		resp.Field("outputenabled", 1)
		resp.Field("outputid", 1)
		resp.Field("outputname", "HTTP Stream")
		resp.Field("outputenabled", 1)
	case "stats":
		resp.Field("uptime", 0)
		resp.Field("playtime", 0)
		resp.Field("artists", 0)
		resp.Field("albums", 0)
		resp.Field("songs", len(m.streamer.Playlist))
	case "config":
		// Ignore
	case "plchanges":
		m.handlePlaylistChanges(args, resp)
	case "plchangesposid":
		m.handlePlaylistInfo(args, resp)
	case "decoders":
		resp.Field("plugin", "mad")
		resp.Field("suffix", "mp3")
		resp.Field("mime_type", "audio/mpeg")
		resp.Field("plugin", "mpg123")
		resp.Field("suffix", "mp3")
		resp.Field("mime_type", "audio/mpeg")
		resp.Field("plugin", "vorbis")
		resp.Field("suffix", "ogg")
		resp.Field("mime_type", "audio/ogg")
	case "tagtypes":
		resp.Field("tagtype", "Artist")
		resp.Field("tagtype", "Title")
		resp.Field("tagtype", "Album")
	case "urlhandlers":
		resp.Field("handler", "http")
		resp.Field("handler", "https")
	case "search":
		m.handleSearch(args, resp)
	case "albumart", "readpicture":
		m.handleAlbumArt(args, resp)
		return false
	default:
		logger.L.Debugf("MPD: Unknown command: %s", cmd)
		resp.ACK(5, 0, cmd, "unknown command")
		return false
	}
	return true
}

func (m *MPDServer) handleIdle(resp *MPDResponse) {
	// Wait for events or timeout
	select {
	case <-m.streamer.idleCh:
		resp.Field("changed", "playlist")
	case <-time.After(30 * time.Second):
	}
}

func (m *MPDServer) handleSearch(args string, resp *MPDResponse) {
	// search <type> <query>
	parts := strings.SplitN(args, " ", 2)
	if len(parts) < 2 {
		resp.OK()
		return
	}
	// type := strings.ToLower(parts[0])
	query := strings.Trim(parts[1], "\"")
	queryLower := strings.ToLower(query)

	m.streamer.mu.RLock()
	musicDir := m.streamer.MusicDir
	m.streamer.mu.RUnlock()

	filepath.Walk(musicDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		// Basic title/filename search
		rel, _ := filepath.Rel(musicDir, path)
		if strings.Contains(strings.ToLower(rel), queryLower) {
			m.writeSongInfo(resp, rel, 0, 0) // Pos/Id 0 for search results usually
		}
		return nil
	})
}

func (m *MPDServer) handleAlbumArt(args string, resp *MPDResponse) {
	// 1x1 transparent PNG
	fakeArt := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
		0x42, 0x60, 0x82,
	}

	// Args: "file" "offset"
	parts := strings.Split(args, " ")
	offset := 0
	if len(parts) > 1 {
		fmt.Sscanf(parts[1], "%d", &offset)
	}

	if offset >= len(fakeArt) {
		resp.ACK(50, 0, "albumart", "No album art is available at this offset")
		return
	}

	resp.Field("size", len(fakeArt))
	resp.Binary(fakeArt[offset:])
	resp.OK()
}

func (m *MPDServer) handlePlaylistChanges(args string, resp *MPDResponse) {
	var version uint32
	fmt.Sscanf(args, "%d", &version)

	m.streamer.mu.RLock()
	currentVersion := m.streamer.PlaylistVersion
	playlist := make([]PlaylistSong, len(m.streamer.Playlist))
	copy(playlist, m.streamer.Playlist)
	m.streamer.mu.RUnlock()

	if version < currentVersion {
		for i, ps := range playlist {
			rel, _ := filepath.Rel(m.streamer.MusicDir, ps.Path)
			m.writeSongInfo(resp, rel, i, ps.ID)
		}
	}
}
func (m *MPDServer) handleStatus(resp *MPDResponse) {
	state_str := "stop"
	if m.streamer.State == StatePlaying {
		state_str = "play"
	} else if m.streamer.State == StatePaused {
		state_str = "pause"
	}
	resp.Field("state", state_str)
	resp.Field("volume", 100)
	resp.Field("repeat", 0)
	resp.Field("random", 0)
	resp.Field("single", 0)
	resp.Field("consume", 0)

	resp.Field("playlist", 1)
	resp.Field("playlistlength", len(m.streamer.Playlist))

	m.streamer.mu.RLock()
	state := m.streamer.State
	elapsed := time.Since(m.streamer.CurrentFileTime).Seconds()
	duration := m.streamer.CurrentFileDuration.Seconds()
	if m.streamer.CurrentFileTime.IsZero() {
		elapsed = 0
	}
	sampleRate := m.streamer.CurrentSampleRate
	channels := m.streamer.CurrentChannels
	bitrate := m.streamer.Bitrate
	playingPos := m.streamer.CurrentPlayingPos
	playingID := m.streamer.CurrentPlayingID
	m.streamer.mu.RUnlock()

	if state != StateStopped {
		resp.Field("time", fmt.Sprintf("%d:%d", int(elapsed), int(duration)))
		resp.Field("elapsed", fmt.Sprintf("%.3f", elapsed))
		resp.Field("duration", fmt.Sprintf("%.3f", duration))
		resp.Field("bitrate", bitrate)
		if sampleRate > 0 {
			resp.Field("audio", fmt.Sprintf("%d:16:%d", sampleRate, channels))
		}
		if playingPos >= 0 {
			resp.Field("song", playingPos)
			resp.Field("songid", playingID)
		}
	}
}

func (m *MPDServer) handleCurrentSong(resp *MPDResponse) {
	m.streamer.mu.RLock()
	defer m.streamer.mu.RUnlock()
	if m.streamer.CurrentPlayingPos >= 0 {
		m.writeSongInfo(resp, m.streamer.CurrentFile, m.streamer.CurrentPlayingPos, m.streamer.CurrentPlayingID)
	}
}

func (m *MPDServer) writeSongInfo(resp *MPDResponse, file string, pos, id int) {
	resp.Field("file", file)
	// Try to get tags from streamer cache
	artist := ""
	title := ""
	album := ""
	duration := 0

	fullPath := file
	if !filepath.IsAbs(file) {
		fullPath = filepath.Join(m.streamer.MusicDir, file)
	}

	m.streamer.mu.RLock()
	// If it's the current song, use the live metadata
	if fullPath == m.streamer.CurrentFilePath {
		artist = m.streamer.CurrentArtist
		title = m.streamer.CurrentTitle
		album = m.streamer.CurrentAlbum
		duration = int(m.streamer.CurrentFileDuration.Seconds())
	} else {
		// Try title cache
		if t, ok := m.streamer.titleCache[fullPath]; ok {
			title = t
		}
	}
	m.streamer.mu.RUnlock()

	if artist != "" {
		resp.Field("Artist", artist)
	}
	if title != "" {
		resp.Field("Title", title)
	}
	if album != "" {
		resp.Field("Album", album)
	}
	if duration > 0 {
		resp.Field("Time", duration)
		resp.Field("duration", fmt.Sprintf("%.3f", float64(duration)))
	}
	resp.Field("Pos", pos)
	resp.Field("Id", id)
}

func (m *MPDServer) handlePlaylistInfo(args string, resp *MPDResponse) {
	m.streamer.mu.RLock()
	playlist := make([]PlaylistSong, len(m.streamer.Playlist))
	copy(playlist, m.streamer.Playlist)
	m.streamer.mu.RUnlock()

	for i, ps := range playlist {
		rel, _ := filepath.Rel(m.streamer.MusicDir, ps.Path)
		m.writeSongInfo(resp, rel, i, ps.ID)
	}
}

func (m *MPDServer) handlePlaylistId(args string, resp *MPDResponse) {
	// Parse ID
	id := 0
	fmt.Sscanf(args, "%d", &id)
	if id <= 0 {
		m.handlePlaylistInfo("", resp)
		return
	}

	m.streamer.mu.RLock()
	if id > 0 {
		for i, ps := range m.streamer.Playlist {
			if ps.ID == id {
				rel, _ := filepath.Rel(m.streamer.MusicDir, ps.Path)
				m.writeSongInfo(resp, rel, i, ps.ID)
				break
			}
		}
	}
	m.streamer.mu.RUnlock()
}

func (m *MPDServer) handleLsInfo(args string, resp *MPDResponse) {
	m.streamer.mu.RLock()
	defer m.streamer.mu.RUnlock()

	dir := m.streamer.MusicDir
	if args != "" {
		dir = filepath.Join(m.streamer.MusicDir, strings.Trim(args, "\""))
	}

	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if entry.IsDir() {
			resp.Field("directory", entry.Name())
		} else {
			resp.Field("file", entry.Name())
		}
	}
}

func (m *MPDServer) handleAdd(args string, resp *MPDResponse) {
	path := strings.Trim(args, "\"")
	full := filepath.Join(m.streamer.MusicDir, path)
	m.streamer.AddToPlaylist(full)
}

func (m *MPDServer) handleAddId(args string, resp *MPDResponse) {
	path := strings.Trim(args, "\"")
	full := filepath.Join(m.streamer.MusicDir, path)
	m.streamer.AddToPlaylist(full)
	resp.Field("Id", len(m.streamer.GetPlaylist()))
}

func (m *MPDServer) handleDelete(args string, resp *MPDResponse) {
	pos := -1
	fmt.Sscanf(args, "%d", &pos)
	if pos >= 0 {
		m.streamer.RemoveFromPlaylist(pos)
	}
}

func (m *MPDServer) handleDeleteId(args string, resp *MPDResponse) {
	id := -1
	fmt.Sscanf(args, "%d", &id)
	if id > 0 {
		m.streamer.RemoveFromPlaylist(id - 1)
	}
}

func (m *MPDServer) handleMove(args string, resp *MPDResponse) {
	var from, to int
	fmt.Sscanf(args, "%d %d", &from, &to)
	m.streamer.MovePlaylistItem(from, to)
}

func (m *MPDServer) handleMoveId(args string, resp *MPDResponse) {
	var fromId, to int
	fmt.Sscanf(args, "%d %d", &fromId, &to)
	if fromId > 0 {
		m.streamer.MovePlaylistItem(fromId-1, to)
	}
}

func (m *MPDServer) handleListPlaylists(resp *MPDResponse) {
	entries, _ := os.ReadDir("playlists")
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".pls") {
			name := strings.TrimSuffix(entry.Name(), ".pls")
			resp.Field("playlist", name)
		}
	}
}

func (m *MPDServer) handleLoad(args string, resp *MPDResponse) {
	name := strings.Trim(args, "\"")
	m.streamer.LoadPlaylist(name + ".pls")
}

func (m *MPDServer) handleSave(args string, resp *MPDResponse) {
	// Streamer already has a SavePlaylist that uses s.Name
	// Let's just override it if we want custom name?
	// For now, use streamer's name or ignore args if they match
	m.streamer.SavePlaylist()
}

func (m *MPDServer) handleRm(args string, resp *MPDResponse) {
	name := strings.Trim(args, "\"")
	os.Remove(filepath.Join("playlists", name+".pls"))
}

func (m *MPDServer) handleListAll(resp *MPDResponse) {
	// Dummy for now, just list what's in the music dir or playlist
	m.streamer.mu.RLock()
	playlist := make([]PlaylistSong, len(m.streamer.Playlist))
	copy(playlist, m.streamer.Playlist)
	m.streamer.mu.RUnlock()

	for _, ps := range playlist {
		rel, err := filepath.Rel(m.streamer.MusicDir, ps.Path)
		if err != nil {
			rel = filepath.Base(ps.Path)
		}
		resp.Field("file", rel)
	}
}
