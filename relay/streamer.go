package relay

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/logger"
	"github.com/bogem/id3v2/v2"
	"github.com/dhowden/tag"
)

type StreamerState int

const (
	StateStopped StreamerState = iota
	StatePlaying
	StatePaused
)

// autoDJSourceLabel identifies an AutoDJ as a mount's source, the same way
// the relay client uses "relay-pull" and WebRTC ingest uses
// "webrtc-source". Stream.SourceIP is a source descriptor, not strictly an
// address.
const autoDJSourceLabel = "autodj"

type Streamer struct {
	Name           string
	OutputMount    string
	MusicDir       string
	Format         string
	Bitrate        int
	Playlist       []PlaylistSong
	Queue          []string
	CurrentPos     int
	State          StreamerState
	Loop           bool
	Shuffle        bool
	InjectMetadata bool
	Visible        bool
	MPDPassword    string
	LastPlaylist        string
	SongCommand          string
	SongCommandTimeout   int
	OnPlayCommand        string
	OnPlayCommandTimeout int
	Volume               float64 // 0..1 playback gain applied before encode

	relay  *Relay
	ctx    context.Context // streamer-lifetime context, parents file/command ctxs
	cancel context.CancelFunc
	mu     sync.RWMutex

	fileCancel   context.CancelFunc
	titleCache   map[string]string
	titleFetchWg sync.WaitGroup

	// Stats
	BytesStreamed       int64
	CurrentFile         string
	CurrentArtist       string
	CurrentTitle        string
	CurrentAlbum        string
	CurrentFilePath     string
	CurrentPlayingPos   int
	CurrentPlayingID    int
	CurrentSampleRate   int
	CurrentChannels     int
	CurrentFileTime     time.Time
	CurrentFileDuration time.Duration
	// CurrentPausedNanos is how long the current track has spent paused,
	// so reported position stays honest instead of counting paused
	// wall-clock as playback progress.
	CurrentPausedNanos atomic.Int64
	MPDServer           *MPDServer
	NextID              int
	PlaylistVersion     uint32
	idleCh              chan string
	stateCh             chan struct{}
}

type StreamerManager struct {
	instances map[string]*Streamer // key is OutputMount
	mu        sync.RWMutex
	relay     *Relay
	config    *config.Config

	// OnTrackStart fires once per track, after metadata is parsed and just
	// before encoding begins. Used by the server to dispatch the
	// `now_playing` webhook event without dragging the server package
	// into relay (which would be a cyclic import). Set by the server at
	// startup; nil on construction.
	//
	// durationSeconds is 0 when the input format doesn't expose a length
	// up-front (typical for streaming PCM); receivers should treat 0 as
	// "unknown" rather than "instantaneous".
	OnTrackStart func(info TrackStartInfo)
}

// TrackStartInfo is the snapshot passed to OnTrackStart subscribers. A
// struct (rather than a long positional argument list) keeps the call
// site readable and lets us add fields without churning every caller.
type TrackStartInfo struct {
	Mount           string
	StreamerName    string
	Artist          string
	Title           string
	Album           string
	FilePath        string
	Format          string  // "mp3" | "opus"
	Bitrate         int     // kbps
	DurationSeconds float64 // 0 when unknown
}

func NewStreamerManager(r *Relay, cfg *config.Config) *StreamerManager {
	return &StreamerManager{
		instances: make(map[string]*Streamer),
		relay:     r,
		config:    cfg,
	}
}

func (s *Streamer) Play() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.State = StatePlaying
	s.signalStateChange()
}

func (s *Streamer) Next() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fileCancel != nil {
		s.fileCancel()
	}
}

// Previous rewinds the playlist by one track and cancels the currently
// playing file. The main loop increments CurrentPos after reading, so we
// subtract 2 here: the cancel triggers the next iteration which does
// CurrentPos++ before picking the file, landing one before the current
// track. Stops at the beginning of the playlist rather than underflowing.
func (s *Streamer) Previous() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CurrentPos -= 2
	if s.CurrentPos < 0 {
		s.CurrentPos = 0
	}
	if s.fileCancel != nil {
		s.fileCancel()
	}
}

// SetVolume records a playback gain (0.0 = silence, 1.0 = unity). The
// encode path applies it to every PCM sample before it reaches the MP3 /
// Opus encoder. Clamped to [0, 1] so the operator can't push into clipping
// territory by accident.
func (s *Streamer) SetVolume(v float64) {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	s.mu.Lock()
	s.Volume = v
	s.mu.Unlock()
}

// Volume returns the current playback gain. 1.0 if never set.
func (s *Streamer) GetVolume() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.Volume <= 0 {
		return 1.0
	}
	return s.Volume
}

func (s *Streamer) TogglePlay() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.State == StatePlaying {
		// Suspend mid-track (pauseGate stops the encoder reading);
		// don't cancel, so unpausing resumes the same file.
		s.State = StatePaused
	} else {
		s.State = StatePlaying
	}
	s.signalStateChange()
}

func (s *Streamer) ToggleShuffle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Shuffle = !s.Shuffle
}

func (s *Streamer) ToggleLoop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Loop = !s.Loop
}

