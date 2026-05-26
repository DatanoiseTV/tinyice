import { useEffect } from 'preact/hooks'
import { signal } from '@preact/signals'
import { api } from '@/lib/api'
import { createSSE } from '@/lib/sse'
import { EqBars } from '@/components/EqBars'
import type { AutoDJEvent } from '@/types'

// Matches the Go API response from /api/autodj
interface AutoDJInstanceRaw {
  name: string
  mount: string
  format: string
  bitrate: number
  state: number // 0=stopped, 1=playing, 2=paused
  current_song: string
  start_time: number
  duration: number
  playlist_pos: number
  playlist_len: number
  shuffle: boolean
  loop: boolean
  inject_metadata: boolean
  song_command: string
  song_command_timeout: number
  on_play_command: string
  on_play_command_timeout: number
  visible: boolean
  music_dir: string
  enabled: boolean
  mpd_enabled: boolean
  mpd_port: string
  last_playlist: string
  queue: Array<{ id: string; file: string; title: string }> | null
}

interface AutoDJInstance {
  name: string
  mount: string
  format: string
  bitrate: number
  state: 'playing' | 'paused' | 'stopped'
  currentSong: string
  duration: number
  playlistLen: number
  shuffle: boolean
  loop: boolean
  injectMetadata: boolean
  musicDir: string
  songCommand: string
  songCommandTimeout: number
  onPlayCommand: string
  onPlayCommandTimeout: number
  queue: string[]
}

function mapInstance(raw: AutoDJInstanceRaw): AutoDJInstance {
  const stateMap: Record<number, 'playing' | 'paused' | 'stopped'> = { 0: 'stopped', 1: 'playing', 2: 'paused' }
  return {
    name: raw.name,
    mount: raw.mount,
    format: raw.format,
    bitrate: raw.bitrate,
    state: stateMap[raw.state] ?? 'stopped',
    currentSong: raw.current_song || '',
    duration: raw.duration,
    playlistLen: raw.playlist_len,
    shuffle: raw.shuffle,
    loop: raw.loop,
    injectMetadata: raw.inject_metadata,
    musicDir: raw.music_dir,
    songCommand: raw.song_command || '',
    songCommandTimeout: raw.song_command_timeout || 5,
    onPlayCommand: raw.on_play_command || '',
    onPlayCommandTimeout: raw.on_play_command_timeout || 10,
    queue: (raw.queue ?? []).map(q => q.title || q.file),
  }
}

const instances = signal<AutoDJInstance[]>([])
const loading = signal(true)
const showForm = signal(false)
const editingMount = signal<string | null>(null) // null = creating new, string = editing existing mount
const formName = signal('')
const formMount = signal('')
const formMusicDir = signal('')
const formFormat = signal('mp3')
const formBitrate = signal(128)
const formLoop = signal(true)
const formInjectMetadata = signal(true)
const formSongCommand = signal('')
const formSongCommandTimeout = signal(5)
const formOnPlayCommand = signal('')
const formOnPlayCommandTimeout = signal(10)
const saveError = signal('')

function resetForm() {
  saveError.value = ''
  formName.value = ''
  formMount.value = ''
  formMusicDir.value = ''
  formFormat.value = 'mp3'
  formBitrate.value = 128
  formLoop.value = true
  formInjectMetadata.value = true
  formSongCommand.value = ''
  formSongCommandTimeout.value = 5
  formOnPlayCommand.value = ''
  formOnPlayCommandTimeout.value = 10
  editingMount.value = null
}

function openEditForm(inst: AutoDJInstance) {
  formName.value = inst.name
  formMount.value = inst.mount
  formMusicDir.value = inst.musicDir
  formFormat.value = inst.format
  formBitrate.value = inst.bitrate
  formLoop.value = inst.loop
  formInjectMetadata.value = inst.injectMetadata
  formSongCommand.value = inst.songCommand || ''
  formSongCommandTimeout.value = inst.songCommandTimeout || 5
  formOnPlayCommand.value = inst.onPlayCommand || ''
  formOnPlayCommandTimeout.value = inst.onPlayCommandTimeout || 10
  editingMount.value = inst.mount
  showForm.value = true
}

