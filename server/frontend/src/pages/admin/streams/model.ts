import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, page } from '@/lib/api'
import { useLive } from '@/lib/live'
import type { StreamEvent } from '@/types'
import { real } from '@/lib/format'

/** GET /api/streams row, exactly as the server sends it (apiGetStreams). */
export interface ApiMount {
  mount: string
  content_type: string
  bitrate: string
  listeners: number
  source_ip: string
  visible: boolean
  enabled: boolean
  health: number
  uptime: string
  current_song: string
  name: string
  has_video: boolean
  video_width?: number
  video_height?: number
  video_fps?: number
  video_gop?: number
  video_kbps?: number
  /** Not sent today; shown when the backend starts exposing the configured fallback. */
  fallback?: string
}

/** The SSE `stream` event carries more than the shared StreamEvent type declares. */
type LiveStream = StreamEvent & { song?: string; ip?: string; type?: string; uptime?: string; name?: string }

export type MountStatus = 'live' | 'stalled' | 'offline' | 'disabled'

export interface Mount extends ApiMount {
  status: MountStatus
  /** Resolved now-playing text, empty when the source sends none. */
  song: string
  codecType: string
  kbps: number
  isLive: boolean
}

/**
 * Green means "this mount has a live source". That is `source_ip`, which is a
 * source descriptor rather than strictly an address: icecast and RTMP record
 * the peer, relays use "relay-pull", WebRTC "webrtc-source" and the AutoDJ
 * "autodj". The AutoDJ was the one ingest that never set it, so a playing
 * AutoDJ showed a permanently grey dot (#56). The live SSE feed carries one
 * `stream` event per live mount, so its presence counts as a source too.
 *
 * `health` only refines this when it is meaningful. Encoders skip work while a
 * mount has no listeners, so an idle-but-playing AutoDJ legitimately reports
 * health 0. Treating that as offline would only move the false negative
 * elsewhere: stalled is reserved for a mount that has listeners waiting and
 * still is not delivering.
 */
export function statusOf(enabled: boolean, hasSource: boolean, listeners: number, health: number): MountStatus {
  if (!enabled) return 'disabled'
  if (!hasSource) return 'offline'
  return listeners > 0 && health < 50 ? 'stalled' : 'live'
}

export function sourceLabel(ip: string): string {
  switch (ip) {
    case '': return 'No source'
    case 'autodj': return 'AutoDJ'
    case 'relay-pull': return 'Relay pull'
    case 'webrtc-source': return 'WebRTC'
    case 'transcode': return 'Transcoder'
    default: return ip
  }
}

export function composeSong(artist?: string, title?: string): string {
  if (!title) return ''
  return artist ? `${artist} - ${title}` : title
}

function merge(api: ApiMount, ev?: LiveStream): Mount {
  const hasSource = !!api.source_ip || !!ev
  const listeners = ev ? ev.listeners : api.listeners
  const health = ev ? ev.health : api.health
  const song = real(ev?.song) || real(api.current_song) || composeSong(real(ev?.artist), real(ev?.title))
  const kbps = Number(ev?.bitrate ?? api.bitrate) || 0
  return {
    ...api,
    source_ip: api.source_ip || ev?.ip || '',
    listeners, health,
    uptime: ev?.uptime || api.uptime,
    has_video: api.has_video || !!ev?.has_video,
    status: statusOf(api.enabled, hasSource, listeners, health),
    song,
    codecType: ev?.type || ev?.format || api.content_type,
    kbps,
    isLive: api.enabled && hasSource,
  }
}

export const STREAMS_KEY = ['admin', 'streams'] as const

export interface Sample { t: number; v: number }

/**
 * Mounts = configured list (polled, includes offline ones) overlaid with the
 * shared live feed, so listener counts move every second while the slower poll
 * keeps config (enabled/visible) and offline mounts honest. Also records a
 * per-mount listener history for the sparklines, one sample per stats tick.
 */
export function useMounts() {
  const { streams, stats } = useLive()
  const qc = useQueryClient()
  const q = useQuery({ queryKey: STREAMS_KEY, queryFn: () => api.get<ApiMount[]>('/api/streams'), refetchInterval: 5000, staleTime: 0 })
  const mounts = useMemo<Mount[]>(() => {
    const byMount = new Map(streams.map((s) => [s.mount, s as LiveStream]))
    const list = (q.data ?? []).map((m) => merge(m, byMount.get(m.mount)))
    // A live mount the poll has not caught yet (new source) still deserves a row.
    for (const s of streams) {
      if (!list.some((m) => m.mount === s.mount) && !s.mount.endsWith('/video')) {
        list.push(merge({ mount: s.mount, content_type: s.format, bitrate: String(s.bitrate), listeners: s.listeners, source_ip: '', visible: true, enabled: true, health: s.health, uptime: '', current_song: '', name: '', has_video: !!s.has_video }, s as LiveStream))
      }
    }
    // On-air first, then waiting, then disabled; alphabetical within a group so rows do not reorder as listener counts move.
    const rank = (m: Mount) => (m.isLive ? 0 : m.status === 'offline' ? 1 : 2)
    return list.sort((a, b) => rank(a) - rank(b) || a.mount.localeCompare(b.mount))
  }, [q.data, streams])

  const hist = useRef(new Map<string, Sample[]>())
  const latest = useRef(mounts)
  latest.current = mounts
  const [, setTick] = useState(0)
  useEffect(() => {
    if (!stats) return
    const now = Date.now()
    const seen = new Set<string>()
    for (const m of latest.current) {
      seen.add(m.mount)
      const arr = hist.current.get(m.mount) ?? []
      arr.push({ t: now, v: m.listeners })
      if (arr.length > 300) arr.shift()
      hist.current.set(m.mount, arr)
    }
    for (const k of hist.current.keys()) if (!seen.has(k)) hist.current.delete(k)
    setTick((n) => n + 1)
  }, [stats])

  return {
    mounts,
    history: (mount: string) => [...(hist.current.get(mount) ?? [])],
    loading: q.isLoading,
    error: q.error as Error | null,
    refresh: () => qc.invalidateQueries({ queryKey: STREAMS_KEY }),
    refetch: q.refetch,
  }
}

export const isSuperadmin = () => page().user?.role === 'superadmin'

/** Absolute public URL for a path on this server. */
export const urlFor = (path: string) => `${window.location.origin}${path}`

export function codecKind(m: Pick<Mount, 'codecType'>): 'mp3' | 'opus' | 'aac' | 'other' {
  const t = (m.codecType || '').toLowerCase()
  if (t.includes('mpeg') || t.includes('mp3')) return 'mp3'
  if (t.includes('opus')) return 'opus'
  if (t.includes('aac')) return 'aac'
  return 'other'
}

/** Pull `<message>` out of an Icecast-style XML reply so the toast reads as text, not markup. */
export function xmlMessage(e: unknown): string {
  const raw = (e as Error)?.message ?? ''
  return /<message>([^<]*)<\/message>/.exec(raw)?.[1] ?? raw
}
