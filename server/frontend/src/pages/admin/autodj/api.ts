import { useQuery } from '@tanstack/react-query'
import { api, adminPost, redirectToLogin } from '@/lib/api'
import type { FileInfo, PlaylistItem } from '@/types'

/** One row of GET /api/autodj (autoDJInfo in server/handlers_api_v2.go). Field names are the wire names. */
export interface DjInstance {
  name: string
  mount: string
  /** 0 stopped, 1 playing, 2 paused. */
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
  inject_metadata: boolean
  visible: boolean
  music_dir: string
  format: string
  bitrate: number
  enabled: boolean
  mpd_enabled: boolean
  mpd_port: string
  last_playlist: string
  song_command: string
  song_command_timeout: number
  on_play_command: string
  on_play_command_timeout: number
  queue: PlaylistItem[] | null
}

/** Body of POST /api/autodj and PUT /api/autodj?mount=. PUT overwrites the command fields, so always send them all. */
export interface DjConfig {
  name: string
  mount: string
  music_dir: string
  format: string
  bitrate: number
  loop: boolean
  inject_metadata: boolean
  visible: boolean
  mpd_enabled: boolean
  mpd_port: string
  mpd_password?: string
  song_command: string
  song_command_timeout?: number
  on_play_command: string
  on_play_command_timeout?: number
}

/** GET /api/autodj/browse (browseResponse in handlers_media_browse.go). */
export interface BrowseEntry { name: string; path: string; track_count: number; dir_count: number }
export interface BrowseResponse {
  roots: string[]
  path: string
  parent: string
  track_count: number
  dir_count: number
  entries: BrowseEntry[]
  truncated: boolean
}

export type Transport = 'play' | 'pause' | 'next' | 'prev' | 'shuffle' | 'loop'

/**
 * Mutations go through this instead of lib/api: several endpoints (playlist/save,
 * playlist/load, metadata) answer 200 with an empty body, which api.post tries to
 * parse as JSON and rejects, and the legacy handlers report errors as plain text.
 * The Content-Type is what makes these /api/ requests CSRF-safe server side.
 */
async function send<T = unknown>(path: string, body?: unknown, method = 'POST'): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (res.status === 401) { redirectToLogin(); throw new Error('Unauthorized') }
  const text = await res.text().catch(() => '')
  let data: unknown = undefined
  if (text) { try { data = JSON.parse(text) } catch { data = text } }
  if (!res.ok) {
    const msg = typeof data === 'object' && data && 'error' in data ? String((data as { error: unknown }).error) : typeof data === 'string' ? data.trim() : ''
    throw new Error(msg || res.statusText || `Request failed (${res.status})`)
  }
  return data as T
}

/** The server routes by suffix on /api/autodj/{url-encoded mount}/{action}. */
const base = (mount: string) => `/api/autodj/${encodeURIComponent(mount)}`

export const dj = {
  list: () => api.get<DjInstance[]>('/api/autodj'),
  playlist: (m: string) => api.get<PlaylistItem[]>(`${base(m)}/playlist`),
  queue: (m: string) => api.get<PlaylistItem[]>(`${base(m)}/queue`),
  files: (m: string, path: string) => api.get<FileInfo[]>(`${base(m)}/files?path=${encodeURIComponent(path)}`),
  browse: (path: string) => api.get<BrowseResponse>(`/api/autodj/browse${path ? `?path=${encodeURIComponent(path)}` : ''}`),

  transport: (m: string, a: Transport) => send(`${base(m)}/${a}`),
  /** The unit is explicit: without it the server guesses "> 1 means percent" and reads 1% as full volume. */
  volume: (m: string, percent: number) => send<{ volume: number }>(`${base(m)}/volume`, { volume: percent, unit: 'percent' }),
  /** Explicit state, not a blind toggle: two tabs or a double click would invert the switch. */
  metadata: (m: string, enabled: boolean) => send(`${base(m)}/metadata`, { enabled }),

  add: (m: string, paths: string[]) => send(`${base(m)}/playlist/add`, { paths }),
  remove: (m: string, id: number) => send(`${base(m)}/playlist/remove`, { id }),
  /** The path route only injects the mount into the query string, which reorder ignores: it reads the body. */
  move: (m: string, from: number, to: number) => send('/api/autodj/playlist/reorder', { mount: m, from, to }),
  /** No body on purpose: clear decodes a JSON body first and a body without "mount" shadows the query parameter. */
  clear: (m: string) => send(`${base(m)}/playlist/clear`),
  save: (m: string) => send(`${base(m)}/playlist/save`),
  /** The .pls name is required; without it the server once loaded nothing and remembered "." as the playlist. */
  load: (m: string, file: string) => send(`${base(m)}/playlist/load?file=${encodeURIComponent(file)}`),
  playNextId: (m: string, id: number) => send(`${base(m)}/playlist/playnext`, { id }),
  enqueuePath: (m: string, path: string, front: boolean) => send(`${base(m)}/queue`, { path, front }),
  enqueueId: (m: string, id: number, front: boolean) => send(`${base(m)}/queue`, { id, front }),
  /** Legacy form endpoint: the only way to empty the queue. */
  clearQueue: (m: string) => adminPost('/admin/player/clear-queue', { mount: m }),
  /** Legacy form endpoint: enables or disables the instance (stops the streamer, keeps the config). */
  toggle: (m: string) => adminPost('/admin/autodj/toggle', { mount: m }),

  create: (cfg: DjConfig) => send('/api/autodj', cfg),
  update: (mount: string, cfg: DjConfig) => send(`/api/autodj?mount=${encodeURIComponent(mount)}`, cfg, 'PUT'),
  remove_instance: (mount: string) => send(`/api/autodj?mount=${encodeURIComponent(mount)}`, undefined, 'DELETE'),
}

export const qk = {
  instances: ['dj', 'instances'] as const,
  playlist: (m: string) => ['dj', 'playlist', m] as const,
  queue: (m: string) => ['dj', 'queue', m] as const,
  files: (m: string, p: string) => ['dj', 'files', m, p] as const,
}

/** The REST list seeds the UI before the first SSE frame and keeps config fields (which the event feed lacks) current. */
export function useInstances() {
  return useQuery({ queryKey: qk.instances, queryFn: dj.list, refetchInterval: 10_000 })
}
