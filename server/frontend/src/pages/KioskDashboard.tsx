import { useEffect } from 'preact/hooks'
import { signal } from '@preact/signals'
import { createSSE } from '../lib/sse'
import { LiveGeoMap, type GeoCity } from '../components/LiveGeoMap'

// KioskDashboard — full-screen status wall for studio / NOC display.
// Designed for a TV mounted in the control room: no nav, no chrome,
// big numbers, live LTC clock, live map, per-mount status cards
// stacked down the right side.
//
// Authenticated via the same admin session as /admin. Bounce to
// /login on 401 like the rest of the dashboard.
//
// Refresh strategy: 100% SSE-driven. The /admin/events stream
// already pushes stats / per-stream / geo every ~500 ms; we just
// re-render whenever data arrives. Wall clock + LTC tick locally
// at 25 fps (40 ms) on a separate animation timer.

// Pulled from window.__TINYICE__ — set by the Go shell renderer. The
// field names must match BasePageData: this used to read `title` /
// `subtitle`, which the server never injects, so the wall display always
// showed the hardcoded fallback instead of the installation's name.
type PageData = {
  pageTitle?: string
  branding?: { accentColor?: string; logoUrl?: string | null }
  user?: { username?: string; role?: string }
}
const data = (window.__TINYICE__ ?? {}) as Partial<PageData>

// SSE shapes — lightly typed; we accept whatever the server sends.
type StatsEv = {
  listeners?: number
  bandwidth_in?: number
  bandwidth_out?: number
  bytes_in?: number
  bytes_out?: number
  uptime?: number
}
type StreamEv = {
  mount: string
  title?: string
  artist?: string
  format?: string
  bitrate?: number | string
  listeners?: number
  health?: number
  is_transcoded?: boolean
  source_type?: string
  source_bitrate?: string
}
const stats = signal<StatsEv>({})
const streams = signal<Record<string, StreamEv>>({})
const geo = signal<GeoCity[]>([])
const sseUp = signal(false)
const tick = signal(0) // bumped every 40 ms by the local wall-clock timer

// ── helpers ───────────────────────────────────────────────────────────────

function formatBandwidth(bps: number): string {
  if (!bps || !Number.isFinite(bps)) return '0 B/s'
  if (bps < 1024) return `${bps} B/s`
  if (bps < 1024 * 1024) return `${(bps / 1024).toFixed(1)} KB/s`
  return `${(bps / (1024 * 1024)).toFixed(1)} MB/s`
}

