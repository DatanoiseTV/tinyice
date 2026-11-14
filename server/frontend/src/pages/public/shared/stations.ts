import { useEffect, useMemo, useState } from 'react'
import { createSSE } from '@/lib/sse'
import { page } from '@/lib/api'
import type { LandingData } from '@/types'

/**
 * One public mount. Field meanings follow the Go handlers, not their names:
 * `title` is the current song and `artist` is the stream's own name
 * (visibleStreamList and /events both fill them that way), so here they are
 * called `song` and `name`.
 */
export interface Station {
  mount: string
  song: string
  name: string
  /** Content-Type, only known from the page bootstrap ('' once a mount appears later). */
  format: string
  bitrate: number
  listeners: number
  live: boolean
  hasVideo: boolean
  genre: string
  description: string
  uptime: string
}

/** Shape of one entry in the unnamed /events frame (PublicStreamInfo in handlers_api.go). */
interface PublicEvent {
  mount: string
  name?: string
  listeners?: number
  bitrate?: string | number
  uptime?: string
  genre?: string
  description?: string
  song?: string
  live?: boolean
  has_video?: boolean
}

const DEFAULT_SUBTITLE = 'Live Streaming Server powered by Go'
export const DEFAULT_TITLE = 'TinyIce'

export function pageMeta() {
  const d = page<Partial<LandingData>>()
  const title = d.pageTitle || DEFAULT_TITLE
  const subtitle = d.pageSubtitle && d.pageSubtitle !== DEFAULT_SUBTITLE ? d.pageSubtitle : ''
  return {
    title,
    subtitle,
    customTitle: title !== DEFAULT_TITLE,
    logoUrl: d.branding?.logoUrl ?? null,
    markdown: d.branding?.landingMarkdown ?? '',
    version: d.version ?? '',
  }
}

function seed(): Station[] {
  // The server marshals a nil slice as `null` when nothing is visible.
  const list = page<Partial<LandingData>>().streams ?? []
  return list.map((s) => ({
    mount: s.mount, song: s.title ?? '', name: s.artist ?? '', format: s.format ?? '', bitrate: Number(s.bitrate) || 0,
    listeners: s.listeners ?? 0, live: !!s.live, hasVideo: !!s.has_video, genre: '', description: '', uptime: '',
  }))
}

/** A server tick is 500 ms; four silent seconds means the feed is down. */
const STALE_MS = 4000

/**
 * Public station list: seeded from the page bootstrap, then replaced by the
 * unnamed /events frame, which the server sends twice a second and which
 * contains exactly the visible mounts. A mount missing from the frame is gone
 * (the old UI used per-mount `stream` events and had to invent expiry timers).
 */
export function useStations() {
  const [stations, setStations] = useState<Station[]>(seed)
  const [lastTick, setLastTick] = useState<number>(() => Date.now())
  const [now, setNow] = useState<number>(() => Date.now())

  useEffect(() => {
    const sse = createSSE('/events')
    const off = sse.on('message', (data) => {
      if (!Array.isArray(data)) return
      setLastTick(Date.now())
      setStations((prev) => {
        const known = new Map(prev.map((s) => [s.mount, s]))
        return (data as PublicEvent[]).map((e) => {
          const old = known.get(e.mount)
          const kbps = Number(e.bitrate)
          return {
            mount: e.mount,
            song: e.song ?? '',
            name: e.name ?? old?.name ?? '',
            format: old?.format ?? '',
            // Bitrate can arrive after connect (measured for sources that declare none): keep the last positive value.
            bitrate: kbps > 0 ? kbps : old?.bitrate ?? 0,
            listeners: e.listeners ?? 0,
            live: e.live ?? true,
            hasVideo: !!e.has_video,
            genre: e.genre ?? '',
            description: e.description ?? '',
            uptime: e.uptime ?? '',
          }
        })
      })
    })
    const clock = setInterval(() => setNow(Date.now()), 1000)
    return () => { off(); clearInterval(clock); sse.close() }
  }, [])

  const connected = now - lastTick < STALE_MS
  const sorted = useMemo(() => [...stations].sort((a, b) => Number(b.live) - Number(a.live) || b.listeners - a.listeners || a.mount.localeCompare(b.mount)), [stations])
  return { stations: sorted, connected }
}

/** Absolute URL of the raw audio stream, for players that are not this page. */
export function streamUrl(mount: string): string {
  return `${location.origin}${mount.startsWith('/') ? mount : `/${mount}`}`
}
export const playerPath = (mount: string) => `/player${mount.startsWith('/') ? mount : `/${mount}`}`
