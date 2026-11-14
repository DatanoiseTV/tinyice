import { useEffect, useMemo, useRef, useState } from 'react'
import { page } from '@/lib/api'
import { useLive } from '@/lib/live'
import { currentTheme, setTheme } from '@/lib/theme'
import { codec, rate, uptime } from '@/lib/format'
import { cn } from '@/lib/cn'
import { GeoMap } from '@/widgets/GeoMap'
import { Sparkline, Bar } from '@/ui/charts'
import { Tally } from '@/ui/console'
import { Icon, Logo } from '@/ui/icons'
import type { StreamEvent, TinyIceBase } from '@/types'

/*
 * Wall display for a studio or lobby TV. Admin session required (the server
 * redirects /kiosk to /login otherwise). No chrome: everything is driven by
 * the shared /admin/events feed through useLive().
 *
 * Legibility from 4 m: the root font size follows the viewport height, so
 * every rem-based size in this tree scales with the screen (a 1080p panel
 * gets ~24px body text, a 720p one ~16px). Keys: F fullscreen, T theme.
 */

const STALE_MS = 6000

/** Etched caption like <Cap>, but sized in rem so it scales with the wall. */
function Cap({ children, className }: { children: React.ReactNode; className?: string }) {
  return <span className={cn('font-mono text-[0.72rem] font-medium uppercase tracking-[0.09em] text-faint', className)}>{children}</span>
}

/** Panel with a rem-scaled header strip (the shared Panel uses fixed pixel sizes). */
function Frame({ legend, lamp, actions, children, className }: { legend: string; lamp: boolean; actions?: React.ReactNode; children: React.ReactNode; className?: string }) {
  return (
    <section className={cn('flex min-h-0 min-w-0 flex-col rounded-lg border border-line bg-surface', className)}>
      <header className="flex h-[2.6rem] shrink-0 items-center gap-3 border-b border-line px-4">
        <span className={cn('size-2 rounded-full', lamp ? 'bg-live tally' : 'bg-warn')} aria-hidden />
        <Cap>{legend}</Cap>
        <div className="ml-auto">{actions}</div>
      </header>
      <div className="min-h-0 flex-1">{children}</div>
    </section>
  )
}

function useWallScale() {
  useEffect(() => {
    const root = document.documentElement
    const prev = root.style.fontSize
    root.style.fontSize = 'max(13px, 2.2vh)'
    return () => { root.style.fontSize = prev }
  }, [])
}

