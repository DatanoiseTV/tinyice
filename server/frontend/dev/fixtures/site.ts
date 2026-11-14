import type { Fixture } from '../types'

/**
 * Public pages (landing, explore, developers, login, setup). Shapes follow
 * server/handlers_stream.go (visibleStreamList), handlers_api.go
 * (handlePublicEvents), server/auth.go and server/handlers_setup.go.
 *
 * Dev knobs, all via one control route so screenshots can drive state:
 *   GET /api/__mock/site?n=12&md=1&title=My%20Radio&sub=...&logo=0&passkeys=0&oidc=0&setup=token
 */
const CATALOG = [
  { mount: '/live', title: 'Morning Show', name: 'Main stage', format: 'audio/mpeg', bitrate: 128, base: 142, video: true, genre: 'Talk', description: 'Weekday mornings with DJ Orbit.' },
  { mount: '/radio', title: 'Blue in Green', name: 'Radio', format: 'audio/mpeg', bitrate: 192, base: 88, genre: 'Jazz', description: 'Round the clock jazz.' },
  { mount: '/jazz', title: 'Take Five', name: 'Late Jazz', format: 'audio/ogg; codecs=opus', bitrate: 96, base: 31, genre: 'Jazz', description: '' },
  { mount: '/talk', title: 'Evening Briefing', name: 'Talk', format: 'audio/aac', bitrate: 64, base: 12, genre: 'News', description: '' },
  { mount: '/ambient', title: 'Weightless Pt. 1', name: 'Ambient', format: 'audio/mpeg', bitrate: 128, base: 23, genre: 'Ambient', description: 'Slow music for work.' },
  { mount: '/techno', title: 'Acid Rain (Extended Mix)', name: 'Warehouse', format: 'audio/mpeg', bitrate: 192, base: 57, genre: 'Techno', description: '' },
  { mount: '/classical', title: 'Symphony No. 5 in C minor, Op. 67: I. Allegro con brio', name: 'Concert Hall', format: 'audio/flac', bitrate: 320, base: 19, genre: 'Classical', description: '' },
  { mount: '/lofi', title: '', name: 'Lo-fi', format: 'audio/mpeg', bitrate: 96, base: 64, genre: 'Lo-fi', description: '' },
  { mount: '/news', title: 'Top of the hour', name: 'News', format: 'audio/aac', bitrate: 64, base: 9, genre: 'News', description: '' },
  { mount: '/dub', title: 'King Tubby Meets the Rockers', name: 'Dub Station', format: 'audio/mpeg', bitrate: 128, base: 5, genre: 'Dub', description: '' },
  { mount: '/archive', title: 'Session 1998-04-12', name: 'Archive', format: 'audio/mpeg', bitrate: 128, base: 0, offline: true, genre: '', description: '' },
  { mount: '/test', title: '', name: 'Test', format: 'audio/mpeg', bitrate: 0, base: 0, offline: true, genre: '', description: '' },
]

const site = { n: 4, md: false, title: 'TinyIce', sub: 'Self-hosted streaming', logo: false, passkeys: true, oidc: true, setup: 'devtoken-0123456789abcdef' }

function listeners(base: number) { return Math.max(0, Math.round(base + Math.sin(Date.now() / 4000) * base * 0.1)) }

function streams() {
  return CATALOG.slice(0, site.n).map((c) => ({
    mount: c.mount, title: c.title, artist: c.name, format: c.format, bitrate: c.bitrate,
    listeners: c.offline ? 0 : listeners(c.base), live: !c.offline, has_video: !!c.video,
  }))
}

const MARKDOWN = `## Welcome

We broadcast **jazz**, ambient and talk from a basement in Leipzig. Everything here is free to listen to.

- Mornings: talk and news
- Evenings: records from the archive

Questions? Write to [studio@example.org](mailto:studio@example.org).

<img src=x onerror="document.title='XSS'"><script>document.title='XSS'</script>`

function base() {
  return {
    pageTitle: site.title, pageSubtitle: site.sub,
    branding: { logoUrl: site.logo ? 'data:image/svg+xml;utf8,<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><rect width="24" height="24" rx="6" fill="%23356"/><circle cx="12" cy="12" r="5" fill="%23fc6"/></svg>' : null, accentColor: '#ff6a00', landingMarkdown: site.md ? MARKDOWN : '' },
    passkeysEnabled: site.passkeys,
    oidcProviders: site.oidc ? [{ id: 'google', name: 'Google', icon: 'google' }, { id: 'corp', name: 'Acme SSO', icon: 'key' }] : null,
  }
}