func (s *Streamer) ToggleInjectMetadata() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.InjectMetadata = !s.InjectMetadata
}

func (s *Streamer) ClearQueue() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Queue = []string{}
}

// Stop halts playback. It is a TRANSPORT operation: the playback loop
// stays alive and parked on the state channel, so a later Play() resumes.
//
// It deliberately does NOT cancel the streamer-lifetime context. Doing so
// terminated runStreamerLoop for good, which is why "pause, then play"
// (API, MPD `stop`, and the enable/disable toggle all funnel here) left
// the AutoDJ permanently silent with the UI still reporting it as an
// instance. Use Shutdown for the teardown case.
func (s *Streamer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.State = StateStopped
	if s.fileCancel != nil {
		s.fileCancel()
	}
	s.signalStateChange()
}

// Pause suspends playback mid-track and reports StatePaused. The track
// is NOT cancelled: pauseGate blocks the encoder's reads, so Play()
// resumes the same file exactly where it stopped. Cancelling here is what
// made the Studio's Play button skip to the next track (#54).
//
// Note the mount goes silent for the duration — this is a live stream
// with no backlog, so listeners hear the gap.
func (s *Streamer) Pause() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.State = StatePaused
	s.signalStateChange()
}

// Shutdown tears the streamer down for good: playback stops and the
// streamer-lifetime context is cancelled, which ends runStreamerLoop and
// SIGKILLs any children (on_play_command `sh -c` invocations) instead of
// letting them live out their per-command timeout. The instance is not
// reusable afterwards — StartStreamer must build a new one.
func (s *Streamer) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.State = StateStopped
	if s.fileCancel != nil {
		s.fileCancel()
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.signalStateChange()
}

func (s *Streamer) Restart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.CurrentPos > 0 {
		s.CurrentPos--
	}
	if s.fileCancel != nil {
		s.fileCancel()
	}
}

func (s *Streamer) PushToQueue(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Queue = append(s.Queue, path)
}

// PushToQueueFront puts a track at the head of the queue — the "play
// next" action, as opposed to PushToQueue's "play eventually".
func (s *Streamer) PushToQueueFront(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Queue = append([]string{path}, s.Queue...)
}

// PathForPlaylistID resolves a playlist entry's stable ID (as handed to
// the UI in PlaylistItem.ID) to its file path.
func (s *Streamer) PathForPlaylistID(id int) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.Playlist {
		if p.ID == id {
			return p.Path, true
		}
	}
	return "", false
}

// RemoveFromPlaylistByID removes the entry with the given stable ID.
// RemoveFromPlaylist takes an INDEX (that's the MPD protocol's model);
// the JSON API hands us the ID it previously read from PlaylistItem, and
// the two only agree until the first removal.
func (s *Streamer) RemoveFromPlaylistByID(id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.Playlist {
		if p.ID == id {
			s.Playlist = append(s.Playlist[:i], s.Playlist[i+1:]...)
			s.PlaylistVersion++
			s.broadcastIdle("playlist")
			return true
		}
	}
	return false
}

func (s *Streamer) RemoveFromQueue(index int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index < 0 || index >= len(s.Queue) {
		return
	}
	s.Queue = append(s.Queue[:index], s.Queue[index+1:]...)
}

func (s *Streamer) MoveQueueItem(from, to int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if from < 0 || from >= len(s.Queue) || to < 0 || to >= len(s.Queue) {
		return
	}
	item := s.Queue[from]
	s.Queue = append(s.Queue[:from], s.Queue[from+1:]...)
	s.Queue = append(s.Queue[:to], append([]string{item}, s.Queue[to:]...)...)
}

type PlaylistSong struct {
	Path string
	ID   int
}

// PlaylistItem is the wire shape of one playlist / queue entry. The JSON
// tags matter: without them Go emitted "Title"/"Path"/"ID" while every
// other field of the AutoDJ API is snake_case, so the admin UI read
// undefined for each one and rendered blank rows.
//
// ID is -1 for queue entries, which have no stable playlist identity.
type PlaylistItem struct {
	Title string `json:"title"`
	Path  string `json:"path"`
	ID    int    `json:"id"`
}

func (s *Streamer) GetPlaylistInfo() []PlaylistItem {
	s.mu.RLock()
	playlist := make([]PlaylistSong, len(s.Playlist))
	copy(playlist, s.Playlist)
	s.mu.RUnlock()

	res := make([]PlaylistItem, len(playlist))
	for i, p := range playlist {
		res[i] = PlaylistItem{
			Title: s.GetSongTitle(p.Path),
			Path:  p.Path,
			ID:    p.ID,
		}
	}
	return res
}

func (s *Streamer) GetSongTitle(path string) string {
	s.mu.RLock()
	if title, ok := s.titleCache[path]; ok {
		s.mu.RUnlock()
		return title
	}
	s.mu.RUnlock()

	// Trigger background fetch if not already in progress
	go s.fetchTitleAndCache(path)

	// Fallback to filename if no title found (yet)
	return filepath.Base(path)
}

