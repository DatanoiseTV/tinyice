import type { Fixture, MockCtx, MockEvent } from '../types'

/**
 * AutoDJ + Studio mock. Shapes mirror server/handlers_api_v2.go (list, create,
 * update, playlist, queue, files), handlers_media_browse.go (browse) and
 * streamerEventInfo in handlers_api.go (the SSE `autodj` frame).
 *
 * Playout is simulated against the wall clock exactly like the Go streamer:
 * position is reported as 0 unless playing, pause time is discounted, the
 * queue is consumed before the playlist, ids are stable and start at 1, queue
 * entries carry id -1, and every playlist mutation bumps playlist_version.
 */

interface Track { dir: string; file: string; title: string; dur: number }
const T = (dir: string, artist: string, name: string, dur: number): Track => ({ dir, file: `${artist} - ${name}.mp3`, title: `${artist} - ${name}`, dur })

const LIBRARY: Track[] = [
  T('', 'Station', 'Identification', 8),
  T('', 'Station', 'Weather Bed', 45),
  T('Jazz', 'Miles Davis', 'Blue in Green', 337),
  T('Jazz', 'Miles Davis', 'So What', 562),
  T('Jazz', 'Dave Brubeck', 'Take Five', 324),
  T('Jazz', 'John Coltrane', 'Naima', 261),
  T('Jazz', 'Bill Evans', 'Waltz for Debby', 431),
  T('Jazz', 'Thelonious Monk', "Round Midnight", 357),
  T('Jazz', 'Chet Baker', 'I Fall in Love Too Easily', 243),
  T('Jazz', 'Herbie Hancock', 'Cantaloupe Island', 329),
  T('Ambient', 'Brian Eno', 'An Ending (Ascent)', 260),
  T('Ambient', 'Brian Eno', 'Music for Airports 1-1', 1000),
  T('Ambient', 'Aphex Twin', 'Xtal', 291),
  T('Ambient', 'Stars of the Lid', 'Requiem for Dying Mothers', 0),
  T('Ambient', 'Harold Budd', 'The Pearl', 372),
  T('Ambient', 'Tim Hecker', 'Virgins', 408),
  T('Electronic', 'Boards of Canada', 'Roygbiv', 151),
  T('Electronic', 'Four Tet', 'Angel Echoes', 332),
  T('Electronic', 'Burial', 'Archangel', 218),
  T('Electronic', 'Floating Points', 'Silhouettes', 305),
  T('Electronic', 'Jon Hopkins', 'Open Eye Signal', 463),
  T('Electronic', 'Caribou', 'Odessa', 249),
  T('Electronic', 'Bonobo', 'Kerala', 0),
  T('Spoken', 'Evening Briefing', 'Headlines', 180),
  T('Spoken', 'Evening Briefing', 'Local Weather', 95),
  T('Spoken', 'Studio Talk', 'Listener Letters', 612),
]
const ROOT = '/srv/music'
const rel = (t: Track) => (t.dir ? `${t.dir}/${t.file}` : t.file)
const abs = (t: Track) => `${ROOT}/${rel(t)}`
const byRel = new Map(LIBRARY.map((t) => [rel(t), t]))
const byAbs = new Map(LIBRARY.map((t) => [abs(t), t]))
const PLS = ['Main Rotation.pls', 'Late Jazz.pls']

interface Entry { id: number; path: string; title: string }
interface Inst {
  name: string; mount: string; format: string; bitrate: number; enabled: boolean
  music_dir: string; mpd_enabled: boolean; mpd_port: string; last_playlist: string
  song_command: string; song_command_timeout: number; on_play_command: string; on_play_command_timeout: number
  loop: boolean; inject_metadata: boolean; visible: boolean; shuffle: boolean
  state: 0 | 1 | 2
  playlist: Entry[]; nextId: number; version: number
  queue: { title: string; path: string; id: number }[]
  pos: number
  cur: { id: number; title: string; path: string; dur: number } | null
  startedAt: number; pausedAt: number; pausedFor: number
  volume: number
}