async function saveAutoDJ() {
  saveError.value = ''
  const body = {
    name: formName.value,
    mount: formMount.value,
    music_dir: formMusicDir.value,
    format: formFormat.value,
    bitrate: formBitrate.value,
    loop: formLoop.value,
    inject_metadata: formInjectMetadata.value,
    song_command: formSongCommand.value || undefined,
    song_command_timeout: formSongCommandTimeout.value || undefined,
    on_play_command: formOnPlayCommand.value || undefined,
    on_play_command_timeout: formOnPlayCommandTimeout.value || undefined,
  }
  try {
    if (editingMount.value) {
      // PUT updates in place — server keeps playlist/queue state and
      // reverts cleanly if validation fails, instead of the previous
      // delete-then-recreate which would lose the instance entirely
      // on any 400.
      await api.put(`/api/autodj?mount=${encodeURIComponent(editingMount.value)}`, body)
    } else {
      await api.post('/api/autodj', body)
    }
    showForm.value = false
    resetForm()
    loadAutoDJ()
  } catch (e) {
    saveError.value = (e as Error).message || 'Save failed'
  }
}

async function deleteAutoDJ(mount: string) {
  await api.del(`/api/autodj?mount=${encodeURIComponent(mount)}`)
  loadAutoDJ()
}

async function loadAutoDJ() {
  loading.value = true
  try {
    const raw = await api.get<AutoDJInstanceRaw[]>('/api/autodj')
    instances.value = raw.map(mapInstance)
  } catch {
    instances.value = []
  }
  loading.value = false
}

function formatTime(seconds: number): string {
  const m = Math.floor(seconds / 60)
  const s = Math.floor(seconds % 60)
  return `${m}:${s.toString().padStart(2, '0')}`
}

