import { useEffect, useCallback, useRef } from 'preact/hooks'
import { signal, computed } from '@preact/signals'
import { api } from '@/lib/api'
import { createSSE } from '@/lib/sse'
import { Visualizer } from '@/components/Visualizer'
import { TransportControls } from '@/components/TransportControls'
import { VolumeKnob } from '@/components/VolumeKnob'
import { Toggle } from '@/components/Toggle'
import { EqBars } from '@/components/EqBars'
import { PlaylistItem as PlaylistItemComp } from '@/components/PlaylistItem'
import { FileItem } from '@/components/FileItem'
import { autoDJState } from '@/types'
import type { PlaylistItem, FileInfo, AutoDJEvent, AutoDJState } from '@/types'

// Subset of the /api/autodj row this page reads. The full shape lives in
// the AutoDJ page; keep the field names identical to the JSON.
interface AutoDJInstanceRaw {
  name: string
  mount: string
  format: string
  state: number
  current_song: string
  start_time: number
  position: number
  duration: number
  current_id: number
  playlist_pos: number
  playlist_len: number
  shuffle: boolean
  loop: boolean
}

// ── Current mount ──────────────────────────────────────────
const currentMount = signal('')
const availableMounts = signal<string[]>([])
const loadingMounts = signal(true)

// ── State signals ──────────────────────────────────────────
const state = signal<AutoDJState>('stopped')
const trackTitle = signal('No Track')
const trackArtist = signal('Unknown Artist')
const position = signal(0)
const duration = signal(0)
const listeners = signal(0)
const uptime = signal(0)
const volume = signal(80)
const metadataEnabled = signal(true)
const format = signal('mp3')
const transportError = signal('')

// ── Library state ──────────────────────────────────────────
const libraryPath = signal('')
const libraryFiles = signal<FileInfo[]>([])
const librarySearch = signal('')
const selectedFile = signal<string | null>(null)

// ── Playlist/Queue/History ─────────────────────────────────
const activeTab = signal<'playlist' | 'queue' | 'history'>('playlist')
const playlist = signal<PlaylistItem[]>([])
const queue = signal<PlaylistItem[]>([])
const history = signal<PlaylistItem[]>([])
const currentTrackId = signal<number | null>(null)

const filteredFiles = computed(() => {
  const search = librarySearch.value.toLowerCase()
  if (!search) return libraryFiles.value
  return libraryFiles.value.filter(
    (f) =>
      f.name.toLowerCase().includes(search) ||
      f.title?.toLowerCase().includes(search)
  )
})

const breadcrumbs = computed(() => {
  const parts = libraryPath.value.split('/').filter(Boolean)
  return [{ name: 'Root', path: '' }, ...parts.map((p, i) => ({
    name: p,
    path: parts.slice(0, i + 1).join('/'),
  }))]
})

function formatTime(seconds: number): string {
  const m = Math.floor(seconds / 60)
  const s = Math.floor(seconds % 60)
  return `${m}:${s.toString().padStart(2, '0')}`
}

function formatUptime(seconds: number): string {
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  return h > 0 ? `${h}h ${m}m` : `${m}m`
}

function enc() { return encodeURIComponent(currentMount.value) }

function resetState() {
  transportError.value = ''
  state.value = 'stopped'
  trackTitle.value = 'No Track'
  trackArtist.value = 'Unknown Artist'
  position.value = 0
  duration.value = 0
  listeners.value = 0
  uptime.value = 0
  volume.value = 80
  metadataEnabled.value = true
  format.value = 'mp3'
  libraryPath.value = ''
  libraryFiles.value = []
  librarySearch.value = ''
  selectedFile.value = null
  activeTab.value = 'playlist'
  playlist.value = []
  queue.value = []
  history.value = []
  currentTrackId.value = null
}

// applyEvent folds one SSE `autodj` payload into the now-playing panel.
// The payload is streamerEventInfo (see server/handlers_api.go): a numeric
// state enum and a flat `song` string, NOT the `currentTrack` object this
// used to reach for. Reading that threw on every tick — and because the
// throw happened after `state` had already been assigned the raw number,
// the transport button compared 1 === 'playing' and stayed on Play
// forever. That is why the Studio "did nothing" when you pressed it.
function applyEvent(evt: AutoDJEvent) {
  state.value = autoDJState(evt.state)
  trackTitle.value = evt.song || 'No Track'
  // The AutoDJ reports "Artist - Title" as one string; split it for the
  // two-line display, and fall back to just the title.
  const dash = evt.song ? evt.song.indexOf(' - ') : -1
  trackArtist.value = dash > 0 ? evt.song.slice(0, dash) : 'Unknown Artist'
  if (dash > 0) trackTitle.value = evt.song.slice(dash + 3)
  position.value = evt.position || 0
  duration.value = evt.duration || 0
  currentTrackId.value = typeof evt.current_id === 'number' && evt.current_id >= 0
    ? evt.current_id
    : null
  if (evt.playlist) playlist.value = evt.playlist
  if (evt.queue) queue.value = evt.queue
}