const mk = (o: Partial<Inst> & Pick<Inst, 'name' | 'mount'>, n: number, from: number): Inst => {
  const playlist: Entry[] = []
  for (let i = 0; i < n; i++) { const t = LIBRARY[(from + i * 7) % LIBRARY.length]; playlist.push({ id: i + 1, path: abs(t), title: t.title }) }
  return {
    format: 'mp3', bitrate: 128, enabled: true, music_dir: ROOT, mpd_enabled: false, mpd_port: '', last_playlist: 'Main Rotation.pls',
    song_command: '', song_command_timeout: 0, on_play_command: '', on_play_command_timeout: 0,
    loop: true, inject_metadata: true, visible: true, shuffle: false, state: 0, playlist, nextId: n + 1, version: 1,
    queue: [], pos: 0, cur: null, startedAt: 0, pausedAt: 0, pausedFor: 0, volume: 1, ...o,
  }
}

const insts: Inst[] = [
  mk({ name: 'Main Rotation', mount: '/radio', state: 1 }, 24, 2),
  mk({ name: 'Late Jazz', mount: '/jazz', state: 2, shuffle: true, bitrate: 96, format: 'opus', last_playlist: 'Late Jazz.pls', mpd_enabled: true, mpd_port: '6600' }, 12, 3),
  mk({ name: 'Overnight', mount: '/night', enabled: false, visible: false, bitrate: 64, song_command: 'node /home/radio/getSong.js', song_command_timeout: 5 }, 0, 0),
]
const now = () => Date.now()
const hiddenDur = (d: number) => (d > 0 ? d : 200) // the real decoder plays a file with no reported length until it ends

function startTrack(i: Inst, id: number, title: string, path: string, dur: number) {
  i.cur = { id, title, path, dur }
  i.startedAt = now(); i.pausedFor = 0; i.pausedAt = 0
}
function startAt(i: Inst, pos: number) {
  if (i.playlist.length === 0) { i.cur = null; i.state = 0; return }
  i.pos = pos
  const e = i.playlist[pos]
  startTrack(i, e.id, e.title, e.path, byAbs.get(e.path)?.dur ?? 0)
}
function advance(i: Inst) {
  const q = i.queue.shift()
  if (q) { startTrack(i, -1, q.title, q.path, byAbs.get(q.path)?.dur ?? 0); return }
  if (i.playlist.length === 0) { i.state = 0; i.cur = null; return }
  let nx = i.shuffle ? Math.floor(Math.random() * i.playlist.length) : (i.cur && i.cur.id !== -1 ? i.pos + 1 : i.pos)
  if (nx >= i.playlist.length) { if (!i.loop) { i.state = 0; i.cur = null; i.pos = 0; return } nx = 0 }
  startAt(i, nx)
}
function elapsed(i: Inst): number {
  if (i.state === 0 || !i.cur) return 0
  const end = i.state === 2 ? i.pausedAt : now()
  return Math.max(0, (end - i.startedAt - i.pausedFor) / 1000)
}
function tick(i: Inst) {
  if (!i.enabled) { i.state = 0; return }
  if (i.state === 1 && !i.cur) startAt(i, Math.min(i.pos, Math.max(0, i.playlist.length - 1)))
  for (let guard = 0; i.state === 1 && i.cur && elapsed(i) >= hiddenDur(i.cur.dur) && guard < 4; guard++) advance(i)
}
function position(i: Inst): number {
  if (i.state !== 1 || !i.cur) return 0
  const e = elapsed(i)
  return i.cur.dur > 0 ? Math.min(e, i.cur.dur) : e
}
// Seed: the playing instance starts mid-track, the paused one holds a track.
startAt(insts[0], 3); insts[0].startedAt -= 71_000
startAt(insts[1], 1); insts[1].pausedAt = now(); insts[1].startedAt -= 120_000

const get = (m: string) => insts.find((x) => x.mount === m)
const decode = (s: string) => { try { return decodeURIComponent(s) } catch { return s } }
const err = (status: number, error: string) => ({ status, body: { error } })