func (s *Streamer) fetchTitleAndCache(path string) {
	s.titleFetchWg.Add(1)
	go func() {
		defer s.titleFetchWg.Done()

		s.mu.Lock()
		if _, ok := s.titleCache[path]; ok {
			s.mu.Unlock()
			return // Already fetched by another concurrent call
		}
		s.mu.Unlock()

		title := filepath.Base(path)

		// Use id3v2 for extraction (Pure Go, no CGO/iconv)
		logger.L.Debugf("fetchTitleAndCache: Opening %s for ID3v2 parsing...", path)
		tag, err := id3v2.Open(path, id3v2.Options{Parse: true})
		if err != nil {
			logger.L.Errorf("fetchTitleAndCache: Failed to open %s for id3v2 parsing: %v", path, err)
		} else {
			defer tag.Close()
			artist := strings.TrimSpace(tag.Artist())
			song := strings.TrimSpace(tag.Title())

			logger.L.Debugf("fetchTitleAndCache: Raw tags for %s: artist=[%s] title=[%s]", path, artist, song)

			if artist != "" && song != "" {
				title = fmt.Sprintf("%s - %s", artist, song)
			} else if song != "" {
				title = song
			}
			logger.L.Debugf("fetchTitleAndCache: Final title for %s set to: %s", path, title)
		}

		s.mu.Lock()
		s.titleCache[path] = title
		s.mu.Unlock()
	}()
}

func (s *Streamer) GetQueueInfo() []PlaylistItem {
	s.mu.RLock()
	queue := make([]string, len(s.Queue))
	copy(queue, s.Queue)
	s.mu.RUnlock()

	res := make([]PlaylistItem, len(queue))
	for i, p := range queue {
		res[i] = PlaylistItem{
			Title: s.GetSongTitle(p),
			Path:  p,
			ID:    -1, // Queue items don't have stable IDs yet
		}
	}
	return res
}

func (s *Streamer) GetPlaylistNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]string, len(s.Playlist))
	for i, p := range s.Playlist {
		res[i] = filepath.Base(p.Path)
	}
	return res
}

func (s *Streamer) GetQueueNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]string, len(s.Queue))
	for i, p := range s.Queue {
		res[i] = filepath.Base(p)
	}
	return res
}

func (s *Streamer) ScanMusicDir() error {
	s.mu.Lock()
	if s.MusicDir == "" {
		s.mu.Unlock()
		return fmt.Errorf("music directory not configured")
	}

	// Clear cache
	s.titleCache = make(map[string]string)

	// Copy playlist to process outside of lock
	currentPlaylist := make([]PlaylistSong, len(s.Playlist))
	copy(currentPlaylist, s.Playlist)
	s.mu.Unlock()

	// Re-verify files and update cache in background
	go func() {
		for _, ps := range currentPlaylist {
			if _, err := os.Stat(ps.Path); err == nil {
				s.fetchTitleAndCache(ps.Path)
			}
		}
	}()

	return nil
}

func (s *Streamer) SavePlaylist() error {
	// Snapshot the playlist + name + cached titles under the lock,
	// then write to disk and call helpers (GetSongTitle) without
	// holding it. The previous code held s.mu.RLock for the entire
	// function and called GetSongTitle inside, which itself takes
	// s.mu.RLock — recursive RLock is undefined behaviour in Go's
	// RWMutex and deadlocks if a writer queues between the outer
	// and inner RLock acquisitions.
	s.mu.RLock()
	name := s.Name
	pl := make([]PlaylistSong, len(s.Playlist))
	copy(pl, s.Playlist)
	// Pre-populate a local title map from the cache so we don't have
	// to call back into GetSongTitle (which would re-RLock).
	titles := make(map[string]string, len(pl))
	for _, p := range pl {
		if t, ok := s.titleCache[p.Path]; ok {
			titles[p.Path] = t
		}
	}
	s.mu.RUnlock()

	if err := os.MkdirAll("playlists", 0755); err != nil {
		return err
	}

	path := filepath.Join("playlists", name+".pls")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprintf(f, "[playlist]\nNumberOfEntries=%d\n", len(pl))
	for i, p := range pl {
		fmt.Fprintf(f, "File%d=%s\n", i+1, p.Path)
		// Fall through to GetSongTitle if the cache miss — it'll
		// take its own RLock now that we've released ours. May
		// trigger a background fetch which is fine.
		title, ok := titles[p.Path]
		if !ok {
			title = s.GetSongTitle(p.Path)
		}
		fmt.Fprintf(f, "Title%d=%s\n", i+1, title)
	}
	fmt.Fprintf(f, "Version=2\n")
	return nil
}

func (s *Streamer) LoadPlaylist(filename string) error {
	if filename == "" {
		filename = s.Name + ".pls"
	}
	path := filepath.Join("playlists", filename)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			if filename == s.Name+".pls" {
				// Only save if we don't have a playlist in memory either
				s.mu.RLock()
				empty := len(s.Playlist) == 0
				s.mu.RUnlock()
				if empty {
					return s.SavePlaylist()
				}
			}
			return nil
		}
		return err
	}
	defer f.Close()

	var newPlaylistPaths []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "file") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				newPlaylistPaths = append(newPlaylistPaths, strings.TrimSpace(parts[1]))
			}
		}
	}

	if len(newPlaylistPaths) > 0 {
		s.mu.Lock()
		s.Playlist = []PlaylistSong{}
		for _, path := range newPlaylistPaths {
			s.Playlist = append(s.Playlist, PlaylistSong{Path: path, ID: s.NextID})
			s.NextID++
		}
		s.LastPlaylist = filename
		s.mu.Unlock()
	}
	return nil
}