function formatUptime(s: number): string {
  if (!s) return '—'
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (d > 0) return `${d}d ${h}h ${m}m`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m`
}

function fmt2(n: number): string {
  return n < 10 ? `0${n}` : `${n}`
}

// LTC clock — 25 fps wall-clock time-of-day display, format
// HH:MM:SS:FF where FF is the frame count (0–24). Same as a
// broadcast LTC generator running in free-run mode locked to
// system time. Updates from the local `tick` signal so a missing
// SSE update doesn't freeze the clock.
function useLtcDisplay() {
  const now = new Date()
  void tick.value // subscribe to the tick signal so we re-render
  const ms = now.getMilliseconds()
  const frame = Math.floor(ms / 40) // 25 fps -> 40 ms per frame
  return {
    hms: `${fmt2(now.getHours())}:${fmt2(now.getMinutes())}:${fmt2(now.getSeconds())}`,
    ff: fmt2(frame),
    utc:
      `${fmt2(now.getUTCHours())}:${fmt2(now.getUTCMinutes())}:${fmt2(now.getUTCSeconds())}`,
    date: now.toLocaleDateString(undefined, {
      weekday: 'short',
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    }),
  }
}

// ── component ────────────────────────────────────────────────────────────

export function KioskDashboard() {
  // SSE wiring — single source of truth for everything live on the page.
  useEffect(() => {
    const sse = createSSE('/admin/events')
    const offStats = sse.on('stats', (d: StatsEv) => {
      stats.value = d
      sseUp.value = true
    })
    const offStream = sse.on('stream', (d: StreamEv) => {
      streams.value = { ...streams.value, [d.mount]: d }
    })
    const offGeo = sse.on('geo', (d: GeoCity[]) => {
      geo.value = Array.isArray(d) ? d : []
    })
    return () => {
      offStats()
      offStream()
      offGeo()
      sse.close()
    }
  }, [])

  // Local wall-clock tick (LTC frame counter). Animates at 25 fps so
  // the FF digits look continuous; cheap because we only signal a
  // counter, not full state.
  useEffect(() => {
    const id = window.setInterval(() => {
      tick.value = tick.value + 1
    }, 40)
    return () => window.clearInterval(id)
  }, [])

  const ltc = useLtcDisplay()
  const streamList = Object.values(streams.value).sort((a, b) => a.mount.localeCompare(b.mount))
  const totalListeners = streamList.reduce((acc, s) => acc + (s.listeners ?? 0), 0)
  const liveMounts = streamList.filter((s) => (s.listeners ?? 0) > 0 || (s.health ?? 0) > 0).length

  return (
    <div class="h-screen w-screen overflow-hidden bg-surface-base text-text-primary flex flex-col">
      {/* Top bar — station name, big LTC clock, KPIs. Fixed height,
          single row, content tightened so it fits a 720p TV without
          wrapping. */}
      <div class="flex items-center gap-4 px-5 py-2 border-b border-border bg-surface-raised shrink-0">
        <div class="flex items-baseline gap-2 min-w-0">
          <span class="font-mono text-[9px] tracking-[3px] uppercase text-text-tertiary">
            On Air
          </span>
          <span class="text-lg font-bold leading-none truncate">
            {data.pageTitle || 'TinyIce'}
          </span>
        </div>

        <div class="flex-1 flex items-baseline justify-center gap-4 font-mono tabular-nums">
          <span class="text-4xl font-bold tracking-wider text-accent leading-none">
            {ltc.hms}
            <span class="text-2xl text-text-secondary ml-1">:{ltc.ff}</span>
          </span>
          <div class="flex flex-col leading-none">
            <span class="text-[8px] tracking-[3px] uppercase text-text-tertiary">UTC</span>
            <span class="text-base text-text-secondary mt-0.5">{ltc.utc}</span>
          </div>
        </div>

        <div class="flex gap-4 font-mono items-baseline">
          <Kpi label="Listeners" value={String(totalListeners)} accent />
          <Kpi label="Mounts" value={String(liveMounts)} />
          <Kpi
            label="Out"
            value={formatBandwidth(stats.value.bandwidth_out ?? stats.value.bytes_out ?? 0)}
            small
          />
          <Kpi label="Uptime" value={formatUptime(stats.value.uptime ?? 0)} small />
          <span
            class="w-2 h-2 rounded-full self-center"
            title={sseUp.value ? 'SSE live' : 'SSE down'}
            style={{
              backgroundColor: sseUp.value ? 'var(--color-live)' : 'var(--color-danger)',
              animation: sseUp.value ? 'pulse-glow 2s ease-in-out infinite' : 'none',
            }}
          />
        </div>
      </div>

      {/* Main flex row — map fills all remaining space; stream
          column on the right is fixed-width with compact rows that
          fit 8+ mounts on 720p without scrolling. No bottom bar —
          attribution sits inside the map's footer overlay. */}
      <div class="flex-1 flex gap-3 p-3 min-h-0 min-w-0">
        <div class="flex-1 rounded-lg border border-border bg-surface-raised overflow-hidden flex flex-col min-h-0 min-w-0">
          <div class="flex items-center justify-between px-3 py-1.5 border-b border-border shrink-0">
            <span class="font-mono text-[10px] tracking-widest uppercase text-text-tertiary">
              Listeners worldwide
            </span>
            <span class="font-mono text-[10px] text-text-tertiary tabular-nums">
              {geo.value.length} {geo.value.length === 1 ? 'city' : 'cities'} · {totalListeners} {totalListeners === 1 ? 'listener' : 'listeners'}
            </span>
          </div>
          <LiveGeoMap data={geo.value} className="flex-1 min-h-0" worldFallback={true} />
        </div>

        <div class="w-[320px] shrink-0 flex flex-col min-h-0 gap-2">
          <div class="rounded-lg border border-border bg-surface-raised flex flex-col min-h-0 overflow-hidden flex-1">
            <div class="flex items-center justify-between px-3 py-1.5 border-b border-border shrink-0">
              <span class="font-mono text-[10px] tracking-widest uppercase text-text-tertiary">
                Streams
              </span>
              <span class="font-mono text-[10px] text-text-tertiary tabular-nums">
                {streamList.length}
              </span>
            </div>
            <div class="flex-1 min-h-0 overflow-y-auto">
              {streamList.length === 0 ? (
                <div class="p-4 text-center text-text-tertiary font-mono text-xs">
                  waiting for streams…
                </div>
              ) : (
                streamList.map((s) => <StreamRow key={s.mount} s={s} />)
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

// Kpi — compact top-bar tile. accent=true paints the value in the
// brand orange; small=true makes the value smaller (used for less-
// important metrics like bandwidth and uptime).
function Kpi({
  label, value, accent = false, small = false,
}: { label: string; value: string; accent?: boolean; small?: boolean }) {
  return (
    <div class="flex flex-col items-end leading-none">
      <span class="text-[8px] tracking-[3px] uppercase text-text-tertiary">{label}</span>
      <span
        class={`mt-0.5 font-bold tabular-nums ${
          small ? 'text-base' : 'text-2xl'
        } ${accent ? 'text-accent' : 'text-text-primary'}`}
      >
        {value}
      </span>
    </div>
  )
}

// StreamRow — compact two-line per-mount summary that fits ~10 mounts
// on a 720p screen without overflow. Top line: live dot + mount +
// big listener count. Bottom line: format chip ("OPUS 160k → MP3
// 320k" for transcoded) + thin health bar. Now-playing text is
// hidden in the kiosk to keep height tight; the operator-facing
// /admin view keeps the full card layout.
function StreamRow({ s }: { s: StreamEv }) {
  const live = (s.listeners ?? 0) > 0
  const health = s.health ?? 0
  return (
    <div class="border-b border-border last:border-b-0 px-3 py-2 hover:bg-surface-hover transition-colors">
      <div class="flex items-baseline justify-between gap-2">
        <div class="flex items-center gap-2 min-w-0">
          <span
            class="w-1.5 h-1.5 rounded-full shrink-0"
            style={{
              backgroundColor: live ? 'var(--color-live)' : 'var(--color-text-tertiary)',
              animation: live ? 'pulse-glow 2s ease-in-out infinite' : 'none',
            }}
          />
          <span class="font-mono font-bold text-sm text-text-primary truncate">
            {s.mount}
          </span>
        </div>
        <span class="font-mono text-xl font-bold text-accent tabular-nums leading-none">
          {s.listeners ?? 0}
        </span>
      </div>
      <div class="flex items-center gap-2 mt-1">
        <div class="flex items-center gap-1 font-mono text-[10px] min-w-0 truncate">
          {s.is_transcoded && s.source_type ? (
            <>
              <span class="text-text-tertiary uppercase">{formatChip(s.source_type)}</span>
              {s.source_bitrate && <span class="text-text-tertiary">{s.source_bitrate}k</span>}
              <span class="text-text-tertiary">→</span>
              <span class="text-text-secondary uppercase">{formatChip(s.format ?? '')}</span>
              {Number(s.bitrate) > 0 && <span class="text-text-tertiary">{s.bitrate}k</span>}
            </>
          ) : (
            <>
              <span class="text-text-secondary uppercase">{formatChip(s.format ?? '')}</span>
              {Number(s.bitrate) > 0 && <span class="text-text-tertiary">{s.bitrate}k</span>}
            </>
          )}
        </div>
        <div class="flex-1 h-1 rounded-full bg-surface-overlay overflow-hidden">
          <div
            class="h-full rounded-full transition-all duration-500"
            style={{
              width: `${Math.min(100, Math.max(0, health))}%`,
              backgroundColor:
                health >= 80
                  ? 'var(--color-live)'
                  : health >= 50
                    ? 'var(--color-accent)'
                    : 'var(--color-danger)',
            }}
          />
        </div>
      </div>
    </div>
  )
}

function formatChip(ct: string): string {
  const lower = (ct || '').toLowerCase()
  if (lower.includes('mpeg') || lower.includes('mp3')) return 'MP3'
  if (lower.includes('opus')) return 'OPUS'
  if (lower.includes('ogg')) return 'OGG'
  if (lower.includes('aac')) return 'AAC'
  if (lower.includes('flac')) return 'FLAC'
  if (lower.includes('vorbis')) return 'VORBIS'
  if (lower.startsWith('audio/')) return lower.slice(6).toUpperCase()
  return lower.toUpperCase() || '—'
}