function info(i: Inst) {
  tick(i)
  return {
    name: i.name, mount: i.mount, state: i.state, current_song: i.cur?.title ?? '', start_time: Math.floor(i.startedAt / 1000),
    position: position(i), duration: i.cur?.dur ?? 0, current_id: i.cur?.id ?? 0, playlist_pos: i.pos, playlist_len: i.playlist.length,
    shuffle: i.shuffle, loop: i.loop, inject_metadata: i.inject_metadata, visible: i.visible, music_dir: i.music_dir, format: i.format,
    bitrate: i.bitrate, enabled: i.enabled, mpd_enabled: i.mpd_enabled, mpd_port: i.mpd_port, last_playlist: i.last_playlist,
    song_command: i.song_command, song_command_timeout: i.song_command_timeout, on_play_command: i.on_play_command,
    on_play_command_timeout: i.on_play_command_timeout, queue: i.queue.map((q) => ({ ...q })),
  }
}
function frame(i: Inst) {
  tick(i)
  return {
    name: i.name, mount: i.mount, state: i.state, song: i.cur?.title ?? '', start_time: Math.floor(i.startedAt / 1000), position: position(i),
    duration: i.cur?.dur ?? 0, current_id: i.cur?.id ?? 0, playlist_pos: i.pos, playlist_len: i.playlist.length, shuffle: i.shuffle, loop: i.loop,
    queue: i.queue.map((q) => ({ ...q })), playlist_version: i.version,
  }
}
const bump = (i: Inst) => { i.version++ }
const addByPath = (i: Inst, p: string) => {
  const key = p.startsWith(ROOT + '/') ? p.slice(ROOT.length + 1) : p
  const hit = LIBRARY.filter((t) => rel(t) === key || rel(t).startsWith(key.replace(/\/$/, '') + '/'))
  for (const t of hit) i.playlist.push({ id: i.nextId++, path: abs(t), title: t.title })
  return hit.length
}

/* ---------- library ---------- */
const DIRS = ['Jazz', 'Ambient', 'Electronic', 'Spoken']
function filesFor(sub: string) {
  const out: unknown[] = []
  if (sub === '') for (const f of PLS) out.push({ name: f, title: `Playlist: ${f}`, is_dir: false, path: f, abs_path: `/var/lib/tinyice/playlists/${f}`, is_pls: true })
  if (sub === '') for (const d of DIRS) out.push({ name: d, title: d, is_dir: true, path: d, abs_path: `${ROOT}/${d}`, is_pls: false })
  for (const t of LIBRARY.filter((x) => x.dir === sub)) out.push({ name: t.file, title: t.title, is_dir: false, path: rel(t), abs_path: abs(t), is_pls: false })
  return out
}

/* ---------- media browser ---------- */
const BROWSE_ROOTS = [ROOT, '/srv/podcasts']
const POD_DIRS = ['Weekly', 'Interviews']
function browse(path: string) {
  const counts = (dir: string) => {
    if (dir === ROOT) return { track_count: LIBRARY.filter((t) => t.dir === '').length, dir_count: DIRS.length }
    const d = dir.slice(ROOT.length + 1)
    if (dir.startsWith(ROOT + '/') && DIRS.includes(d)) return { track_count: LIBRARY.filter((t) => t.dir === d).length, dir_count: 0 }
    if (dir === '/srv/podcasts') return { track_count: 0, dir_count: POD_DIRS.length }
    return { track_count: 14, dir_count: 0 }
  }
  if (!path) return { roots: BROWSE_ROOTS, path: '', parent: '', track_count: 0, dir_count: 0, truncated: false, entries: BROWSE_ROOTS.map((r) => ({ name: r, path: r, ...counts(r) })) }
  if (!BROWSE_ROOTS.some((r) => path === r || path.startsWith(r + '/'))) return err(403, 'Path is outside every configured media root')
  const root = BROWSE_ROOTS.find((r) => path === r || path.startsWith(r + '/'))!
  const kids = root === ROOT && path === ROOT ? DIRS : root === '/srv/podcasts' && path === root ? POD_DIRS : []
  const c = counts(path)
  return { roots: BROWSE_ROOTS, path, parent: path === root ? '' : path.slice(0, path.lastIndexOf('/')), ...c, truncated: false, entries: kids.map((k) => ({ name: k, path: `${path}/${k}`, ...counts(`${path}/${k}`) })) }
}