func (s *Streamer) GetMusicDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.MusicDir
}

func (s *Streamer) GetPlaylist() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]string, len(s.Playlist))
	for i, p := range s.Playlist {
		res[i] = p.Path
	}
	return res
}

func (s *Streamer) SetPlaylist(p []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Playlist = []PlaylistSong{}
	for _, path := range p {
		s.Playlist = append(s.Playlist, PlaylistSong{Path: path, ID: s.NextID})
		s.NextID++
	}
	s.PlaylistVersion++
	s.broadcastIdle("playlist")
}

func (s *Streamer) SetLastPlaylist(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastPlaylist = name
}

func (s *Streamer) AddToPlaylist(path string) {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Playlist = append(s.Playlist, PlaylistSong{Path: path, ID: s.NextID})
	s.NextID++
	s.PlaylistVersion++
	s.broadcastIdle("playlist")
}

func (s *Streamer) RemoveFromPlaylist(idx int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx >= 0 && idx < len(s.Playlist) {
		s.Playlist = append(s.Playlist[:idx], s.Playlist[idx+1:]...)
		s.PlaylistVersion++
		s.broadcastIdle("playlist")
	}
}

func (s *Streamer) ClearPlaylist() {
	s.mu.Lock()
	s.Playlist = []PlaylistSong{}
	s.PlaylistVersion++
	s.mu.Unlock()
	s.SavePlaylist()
	s.broadcastIdle("playlist")
}

func (s *Streamer) broadcastIdle(subsystem string) {
	// Non-blocking broadcast
	select {
	case s.idleCh <- subsystem:
	default:
	}
}

func (s *Streamer) signalStateChange() {
	select {
	case s.stateCh <- struct{}{}:
	default:
	}
}

func (s *Streamer) execSongCommand() (string, error) {
	if s.SongCommand == "" {
		return "", fmt.Errorf("no song command configured")
	}

	timeout := s.SongCommandTimeout
	if timeout <= 0 {
		timeout = 5
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", s.SongCommand)
	cmd.Dir = s.MusicDir

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("song command failed: %w", err)
	}

	filePath := strings.TrimSpace(string(output))
	if filePath == "" {
		return "", fmt.Errorf("song command returned empty output")
	}

	// Take only the first line
	if idx := strings.IndexByte(filePath, '\n'); idx >= 0 {
		filePath = filePath[:idx]
	}
	filePath = strings.TrimSpace(filePath)

	// Resolve relative paths against music dir
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(s.MusicDir, filePath)
	}

	// Validate the file exists and is a supported audio format
	if err := validateAudioFile(filePath); err != nil {
		return "", fmt.Errorf("song command returned invalid file %q: %w", filePath, err)
	}

	return filePath, nil
}

// execOnPlayCommand runs the configured on_play_command in a background
// goroutine, passing track metadata via environment variables. This allows
// operators to integrate with external services (e.g. TuneIn AIR API,
// Discord webhooks) whenever a new track begins playing.
func (s *Streamer) execOnPlayCommand(artist, title, album, filePath, mount string) {
	if s.OnPlayCommand == "" {
		return
	}

	timeout := s.OnPlayCommandTimeout
	if timeout <= 0 {
		timeout = 10
	}

	parent := s.ctx
	if parent == nil {
		parent = context.Background()
	}
	go func() {
		// Inherit from the streamer's lifetime context so Stop()
		// propagates cancellation to the child shell (and thus the
		// command it spawned). The per-call WithTimeout still bounds
		// well-behaved commands; what changes is that an operator
		// stopping the streamer no longer waits 10 s of zombie
		// children to drain.
		ctx, cancel := context.WithTimeout(parent, time.Duration(timeout)*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "sh", "-c", s.OnPlayCommand)
		cmd.Dir = s.MusicDir
		cmd.Env = append(os.Environ(),
			"TINYICE_ARTIST="+artist,
			"TINYICE_TITLE="+title,
			"TINYICE_ALBUM="+album,
			"TINYICE_FILE="+filePath,
			"TINYICE_MOUNT="+mount,
		)

		output, err := cmd.CombinedOutput()
		if err != nil {
			logger.L.Warnw("on_play_command failed",
				"mount", mount,
				"command", s.OnPlayCommand,
				"error", err,
				"output", strings.TrimSpace(string(output)),
			)
			return
		}
		logger.L.Debugw("on_play_command completed",
			"mount", mount,
			"title", title,
		)
	}()
}

