import type { Plugin } from 'vite'
import type { IncomingMessage, ServerResponse } from 'http'

/**
 * Dev-only fixtures so the UI can be built and reviewed without a Go server,
 * a login, or real encoders. It serves the same shapes the real endpoints do
 * (see server/handlers_api.go and server/openapi.yaml) and injects the same
 * window.__TINYICE__ the Go ShellRenderer injects. Run with MOCK=0 to proxy
 * to a real server on :8000 instead.
 */
const base = {
  csrfToken: 'dev-csrf',
  version: '2.13.0-dev',
  pageTitle: 'TinyIce',
  pageSubtitle: 'Self-hosted streaming',
  branding: { logoUrl: null, accentColor: '#ff6a00', landingMarkdown: '' },
  passkeysEnabled: true,
  oidcProviders: [{ id: 'google', name: 'Google', icon: 'google' }],
}
const mounts = ['/live', '/radio', '/jazz', '/talk']
const pageData: Record<string, object> = {
  admin: { ...base, user: { username: 'admin', role: 'superadmin' }, mounts },
}

const sources = [
  { mount: '/live', title: 'Morning Show', artist: 'DJ Orbit', format: 'audio/mpeg', bitrate: 128, base: 142, health: 98, has_video: true },
  { mount: '/radio', title: 'Blue in Green', artist: 'Miles Davis', format: 'audio/mpeg', bitrate: 192, base: 88, health: 100 },
  { mount: '/jazz', title: 'Take Five', artist: 'Dave Brubeck', format: 'audio/ogg; codecs=opus', bitrate: 96, base: 31, health: 64 },
  { mount: '/talk', title: 'Evening Briefing', artist: '', format: 'audio/aac', bitrate: 64, base: 12, health: 100, is_transcoded: true, source_mount: '/live', source_type: 'audio/mpeg', source_bitrate: '128' },
]
const t0 = Date.now() - 3 * 86400_000 - 4 * 3600_000

function sse(req: IncomingMessage, res: ServerResponse) {
  res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache', Connection: 'keep-alive' })
  let n = 0
  const tick = () => {
    n++
    const wob = (amp: number, p: number) => Math.sin(n / p) * amp + (Math.random() - 0.5) * amp * 0.4
    const streams = sources.map((s) => ({ ...s, listeners: Math.max(0, Math.round(s.base + wob(s.base * 0.12, 9))), health: Math.max(0, Math.min(100, Math.round(s.health + wob(2, 3)))) }))
    const listeners = streams.reduce((a, s) => a + s.listeners, 0)
    const out = streams.reduce((a, s) => a + s.listeners * s.bitrate * 125, 0)
    res.write(`event: stats\ndata: ${JSON.stringify({ listeners, streams: 6, bandwidth: out + 190_000, bandwidth_in: 190_000 + wob(8000, 5), bandwidth_out: out, uptime: Math.floor((Date.now() - t0) / 1000), goroutines: 212 + (n % 7), memory: 61_000_000 + n * 900, gc: 340 + Math.floor(n / 20) })}\n\n`)
    for (const s of streams) res.write(`event: stream\ndata: ${JSON.stringify(s)}\n\n`)
  }
  tick()
  const id = setInterval(tick, 1000)
  req.on('close', () => clearInterval(id))
}

export function mockApi(): Plugin {
  return {
    name: 'tinyice-mock-api',
    apply: 'serve',
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        const path = (req.url ?? '').split('?')[0]
        if (path === '/admin/events') return sse(req, res)
        if (path === '/api/streams') { res.setHeader('Content-Type', 'application/json'); return void res.end(JSON.stringify(sources)) }
        next()
      })
    },
    transformIndexHtml: {
      order: 'pre',
      handler(html, ctx) {
        const name = /entries\/(\w+)\.html/.exec(ctx.path)?.[1] ?? 'admin'
        const data = pageData[name] ?? { ...base }
        return html.replace('<div id="app"></div>', `<script>window.__TINYICE__=${JSON.stringify(data)}</script>\n<div id="app"></div>`)
      },
    },
  }
}
