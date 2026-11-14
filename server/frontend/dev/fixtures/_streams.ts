import type { Fixture, MockStream } from '../types'
import { state } from '../state'

/**
 * Streams, relays and transcoders. Shapes mirror the Go handlers:
 * apiGetStreams / apiGetRelays / apiGetTranscoders (handlers_api_v2.go) and
 * handleTranscoderStats (handlers_relay.go).
 *
 * The file name starts with an underscore on purpose: fixtures are matched in
 * file-name order and core.ts ships a placeholder GET /api/streams that would
 * otherwise shadow the real shape below.
 */

interface MountCfg { mount: string; enabled: boolean; visible: boolean; burst: number; fallback?: string }
interface SrcMeta { ip: string; startedAt: number; name: string }
interface RelayCfg { url: string; mount: string; burst_size: number; enabled: boolean; since?: number }
interface TcCfg {
  name: string; input: string; output: string; format: string; bitrate: number; enabled: boolean; visibility: string
  sample_rate?: number; channels?: number; opus_application?: string; opus_vbr?: boolean; opus_complexity?: number; opus_frame_size_ms?: number
  startedAt: number
}
interface Store { mounts: MountCfg[]; src: Record<string, SrcMeta>; relays: RelayCfg[]; tcs: TcCfg[]; lowLatency: boolean; seeded: boolean; gone: Set<string>; songs: Record<string, { artist: string; title: string }> }

const g = globalThis as unknown as { __tiStreams?: Store }
const st: Store = (g.__tiStreams ??= { mounts: [], src: {}, relays: [], tcs: [], lowLatency: false, seeded: false, gone: new Set(), songs: {} })

// A store created by an older revision of this file lacks newer fields; fill them in rather than crash the SSE tick.
st.gone ??= new Set()
st.songs ??= {}

const hms = (sec: number) => {
  const s = Math.max(0, Math.floor(sec))
  return [Math.floor(s / 3600), Math.floor((s % 3600) / 60), s % 60].map((n) => String(n).padStart(2, '0')).join(':')
}
const goDur = (sec: number) => {
  const s = Math.max(0, Math.floor(sec))
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60)
  return `${h ? `${h}h` : ''}${h || m ? `${m}m` : ''}${s % 60}s`
}

/**
 * The shared dev/state module can be re-evaluated independently of this file, which would silently
 * undo kicks and metadata edits. Re-applying them from our own store on every call and every SSE tick
 * keeps the preview consistent.
 */
function apply() {
  // An AutoDJ mount whose health legitimately reads 0 while it plays (idle encoders skip work).
  if (!st.gone.has('/autodj') && !state.sources.find((s) => s.mount === '/autodj')) {
    state.sources.push({ mount: '/autodj', title: 'Teardrop', artist: 'Massive Attack', format: 'audio/mpeg', bitrate: 128, base: 0, health: 0 } as MockStream)
  }
  for (const m of st.gone) {
    const i = state.sources.findIndex((x) => x.mount === m)
    if (i >= 0) state.sources.splice(i, 1)
  }
  for (const [m, v] of Object.entries(st.songs)) {
    const src = state.sources.find((x) => x.mount === m)
    if (src) { src.artist = v.artist; src.title = v.title }
  }
}

function seed() {
  apply()
  if (st.seeded) return
  st.seeded = true
  const now = Date.now()
  st.mounts = [
    { mount: '/live', enabled: true, visible: true, burst: 65536 },
    { mount: '/radio', enabled: true, visible: true, burst: 65536, fallback: '/autodj' },
    { mount: '/jazz', enabled: true, visible: true, burst: 131072 },
    { mount: '/talk', enabled: true, visible: false, burst: 65536 },
    { mount: '/autodj', enabled: true, visible: true, burst: 65536 },
    { mount: '/events', enabled: true, visible: false, burst: 65536 },
    { mount: '/archive', enabled: false, visible: false, burst: 65536 },
  ]
  st.src = {
    '/live': { ip: '203.0.113.24:51734', startedAt: now - 3 * 3600_000 - 12 * 60_000, name: 'Orbit Morning' },
    '/radio': { ip: '198.51.100.7:40122', startedAt: now - 26 * 3600_000, name: 'Radio One' },
    '/jazz': { ip: 'webrtc-source', startedAt: now - 47 * 60_000, name: 'Jazz Room' },
    '/talk': { ip: 'transcode', startedAt: now - 3 * 3600_000, name: 'Evening Briefing' },
    '/autodj': { ip: 'autodj', startedAt: now - 9 * 3600_000, name: 'Night Shift' },
  }
  st.relays = [
    { url: 'https://stream.example.org:8443/main', mount: '/mirror', burst_size: 65536, enabled: true },
    { url: 'http://down.example.net:8000/live', mount: '/partner', burst_size: 65536, enabled: true },
    { url: 'http://radio.example.com/stream', mount: '/old', burst_size: 131072, enabled: false },
  ]
  st.tcs = [
    { name: 'Talk 128 AAC', input: '/live', output: '/talk', format: 'mp3', bitrate: 64, enabled: true, visibility: '', sample_rate: 44100, startedAt: now - 3 * 3600_000 },
    { name: 'Live Opus', input: '/live', output: '/live-opus', format: 'opus', bitrate: 96, enabled: true, visibility: 'public', opus_application: 'audio', opus_vbr: true, opus_complexity: 8, opus_frame_size_ms: 20, startedAt: now - 55 * 60_000 },
    { name: 'Mobile 32k', input: '/radio', output: '/radio-lo', format: 'mp3', bitrate: 32, enabled: false, visibility: 'unlisted', startedAt: 0 },
  ]
}