function InstanceCard({ inst }: { inst: AutoDJInstance }) {
  const isPlaying = inst.state === 'playing'
  const isPaused = inst.state === 'paused'
  const isStopped = inst.state === 'stopped'
  const progress = 0 // Position tracking requires SSE updates

  const handleTransport = (action: string) => {
    api.post(`/api/autodj/${encodeURIComponent(inst.mount)}/${action}`)
  }

  return (
    <div class="bg-surface-raised border border-border rounded-xl p-5 hover:border-border-hover transition-colors">
      {/* Header: status + mount + format */}
      <div class="flex items-center gap-3 mb-4">
        <span
          class={`w-2.5 h-2.5 rounded-full flex-shrink-0 ${
            isPlaying ? 'bg-live' : 'bg-text-tertiary'
          }`}
          style={isPlaying ? { animation: 'pulse-glow 2s ease-in-out infinite' } : undefined}
        />
        <span class="font-mono font-bold text-text-primary text-sm">
          {inst.mount}
        </span>
        <span class="font-mono text-[10px] text-text-tertiary tracking-wider uppercase">
          {inst.format} {inst.bitrate}kbps
        </span>
        {inst.songCommand && (
          <span class="font-mono text-[10px] text-text-tertiary tracking-wider">
            CMD
          </span>
        )}
        <div class="ml-auto flex items-center gap-1.5">
          <span class="text-2xl font-bold text-text-primary leading-none">{inst.playlistLen}</span>
          <span class="text-[9px] font-mono text-text-tertiary uppercase tracking-wider">
            tracks
          </span>
        </div>
      </div>

      {/* Now Playing or Stopped */}
      {isStopped ? (
        <div class="flex items-center justify-between py-4">
          <span class="text-sm text-text-tertiary">
            Stopped &mdash; {inst.playlistLen} tracks
          </span>
          <div class="flex items-center gap-2">
            <button
              onClick={() => handleTransport('play')}
              class="w-10 h-10 rounded-full border border-border flex items-center justify-center text-text-tertiary hover:text-accent hover:border-accent transition-colors"
              aria-label="Play"
            >
              <svg class="w-4 h-4 ml-0.5" viewBox="0 0 24 24" fill="currentColor">
                <path d="M8 5v14l11-7z" />
              </svg>
            </button>
            <button
              onClick={() => openEditForm(inst)}
              class="w-8 h-8 rounded-lg border border-border flex items-center justify-center text-text-secondary hover:text-accent hover:border-accent/30 transition-colors"
              aria-label="Edit AutoDJ"
              title="Edit AutoDJ"
            >
              <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M11 4H4a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-7" />
                <path d="M18.5 2.5a2.121 2.121 0 013 3L12 15l-4 1 1-4 9.5-9.5z" />
              </svg>
            </button>
            <button
              onClick={() => deleteAutoDJ(inst.mount)}
              class="w-8 h-8 rounded-lg border border-border flex items-center justify-center text-danger hover:border-danger/30 transition-colors"
              aria-label="Delete AutoDJ"
              title="Delete AutoDJ"
            >
              <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <polyline points="3 6 5 6 21 6" />
                <path d="M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2" />
              </svg>
            </button>
          </div>
        </div>
      ) : (
        <>
          {/* Track info */}
          <div class="mb-3">
            <div class="text-sm font-medium text-text-primary truncate">
              {inst.currentSong || 'Unknown'}
            </div>
            <div class="text-xs text-text-tertiary truncate">
              {inst.name}
            </div>
          </div>

          {/* Progress bar */}
          <div class="mb-4">
            <div class="w-full h-1 bg-surface-overlay rounded-full overflow-hidden">
              <div
                class="h-full bg-accent rounded-full transition-[width] duration-1000 ease-linear"
                style={{ width: `${progress}%` }}
              />
            </div>
            <div class="flex justify-between mt-1">
              <span class="font-mono text-[10px] text-text-tertiary">{formatTime(0)}</span>
              <span class="font-mono text-[10px] text-text-tertiary">{formatTime(inst.duration)}</span>
            </div>
          </div>

          {/* Transport controls */}
          <div class="flex items-center gap-2">
            <button
              onClick={() => handleTransport('prev')}
              class="w-8 h-8 rounded-full flex items-center justify-center text-text-secondary hover:text-text-primary transition-colors"
              aria-label="Previous"
            >
              <svg class="w-4 h-4" viewBox="0 0 24 24" fill="currentColor">
                <path d="M6 6h2v12H6zm3.5 6l8.5 6V6z" />
              </svg>
            </button>
            <button
              onClick={() => handleTransport(isPlaying ? 'pause' : 'play')}
              class="w-10 h-10 rounded-full bg-accent flex items-center justify-center shadow-[0_0_16px_rgba(255,102,0,0.25)] hover:shadow-[0_0_24px_rgba(255,102,0,0.4)] transition-shadow"
              aria-label={isPlaying ? 'Pause' : 'Play'}
            >
              {isPlaying ? (
                <svg class="w-4 h-4 text-surface-base" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M6 19h4V5H6v14zm8-14v14h4V5h-4z" />
                </svg>
              ) : (
                <svg class="w-4 h-4 text-surface-base ml-0.5" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M8 5v14l11-7z" />
                </svg>
              )}
            </button>
            <button
              onClick={() => handleTransport('next')}
              class="w-8 h-8 rounded-full flex items-center justify-center text-text-secondary hover:text-text-primary transition-colors"
              aria-label="Next"
            >
              <svg class="w-4 h-4" viewBox="0 0 24 24" fill="currentColor">
                <path d="M6 18l8.5-6L6 6v12zm2-8.14L11.03 12 8 14.14V9.86zM16 6h2v12h-2z" />
              </svg>
            </button>

            <div class="w-px h-6 bg-border mx-1" />

            <a
              href={`/admin/studio?mount=${encodeURIComponent(inst.mount)}`}
              class="h-8 px-3 rounded-lg border border-border flex items-center gap-1.5 text-[11px] font-mono text-text-secondary hover:text-text-primary hover:border-border-hover transition-colors"
            >
              <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="currentColor">
                <path d="M3 13h8V3H3v10zm0 8h8v-6H3v6zm10 0h8V11h-8v10zm0-18v6h8V3h-8z" />
              </svg>
              Studio
            </a>

            <button
              onClick={() => openEditForm(inst)}
              class="w-8 h-8 rounded-lg border border-border flex items-center justify-center text-text-secondary hover:text-accent hover:border-accent/30 transition-colors"
              aria-label="Edit AutoDJ"
              title="Edit AutoDJ"
            >
              <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M11 4H4a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-7" />
                <path d="M18.5 2.5a2.121 2.121 0 013 3L12 15l-4 1 1-4 9.5-9.5z" />
              </svg>
            </button>
            <button
              onClick={() => deleteAutoDJ(inst.mount)}
              class="w-8 h-8 rounded-lg border border-border flex items-center justify-center text-danger hover:border-danger/30 transition-colors"
              aria-label="Delete AutoDJ"
              title="Delete AutoDJ"
            >
              <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <polyline points="3 6 5 6 21 6" />
                <path d="M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2" />
              </svg>
            </button>
          </div>
        </>
      )}

      {/* Queue preview strip */}
      {!isStopped && inst.queue.length > 0 && (
        <div class="mt-4 pt-3 border-t border-border">
          <div class="flex items-center gap-2">
            <span class="font-mono text-[9px] tracking-widest text-text-tertiary uppercase flex-shrink-0">
              UP NEXT
            </span>
            <div class="flex items-center gap-1 min-w-0 overflow-hidden">
              {inst.queue.slice(0, 3).map((track, i) => (
                <div key={i} class="flex items-center gap-1 min-w-0">
                  {i > 0 && (
                    <svg class="w-3 h-3 text-text-tertiary flex-shrink-0" viewBox="0 0 24 24" fill="currentColor">
                      <path d="M10 6L8.59 7.41 13.17 12l-4.58 4.59L10 18l6-6z" />
                    </svg>
                  )}
                  <span class="text-[11px] text-text-tertiary truncate">{track}</span>
                </div>
              ))}
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

export function AutoDJ() {
  useEffect(() => {
    loadAutoDJ()

    const sse = createSSE('/events')
    sse.on('autodj', (evt: AutoDJEvent) => {
      instances.value = instances.value.map((inst) =>
        inst.mount === evt.mount
          ? {
              ...inst,
              state: evt.state,
              currentTrack: evt.currentTrack,
              position: evt.position,
              duration: evt.duration,
              queue: evt.queue,
            }
          : inst
      )
    })

    sse.on('stream', () => {
      // Stream events update listener counts — reload data
      loadAutoDJ()
    })

    return () => sse.close()
  }, [])

  return (
    <div class="p-6">
      {/* Header */}
      <div class="flex items-center justify-between mb-6">
        <h1 class="text-xl font-bold text-text-primary font-heading">AutoDJ</h1>
        <button
          onClick={() => { resetForm(); showForm.value = true }}
          class="h-9 px-4 rounded-lg bg-accent text-surface-base text-sm font-medium flex items-center gap-2 hover:shadow-[0_0_20px_rgba(255,102,0,0.3)] transition-shadow"
        >
          <svg class="w-4 h-4" viewBox="0 0 24 24" fill="currentColor">
            <path d="M19 13h-6v6h-2v-6H5v-2h6V5h2v6h6v2z" />
          </svg>
          New AutoDJ
        </button>
      </div>

      {/* Loading */}
      {loading.value && (
        <div class="flex items-center justify-center py-20">
          <div class="flex items-center gap-3 text-text-tertiary">
            <EqBars bars={3} />
            <span class="text-sm">Loading...</span>
          </div>
        </div>
      )}

      {/* Empty state */}
      {!loading.value && instances.value.length === 0 && (
        <div class="flex flex-col items-center justify-center py-20 text-text-tertiary">
          <svg class="w-12 h-12 mb-4 opacity-30" viewBox="0 0 24 24" fill="currentColor">
            <path d="M12 3v10.55c-.59-.34-1.27-.55-2-.55-2.21 0-4 1.79-4 4s1.79 4 4 4 4-1.79 4-4V7h4V3h-6z" />
          </svg>
          <p class="text-sm">No AutoDJ instances configured</p>
        </div>
      )}

      {/* Instance cards */}
      {!loading.value && instances.value.length > 0 && (
        <div class="grid gap-4 grid-cols-1 lg:grid-cols-2">
          {instances.value.map((inst) => (
            <InstanceCard key={inst.mount} inst={inst} />
          ))}
        </div>
      )}

      {/* New AutoDJ Modal */}
      {showForm.value && (
        <div class="fixed inset-0 bg-black/60 backdrop-blur-sm z-50 overflow-y-auto">
          <div class="flex min-h-full items-center justify-center p-4">
          <div class="bg-surface-raised border border-border rounded-xl p-6 w-full max-w-md">
            <h2 class="text-lg font-bold text-text-primary mb-4">{editingMount.value ? 'Edit AutoDJ' : 'New AutoDJ'}</h2>
            <div class="flex flex-col gap-3">
              <div>
                <label class="text-text-secondary text-xs font-mono tracking-wider uppercase mb-1.5 block">NAME</label>
                <input
                  type="text"
                  value={formName.value}
                  onInput={(e) => { formName.value = (e.target as HTMLInputElement).value }}
                  placeholder="My AutoDJ"
                  class="bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none w-full"
                />
              </div>
              <div>
                <label class="text-text-secondary text-xs font-mono tracking-wider uppercase mb-1.5 block">MOUNT</label>
                <input
                  type="text"
                  value={formMount.value}
                  onInput={(e) => { formMount.value = (e.target as HTMLInputElement).value }}
                  placeholder="/autodj"
                  class="bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none w-full"
                />
              </div>
              <div>
                <label class="text-text-secondary text-xs font-mono tracking-wider uppercase mb-1.5 block">MUSIC DIRECTORY</label>
                <input
                  type="text"
                  value={formMusicDir.value}
                  onInput={(e) => { formMusicDir.value = (e.target as HTMLInputElement).value }}
                  placeholder="/path/to/music"
                  class="bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none w-full"
                />
              </div>
              <div class="grid grid-cols-2 gap-3">
                <div>
                  <label class="text-text-secondary text-xs font-mono tracking-wider uppercase mb-1.5 block">FORMAT</label>
                  <select
                    value={formFormat.value}
                    onChange={(e) => { formFormat.value = (e.target as HTMLSelectElement).value }}
                    class="bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none w-full"
                  >
                    <option value="mp3">MP3</option>
                    <option value="opus">Opus</option>
                    <option value="ogg">OGG</option>
                  </select>
                </div>
                <div>
                  <label class="text-text-secondary text-xs font-mono tracking-wider uppercase mb-1.5 block">BITRATE (KBPS)</label>
                  <input
                    type="number"
                    value={formBitrate.value}
                    onInput={(e) => { formBitrate.value = parseInt((e.target as HTMLInputElement).value) || 0 }}
                    class="bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none w-full"
                  />
                </div>
              </div>
              <div class="flex items-center gap-6 mt-1">
                <label class="flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={formLoop.value}
                    onChange={(e) => { formLoop.value = (e.target as HTMLInputElement).checked }}
                    class="accent-accent"
                  />
                  <span class="text-text-secondary text-xs font-mono tracking-wider uppercase">Loop</span>
                </label>
                <label class="flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={formInjectMetadata.value}
                    onChange={(e) => { formInjectMetadata.value = (e.target as HTMLInputElement).checked }}
                    class="accent-accent"
                  />
                  <span class="text-text-secondary text-xs font-mono tracking-wider uppercase">Inject Metadata</span>
                </label>
              </div>
              {/* Divider */}
              <div class="border-t border-border pt-3 mt-1">
                <label class="text-text-secondary text-xs font-mono tracking-wider uppercase mb-1.5 block">
                  EXTERNAL SONG COMMAND
                </label>
                <p class="text-[10px] text-text-tertiary mb-2">
                  Optional script that returns the next song path. Overrides playlist when set. Falls back to playlist on failure.
                </p>
                <input
                  type="text"
                  value={formSongCommand.value}
                  onInput={(e) => { formSongCommand.value = (e.target as HTMLInputElement).value }}
                  placeholder="node /home/radio/getSong.js"
                  class="bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none w-full"
                />
              </div>
              {formSongCommand.value && (
                <div>
                  <label class="text-text-secondary text-xs font-mono tracking-wider uppercase mb-1.5 block">
                    COMMAND TIMEOUT (SECONDS)
                  </label>
                  <input
                    type="number"
                    value={formSongCommandTimeout.value}
                    onInput={(e) => { formSongCommandTimeout.value = parseInt((e.target as HTMLInputElement).value) || 5 }}
                    min={1}
                    max={30}
                    class="bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none w-24"
                  />
                </div>
              )}
              {/* On Play Command */}
              <div class="border-t border-border pt-3 mt-1">
                <label class="text-text-secondary text-xs font-mono tracking-wider uppercase mb-1.5 block">
                  ON PLAY COMMAND
                </label>
                <p class="text-[10px] text-text-tertiary mb-2">
                  Optional script executed when a track starts playing. Receives metadata via environment variables: TINYICE_ARTIST, TINYICE_TITLE, TINYICE_ALBUM, TINYICE_FILE, TINYICE_MOUNT.
                </p>
                <input
                  type="text"
                  value={formOnPlayCommand.value}
                  onInput={(e) => { formOnPlayCommand.value = (e.target as HTMLInputElement).value }}
                  placeholder="/usr/local/bin/notify-tunein.sh"
                  class="bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none w-full"
                />
              </div>
              {formOnPlayCommand.value && (
                <div>
                  <label class="text-text-secondary text-xs font-mono tracking-wider uppercase mb-1.5 block">
                    ON PLAY TIMEOUT (SECONDS)
                  </label>
                  <input
                    type="number"
                    value={formOnPlayCommandTimeout.value}
                    onInput={(e) => { formOnPlayCommandTimeout.value = parseInt((e.target as HTMLInputElement).value) || 10 }}
                    min={1}
                    max={60}
                    class="bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none w-24"
                  />
                </div>
              )}
            </div>
            {saveError.value && (
              <div class="mt-4 text-danger font-mono text-xs">{saveError.value}</div>
            )}
            <div class="flex justify-end gap-2 mt-6">
              <button
                onClick={() => { showForm.value = false; resetForm() }}
                class="border border-border text-text-secondary font-mono text-xs px-5 py-2.5 rounded-lg hover:border-border-hover"
              >
                CANCEL
              </button>
              <button
                onClick={saveAutoDJ}
                class="bg-accent text-surface-base font-mono font-bold text-xs tracking-wider px-5 py-2.5 rounded-lg"
              >
                {editingMount.value ? 'SAVE' : 'CREATE'}
              </button>
            </div>
          </div>
          </div>
        </div>
      )}
    </div>
  )
}