type StreamerStats struct {
	Name        string
	Mount       string
	State       StreamerState
	CurrentSong string
	StartTime   time.Time
	Duration    time.Duration
	// PlaylistPos is the cursor for the NEXT selection; CurrentPos is
	// incremented as soon as a track is picked. CurrentID / CurrentPos
	// identify the track actually playing, which is what a UI needs to
	// highlight the right row (-1 when the track came from the queue or
	// an external song command).
	CurrentID      int
	CurrentPos     int
	PlaylistPos    int
	PlaylistLen    int
	// PlaylistVersion bumps on every playlist mutation. Clients watch it
	// to know when to refetch the playlist, so the live event feed
	// doesn't have to carry the whole array on every tick.
	PlaylistVersion uint32
	// PausedFor is how long the current track has been paused.
	PausedFor      time.Duration
	Shuffle        bool
	MPDPort        string
	MPDPassword    string
	MusicDir       string
	Loop           bool
	InjectMetadata bool
	Visible        bool
	LastPlaylist   string
}

func (s *Streamer) GetStats() StreamerStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	mpdPort := ""
	mpdPassword := ""
	if s.MPDServer != nil {
		mpdPort = s.MPDServer.Port
		mpdPassword = s.MPDPassword
	}

	return StreamerStats{
		Name:           s.Name,
		Mount:          s.OutputMount,
		State:          s.State,
		CurrentSong:    s.CurrentFile,
		StartTime:      s.CurrentFileTime,
		Duration:       s.CurrentFileDuration,
		CurrentID:      s.CurrentPlayingID,
		CurrentPos:      s.CurrentPlayingPos,
		PlaylistPos:     s.CurrentPos,
		PlaylistVersion: s.PlaylistVersion,
		PausedFor:       time.Duration(s.CurrentPausedNanos.Load()),
		PlaylistLen:    len(s.Playlist),
		Shuffle:        s.Shuffle,
		MPDPort:        mpdPort,
		MPDPassword:    mpdPassword,
		MusicDir:       s.MusicDir,
		Loop:           s.Loop,
		InjectMetadata: s.InjectMetadata,
		Visible:        s.Visible,
		LastPlaylist:   s.LastPlaylist,
	}
}

func (sm *StreamerManager) StartStreamer(name, mount, musicDir string, loop bool, format string, bitrate int, injectMetadata bool, initialPlaylistPaths []string, mpdEnabled bool, mpdPort, mpdPassword string, visible bool, lastPlaylist string, songCommand string, songCommandTimeout int, onPlayCommand string, onPlayCommandTimeout int) (*Streamer, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, ok := sm.instances[mount]; ok {
		return nil, fmt.Errorf("streamer for mount %s already exists", mount)
	}

	ctx, cancel := context.WithCancel(context.Background())
	absMusicDir, _ := filepath.Abs(musicDir)

	initialPlaylist := make([]PlaylistSong, 0, len(initialPlaylistPaths))
	nextID := 1
	for _, path := range initialPlaylistPaths {
		initialPlaylist = append(initialPlaylist, PlaylistSong{Path: path, ID: nextID})
		nextID++
	}

	s := &Streamer{
		Name:              name,
		OutputMount:       mount,
		MusicDir:          absMusicDir,
		Format:            format,
		Bitrate:           bitrate,
		Playlist:          initialPlaylist,
		State:             StateStopped,
		Loop:              loop,
		InjectMetadata:    injectMetadata,
		Visible:           visible,
		MPDPassword:       mpdPassword,
		LastPlaylist:       lastPlaylist,
		SongCommand:          songCommand,
		SongCommandTimeout:   songCommandTimeout,
		OnPlayCommand:        onPlayCommand,
		OnPlayCommandTimeout: onPlayCommandTimeout,
		relay:                sm.relay,
		ctx:               ctx,
		cancel:            cancel,
		titleCache:        make(map[string]string),
		NextID:            nextID, // Start NextID after initial playlist
		CurrentPlayingPos: -1,
		CurrentPlayingID:  -1,
		PlaylistVersion:   1,
		idleCh:            make(chan string, 10),
		stateCh:           make(chan struct{}, 1),
	}

	if mpdEnabled && mpdPort != "" {
		logger.L.Debugf("AutoDJ %s: MPD enabled on port %s", name, mpdPort)
		// Check for port conflicts within our own instances
		for _, inst := range sm.instances {
			inst.mu.RLock()
			if inst.MPDServer != nil && inst.MPDServer.Port == mpdPort {
				inst.mu.RUnlock()
				logger.L.Warnf("AutoDJ %s: MPD port %s is already in use by %s", name, mpdPort, inst.Name)
				return nil, fmt.Errorf("MPD port %s is already in use by AutoDJ %s", mpdPort, inst.Name)
			}
			inst.mu.RUnlock()
		}

		s.MPDServer = NewMPDServer(mpdPort, mpdPassword, s)
		if err := s.MPDServer.Start(); err != nil {
			logger.L.Errorf("Failed to start MPD server for AutoDJ %s: %v", name, err)
		} else {
			logger.L.Infof("MPD Server for %s listening on port %s", name, mpdPort)
		}
	} else {
		logger.L.Debugf("AutoDJ %s: MPD not enabled or no port specified (enabled=%v, port=%s)", name, mpdEnabled, mpdPort)
	}

	sm.instances[mount] = s

	if lastPlaylist != "" {
		s.LoadPlaylist(lastPlaylist)
	} else {
		s.LoadPlaylist("")
	}

	go sm.runStreamerLoop(ctx, s)
	return s, nil
}