const isLive = (mount: string) => !!state.sources.find((s) => s.mount === mount) && !!st.src[mount]
const cfg = (mount: string) => st.mounts.find((m) => m.mount === mount)
const norm = (m: string) => (m.startsWith('/') ? m : `/${m}`)
const err = (status: number, message: string) => ({ status, body: { error: message } })

function mountRow(c: MountCfg) {
  const live = state.sources.find((s) => s.mount === c.mount)
  const src = st.src[c.mount]
  if (!live || !src || !c.enabled) return { mount: c.mount, content_type: '', bitrate: '', listeners: 0, source_ip: '', visible: c.visible, enabled: c.enabled, health: 0, uptime: '', current_song: '', name: '', has_video: false }
  const listeners = Math.round(live.base)
  return {
    mount: c.mount, content_type: live.format, bitrate: String(live.bitrate), listeners, source_ip: src.ip, visible: c.visible, enabled: c.enabled,
    health: live.health, uptime: hms((Date.now() - src.startedAt) / 1000), current_song: live.artist ? `${live.artist} - ${live.title}` : live.title, name: src.name, has_video: !!live.has_video,
    ...(live.has_video ? { video_width: 1280, video_height: 720, video_fps: 30, video_gop: 2, video_kbps: 2400 } : {}),
  }
}

function tcRow(t: TcCfg) {
  const active = t.enabled && isLive(t.input)
  const secs = active ? (Date.now() - t.startedAt) / 1000 : 0
  return {
    name: t.name, input: t.input, output: t.output, format: t.format, bitrate: t.bitrate, enabled: t.enabled, active,
    frames_processed: Math.floor(secs * (t.format === 'opus' ? 50 : 38.28)), bytes_encoded: Math.floor(secs * t.bitrate * 125), uptime: active ? goDur(secs) : 'OFF',
    ...(t.visibility ? { visibility: t.visibility } : {}), ...(t.sample_rate ? { sample_rate: t.sample_rate } : {}), ...(t.channels ? { channels: t.channels } : {}),
    ...(t.opus_application ? { opus_application: t.opus_application } : {}), ...(t.opus_vbr !== undefined ? { opus_vbr: t.opus_vbr } : {}),
    ...(t.opus_complexity ? { opus_complexity: t.opus_complexity } : {}), ...(t.opus_frame_size_ms ? { opus_frame_size_ms: t.opus_frame_size_ms } : {}),
  }
}

const relayActive = (r: RelayCfg) => r.enabled && !r.url.includes('down.example') && Date.now() - (r.since ?? 0) > 4000