/** HH:MM:SS plus a 25 fps frame counter, written straight to the DOM so the clock never re-renders React. */
function Clock() {
  const hms = useRef<HTMLSpanElement>(null)
  const ff = useRef<HTMLSpanElement>(null)
  const sub = useRef<HTMLDivElement>(null)
  useEffect(() => {
    let raf = 0
    let last = -1
    const two = (n: number) => (n < 10 ? `0${n}` : String(n))
    const date = new Intl.DateTimeFormat(undefined, { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric' })
    const frame = () => {
      raf = requestAnimationFrame(frame)
      const d = new Date()
      const f = Math.floor(d.getMilliseconds() / 40)
      if (f === last) return
      last = f
      if (hms.current) hms.current.textContent = `${two(d.getHours())}:${two(d.getMinutes())}:${two(d.getSeconds())}`
      if (ff.current) ff.current.textContent = `:${two(f)}`
      if (sub.current && f === 0) sub.current.textContent = `${date.format(d)}   UTC ${two(d.getUTCHours())}:${two(d.getUTCMinutes())}:${two(d.getUTCSeconds())}`
    }
    raf = requestAnimationFrame(frame)
    return () => cancelAnimationFrame(raf)
  }, [])
  return (
    <div className="text-right">
      <div className="num font-mono font-medium leading-none text-ink">
        <span ref={hms} className="text-[2.6rem]">00:00:00</span>
        <span ref={ff} className="text-[1.5rem] text-faint">:00</span>
      </div>
      <div ref={sub} className="num mt-1 font-mono text-[0.7rem] text-faint">&nbsp;</div>
    </div>
  )
}

function Kpi({ label, value, unit, series, big, foot }: { label: string; value: string; unit?: string; series?: number[]; big?: boolean; foot?: string }) {
  return (
    <div className="flex min-w-0 flex-col rounded-lg border border-line bg-surface px-4 py-3">
      <Cap>{label}</Cap>
      <div className="mt-1 flex items-baseline gap-2">
        <span className={cn('num truncate font-mono font-medium leading-none tracking-tight', big ? 'text-[4.2rem] text-accent' : 'text-[2.4rem] text-ink')}>{value}</span>
        {unit && <Cap>{unit}</Cap>}
      </div>
      {series && series.length > 1 ? <div className="mt-2"><Sparkline values={series} className="h-7" /></div> : foot ? <div className="mt-2 flex h-7 items-end text-[0.8rem] text-muted">{foot}</div> : null}
    </div>
  )
}

function healthTone(s: StreamEvent) {
  if (s.listeners === 0) return 'live' // AutoDJ legitimately reports 0 when idle
  return s.health >= 80 ? 'live' : s.health >= 50 ? 'warn' : 'danger'
}

function StreamRow({ s }: { s: StreamEvent }) {
  const tone = healthTone(s)
  const fmt = s.is_transcoded && s.source_type ? `${codec(s.source_type)} to ${codec(s.format)}` : codec(s.format)
  return (
    <li className="flex min-h-0 flex-1 flex-col justify-center border-b border-line px-4 last:border-b-0">
      <div className="flex items-center gap-3">
        <Tally on={s.listeners > 0} />
        <span className="min-w-0 flex-1 truncate font-mono text-[1.05rem] font-medium text-ink">{s.mount}</span>
        <span className="num font-mono text-[1.7rem] font-medium leading-none text-accent">{s.listeners}</span>
      </div>
      <div className="mt-1 flex items-center gap-3 pl-5 text-[0.8rem] text-muted">
        <span className="min-w-0 flex-1 truncate">{s.title ? (s.artist ? `${s.title}` : s.title) : <span className="text-faint">No metadata</span>}</span>
        <span className="num shrink-0 font-mono text-[0.7rem] uppercase text-faint">{fmt}{Number(s.bitrate) > 0 ? ` ${s.bitrate}k` : ''}</span>
      </div>
      <Bar value={s.health / 100} tone={`var(--${tone})`} className="mt-1.5 ml-5 h-1" />
    </li>
  )
}

export function Kiosk() {
  useWallScale()
  const { stats, streams, geo, history, connected } = useLive()
  const data = page<Partial<TinyIceBase>>()

  // Staleness: the feed ticks about every second; a long gap means it is down.
  const seen = useRef(Date.now())
  const [stale, setStale] = useState(false)
  useEffect(() => { seen.current = Date.now(); setStale(false) }, [stats])
  useEffect(() => {
    const id = setInterval(() => setStale(Date.now() - seen.current > STALE_MS), 1000)
    return () => clearInterval(id)
  }, [])

  // Hide the pointer after a few idle seconds; keep the panel awake while shown.
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout>
    const show = () => { document.body.style.cursor = ''; clearTimeout(timer); timer = setTimeout(() => { document.body.style.cursor = 'none' }, 3000) }
    show()
    window.addEventListener('mousemove', show)
    let lock: WakeLockSentinel | null = null
    const acquire = () => { navigator.wakeLock?.request('screen').then((l) => { lock = l }).catch(() => {}) }
    acquire()
    const vis = () => { if (document.visibilityState === 'visible') acquire() }
    document.addEventListener('visibilitychange', vis)
    const key = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (e.key === 'f') { if (document.fullscreenElement) void document.exitFullscreen(); else void document.documentElement.requestFullscreen().catch(() => {}) }
      if (e.key === 't') setTheme(currentTheme() === 'dark' ? 'light' : 'dark')
    }
    window.addEventListener('keydown', key)
    return () => {
      clearTimeout(timer); document.body.style.cursor = ''
      window.removeEventListener('mousemove', show); window.removeEventListener('keydown', key)
      document.removeEventListener('visibilitychange', vis); void lock?.release()
    }
  }, [])

  const sorted = useMemo(() => [...streams].sort((a, b) => b.listeners - a.listeners || a.mount.localeCompare(b.mount)), [streams])
  const total = streams.reduce((a, s) => a + s.listeners, 0)
  const cities = geo.filter((c) => (c.lat !== 0 || c.lon !== 0) && c.listeners > 0)
  const geoListeners = cities.reduce((a, c) => a + c.listeners, 0)
  const series = useMemo(() => history.map((h) => h.listeners), [history])
  const out = useMemo(() => history.map((h) => h.out), [history])

  // Fit as many stream rows as the column holds at this scale; summarise the rest.
  const listRef = useRef<HTMLDivElement>(null)
  const [fit, setFit] = useState(8)
  useEffect(() => {
    const el = listRef.current
    if (!el) return
    const measure = () => {
      const rem = parseFloat(getComputedStyle(document.documentElement).fontSize) || 16
      setFit(Math.max(1, Math.floor(el.clientHeight / (rem * 3.6))))
    }
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  const shown = sorted.length > fit ? sorted.slice(0, Math.max(1, fit - 1)) : sorted
  const hidden = sorted.length - shown.length

  return (
    <div className="flex h-dvh w-dvw flex-col gap-3 overflow-hidden bg-bg p-4">
      <header className="flex shrink-0 items-center gap-4 rounded-lg border border-line bg-surface px-5 py-3">
        <Logo size={44} />
        <div className="min-w-0 flex-1">
          <Cap>On air</Cap>
          <h1 className="truncate text-[1.7rem] font-semibold leading-tight tracking-tight text-ink">{data.pageTitle || 'TinyIce'}</h1>
        </div>
        <Clock />
        <span className="ml-2 flex items-center gap-2" role="status" aria-live="polite" title={stale ? 'Event feed lost' : 'Event feed connected'}>
          <span className={cn('size-3 rounded-full', stale || !connected ? 'bg-danger' : 'bg-live tally')} />
          <Cap className={stale || !connected ? 'text-danger' : 'text-live'}>{!connected && !stale ? 'Connecting' : stale ? 'Feed lost' : 'Live'}</Cap>
        </span>
      </header>

      {stale && (
        <div role="alert" className="flex shrink-0 items-center gap-3 rounded-lg border border-danger/50 bg-danger/10 px-5 py-2 text-[1.1rem] text-ink">
          <Icon name="alert" size={26} className="text-danger" />
          The event feed has stopped. The figures below are the last ones received; reconnecting.
        </div>
      )}

      <div className="grid shrink-0 grid-cols-[1.5fr_1fr_1fr_1fr] gap-3">
        <Kpi big label="Listeners" value={String(total)} series={series} />
        <Kpi label="Live streams" value={String(streams.length)} unit={`of ${stats?.streams ?? streams.length}`} foot={`${streams.filter((s) => s.listeners > 0).length} with listeners`} />
        <Kpi label="Outbound" value={rate(stats?.bandwidth_out ?? 0)} series={out} />
        <Kpi label="Uptime" value={uptime(stats?.uptime ?? 0)} foot={stats ? `Since ${new Date(Date.now() - stats.uptime * 1000).toLocaleDateString([], { day: 'numeric', month: 'short' })}` : undefined} />
      </div>

      <div className={cn('grid min-h-0 flex-1 grid-cols-[minmax(0,1fr)_minmax(0,28rem)] gap-3', stale && 'opacity-60')}>
        <Frame legend="Listeners worldwide" lamp={!stale}
          actions={<span className="num font-mono text-[0.8rem] text-muted">{cities.length} {cities.length === 1 ? 'city' : 'cities'}, {geoListeners} {geoListeners === 1 ? 'listener' : 'listeners'}</span>}>
          <div className="relative h-full min-h-0">
            <GeoMap data={geo} className="absolute inset-0 rounded-b-lg" worldFallback />
          </div>
        </Frame>
        <Frame legend="Streams" lamp={!stale}
          actions={<span className="num font-mono text-[0.8rem] text-muted">{sorted.length}</span>}>
          <div ref={listRef} className="flex h-full min-h-0 flex-col">
            {sorted.length === 0 ? (
              <div className="flex flex-1 flex-col items-center justify-center gap-2 px-6 text-center text-muted">
                <Icon name="streams" size={32} className="text-faint" />
                <span className="text-[1.1rem]">{connected ? 'No streams are live' : 'Waiting for the event feed'}</span>
              </div>
            ) : (
              <ul className="flex min-h-0 flex-1 flex-col">
                {shown.map((s) => <StreamRow key={s.mount} s={s} />)}
                {hidden > 0 && (
                  <li className="flex flex-1 items-center justify-between px-4 text-[0.9rem] text-muted">
                    <span>+{hidden} more</span>
                    <span className="num font-mono text-accent">{sorted.slice(shown.length).reduce((a, s) => a + s.listeners, 0)}</span>
                  </li>
                )}
              </ul>
            )}
          </div>
        </Frame>
      </div>
    </div>
  )
}