function fetchLibrary(path: string) {
  libraryPath.value = path
  api.get<FileInfo[]>(`/api/autodj/${enc()}/files?path=${encodeURIComponent(path)}`)
    .then((data) => { libraryFiles.value = data })
    .catch(() => { libraryFiles.value = [] })
}

function fetchPlaylist() {
  api.get<PlaylistItem[]>(`/api/autodj/${enc()}/playlist`)
    .then((data) => { playlist.value = data })
    .catch(() => { playlist.value = [] })
}

function fetchQueue() {
  api.get<PlaylistItem[]>(`/api/autodj/${enc()}/queue`)
    .then((data) => { queue.value = data })
    .catch(() => { queue.value = [] })
}

function fetchAllData() {
  fetchLibrary('')
  fetchPlaylist()
  fetchQueue()
  fetchTransportState()
}

// fetchTransportState seeds the now-playing panel from the REST list.
// Without it the panel sat at "stopped / No Track" until the first SSE
// tick, and a Studio opened against an already-playing AutoDJ showed a
// Play button for an instance that was live.
function fetchTransportState() {
  const mount = currentMount.value
  api.get<AutoDJInstanceRaw[]>('/api/autodj')
    .then((data) => {
      const inst = data.find((d) => d.mount === mount)
      if (!inst) return
      format.value = inst.format || format.value
      applyEvent({
        name: inst.name,
        mount: inst.mount,
        state: inst.state,
        song: inst.current_song,
        start_time: inst.start_time,
        position: inst.position,
        duration: inst.duration,
        current_id: inst.current_id,
        playlist_pos: inst.playlist_pos,
        playlist_len: inst.playlist_len,
        shuffle: inst.shuffle,
        loop: inst.loop,
        // Playlist and queue have their own endpoints; don't let the
        // summary overwrite what those returned.
        queue: null,
        playlist: null,
      })
    })
    .catch(() => { /* the mount list effect already surfaces load failures */ })
}