func (sm *StreamerManager) StopStreamer(mount string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if s, ok := sm.instances[mount]; ok {
		if s.MPDServer != nil {
			logger.L.Debugf("AutoDJ %s: Stopping MPD server", s.Name)
			s.MPDServer.Stop()
		}
		// Transport stop, not Shutdown: the instance stays in the map so
		// the UI can restart it, and that only works while the playback
		// loop is still alive. ResumeStreamer is the matching restart.
		s.Stop()
	}
}

// ResumeStreamer restarts a streamer that StopStreamer parked: it re-opens
// the MPD listener (StopStreamer closes it to release the port) and puts
// the transport back into playing state. Returns nil when no such instance
// exists, so callers can fall back to StartStreamer.
func (sm *StreamerManager) ResumeStreamer(mount string) *Streamer {
	sm.mu.RLock()
	s, ok := sm.instances[mount]
	sm.mu.RUnlock()
	if !ok {
		return nil
	}
	if s.MPDServer != nil {
		if err := s.MPDServer.Start(); err != nil {
			logger.L.Warnf("AutoDJ %s: could not restart MPD server: %v", s.Name, err)
		}
	}
	s.Play()
	return s
}

// DeleteStreamer stops the streamer and removes it from the manager entirely.
// Use this when the underlying AutoDJ config is being deleted (as opposed to
// StopStreamer which keeps the instance around so the UI can restart it).
func (sm *StreamerManager) DeleteStreamer(mount string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if s, ok := sm.instances[mount]; ok {
		if s.MPDServer != nil {
			logger.L.Debugf("AutoDJ %s: Stopping MPD server", s.Name)
			s.MPDServer.Stop()
		}
		s.Shutdown()
		delete(sm.instances, mount)
	}
}

func (sm *StreamerManager) GetStreamers() []*Streamer {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	res := make([]*Streamer, 0, len(sm.instances))
	for _, s := range sm.instances {
		res = append(res, s)
	}
	return res
}

func (sm *StreamerManager) GetStreamer(mount string) *Streamer {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.instances[mount]
}

func (s *Streamer) MovePlaylistItem(from, to int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if from < 0 || from >= len(s.Playlist) || to < 0 || to >= len(s.Playlist) {
		return
	}
	item := s.Playlist[from]
	// Remove
	s.Playlist = append(s.Playlist[:from], s.Playlist[from+1:]...)
	// Insert
	s.Playlist = append(s.Playlist[:to], append([]PlaylistSong{item}, s.Playlist[to:]...)...)
	s.PlaylistVersion++
	s.broadcastIdle("playlist")
}

func (sm *StreamerManager) runStreamerLoop(ctx context.Context, s *Streamer) {
	logger.L.Infof("Streamer %s starting for mount %s", s.Name, s.OutputMount)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			if s.State != StatePlaying {
				// Release the source label while parked: a stopped
				// AutoDJ is not a live source, and leaving the field
				// set would both show the mount as sourced in the admin
				// UI and block TryClaimSource for a real encoder.
				if out, ok := sm.relay.GetStream(s.OutputMount); ok {
					if out.GetSourceIP() == autoDJSourceLabel {
						out.SetSourceIP("")
					}
				}
				select {
				case <-ctx.Done():
					return
				case <-s.stateCh:
					continue
				}
			}

			s.mu.Lock()
			var filePath string
			var fileID int

			var filePos int
			// 1. Check Queue first
			if len(s.Queue) > 0 {
				filePath = s.Queue[0]
				s.Queue = s.Queue[1:]
				fileID = -1 // Queue items don't have an ID from the playlist
				filePos = -1
			} else if s.SongCommand != "" {
				// 2. External song command (unlock during exec to avoid blocking)
				s.mu.Unlock()
				if path, err := s.execSongCommand(); err == nil {
					filePath = path
					fileID = -1
					filePos = -1
				} else {
					logger.L.Warnf("Streamer %s: Song command error, falling back to playlist: %v", s.Name, err)
				}
				s.mu.Lock()
				// If command failed, try playlist as fallback
				if filePath == "" && len(s.Playlist) > 0 {
					if s.Shuffle {
						s.CurrentPos = rand.Intn(len(s.Playlist))
					} else {
						if s.CurrentPos >= len(s.Playlist) {
							if s.Loop {
								s.CurrentPos = 0
							} else {
								s.State = StateStopped
								s.mu.Unlock()
								continue
							}
						}
					}
					filePath = s.Playlist[s.CurrentPos].Path
					fileID = s.Playlist[s.CurrentPos].ID
					filePos = s.CurrentPos
					if !s.Shuffle {
						s.CurrentPos++
					}
				}
			} else if len(s.Playlist) > 0 {
				// 3. Normal playlist selection
				if s.Shuffle {
					s.CurrentPos = rand.Intn(len(s.Playlist))
				} else {
					if s.CurrentPos >= len(s.Playlist) {
						if s.Loop {
							s.CurrentPos = 0
						} else {
							s.State = StateStopped
							s.mu.Unlock()
							continue
						}
					}
				}
				filePath = s.Playlist[s.CurrentPos].Path
				fileID = s.Playlist[s.CurrentPos].ID
				filePos = s.CurrentPos
				if !s.Shuffle {
					s.CurrentPos++
				}
			}
			s.mu.Unlock()

			if filePath == "" {
				time.Sleep(1 * time.Second)
				continue
			}

			if err := validateAudioFile(filePath); err != nil {
				logger.L.Warnf("Streamer %s: Skipping invalid file %s: %v", s.Name, filePath, err)
				continue
			}

			// Create a per-file context for skipping
			fileCtx, fileCancel := context.WithCancel(ctx)
			s.mu.Lock()
			s.fileCancel = fileCancel
			s.mu.Unlock()

			err := sm.streamFile(fileCtx, s, filePath, filePos, fileID)
			if err != nil && fileCtx.Err() == nil {
				logger.L.Errorf("Streamer %s: Failed to stream %s: %v", s.Name, filePath, err)
				time.Sleep(1 * time.Second)
			}

			s.mu.Lock()
			s.fileCancel = nil
			fileCancel()
			s.mu.Unlock()
		}
	}
}

func validateAudioFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("file not accessible: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("path is a directory")
	}
	if info.Size() == 0 {
		return fmt.Errorf("file is empty")
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot open: %w", err)
	}
	defer f.Close()

	header := make([]byte, 4)
	if _, err := f.Read(header); err != nil {
		return fmt.Errorf("cannot read header: %w", err)
	}
	// MP3 with ID3v2 tags
	if header[0] == 'I' && header[1] == 'D' && header[2] == '3' {
		return nil
	}
	// Raw MP3 frame sync
	if header[0] == 0xFF && (header[1]&0xE0) == 0xE0 {
		return nil
	}
	// Ogg container (Vorbis / Opus / FLAC-in-Ogg)
	if string(header) == "OggS" {
		return nil
	}
	// Native FLAC
	if string(header) == "fLaC" {
		return nil
	}
	// WAV / RIFF
	if string(header) == "RIFF" {
		return nil
	}
	return fmt.Errorf("unrecognized audio format (header: %x)", header[:4])
}

func (sm *StreamerManager) streamFile(ctx context.Context, s *Streamer, path string, pos, id int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Extract metadata
	songTitle := filepath.Base(path)
	var tagMeta tag.Metadata
	if m, err := tag.ReadFrom(f); err == nil {
		tagMeta = m
		if m.Artist() != "" && m.Title() != "" {
			songTitle = fmt.Sprintf("%s - %s", m.Artist(), m.Title())
		} else if m.Title() != "" {
			songTitle = m.Title()
		}
	}
	// Seek back to start after reading tags
	f.Seek(0, 0)

	decoder, err := OpenDecoder(f)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.CurrentFile = songTitle
	s.CurrentArtist = ""
	s.CurrentTitle = songTitle
	s.CurrentAlbum = ""
	s.CurrentFilePath = path
	s.CurrentPlayingPos = pos
	s.CurrentPlayingID = id
	if tagMeta != nil {
		s.CurrentArtist = tagMeta.Artist()
		s.CurrentTitle = tagMeta.Title()
		s.CurrentAlbum = tagMeta.Album()
		if s.CurrentTitle == "" {
			s.CurrentTitle = songTitle
		}
	}
	s.CurrentSampleRate = decoder.SampleRate()
	s.CurrentChannels = 2 // decoders always emit 2 channels via OpenDecoder
	s.CurrentFileTime = time.Now()
	s.CurrentPausedNanos.Store(0)
	s.CurrentFileDuration = 0 // PCM length isn't known up-front for non-MP3 inputs
	s.mu.Unlock()

	// Update stream metadata under the output stream's mutex so concurrent
	// Snapshot / listener reads see a coherent set of fields.
	output := sm.relay.GetOrCreateStream(s.OutputMount)
	output.mu.Lock()
	if s.InjectMetadata {
		output.CurrentSong = s.CurrentFile
		output.Name = s.Name
		output.Visible = true
	}
	// Identify the AutoDJ as the mount's source. Every other ingest does
	// this — icecast/RTMP/SRT record the peer address, the relay client
	// uses "relay-pull" and WebRTC uses "webrtc-source" — but the AutoDJ
	// writes in-process and left the field empty, so the admin UI showed a
	// playing AutoDJ mount as "No source" with a grey (offline) dot.
	output.SourceIP = autoDJSourceLabel
	output.Bitrate = fmt.Sprintf("%d", s.Bitrate)
	if s.Format == "opus" {
		output.ContentType = "audio/ogg"
	} else {
		output.ContentType = "audio/mpeg"
	}
	output.mu.Unlock()
	if s.InjectMetadata {
		sm.relay.UpdateMetadata(s.OutputMount, s.CurrentFile)
	}

	// Fire on_play_command hook (runs in background, non-blocking).
	s.execOnPlayCommand(s.CurrentArtist, s.CurrentTitle, s.CurrentAlbum, path, s.OutputMount)

	// Notify the server so it can dispatch webhook subscribers. Run in a
	// goroutine: the server callback may block briefly on JSON marshal /
	// HTTP send setup and we don't want to delay the encoder start.
	// recover() because this runs outside any HTTP handler so net/http's
	// own panic guard doesn't apply — a panic here would otherwise crash
	// the whole process.
	if cb := sm.OnTrackStart; cb != nil {
		info := TrackStartInfo{
			Mount:           s.OutputMount,
			StreamerName:    s.Name,
			Artist:          s.CurrentArtist,
			Title:           s.CurrentTitle,
			Album:           s.CurrentAlbum,
			FilePath:        path,
			Format:          s.Format,
			Bitrate:         s.Bitrate,
			DurationSeconds: s.CurrentFileDuration.Seconds(),
		}
		go func() {
			defer func() {
				if r := recover(); r != nil {
					logger.L.Errorw("OnTrackStart callback panicked",
						"mount", info.Mount, "panic", fmt.Sprintf("%v", r))
				}
			}()
			cb(info)
		}()
	}

	// Apply the streamer's volume setting (0..1) to the PCM stream before
	// it reaches the encoder. When Volume is 1.0 the wrapper is a no-op.
	var pcm io.Reader = decoder
	if gain := s.GetVolume(); gain < 1.0 {
		pcm = newGainReader(pcm, gain)
	}
	// Pause support: blocks the encoder's reads while paused so the track
	// resumes in place rather than being cancelled.
	pcm = newPauseGate(ctx, s, pcm)

	if s.Format == "opus" {
		// Opus encoder is locked at 48 kHz; resample if the file is at a
		// different rate so playback isn't sped up / slowed down.
		if decoder.SampleRate() != 48000 {
			pcm = NewLinearResampler(pcm, decoder.SampleRate(), 48000)
		}
		EncodeOpus(ctx, sm.relay, output, pcm, s.Bitrate, &s.BytesStreamed, true)
	} else {
		EncodeMP3(ctx, sm.relay, output, pcm, s.Bitrate, &s.BytesStreamed, true, decoder.SampleRate())
	}

	return nil
}