const fixture: Fixture = {
  // Seeds the shared live sources before the first SSE tick, not only on the first REST call.
  // Real-shaped `stream` frames (ip, song, type, uptime) for every live mount. They follow the frames
  // dev/mock.ts emits from its own copy of the state, so the last frame per mount is ours. This also
  // adds the AutoDJ mount, which reports health 0 while it plays.
  events: (n) => {
    try {
      seed()
      return Object.keys(st.src).flatMap((mount) => {
        const live = state.sources.find((x) => x.mount === mount)
        const meta = st.src[mount]
        if (!live || !meta || !cfg(mount)?.enabled || st.gone.has(mount)) return []
        const wob = Math.round(Math.sin(n / 9 + mount.length) * live.base * 0.1)
        const listeners = Math.max(0, Math.round(live.base) + (live.base ? wob : 0))
        const song = st.songs[mount] ? [st.songs[mount].artist, st.songs[mount].title].filter(Boolean).join(' - ') : live.artist ? `${live.artist} - ${live.title}` : live.title
        return [{ event: 'stream', data: { mount, name: meta.name, artist: st.songs[mount]?.artist ?? live.artist, title: st.songs[mount]?.title ?? live.title, song, format: live.format, type: live.format, bitrate: String(live.bitrate), listeners, viewers: 0, health: live.health, ip: meta.ip, uptime: hms((Date.now() - meta.startedAt) / 1000), is_transcoded: !!live.is_transcoded, ...(live.is_transcoded ? { source_mount: live.source_mount, source_type: live.source_type, source_bitrate: live.source_bitrate } : {}), bytes_in: 0, bytes_out: 0, bytes_dropped: 0, has_video: !!live.has_video } }]
      })
    } catch { return [] /* never let a fixture take the dev server down */ }
  },
  default: [
    { method: 'GET', path: '/api/streams', handle: () => { seed(); return st.mounts.map(mountRow) } },
    {
      method: 'POST', path: '/api/streams', handle: ({ body }) => {
        seed()
        if (!body.mount || !body.password) return err(400, 'Mount and password are required')
        if (body.burst_size < 0 || body.burst_size > 1048576) return err(400, 'burst_size must be between 0 and 1048576')
        const mount = norm(body.mount)
        if (cfg(mount)) return err(409, 'Mount taken')
        st.gone.delete(mount)
        st.mounts.push({ mount, enabled: true, visible: false, burst: body.burst_size || 65536 })
        return { status: 'created', mount }
      },
    },
    {
      method: 'PUT', path: '/api/streams', handle: ({ body }) => {
        seed()
        const c = body.mount && cfg(norm(body.mount))
        if (!body.mount) return err(400, 'Mount is required')
        if (!c) return err(404, 'Mount not found')
        if (typeof body.enabled === 'boolean') {
          c.enabled = body.enabled
          if (!c.enabled) {
            delete st.src[c.mount]
            st.gone.add(c.mount)
          }
        }
        if (typeof body.visible === 'boolean') c.visible = body.visible
        return { status: 'updated', mount: c.mount }
      },
    },
    {
      method: 'DELETE', path: '/api/streams', handle: ({ url }) => {
        seed()
        const mount = url.searchParams.get('mount') ?? ''
        if (!mount) return err(400, 'Mount is required')
        st.mounts = st.mounts.filter((m) => m.mount !== mount)
        delete st.src[mount]
        st.gone.add(mount)
        apply()
        return { status: 'deleted' }
      },
    },
    {
      method: 'POST', path: '/api/streams/kick', handle: ({ body }) => {
        seed()
        if (!body.mount) return err(400, 'Mount is required')
        if (body.type === 'listeners') return { status: 'ok' }
        delete st.src[body.mount]
        st.gone.add(body.mount)
        apply()
        return { status: 'ok' }
      },
    },
    {
      method: 'POST', path: '/admin/metadata', handle: ({ url }) => {
        seed()
        const mount = url.searchParams.get('mount') ?? '', song = url.searchParams.get('song') ?? ''
        const live = state.sources.find((s) => s.mount === mount)
        if (!live) return { status: 404, body: '<?xml version="1.0"?>\n<iceresponse><message>No such mount</message><return>0</return></iceresponse>\n', headers: { 'Content-Type': 'text/xml' } }
        const [a, ...rest] = song.split(' - ')
        st.songs[mount] = { artist: rest.length ? a : '', title: rest.length ? rest.join(' - ') : song }
        apply()
        return { body: '<?xml version="1.0"?>\n<iceresponse><message>OK</message><return>1</return></iceresponse>\n', headers: { 'Content-Type': 'text/xml' } }
      },
    },
    {
      method: 'POST', path: '/admin/update-fallback', handle: ({ body }) => {
        seed()
        const c = cfg(body.mount)
        if (c) c.fallback = body.fallback ? norm(body.fallback) : undefined
        return { body: '' }
      },
    },
    { method: 'POST', path: '/admin/kick-all-listeners', handle: () => ({ body: '' }) },
    { method: 'POST', path: '/admin/hotswap', handle: () => ({ body: '' }) },

    // ---- relays
    { method: 'GET', path: '/api/relays', handle: () => (seed(), st.relays.map(({ since: _s, ...r }) => ({ ...r, active: relayActive(r) }))) },
    {
      method: 'POST', path: '/api/relays', handle: ({ body }) => {
        seed()
        if (!body.url || !body.mount) return err(400, 'URL and mount are required')
        if (!/^https?:\/\//.test(body.url)) return err(400, `Upstream URL rejected: scheme "${body.url.split(':')[0]}" not allowed (only http/https)`)
        if (/\/\/(127\.|localhost|10\.|192\.168\.)/.test(body.url)) return err(400, 'Upstream URL rejected: URL points at a non-routable address: loopback address')
        const mount = norm(body.mount)
        const burst = body.burst_size ?? 65536
        const ex = st.relays.find((r) => r.mount === mount)
        if (ex) { ex.url = body.url; ex.burst_size = burst } else st.relays.push({ url: body.url, mount, burst_size: burst, enabled: true, since: Date.now() })
        return { status: 'ok' }
      },
    },
    {
      method: 'DELETE', path: '/api/relays', handle: ({ url }) => {
        seed()
        const mount = url.searchParams.get('mount') ?? ''
        if (!st.relays.some((r) => r.mount === mount)) return err(404, 'Relay not found')
        st.relays = st.relays.filter((r) => r.mount !== mount)
        return { status: 'deleted' }
      },
    },
    {
      method: 'POST', path: '/api/relays/toggle', handle: ({ body }) => {
        seed()
        const r = st.relays.find((x) => x.mount === body.mount)
        if (!r) return err(404, 'Relay not found')
        r.enabled = !r.enabled
        return { status: 'ok', enabled: r.enabled }
      },
    },
    { method: 'POST', path: '/admin/restart-relay', handle: () => ({ body: '' }) },

    // ---- transcoders
    { method: 'GET', path: '/api/transcoders', handle: () => (seed(), st.tcs.map(tcRow)) },
    {
      method: 'GET', path: '/admin/transcoder-stats', handle: () => {
        seed()
        return st.tcs.map((t) => { const r = tcRow(t); return { name: r.name, input: r.input, output: r.output, format: r.format, bitrate: r.bitrate, active: r.active, frames: r.frames_processed, bytes: r.bytes_encoded, uptime: r.uptime } })
      },
    },
    {
      method: 'POST', path: '/api/transcoders', handle: ({ body }) => {
        seed()
        if (!body.name || !body.input_mount || !body.output_mount) return err(400, 'Name, input_mount, and output_mount are required')
        st.tcs.push({
          name: body.name, input: body.input_mount, output: body.output_mount, format: body.format || 'mp3', bitrate: body.bitrate || 128, enabled: body.enabled !== false, visibility: body.visibility ?? '',
          sample_rate: body.sample_rate, channels: body.channels, opus_application: body.opus_application, opus_vbr: body.opus_vbr, opus_complexity: body.opus_complexity, opus_frame_size_ms: body.opus_frame_size_ms, startedAt: Date.now(),
        })
        return { status: 'created' }
      },
    },
    {
      method: 'PUT', path: '/api/transcoders', handle: ({ url, body }) => {
        seed()
        const t = st.tcs.find((x) => x.name === url.searchParams.get('name'))
        if (!t) return err(404, 'Transcoder not found')
        Object.assign(t, {
          name: body.name || t.name, input: body.input_mount || t.input, output: body.output_mount || t.output, format: body.format || t.format, bitrate: body.bitrate || t.bitrate,
          enabled: body.enabled ?? t.enabled, visibility: body.visibility ?? t.visibility, sample_rate: body.sample_rate, channels: body.channels,
          opus_application: body.opus_application, opus_vbr: body.opus_vbr, opus_complexity: body.opus_complexity, opus_frame_size_ms: body.opus_frame_size_ms, startedAt: Date.now(),
        })
        return { status: 'updated', name: t.name }
      },
    },
    {
      method: 'DELETE', path: '/api/transcoders', handle: ({ url }) => {
        seed()
        const name = url.searchParams.get('name') ?? ''
        if (!name) return err(400, 'Name is required')
        if (!st.tcs.some((t) => t.name === name)) return err(404, 'Transcoder not found')
        st.tcs = st.tcs.filter((t) => t.name !== name)
        return { status: 'deleted' }
      },
    },
    {
      method: 'POST', path: '/admin/toggle-transcoder', handle: ({ body }) => {
        seed()
        const t = st.tcs.find((x) => x.name === body.name)
        if (t) { t.enabled = !t.enabled; t.startedAt = Date.now() }
        return { body: '' }
      },
    },
  ],
}
export const events = fixture.events
export default fixture.default