/* ---------- configuration (create / update) ---------- */
function applyCfg(i: Inst, b: Record<string, any>, creating: boolean): { status: number; body: unknown } | null {
  if (creating && (!b.name || !b.mount)) return err(400, 'Name and mount are required')
  if (!b.music_dir && !b.song_command) return err(400, 'Either music_dir or song_command is required')
  if (b.format && !['mp3', 'opus'].includes(String(b.format).toLowerCase())) return err(400, 'format must be "mp3" or "opus"')
  let mount = b.mount || i.mount
  if (mount[0] !== '/') mount = '/' + mount
  if (mount !== i.mount && get(mount)) return err(409, `Mount ${mount} is already in use`)
  if (b.mpd_enabled && !/^\d{2,5}$/.test(String(b.mpd_port ?? ''))) return err(500, `Failed to restart AutoDJ: listen tcp :${b.mpd_port || '0'}: invalid port`)
  if (b.name) i.name = b.name
  i.mount = mount
  i.music_dir = b.music_dir ?? ''
  i.format = b.format || i.format
  i.bitrate = b.bitrate || i.bitrate
  for (const k of ['loop', 'inject_metadata', 'mpd_enabled', 'visible'] as const) if (typeof b[k] === 'boolean') i[k] = b[k]
  if (typeof b.mpd_port === 'string') i.mpd_port = b.mpd_port
  i.song_command = b.song_command ?? ''
  i.song_command_timeout = b.song_command_timeout ?? 0
  i.on_play_command = b.on_play_command ?? ''
  i.on_play_command_timeout = b.on_play_command_timeout ?? 0
  return null
}

/* ---------- per-mount actions ---------- */
function action(c: MockCtx) {
  const i = get(decode(c.params.m))
  if (!i) return err(404, 'Streamer not found')
  const a = c.params.a
  const m = c.req.method
  const body = c.body
  tick(i)
  switch (a) {
    case 'play':
      if (i.state === 2) { i.pausedFor += now() - i.pausedAt; i.state = 1 } // resume in place, never skip
      else if (i.state === 0) { i.state = 1; i.cur = null; if (i.pos >= i.playlist.length) i.pos = 0; tick(i) }
      return { status: 'playing' }
    case 'pause': if (i.state === 1) { i.state = 2; i.pausedAt = now() } return { status: 'paused' }
    case 'next': if (i.state === 1) advance(i); return { status: 'skipped' }
    case 'prev': case 'previous': if (i.playlist.length) startAt(i, Math.max(0, i.pos - 1)); return { status: 'rewound' }
    case 'shuffle': i.shuffle = !i.shuffle; return { status: 'ok', shuffle: i.shuffle }
    case 'loop': i.loop = !i.loop; return { status: 'ok', loop: i.loop }
    case 'volume': {
      let v = Number(body.volume) || 0
      if (body.unit === 'percent') v /= 100; else if (body.unit !== 'fraction' && v > 1) v /= 100
      i.volume = Math.max(0, Math.min(1, v)); return { status: 'ok', volume: i.volume }
    }
    case 'metadata': i.inject_metadata = typeof body.enabled === 'boolean' ? body.enabled : !i.inject_metadata; return { status: 200, body: '' }
    case 'files': return filesFor(c.url.searchParams.get('path') ?? '')
    case 'queue':
      if (m === 'GET') return i.queue.map((q) => ({ ...q }))
      return queuePath(i, body.path, !!body.front, body.id)
    case 'playlist':
      if (m === 'GET') return i.playlist.map((e) => ({ title: e.title, path: e.path, id: e.id }))
      return null
    case 'playlist/add': {
      const paths: string[] = [...(body.files ?? []), ...(body.path ? [body.path] : []), ...(body.paths ?? [])]
      for (const p of paths) addByPath(i, p)
      i.last_playlist = i.last_playlist || `${i.name}.pls`
      bump(i); return { status: 'ok' }
    }
    case 'playlist/remove': {
      const id = body.id || Number(c.url.searchParams.get('id'))
      const ix = i.playlist.findIndex((e) => e.id === id)
      if (ix < 0) return err(404, 'Playlist entry not found')
      i.playlist.splice(ix, 1)
      if (ix < i.pos) i.pos--
      bump(i); return { status: 'ok' }
    }
    case 'playlist/clear': i.playlist = []; i.pos = 0; bump(i); return { status: 'ok' }
    case 'playlist/playnext': return queuePath(i, '', true, body.id)
    case 'playlist/save': return { status: 200, body: '' }
    case 'playlist/load': {
      const f = c.url.searchParams.get('file') ?? ''
      if (!f) return { status: 400, body: 'file query parameter is required (the .pls to load)' }
      if (!PLS.includes(f)) return { status: 500, body: `open playlists/${f}: no such file or directory` }
      i.playlist = []
      const n = f === 'Late Jazz.pls' ? LIBRARY.filter((t) => t.dir === 'Jazz') : LIBRARY.filter((_, k) => k % 2 === 0)
      for (const t of n) i.playlist.push({ id: i.nextId++, path: abs(t), title: t.title })
      i.last_playlist = f; i.pos = 0; bump(i); return { status: 200, body: '' }
    }
  }
  return err(404, 'not found')
}
function queuePath(i: Inst, path: string, front: boolean, id?: number) {
  let t: { title: string; path: string } | undefined
  if (path) {
    // Like the Go handler, which resolves with filepath.Abs against the server's cwd: relative paths are refused.
    const tr = path.startsWith(ROOT + '/') ? byRel.get(path.slice(ROOT.length + 1)) : undefined
    if (!tr) return err(403, 'Forbidden')
    t = { title: tr.title, path: abs(tr) }
  } else if (id) {
    const e = i.playlist.find((x) => x.id === id)
    if (!e) return err(404, 'Playlist entry not found')
    t = { title: e.title, path: e.path }
  } else return err(400, 'Either path or id is required')
  const item = { ...t, id: -1 }
  if (front) i.queue.unshift(item); else i.queue.push(item)
  return { status: 'ok' }
}