// gainReader multiplies every S16LE stereo sample it passes through by a
// fixed gain in [0, 1]. Used to apply the AutoDJ's Volume setting without
// touching the decoder / encoder interfaces.
type gainReader struct {
	src  io.Reader
	gain float64
}

func newGainReader(src io.Reader, gain float64) *gainReader {
	if gain < 0 {
		gain = 0
	}
	if gain > 1 {
		gain = 1
	}
	return &gainReader{src: src, gain: gain}
}

func (g *gainReader) Read(p []byte) (int, error) {
	n, err := g.src.Read(p)
	if n == 0 {
		return n, err
	}
	// Process whole 2-byte samples only; defer odd trailing byte to the
	// next read (the source always feeds us whole stereo frames so this
	// rarely matters).
	end := n - (n % 2)
	for i := 0; i+1 < end; i += 2 {
		s := int32(int16(p[i]) | int16(p[i+1])<<8)
		s = int32(float64(s) * g.gain)
		if s > 32767 {
			s = 32767
		} else if s < -32768 {
			s = -32768
		}
		p[i] = byte(s)
		p[i+1] = byte(s >> 8)
	}
	return n, err
}

// pauseGate sits between the decoder and the encoder. While the streamer
// is paused it blocks in Read, which stops the encoder pulling PCM and
// leaves the decoder exactly where it was — so resuming continues the
// same track at the same position instead of skipping to the next one.
//
// It also records how long it spent blocked. The encoders pace themselves
// against wall-clock time from the start of the track; without that
// correction, resuming after a 60 s pause would leave them "behind
// schedule" and they would dump the rest of the file into the ring buffer
// as fast as the CPU allows.
type pauseGate struct {
	src        io.Reader
	s          *Streamer
	ctx        context.Context
	pausedTotal atomic.Int64 // nanoseconds spent blocked
}

func newPauseGate(ctx context.Context, s *Streamer, src io.Reader) *pauseGate {
	return &pauseGate{src: src, s: s, ctx: ctx}
}

// PausedTotal implements the pauseAware interface the encoders check when
// computing how far ahead of real time they have encoded.
func (g *pauseGate) PausedTotal() time.Duration {
	return time.Duration(g.pausedTotal.Load())
}

func (g *pauseGate) Read(p []byte) (int, error) {
	const poll = 50 * time.Millisecond // inaudible resume latency
	var pausedFrom time.Time
	for {
		g.s.mu.RLock()
		paused := g.s.State == StatePaused
		g.s.mu.RUnlock()
		if !paused {
			if !pausedFrom.IsZero() {
				d := int64(time.Since(pausedFrom))
				g.pausedTotal.Add(d)
				g.s.CurrentPausedNanos.Add(d)
			}
			return g.src.Read(p)
		}
		if pausedFrom.IsZero() {
			pausedFrom = time.Now()
		}
		select {
		case <-g.ctx.Done():
			// Track cancelled (skip / stop / shutdown) while paused.
			if !pausedFrom.IsZero() {
				g.pausedTotal.Add(int64(time.Since(pausedFrom)))
			}
			return 0, io.EOF
		case <-time.After(poll):
		}
	}
}
