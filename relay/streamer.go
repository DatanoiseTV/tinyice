package relay

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/dhowden/tag"
	"github.com/hajimehoshi/go-mp3"
	"github.com/sirupsen/logrus"
)

type StreamerState int

const (
	StateStopped StreamerState = iota
	StatePlaying
	StatePaused
)

type Streamer struct {
	Name           string
	OutputMount    string
	MusicDir       string
	Format         string
	Bitrate        int
	Playlist       []string
	Queue          []string
	CurrentPos     int
	State          StreamerState
	Loop           bool
	Shuffle        bool
	InjectMetadata bool
	MPDPassword    string

	relay  *Relay
	cancel context.CancelFunc
	mu     sync.RWMutex

	fileCancel context.CancelFunc

	// Stats
	BytesStreamed       int64
	CurrentFile         string
	CurrentFileTime     time.Time
	CurrentFileDuration time.Duration
	MPDServer           *MPDServer
}

type StreamerManager struct {
	instances map[string]*Streamer // key is OutputMount
	mu        sync.RWMutex
	relay     *Relay
	config    *config.Config
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
}

func (s *Streamer) Next() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fileCancel != nil {
		s.fileCancel()
	}
}

func (s *Streamer) TogglePlay() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.State == StatePlaying {
		s.State = StateStopped
	} else {
		s.State = StatePlaying
	}
}

func (s *Streamer) ToggleShuffle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Shuffle = !s.Shuffle
}

func (s *Streamer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.State = StateStopped
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *Streamer) PushToQueue(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Queue = append(s.Queue, path)
}

func (s *Streamer) RemoveFromQueue(index int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index < 0 || index >= len(s.Queue) {
		return
	}
	s.Queue = append(s.Queue[:index], s.Queue[index+1:]...)
}

func (s *Streamer) GetPlaylistNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]string, len(s.Playlist))
	for i, p := range s.Playlist {
		res[i] = filepath.Base(p)
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
	defer s.mu.Unlock()

	if s.MusicDir == "" {
		return fmt.Errorf("music directory not configured")
	}

	s.Playlist = []string{}
	err := filepath.Walk(s.MusicDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".mp3" {
				absPath, _ := filepath.Abs(path)
				s.Playlist = append(s.Playlist, absPath)
			}
		}
		return nil
	})
	return err
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
	copy(res, s.Playlist)
	return res
}

func (s *Streamer) SetPlaylist(p []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Playlist = p
}

func (s *Streamer) AddToPlaylist(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Playlist = append(s.Playlist, path)
}

func (s *Streamer) RemoveFromPlaylist(idx int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx >= 0 && idx < len(s.Playlist) {
		s.Playlist = append(s.Playlist[:idx], s.Playlist[idx+1:]...)
	}
}

type StreamerStats struct {
	Name         string
	Mount        string
	State        StreamerState
	CurrentSong  string
	StartTime    time.Time
	Duration     time.Duration
	PlaylistPos  int
	PlaylistLen  int
	Shuffle      bool
	MPDPort      string
	MPDPassword  string
	MusicDir     string
	Loop         bool
	InjectMetadata bool
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
		PlaylistPos:    s.CurrentPos,
		PlaylistLen:    len(s.Playlist),
		Shuffle:        s.Shuffle,
		MPDPort:        mpdPort,
		MPDPassword:    mpdPassword,
		MusicDir:       s.MusicDir,
		Loop:           s.Loop,
		InjectMetadata: s.InjectMetadata,
	}
}