const fixture: Fixture = {
  default: [
    { method: 'GET', path: '/api/autodj', handle: () => insts.map(info) },
    {
      method: 'POST', path: '/api/autodj', handle: ({ body }) => {
        const i = mk({ name: body.name, mount: body.mount?.[0] === '/' ? body.mount : `/${body.mount}` }, 0, 0)
        const bad = applyCfg(i, body, true)
        if (bad) return bad
        if (get(i.mount)) return err(409, `Mount ${i.mount} is already in use`)
        if (i.music_dir) addByPath(i, '')
        i.format = i.format || 'mp3'; i.bitrate = i.bitrate || 128
        insts.push(i); i.state = 1; tick(i)
        return { status: 'created', mount: i.mount }
      },
    },
    {
      method: 'PUT', path: '/api/autodj', handle: ({ url, body }) => {
        const i = get(url.searchParams.get('mount') ?? '')
        if (!i) return err(404, 'AutoDJ not found')
        const bad = applyCfg(i, body, false)
        if (bad) return bad
        return { status: 'updated', mount: i.mount }
      },
    },
    {
      method: 'DELETE', path: '/api/autodj', handle: ({ url }) => {
        const ix = insts.findIndex((x) => x.mount === url.searchParams.get('mount'))
        if (ix < 0) return err(404, 'AutoDJ not found')
        insts.splice(ix, 1); return { status: 'deleted' }
      },
    },
    { method: 'GET', path: '/api/autodj/browse', handle: ({ url }) => browse(url.searchParams.get('path') ?? '') },
    {
      method: 'POST', path: '/api/autodj/playlist/reorder', handle: ({ body }) => {
        const i = get(body.mount)
        if (!i) return err(404, 'Streamer not found')
        const { from, to } = body as { from: number; to: number }
        const n = i.playlist.length
        if (from < 0 || from >= n || to < 0 || to >= n) return { status: 'ok' } // the Go handler ignores out-of-range moves silently
        const [it] = i.playlist.splice(from, 1)
        i.playlist.splice(to, 0, it)
        if (from < i.pos && to >= i.pos) i.pos--
        else if (from >= i.pos && to < i.pos) i.pos++
        bump(i); return { status: 'ok' }
      },
    },
    { path: /^\/api\/autodj\/(?<m>[^/]+)\/(?<a>.+)$/, handle: (c) => action(c) },
    // Legacy form endpoints (CSRF-checked in production, adminPost sends the token).
    {
      method: 'POST', path: '/admin/autodj/toggle', handle: ({ body }) => {
        const i = get(body.mount)
        if (i) { i.enabled = !i.enabled; if (i.enabled) { i.state = 1; i.cur = null; tick(i) } else i.state = 0 }
        return { status: 303, headers: { Location: '/admin#tab-streamer' }, body: '' }
      },
    },
    { method: 'POST', path: '/admin/player/clear-queue', handle: ({ body }) => { const i = get(body.mount); if (i) i.queue = []; return { status: 200, body: '' } } },
  ],
  events: (): MockEvent[] => insts.map((i) => ({ event: 'autodj', data: frame(i) })),
}

export default fixture.default
export const events = fixture.events