export function Studio() {
  const sseRef = useRef<ReturnType<typeof createSSE> | null>(null)
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null)

  // Initialize: fetch mounts, pick initial mount
  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const urlMount = params.get('mount') || ''

    api.get<Array<{ mount: string; format: string }>>('/api/autodj')
      .then((data) => {
        const mounts = data.map((d) => d.mount)
        availableMounts.value = mounts

        // Pick mount: URL param > first available > /live
        let picked = urlMount
        if (!picked || !mounts.includes(picked)) {
          picked = mounts[0] || '/live'
        }
        currentMount.value = picked

        // Set format from the matching instance
        const inst = data.find((d) => d.mount === picked)
        if (inst) format.value = inst.format

        loadingMounts.value = false
        // Data fetch + SSE will trigger via the mount change effect
      })
      .catch(() => {
        availableMounts.value = []
        currentMount.value = urlMount || '/live'
        loadingMounts.value = false
      })

    return () => {
      if (sseRef.current) sseRef.current.close()
      if (timerRef.current) clearInterval(timerRef.current)
    }
  }, [])

  // React to mount changes: re-fetch everything, reconnect SSE
  useEffect(() => {
    const mount = currentMount.value
    if (!mount) return

    // Update URL without reload
    const url = new URL(window.location.href)
    url.searchParams.set('mount', mount)
    window.history.replaceState(null, '', url.toString())

    // Reset state for new mount
    resetState()
    fetchAllData()

    // Reconnect SSE for new mount
    if (sseRef.current) sseRef.current.close()
    if (timerRef.current) clearInterval(timerRef.current)

    const sse = createSSE('/admin/events')
    sseRef.current = sse

    sse.on('autodj', (evt: AutoDJEvent) => {
      if (evt.mount !== mount) return
      applyEvent(evt)
    })
    sse.on('stream', (evt: { mount: string; listeners: number }) => {
      if (evt.mount !== mount) return
      listeners.value = evt.listeners
    })

    timerRef.current = setInterval(() => {
      if (state.value === 'playing') uptime.value++
    }, 1000)
  }, [currentMount.value])

  const handleMountChange = useCallback((newMount: string) => {
    currentMount.value = newMount
  }, [])

  const handleTransport = useCallback((action: string) => {
    api.post(`/api/autodj/${enc()}/${action}`)
      .then(() => { fetchTransportState(); fetchQueue() })
      .catch((e) => { transportError.value = (e as Error).message || `${action} failed` })
  }, [])

  const handleVolumeChange = useCallback((v: number) => {
    volume.value = v
    api.post(`/api/autodj/${enc()}/volume`, { volume: v })
  }, [])

  const handleMetadataToggle = useCallback((checked: boolean) => {
    metadataEnabled.value = checked
    api.post(`/api/autodj/${enc()}/metadata`, { enabled: checked })
  }, [])

  const handleAddFile = useCallback((file: FileInfo) => {
    api.post(`/api/autodj/${enc()}/playlist/add`, { path: file.path })
      .then(() => fetchPlaylist())
  }, [])

  const handleAddAll = useCallback(() => {
    const files = libraryFiles.value.filter((f) => !f.is_dir)
    api.post(`/api/autodj/${enc()}/playlist/add`, { paths: files.map((f) => f.path) })
      .then(() => fetchPlaylist())
  }, [])

  const handleRemoveTrack = useCallback((id: number) => {
    api.post(`/api/autodj/${enc()}/playlist/remove`, { id })
      .then(() => fetchPlaylist())
  }, [])

  const handlePlayNext = useCallback((id: number) => {
    api.post(`/api/autodj/${enc()}/playlist/playnext`, { id })
      .then(() => fetchQueue())
      .catch((e) => { transportError.value = (e as Error).message || 'Play next failed' })
  }, [])

  const handleClear = useCallback(() => {
    api.post(`/api/autodj/${enc()}/playlist/clear`)
      .then(() => fetchPlaylist())
  }, [])

  const handleSavePlaylist = useCallback(() => {
    api.post(`/api/autodj/${enc()}/playlist/save`)
  }, [])

  const handleLoadPlaylist = useCallback(() => {
    api.post(`/api/autodj/${enc()}/playlist/load`)
      .then(() => fetchPlaylist())
  }, [])

  const handleFolderClick = useCallback((file: FileInfo) => {
    if (file.is_dir) fetchLibrary(file.path)
  }, [])

  const getFreqData = useCallback(() => null, [])

  const progress = duration.value > 0 ? (position.value / duration.value) * 100 : 0
  const isPlaying = state.value === 'playing'
  const mount = currentMount.value

  const activeList = activeTab.value === 'playlist' ? playlist.value
    : activeTab.value === 'queue' ? queue.value
    : history.value

  // Loading state
  if (loadingMounts.value) {
    return (
      <div class="h-screen flex items-center justify-center bg-surface-base">
        <div class="flex items-center gap-3 text-text-tertiary">
          <EqBars bars={3} />
          <span class="text-sm">Loading...</span>
        </div>
      </div>
    )
  }

  // No AutoDJ instances
  if (availableMounts.value.length === 0) {
    return (
      <div class="h-screen flex flex-col items-center justify-center bg-surface-base gap-4">
        <svg class="w-12 h-12 text-text-tertiary opacity-30" viewBox="0 0 24 24" fill="currentColor">
          <path d="M12 3v10.55c-.59-.34-1.27-.55-2-.55-2.21 0-4 1.79-4 4s1.79 4 4 4 4-1.79 4-4V7h4V3h-6z" />
        </svg>
        <p class="text-text-tertiary text-sm">No AutoDJ instances configured</p>
        <a
          href="/admin/autodj"
          class="text-accent font-mono text-xs tracking-wider hover:underline"
        >
          Create one in AutoDJ
        </a>
      </div>
    )
  }

  return (
    <div class="h-screen flex flex-col bg-surface-base overflow-hidden">
      {/* Top bar */}
      <div class="flex items-center justify-between px-5 py-3 border-b border-border flex-shrink-0">
        <div class="flex items-center gap-4">
          <a
            href="/admin/autodj"
            class="flex items-center gap-1.5 text-sm text-text-secondary hover:text-text-primary transition-colors"
          >
            <svg class="w-4 h-4" viewBox="0 0 24 24" fill="currentColor">
              <path d="M20 11H7.83l5.59-5.59L12 4l-8 8 8 8 1.41-1.41L7.83 13H20v-2z" />
            </svg>
            Back to AutoDJ
          </a>
          <div class="w-px h-5 bg-border" />
          <div class="flex items-center gap-2">
            <span
              class={`w-2 h-2 rounded-full ${isPlaying ? 'bg-live' : 'bg-text-tertiary'}`}
              style={isPlaying ? { animation: 'pulse-glow 2s ease-in-out infinite' } : undefined}
            />
            <select
              value={mount}
              onChange={(e) => handleMountChange((e.target as HTMLSelectElement).value)}
              class="bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-3 py-1.5 font-mono font-bold text-text-primary text-sm focus:border-accent outline-none cursor-pointer"
            >
              {availableMounts.value.map((m) => (
                <option key={m} value={m}>{m}</option>
              ))}
            </select>
            <span class="font-mono text-[10px] text-text-tertiary uppercase">{format.value}</span>
          </div>
        </div>
        <div class="flex items-center gap-4">
          <div class="flex items-center gap-1.5">
            <span class="text-lg font-bold text-accent">{listeners.value}</span>
            <span class="text-[9px] font-mono text-text-tertiary uppercase tracking-wider">
              {listeners.value === 1 ? 'listener' : 'listeners'}
            </span>
          </div>
          <span class="font-mono text-[10px] text-text-tertiary">{formatUptime(uptime.value)}</span>
        </div>
      </div>

      {/* Transport error — a thin persistent bar rather than an overlay,
          so a failed command is visible without hiding the console. */}
      {transportError.value && (
        <div
          role="alert"
          class="flex items-center gap-3 border-b border-danger/30 bg-danger/10 px-5 py-2 text-sm text-danger flex-shrink-0"
        >
          <svg class="w-4 h-4 flex-shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="12" cy="12" r="10" /><line x1="12" y1="8" x2="12" y2="12" /><line x1="12" y1="16" x2="12.01" y2="16" />
          </svg>
          <span class="flex-1">{transportError.value}</span>
          <button
            onClick={() => { transportError.value = '' }}
            class="font-mono text-[10px] tracking-widest uppercase opacity-70 hover:opacity-100"
            aria-label="Dismiss error"
          >
            Dismiss
          </button>
        </div>
      )}

      {/* Main 3-column layout */}
      <div class="flex flex-1 overflow-hidden">
        {/* Column 1: Library */}
        <div class="w-[260px] flex-shrink-0 border-r border-border flex flex-col">
          <div class="p-3 border-b border-border">
            <span class="font-mono text-[9px] tracking-widest text-text-tertiary uppercase">LIBRARY</span>
            <input
              type="text"
              placeholder="Search files..."
              value={librarySearch.value}
              onInput={(e) => { librarySearch.value = (e.target as HTMLInputElement).value }}
              class="mt-2 w-full h-8 px-3 rounded-lg bg-surface-overlay border border-border text-sm text-text-primary placeholder:text-text-tertiary focus:border-accent outline-none transition-colors"
            />
          </div>

          {/* Breadcrumb */}
          <div class="px-3 py-2 border-b border-border overflow-x-auto">
            <div class="flex items-center gap-1 font-mono text-[10px] text-text-tertiary whitespace-nowrap">
              {breadcrumbs.value.map((crumb, i) => (
                <span key={crumb.path} class="flex items-center gap-1">
                  {i > 0 && <span>/</span>}
                  <button
                    onClick={() => fetchLibrary(crumb.path)}
                    class="hover:text-text-primary transition-colors"
                  >
                    {crumb.name}
                  </button>
                </span>
              ))}
            </div>
          </div>

          {/* File list */}
          <div class="flex-1 overflow-y-auto p-2 space-y-0.5">
            {filteredFiles.value.map((file) => (
              <div key={file.path} onClick={() => handleFolderClick(file)}>
                <FileItem
                  file={file}
                  onAdd={() => handleAddFile(file)}
                  active={selectedFile.value === file.path}
                />
              </div>
            ))}
            {filteredFiles.value.length === 0 && (
              <div class="py-8 text-center text-text-tertiary text-xs">No files found</div>
            )}
          </div>

          {/* Footer */}
          <div class="p-3 border-t border-border">
            <button
              onClick={handleAddAll}
              class="w-full h-8 rounded-lg border border-border text-[11px] font-mono text-text-secondary hover:text-text-primary hover:border-border-hover transition-colors uppercase tracking-wider"
            >
              Add All to Playlist
            </button>
          </div>
        </div>

        {/* Column 2: Now Playing */}
        <div class="flex-1 flex flex-col items-center border-r border-border overflow-y-auto">
          <div class="flex flex-col items-center gap-5 py-6 px-4 max-w-md w-full">
            {/* Visualizer */}
            <Visualizer size={140} getFreqData={getFreqData} />

            {/* Track info */}
            <div class="flex flex-col items-center gap-1 w-full text-center">
              <h2 class="text-lg font-bold text-text-primary leading-tight truncate w-full">
                {trackTitle}
              </h2>
              <p class="font-mono text-xs text-text-tertiary truncate w-full">
                {trackArtist}
              </p>
            </div>

            {/* Progress scrubber */}
            <div class="w-full">
              <div class="w-full h-1.5 bg-surface-overlay rounded-full overflow-hidden cursor-pointer">
                <div
                  class="h-full bg-accent rounded-full transition-[width] duration-1000 ease-linear"
                  style={{ width: `${progress}%` }}
                />
              </div>
              <div class="flex justify-between mt-1">
                <span class="font-mono text-[10px] text-text-tertiary">{formatTime(position.value)}</span>
                <span class="font-mono text-[10px] text-text-tertiary">{formatTime(duration.value)}</span>
              </div>
            </div>

            {/* Transport */}
            <TransportControls
              playing={isPlaying}
              onPlay={() => handleTransport('play')}
              onPause={() => handleTransport('pause')}
              onNext={() => handleTransport('next')}
              onPrev={() => handleTransport('prev')}
            />

            {/* Volume */}
            <VolumeKnob value={volume.value} onChange={handleVolumeChange} />

            {/* Metadata toggle */}
            <div class="flex items-center gap-3 pt-2 border-t border-border w-full justify-center">
              <span class="font-mono text-[9px] tracking-widest text-text-tertiary uppercase">METADATA</span>
              <Toggle
                checked={metadataEnabled.value}
                onChange={handleMetadataToggle}
                label="Metadata"
              />
            </div>

            {/* Monitor. The transport above drives what the MOUNT
                broadcasts; this is local playback of that mount so the
                operator can hear what listeners hear. preload="none"
                keeps it from opening a listener connection until the
                operator actually presses play. */}
            <div class="flex flex-col items-center gap-2 pt-3 border-t border-border w-full">
              <span class="font-mono text-[9px] tracking-widest text-text-tertiary uppercase">
                MONITOR
              </span>
              <audio
                key={mount}
                src={mount}
                controls
                preload="none"
                class="w-full h-9"
                onError={() => {
                  transportError.value = `Monitor could not play ${mount}. The mount is only live while the AutoDJ is playing.`
                }}
              />
            </div>
          </div>
        </div>

        {/* Column 3: Playlist/Queue/History */}
        <div class="w-[300px] flex-shrink-0 flex flex-col">
          {/* Tabs */}
          <div class="flex border-b border-border">
            {(['playlist', 'queue', 'history'] as const).map((tab) => (
              <button
                key={tab}
                onClick={() => { activeTab.value = tab; if (tab === 'queue') fetchQueue() }}
                class={`flex-1 py-3 font-mono text-[10px] tracking-widest uppercase transition-colors relative ${
                  activeTab.value === tab
                    ? 'text-text-primary'
                    : 'text-text-tertiary hover:text-text-secondary'
                }`}
              >
                {tab}
                {activeTab.value === tab && (
                  <div class="absolute bottom-0 left-2 right-2 h-0.5 bg-accent rounded-full" />
                )}
              </button>
            ))}
          </div>

          {/* Actions bar */}
          <div class="flex items-center justify-between px-3 py-2 border-b border-border">
            <div class="flex items-center gap-1">
              <button
                onClick={handleSavePlaylist}
                class="h-7 px-2 rounded text-[10px] font-mono text-text-tertiary hover:text-text-secondary transition-colors uppercase tracking-wider"
              >
                Save
              </button>
              <button
                onClick={handleLoadPlaylist}
                class="h-7 px-2 rounded text-[10px] font-mono text-text-tertiary hover:text-text-secondary transition-colors uppercase tracking-wider"
              >
                Load
              </button>
              <button
                onClick={handleClear}
                class="h-7 px-2 rounded text-[10px] font-mono text-text-tertiary hover:text-danger transition-colors uppercase tracking-wider"
              >
                Clear
              </button>
            </div>
            <span class="font-mono text-[10px] text-text-tertiary">
              {activeList.length} {activeList.length === 1 ? 'track' : 'tracks'}
            </span>
          </div>

          {/* Track list */}
          <div class="flex-1 overflow-y-auto p-2 space-y-0.5">
            {activeList.map((item, i) => (
              <PlaylistItemComp
                key={item.id}
                item={item}
                index={i}
                playing={currentTrackId.value === item.id}
                onRemove={() => handleRemoveTrack(item.id)}
                onPlayNext={() => handlePlayNext(item.id)}
              />
            ))}
            {activeList.length === 0 && (
              <div class="py-8 text-center text-text-tertiary text-xs">
                {activeTab.value === 'playlist' ? 'Add tracks from the library' : `No ${activeTab.value} items`}
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