func (sm *StreamerManager) StartStreamer(name, mount, musicDir string, loop bool, format string, bitrate int, injectMetadata bool, initialPlaylist []string, mpdEnabled bool, mpdPort, mpdPassword string) (*Streamer, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, ok := sm.instances[mount]; ok {
		return nil, fmt.Errorf("streamer for mount %s already exists", mount)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &Streamer{
		Name:           name,
		OutputMount:    mount,
		MusicDir:       musicDir,
		Format:         format,
		Bitrate:        bitrate,
		Playlist:       initialPlaylist,
		State:          StateStopped,
		Loop:           loop,
		InjectMetadata: injectMetadata,
		MPDPassword:    mpdPassword,
		relay:          sm.relay,
		cancel:         cancel,
	}

	if mpdEnabled && mpdPort != "" {
		s.MPDServer = NewMPDServer(mpdPort, mpdPassword, s)
		if err := s.MPDServer.Start(); err != nil {
			logrus.WithError(err).Errorf("Failed to start MPD server for AutoDJ %s", name)
		} else {
			logrus.Infof("MPD Server for %s listening on port %s", name, mpdPort)
		}
	}

	sm.instances[mount] = s

	go sm.runStreamerLoop(ctx, s)
	return s, nil
}

func (sm *StreamerManager) StopStreamer(mount string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if s, ok := sm.instances[mount]; ok {
		if s.MPDServer != nil {
			s.MPDServer.Stop()
		}
		s.Stop()
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
	s.Playlist = append(s.Playlist[:to], append([]string{item}, s.Playlist[to:]...)...)
}

func (sm *StreamerManager) runStreamerLoop(ctx context.Context, s *Streamer) {
	logrus.Infof("Streamer %s starting for mount %s", s.Name, s.OutputMount)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			if s.State != StatePlaying {
				time.Sleep(100 * time.Millisecond)
				continue
			}

			s.mu.Lock()
			var filePath string
			
			// 1. Check Queue first
			if len(s.Queue) > 0 {
				filePath = s.Queue[0]
				s.Queue = s.Queue[1:]
			} else if len(s.Playlist) > 0 {
				// 2. Handle Shuffle or Sequential Playlist
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
				filePath = s.Playlist[s.CurrentPos]
				if !s.Shuffle {
					s.CurrentPos++
				}
			}
			s.mu.Unlock()

			                        if filePath == "" {
			                                time.Sleep(1 * time.Second)
			                                continue
			                        }
			
			                        // Create a per-file context for skipping
			                        fileCtx, fileCancel := context.WithCancel(ctx)
			                        s.mu.Lock()
			                        s.fileCancel = fileCancel
			                        s.mu.Unlock()
			
			                        err := sm.streamFile(fileCtx, s, filePath)
			                        if err != nil && fileCtx.Err() == nil {
			                                logrus.WithError(err).Errorf("Streamer %s: Failed to stream %s", s.Name, filePath)
			                                time.Sleep(1 * time.Second)
			                        }
			
			                        s.mu.Lock()
			                        s.fileCancel = nil
			                        fileCancel()
			                        s.mu.Unlock()
			                }
			        }
			}
func (sm *StreamerManager) streamFile(ctx context.Context, s *Streamer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Extract metadata
	songTitle := filepath.Base(path)
	if m, err := tag.ReadFrom(f); err == nil {
		if m.Artist() != "" && m.Title() != "" {
			songTitle = fmt.Sprintf("%s - %s", m.Artist(), m.Title())
		} else if m.Title() != "" {
			songTitle = m.Title()
		}
	}
	// Seek back to start after reading tags
	f.Seek(0, 0)

	decoder, err := mp3.NewDecoder(f)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.CurrentFile = songTitle
	s.CurrentFileTime = time.Now()
	s.CurrentFileDuration = time.Duration(decoder.Length()) * time.Second / (44100 * 2 * 2) // Rough estimate for 44.1kHz 16bit stereo
	s.mu.Unlock()

	// Update stream metadata
	output := sm.relay.GetOrCreateStream(s.OutputMount)
	if s.InjectMetadata {
		output.CurrentSong = s.CurrentFile
		output.Name = s.Name
		output.Visible = true
		sm.relay.UpdateMetadata(s.OutputMount, s.CurrentFile)
	}
	output.Bitrate = fmt.Sprintf("%d", s.Bitrate)

	if s.Format == "opus" {
		output.ContentType = "audio/ogg"
		EncodeOpus(ctx, sm.relay, output, decoder, s.Bitrate, &s.BytesStreamed)
	} else {
		output.ContentType = "audio/mpeg"
		EncodeMP3(ctx, sm.relay, output, decoder, s.Bitrate, &s.BytesStreamed)
	}

	return nil
}
