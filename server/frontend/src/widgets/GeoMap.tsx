import { useEffect, useMemo, useRef, useState } from 'react'
import type { Map as MlMap, Marker, Popup } from 'maplibre-gl'
import { cn } from '@/lib/cn'
import { Legend } from '@/ui/console'
import type { GeoCity } from '@/types'

export type { GeoCity }

/**
 * Live listener map. Fed by props only (the page decides where `data` comes
 * from: useLive().geo on the admin app and kiosk).
 *
 * - maplibre-gl is imported on demand, so pages that never mount the map do
 *   not pay for it and a missing WebGL context or blocked CDN degrades to the
 *   schematic view instead of throwing.
 * - Basemap: OpenFreeMap `positron` (light) / `dark` styles, swapped when the
 *   theme changes. Tiles come from the network at runtime. When the style
 *   cannot be fetched (offline kiosk, firewalled network) the widget shows an
 *   equirectangular schematic with the same bubbles plus a ranked city list.
 * - Camera: refits only when the SET of cities changes. Listener-count
 *   changes resize bubbles in place; the old implementation re-flew the camera
 *   on every 500 ms tick and the view never settled.
 */
interface Props {
  data: GeoCity[]
  /** Size and layout of the outer box. Defaults to a fixed 288px height. */
  className?: string
  /** Show the world instead of a mid-ocean fit when cities are spread across continents. */
  worldFallback?: boolean
}

const STYLE = { light: 'https://tiles.openfreemap.org/styles/positron', dark: 'https://tiles.openfreemap.org/styles/dark' }
/** Give the style this long before concluding the network cannot reach the tiles. */
const STYLE_TIMEOUT_MS = 8000

const currentTheme = (): 'light' | 'dark' => (document.documentElement.dataset.theme === 'light' ? 'light' : 'dark')