const fixture: Fixture = {
  default: [
    {
      method: 'GET', path: '/api/__mock/site',
      handle: ({ url }) => {
        const q = url.searchParams
        if (q.has('n')) site.n = Math.max(0, Math.min(CATALOG.length, Number(q.get('n'))))
        if (q.has('md')) site.md = q.get('md') === '1'
        if (q.has('title')) site.title = q.get('title')!
        if (q.has('sub')) site.sub = q.get('sub')!
        if (q.has('logo')) site.logo = q.get('logo') === '1'
        if (q.has('passkeys')) site.passkeys = q.get('passkeys') === '1'
        if (q.has('oidc')) site.oidc = q.get('oidc') === '1'
        return site
      },
    },
    {
      // Unnamed `data:` frame of the public SSE feed. A fixture cannot hold a
      // connection open, so it sends one frame and EventSource reconnects
      // after `retry`, which is a close enough stand-in for the 500 ms tick.
      method: 'GET', path: '/events',
      handle: () => {
        const frame = streams().map((s) => ({
          mount: s.mount, name: s.artist, listeners: s.listeners, bitrate: String(s.bitrate), uptime: '3h 12m',
          genre: CATALOG.find((c) => c.mount === s.mount)!.genre, description: CATALOG.find((c) => c.mount === s.mount)!.description,
          song: s.title, live: s.live, has_video: s.has_video,
        }))
        return { status: 200, headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' }, body: `retry: 1000\n\ndata: ${JSON.stringify(frame)}\n\n` }
      },
    },
    // Login: the form posts here with Accept: application/json. The dev server
    // rewrites POST /login to the page itself, so this only answers when a
    // test calls it under another path; see the report.
    {
      method: 'POST', path: '/api/passkey/login/begin',
      handle: () => ({ status: 503, body: { error: 'WebAuthn not configured' } }),
    },
    {
      method: 'POST', path: '/setup/verify-token',
      handle: ({ body }) => body.token === site.setup ? { valid: true } : { status: 403, body: { error: 'Invalid setup token' } },
    },
    {
      // Mirrors the /setup/complete contract: every new group optional, all-or-nothing validation,
      // "<field>: <message>" errors, and `applied` true only for groups that were supplied and stored.
      method: 'POST', path: '/setup/complete',
      handle: ({ body }) => {
        const bad = (error: string) => ({ status: 400, body: { error } })
        if (body.token !== site.setup) return { status: 403, body: { error: 'Invalid setup token' } }
        if (!body.username || !body.password) return bad('username: Username and password are required')
        if (String(body.password).length < 8) return bad('password: must be at least 8 characters')
        const st = body.station, ad = body.address, ap = body.appearance, au = body.audience, mo = body.mount
        if (st) {
          if (String(st.title ?? '').length > 80) return bad('station.title: at most 80 characters')
          if (String(st.subtitle ?? '').length > 160) return bad('station.subtitle: at most 160 characters')
          if (String(st.location ?? '').length > 80) return bad('station.location: at most 80 characters')
          if (st.admin_email && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(st.admin_email)) return bad('station.admin_email: not a valid email address')
        }
        if (ad) {
          if (ad.hostname && (/[/\s:]/.test(ad.hostname) || ad.hostname.length > 253)) return bad('address.hostname: use a host name or IP address only, without scheme, port or path')
          if (ad.base_url && !/^https?:\/\/[^/\s]+$/.test(ad.base_url)) return bad('address.base_url: must be an http or https address without a path or trailing slash')
        }
        if (ap && !/^#[0-9a-fA-F]{6}$/.test(ap.accent_color ?? '')) return bad('appearance.accent_color: must look like #rrggbb')
        if (au && !(Number.isInteger(au.max_listeners) && au.max_listeners >= 0 && au.max_listeners <= 1000000)) return bad('audience.max_listeners: must be between 0 and 1000000')
        const path = mo?.path ?? '/live'
        if (!/^\/[A-Za-z0-9._-]{1,64}$/.test(path)) return bad('mount.path: use 1 to 64 letters, digits, dots, dashes or underscores after the slash')
        if (['/admin', '/api', '/login', '/setup', '/events', '/explore'].includes(path)) return bad(`mount.path: ${path} is reserved`)
        return {
          success: true, default_source_pass: 'a3f19c0b7d24', live_mount_pass: '5e8d02c1f6a9', mount: path,
          applied: { station: !!st, address: !!ad, appearance: !!ap, audience: !!au },
        }
      },
    },
    { method: 'POST', path: '/api/passkey/register/begin', handle: () => ({ status: 503, body: { error: 'WebAuthn not configured' } }) },
  ],
  pages: {
    get landing() { return { ...base(), streams: site.n ? streams() : null } },
    get explore() { return { ...base(), streams: site.n ? streams() : null } },
    get developers() { return base() },
    get login() { return base() },
    get setup() { return base() },
  } as Record<string, Record<string, unknown>>,
}
export const pages = fixture.pages
export default fixture.default
