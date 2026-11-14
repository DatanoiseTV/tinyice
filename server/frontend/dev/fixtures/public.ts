import type { Fixture, MockRoute } from '../types'
import { state } from '../state'

/*
 * Public listener pages: player, embed, kiosk.
 *
 * Real shapes (server/handlers_public.go, handlers_api.go handlePublicEvents):
 *   player / embed page data: mount, title (= current song), artist (= stream
 *   NAME, not the performer), format (content type), bitrate (number), listeners,
 *   hasWebRTC, hasVideo, hasHLS.
 *   GET /events: unnamed `data:` frame of visible mounts, then one named
 *   `stream` event per mount: {mount, title, artist, format: "", bitrate: "128",
 *   listeners, viewers, live, has_video, video_*}.
 *   /admin/events `geo`: [{iso, country, city, lat, lon, listeners, mounts}].
 */

const SONGS: Record<string, string[]> = {
  '/radio': ['Miles Davis - Blue in Green', 'John Coltrane - Naima', 'Bill Evans - Peace Piece', 'Nina Simone - Naive Song'],
  '/live': ['Morning Show', 'DJ Orbit - Opening Set'],
  '/jazz': ['Dave Brubeck - Take Five', 'Charles Mingus - Goodbye Pork Pie Hat'],
  '/talk': ['Evening Briefing'],
}
const NAMES: Record<string, string> = { '/radio': 'Radio Orbit', '/live': 'Orbit Live', '/jazz': 'Orbit Jazz', '/talk': 'Orbit Talk' }

/** Tunable from a test: GET /api/__mock/public?live=0&rotate=0&song=... */
const knob = { live: true, rotate: true, song: '' }

const songFor = (mount: string, n: number) => {
  if (mount === '/radio' && knob.song) return knob.song
  const list = SONGS[mount] ?? ['']
  return knob.rotate ? list[Math.floor(n / 25) % list.length] : list[0]
}

let tick = 0
function streamEvents(): string {
  tick++
  const frames: string[] = []
  const visible = state.sources.map((s) => ({
    mount: s.mount, name: NAMES[s.mount] ?? s.mount, listeners: s.base, bitrate: String(s.bitrate), uptime: '3h 12m',
    genre: 'Jazz', description: '', song: songFor(s.mount, tick), live: knob.live, has_video: !!s.has_video,
  }))
  frames.push(`data: ${JSON.stringify(visible)}\n\n`)
  for (const s of state.sources) {
    const listeners = Math.max(0, Math.round(s.base + Math.sin(tick / 9) * s.base * 0.08))
    frames.push(`event: stream\ndata: ${JSON.stringify({
      mount: s.mount, title: songFor(s.mount, tick), artist: NAMES[s.mount] ?? s.mount, format: '', bitrate: String(s.bitrate),
      listeners, viewers: s.has_video ? listeners : 0, live: knob.live, has_video: !!s.has_video,
      ...(s.has_video ? { video_width: 1280, video_height: 720, video_fps: 30, video_gop: 2, video_kbps: 2400 } : {}),
    })}\n\n`)
  }
  return `retry: 1000\n\n${frames.join('')}`
}

// A fixture cannot hold a connection open, so the feed answers with one burst
// and EventSource reconnects after `retry`: close enough to the 500 ms tick.
// site.ts (landing/explore) mocks the same path with an unnamed frame only;
// requests from the player and embed get the full feed, everything else is
// delegated to it so those pages keep working.
const feed: MockRoute = {
  method: 'GET', path: '/events',
  handle: async (ctx) => {
    const ref = String(ctx.req.headers.referer ?? '')
    if (!/\/(player|embed)\//.test(ref)) {
      try {
        const site = (await import('./site')) as { default?: MockRoute[] }
        const r = site.default?.find((x) => x.path === '/events')
        if (r) return r.handle(ctx)
      } catch { /* fall through to the full feed */ }
    }
    return { status: 200, headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' }, body: streamEvents() }
  },
}

const CITIES = [
  { iso: 'DE', country: 'Germany', city: 'Berlin', lat: 52.52, lon: 13.4, base: 38 },
  { iso: 'US', country: 'United States', city: 'New York', lat: 40.71, lon: -74.0, base: 52 },
  { iso: 'US', country: 'United States', city: 'San Francisco', lat: 37.77, lon: -122.42, base: 21 },
  { iso: 'GB', country: 'United Kingdom', city: 'London', lat: 51.51, lon: -0.13, base: 44 },
  { iso: 'JP', country: 'Japan', city: 'Tokyo', lat: 35.68, lon: 139.69, base: 29 },
  { iso: 'BR', country: 'Brazil', city: 'Sao Paulo', lat: -23.55, lon: -46.63, base: 17 },
  { iso: 'AU', country: 'Australia', city: 'Sydney', lat: -33.87, lon: 151.21, base: 12 },
  { iso: 'IN', country: 'India', city: 'Mumbai', lat: 19.08, lon: 72.88, base: 9 },
  { iso: 'ZA', country: 'South Africa', city: 'Cape Town', lat: -33.92, lon: 18.42, base: 4 },
  { iso: 'FR', country: 'France', city: 'Paris', lat: 48.86, lon: 2.35, base: 26 },
  { iso: 'CA', country: 'Canada', city: 'Toronto', lat: 43.65, lon: -79.38, base: 14 },
  { iso: 'SE', country: 'Sweden', city: 'Stockholm', lat: 59.33, lon: 18.07, base: 7 },
]

export const geo = (n: number) =>
  CITIES.map((c) => ({
    iso: c.iso, country: c.country, city: c.city, lat: c.lat, lon: c.lon,
    listeners: Math.max(1, Math.round(c.base + Math.sin(n / 11 + c.lat) * c.base * 0.1)),
    mounts: { '/radio': Math.ceil(c.base * 0.6), '/live': Math.floor(c.base * 0.4) },
  }))

const fixture: Fixture = {
  default: [
    feed,
    {
      method: 'GET', path: '/api/__mock/public',
      handle: ({ url }) => {
        const q = url.searchParams
        if (q.has('live')) knob.live = q.get('live') === '1'
        if (q.has('rotate')) knob.rotate = q.get('rotate') === '1'
        if (q.has('song')) knob.song = q.get('song')!
        return knob
      },
    },
  ],
  pages: {
    player: { pageTitle: 'Radio Orbit', mount: '/radio', title: SONGS['/radio'][0], artist: 'Radio Orbit', format: 'audio/mpeg', bitrate: 192, listeners: 88, hasWebRTC: true, hasVideo: false, hasHLS: true },
    embed: { pageTitle: 'Radio Orbit', mount: '/radio', title: SONGS['/radio'][0], artist: 'Radio Orbit', format: 'audio/mpeg', bitrate: 192, listeners: 88 },
    kiosk: { pageTitle: 'Radio Orbit', user: { username: 'admin', role: 'superadmin' } },
  },
  events: (n) => [{ event: 'geo', data: geo(n) }],
}

export const pages = fixture.pages
export const events = fixture.events
export default fixture.default
