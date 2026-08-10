// Data injected by Go server into window.__TINYICE__
export interface TinyIceBase {
  csrfToken: string
  version: string
  pageTitle: string
  pageSubtitle: string
  branding: {
    logoUrl: string | null
    accentColor: string
    landingMarkdown: string
  }
}

export interface PlayerData extends TinyIceBase {
  mount: string
  title: string
  artist: string
  format: 'mp3' | 'opus'
  bitrate: number
  listeners: number
  hasWebRTC: boolean
  // hasVideo is true when the mount has a companion /video sub-mount —
  // the player then swaps its <audio> for a <video> bound to the HLS
  // playlist so the user sees picture + audio together.
  hasVideo?: boolean
}

export interface AdminData extends TinyIceBase {
  user: { username: string; role: 'superadmin' | 'admin' }
  mounts: string[]
}

export interface LandingData extends TinyIceBase {
  streams: StreamInfo[]
}

export interface StreamInfo {
  mount: string
  title: string
  artist: string
  format: string
  bitrate: number
  listeners: number
  live: boolean
  has_video?: boolean
}

// SSE Events
export interface StatsEvent {
  listeners: number
  streams: number
  bandwidth: number
  bandwidth_in: number
  bandwidth_out: number
  uptime: number
  goroutines: number
  memory: number
  gc: number
}

export interface StreamEvent {
  mount: string
  title: string
  artist: string
  format: string
  bitrate: number | string
  listeners: number
  viewers?: number
  health: number
  is_transcoded?: boolean
  // For transcoded outputs: the source mount + its format/bitrate
  // so the dashboard can render "<src-format> → <out-format>".
  source_mount?: string
  source_type?: string
  source_bitrate?: string
  video_width?: number
  video_height?: number
  video_fps?: number
  video_gop?: number
  video_kbps?: number
}

// AutoDJState mirrors relay.StreamerState.
export const AUTODJ_STOPPED = 0
export const AUTODJ_PLAYING = 1
export const AUTODJ_PAUSED = 2

export type AutoDJState = 'playing' | 'paused' | 'stopped'

export function autoDJState(raw: number | undefined): AutoDJState {
  switch (raw) {
    case AUTODJ_PLAYING:
      return 'playing'
    case AUTODJ_PAUSED:
      return 'paused'
    default:
      return 'stopped'
  }
}

// AutoDJEvent is the payload of the SSE `autodj` event. It mirrors
// streamerEventInfo in server/handlers_api.go field for field — this type
// previously described a shape the server never sent (a `currentTrack`
// object and a string state), so every consumer read undefined and threw.
export interface AutoDJEvent {
  name: string
  mount: string
  /** 0 = stopped, 1 = playing, 2 = paused — use autoDJState() to map. */
  state: number
  song: string
  start_time: number
  /** Seconds elapsed in the current track; 0 unless playing. */
  position: number
  /** Track length in seconds, or 0 when the input format doesn't expose one. */
  duration: number
  /** Playlist id of the track actually playing; -1 for queue / command tracks. */
  current_id: number
  playlist_pos: number
  playlist_len: number
  shuffle: boolean
  loop: boolean
  queue: PlaylistItem[] | null
  playlist: PlaylistItem[] | null
}

// API types

// PlaylistItem mirrors relay.PlaylistItem. `id` is -1 for queue entries,
// which have no stable playlist identity.
export interface PlaylistItem {
  id: number
  path: string
  title: string
}

// FileInfo mirrors the fileEntry struct in apiGetFiles. It is snake_case
// on the wire; the camelCase version this used to declare meant `isDir`
// read undefined, so directories rendered as tracks and clicking one did
// nothing. There is no per-file artist/duration/bitrate — the endpoint
// only stats names and reads the title tag.
export interface FileInfo {
  name: string
  title: string
  is_dir: boolean
  /** Path relative to the AutoDJ music directory. */
  path: string
  abs_path: string
  is_pls: boolean
}

declare global {
  interface Window {
    __TINYICE__: TinyIceBase | PlayerData | AdminData | LandingData
  }
}