/** sqrt scaling so a 100-listener city is not 100x the area of a 1-listener one; floor keeps singles hoverable. */
const radiusFor = (n: number) => (n <= 0 ? 0 : Math.max(6, Math.min(36, Math.round(6 + Math.sqrt(n) * 3))))
const cityKey = (c: GeoCity) => `${c.iso}|${c.city ?? ''}|${c.lat.toFixed(3)}|${c.lon.toFixed(3)}`
const placeable = (c: GeoCity) => (c.lat !== 0 || c.lon !== 0) && c.listeners > 0
const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`

function escapeHTML(s: string): string {
  return s.replace(/[&<>"']/g, (ch) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[ch] ?? ch)
}

function popupHTML(c: GeoCity): string {
  const head = c.city
    ? `<strong>${escapeHTML(c.city)}</strong>${c.country ? ` <span class="tg-dim">${escapeHTML(c.country)}</span>` : ''}`
    : `<strong>${escapeHTML(c.country || c.iso)}</strong>`
  const mounts = c.mounts ? Object.entries(c.mounts).map(([m, n]) => `<div class="tg-dim"><span class="tg-mono">${escapeHTML(m)}</span> ${n}</div>`).join('') : ''
  return `${head}<div>${plural(c.listeners, 'listener', 'listeners')}</div>${mounts}`
}

// Popup and control chrome restyled with the app tokens (flat, themed). Injected
// once: maplibre renders these outside React.
const CSS = `
.tinyice-geo-marker{border-radius:50%;cursor:pointer;background:color-mix(in srgb,var(--accent) 38%,transparent);border:1.5px solid var(--accent);transition:width .2s,height .2s}
.tinyice-geo-popup .maplibregl-popup-content{background:var(--raised);color:var(--text);border:1px solid var(--border-strong);border-radius:6px;padding:7px 10px;box-shadow:none;font:12px/1.45 var(--font-sans),system-ui,sans-serif}
.tinyice-geo-popup .tg-dim{color:var(--muted)}
.tinyice-geo-popup .tg-mono{font-family:var(--font-mono),monospace}
.tinyice-geo-popup.maplibregl-popup-anchor-top .maplibregl-popup-tip{border-bottom-color:var(--border-strong)}
.tinyice-geo-popup.maplibregl-popup-anchor-bottom .maplibregl-popup-tip{border-top-color:var(--border-strong)}
.tinyice-geo-popup.maplibregl-popup-anchor-left .maplibregl-popup-tip{border-right-color:var(--border-strong)}
.tinyice-geo-popup.maplibregl-popup-anchor-right .maplibregl-popup-tip{border-left-color:var(--border-strong)}
.tinyice-geo-map .maplibregl-ctrl-group{background:var(--raised);border:1px solid var(--border-strong);box-shadow:none;border-radius:6px}
.tinyice-geo-map .maplibregl-ctrl-group button+button{border-top:1px solid var(--border)}
:root:not([data-theme="light"]) .tinyice-geo-map .maplibregl-ctrl-icon{filter:invert(1)}
.tinyice-geo-map .maplibregl-ctrl.maplibregl-ctrl-attrib,.tinyice-geo-map .maplibregl-ctrl-attrib.maplibregl-compact{background:color-mix(in srgb,var(--surface) 85%,transparent);color:var(--muted);font-size:10px;box-shadow:none;border:1px solid var(--border);border-radius:6px}
.tinyice-geo-map .maplibregl-ctrl-attrib a{color:var(--muted)}
:root:not([data-theme="light"]) .tinyice-geo-map .maplibregl-ctrl-attrib-button{filter:invert(1)}
`
function ensureCSS() {
  if (document.getElementById('tinyice-geo-css')) return
  const el = document.createElement('style')
  el.id = 'tinyice-geo-css'
  el.textContent = CSS
  document.head.appendChild(el)
}

interface Entry { marker: Marker; popup: Popup; el: HTMLDivElement; last: number }
type Phase = 'loading' | 'map' | 'offline'

export function GeoMap({ data, className, worldFallback = true }: Props) {
  const box = useRef<HTMLDivElement>(null)
  const mapRef = useRef<MlMap | null>(null)
  const libRef = useRef<typeof import('maplibre-gl') | null>(null)
  const markers = useRef(new Map<string, Entry>())
  const lastSig = useRef('')
  const [phase, setPhase] = useState<Phase>('loading')

  const cities = useMemo(() => {
    // The server aggregates by (country, city); keep the larger entry if a duplicate slips through.
    const m = new Map<string, GeoCity>()
    for (const c of data) {
      if (!placeable(c)) continue
      const k = cityKey(c)
      const prev = m.get(k)
      if (!prev || c.listeners > prev.listeners) m.set(k, c)
    }
    return [...m.values()]
  }, [data])

  // Create the map once.
  useEffect(() => {
    const host = box.current
    if (!host) return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    let ro: ResizeObserver | undefined
    let mo: MutationObserver | undefined
    const fail = () => { if (!cancelled) { setPhase('offline'); try { mapRef.current?.remove() } catch { /* already gone */ } mapRef.current = null } }

    ;(async () => {
      try {
        ensureCSS()
        const [mod] = await Promise.all([import('maplibre-gl'), import('maplibre-gl/dist/maplibre-gl.css')])
        if (cancelled) return
        const lib = (mod as unknown as { default?: typeof import('maplibre-gl') }).default ?? mod
        libRef.current = lib
        const map = new lib.Map({
          container: host, style: STYLE[currentTheme()], center: [0, 20], zoom: 1.5, minZoom: 1, maxZoom: 8,
          attributionControl: { compact: true }, renderWorldCopies: true,
        })
        mapRef.current = map
        map.addControl(new lib.NavigationControl({ showCompass: false }), 'top-right')
        timer = setTimeout(fail, STYLE_TIMEOUT_MS)
        map.on('load', () => { clearTimeout(timer); if (!cancelled) setPhase('map') })
        // Before the first style load an error means the basemap is unreachable.
        map.on('error', () => { if (!map.isStyleLoaded() && !cancelled) { clearTimeout(timer); fail() } })
        requestAnimationFrame(() => map.resize())
        ro = new ResizeObserver(() => map.resize())
        ro.observe(host)
        mo = new MutationObserver(() => map.setStyle(STYLE[currentTheme()]))
        mo.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
      } catch {
        fail() // no WebGL, or the chunk failed to load
      }
    })()

    const live = markers.current
    return () => {
      cancelled = true
      clearTimeout(timer)
      ro?.disconnect()
      mo?.disconnect()
      for (const e of live.values()) e.marker.remove()
      live.clear()
      mapRef.current?.remove()
      mapRef.current = null
    }
  }, [])

  // Diff markers and refit the camera.
  useEffect(() => {
    const map = mapRef.current
    const lib = libRef.current
    if (phase !== 'map' || !map || !lib) return
    const incoming = new Map(cities.map((c) => [cityKey(c), c]))

    for (const [k, e] of markers.current) if (!incoming.has(k)) { e.marker.remove(); markers.current.delete(k) }
    for (const [k, c] of incoming) {
      const existing = markers.current.get(k)
      const size = `${radiusFor(c.listeners) * 2}px`
      if (existing) {
        if (existing.last !== c.listeners) { existing.el.style.width = size; existing.el.style.height = size; existing.last = c.listeners }
        existing.popup.setHTML(popupHTML(c))
        continue
      }
      const el = document.createElement('div')
      el.className = 'tinyice-geo-marker'
      el.style.width = size
      el.style.height = size
      el.setAttribute('aria-label', `${c.city || c.country || c.iso}: ${plural(c.listeners, 'listener', 'listeners')}`)
      const popup = new lib.Popup({ offset: 12, closeButton: false, className: 'tinyice-geo-popup' }).setHTML(popupHTML(c))
      const marker = new lib.Marker({ element: el, anchor: 'center' }).setLngLat([c.lon, c.lat]).setPopup(popup).addTo(map)
      // Hover on desktop; click (the marker's default) toggles it on touch.
      el.addEventListener('mouseenter', () => { if (!popup.isOpen()) marker.togglePopup() })
      el.addEventListener('mouseleave', () => { if (popup.isOpen()) marker.togglePopup() })
      markers.current.set(k, { marker, popup, el, last: c.listeners })
    }

    const sig = [...incoming.keys()].sort().join(';')
    if (sig === lastSig.current) return
    lastSig.current = sig
    const pts = cities.map((c) => [c.lon, c.lat] as [number, number])
    if (pts.length === 0) return void map.flyTo({ center: [0, 20], zoom: 1.5, duration: 800 })
    if (pts.length === 1) return void map.flyTo({ center: pts[0], zoom: 5, duration: 800 })
    const lons = pts.map((p) => p[0]), lats = pts.map((p) => p[1])
    const [w, e, s, n] = [Math.min(...lons), Math.max(...lons), Math.min(...lats), Math.max(...lats)]
    // A worldwide spread would centre on a meaningless mid-ocean point: show the world.
    if (worldFallback && (e - w > 100 || n - s > 60)) return void map.flyTo({ center: [0, 20], zoom: 1.5, duration: 800 })
    map.fitBounds([[w, s], [e, n]], { padding: 50, maxZoom: 6, duration: 800 })
  }, [cities, phase, worldFallback])

  return (
    <div className={cn('tinyice-geo-map overflow-hidden bg-surface', className ?? 'h-72 w-full rounded-md border border-line')} role="region" aria-label="Listener map">
      <div className="relative size-full">
        {/* maplibre's stylesheet sets position:relative on its container, so size it with h/w, not inset. */}
        <div ref={box} className={cn('h-full w-full', phase === 'offline' && 'hidden')} />
        {phase === 'loading' && <div className="skeleton absolute inset-0 rounded-none" aria-hidden />}
        {phase === 'offline' && <OfflineMap cities={cities} />}
        {phase === 'map' && cities.length === 0 && (
          <div className="pointer-events-none absolute inset-x-0 bottom-3 flex justify-center">
            <span className="rounded-md border border-line bg-surface/90 px-2.5 py-1"><Legend>No listeners yet</Legend></span>
          </div>
        )}
      </div>
    </div>
  )
}

/* ---------------------------- offline schematic ---------------------------- */

// Coarse continent outlines (lon, lat). Schematic only: enough to place a bubble
// on the right landmass when the basemap cannot be fetched.
const LAND: [number, number][][] = [
  [[-168, 66], [-162, 70], [-140, 70], [-125, 70], [-95, 72], [-82, 70], [-62, 60], [-55, 50], [-66, 44], [-76, 35], [-81, 25], [-97, 26], [-97, 19], [-88, 16], [-83, 9], [-78, 8], [-92, 14], [-105, 20], [-112, 29], [-117, 32], [-124, 40], [-125, 49], [-135, 58], [-150, 60], [-165, 60]],
  [[-78, 8], [-72, 12], [-62, 10], [-50, 0], [-35, -6], [-39, -15], [-48, -26], [-58, -38], [-65, -42], [-68, -52], [-74, -50], [-73, -38], [-71, -18], [-81, -5], [-80, 0]],
  [[-55, 60], [-45, 60], [-20, 70], [-20, 82], [-60, 82], [-72, 76]],
  [[-10, 36], [-9, 43], [-2, 48], [5, 52], [10, 57], [20, 70], [40, 68], [70, 73], [100, 77], [140, 72], [170, 70], [180, 66], [160, 58], [140, 52], [130, 42], [122, 38], [122, 30], [110, 20], [106, 10], [100, 2], [98, 16], [92, 22], [88, 22], [78, 8], [72, 20], [58, 25], [50, 30], [35, 30], [28, 37], [10, 38]],
  [[35, 30], [44, 12], [52, 16], [58, 22], [56, 26], [50, 30]],
  [[-17, 21], [-10, 35], [10, 37], [32, 31], [43, 12], [51, 12], [40, -5], [40, -15], [33, -26], [20, -35], [12, -18], [9, 0], [-8, 4], [-17, 14]],
  [[114, -22], [130, -12], [142, -11], [153, -26], [147, -38], [135, -35], [115, -34]],
  [[-5, 50], [2, 52], [-2, 58], [-6, 57]],
  [[130, 32], [141, 36], [142, 44], [136, 36]],
]
const landPath = LAND.map((poly) => `M${poly.map(([lon, lat]) => `${lon + 180} ${90 - lat}`).join('L')}Z`).join('')

function OfflineMap({ cities }: { cities: GeoCity[] }) {
  const ranked = useMemo(() => [...cities].sort((a, b) => b.listeners - a.listeners), [cities])
  const total = ranked.reduce((a, c) => a + c.listeners, 0)
  return (
    <div className="absolute inset-0 flex min-h-0 flex-col sm:flex-row">
      <div className="relative min-h-0 flex-1">
        <svg viewBox="0 0 360 180" preserveAspectRatio="xMidYMid meet" className="absolute inset-0 size-full" role="img" aria-label={`${plural(ranked.length, 'city', 'cities')}, ${plural(total, 'listener', 'listeners')}`}>
          {[-150, -120, -90, -60, -30, 0, 30, 60, 90, 120, 150].map((lon) => <line key={lon} x1={lon + 180} x2={lon + 180} y1="0" y2="180" stroke="var(--border)" strokeWidth="0.4" />)}
          {[-60, -30, 0, 30, 60].map((lat) => <line key={lat} x1="0" x2="360" y1={90 - lat} y2={90 - lat} stroke={lat === 0 ? 'var(--border-strong)' : 'var(--border)'} strokeWidth="0.4" />)}
          <path d={landPath} fill="var(--hover)" stroke="var(--border-strong)" strokeWidth="0.5" strokeLinejoin="round" />
          {ranked.map((c) => (
            <circle key={cityKey(c)} cx={c.lon + 180} cy={90 - c.lat} r={radiusFor(c.listeners) / 5} fill="var(--accent)" fillOpacity="0.4" stroke="var(--accent)" strokeWidth="0.6">
              <title>{`${c.city || c.country || c.iso}: ${plural(c.listeners, 'listener', 'listeners')}`}</title>
            </circle>
          ))}
        </svg>
        <div className="absolute left-3 top-3"><Legend>Basemap unavailable: schematic positions</Legend></div>
      </div>
      <ol className="max-h-full min-w-0 shrink-0 overflow-y-auto border-t border-line sm:w-56 sm:border-l sm:border-t-0" aria-label="Cities by listeners">
        {ranked.length === 0 && <li className="px-3 py-3 text-xs text-faint">No listeners yet.</li>}
        {ranked.slice(0, 12).map((c) => (
          <li key={cityKey(c)} className="flex items-baseline justify-between gap-3 border-b border-line px-3 py-1.5 last:border-b-0">
            <span className="min-w-0 truncate text-xs text-ink">{c.city || c.country || c.iso}{c.city && c.country && <span className="text-faint"> {c.country}</span>}</span>
            <span className="num font-mono text-xs text-accent">{c.listeners}</span>
          </li>
        ))}
      </ol>
    </div>
  )
}
