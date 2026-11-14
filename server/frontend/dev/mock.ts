import type { Plugin, ViteDevServer } from 'vite'
import type { IncomingMessage, ServerResponse } from 'http'
import { readdirSync } from 'fs'
import { resolve } from 'path'
import { state } from './state'
import type { Fixture, MockCtx, MockEvent, MockResult, MockRoute } from './types'

/**
 * Dev-only mock backend. Serves fixtures with the same JSON shapes the Go
 * server returns (server/handlers_*.go, server/openapi.yaml) and injects the
 * same window.__TINYICE__ the Go ShellRenderer injects, so every page can be
 * built and live-reloaded without a server, a login or real encoders.
 *
 * Every file in dev/fixtures/*.ts is a fixture (see types.ts). They are
 * loaded through Vite's SSR loader, so editing one takes effect on the next
 * request with no restart. MOCK=0 bypasses all of this and proxies to :8000.
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

async function loadFixtures(server: ViteDevServer): Promise<Fixture[]> {
  const dir = resolve(__dirname, 'fixtures')
  const out: Fixture[] = []
  for (const f of readdirSync(dir).filter((n) => n.endsWith('.ts')).sort()) {
    try { out.push(await server.ssrLoadModule(`/dev/fixtures/${f}`) as Fixture) }
    catch (e) { server.config.logger.error(`[mock] fixture ${f} failed to load: ${(e as Error).message}`) }
  }
  return out
}

async function readBody(req: IncomingMessage): Promise<Record<string, any>> {
  if (req.method === 'GET' || req.method === 'HEAD') return {}
  const chunks: Buffer[] = []
  for await (const c of req) chunks.push(c as Buffer)
  const raw = Buffer.concat(chunks).toString('utf8')
  if (!raw) return {}
  const ct = req.headers['content-type'] ?? ''
  try {
    if (ct.includes('json')) return JSON.parse(raw)
    if (ct.includes('x-www-form-urlencoded')) return Object.fromEntries(new URLSearchParams(raw))
  } catch { /* fall through */ }
  return {}
}

function send(res: ServerResponse, r: MockResult | unknown) {
  // A {status, body, headers} envelope only when it is exactly that shape. A bare
  // payload such as {status: 'updated'} (string status) is a plain 200 body.
  const isResult = !!r && typeof r === 'object' && !Array.isArray(r)
    && Object.keys(r as object).every((k) => k === 'status' || k === 'body' || k === 'headers')
    && ('body' in (r as object) || typeof (r as MockResult).status === 'number')
  const { status = 200, body, headers } = (isResult ? r : { body: r }) as MockResult
  res.statusCode = status
  for (const [k, v] of Object.entries(headers ?? {})) res.setHeader(k, v)
  if (status === 204 || body === undefined) { res.end(); return }
  if (!res.hasHeader('Content-Type')) res.setHeader('Content-Type', 'application/json')
  res.end(typeof body === 'string' ? body : JSON.stringify(body))
}

function matchRoute(routes: MockRoute[], method: string, path: string) {
  for (const r of routes) {
    if (r.method && r.method !== method) continue
    if (typeof r.path === 'string') { if (r.path === path) return { r, params: {} } }
    else { const m = r.path.exec(path); if (m) return { r, params: { ...(m.groups ?? {}) } } }
  }
  return null
}

export function mockApi(): Plugin {
  let server: ViteDevServer
  return {
    name: 'tinyice-mock-api',
    apply: 'serve',
    configureServer(s) {
      server = s
      s.middlewares.use(async (req, res, next) => {
        const url = new URL(req.url ?? '/', 'http://x')
        const path = url.pathname
        const isApi = /^\/(api|admin\/(?!$)[\w-]+|auth|setup\/|login$|logout$|events$|status-json|webrtc|branding|metrics)/.test(path) && !path.includes('.') && !/^\/admin(\/(streams|autodj|golive|studio|relays|transcoders|users|pending|security|tokens|webhooks|settings))?\/?$/.test(path)
        if (!isApi && path !== '/admin/events') return next()
        const fixtures = await loadFixtures(server)

        if (path === '/admin/events') return sse(req, res, fixtures)

        const routes = fixtures.flatMap((f) => f.default ?? [])
        const hit = matchRoute(routes, req.method ?? 'GET', path)
        if (!hit) return send(res, { status: 404, body: { error: `mock: no fixture for ${req.method} ${path}` } })
        try {
          const ctx: MockCtx = { req, url, params: hit.params, body: await readBody(req) }
          send(res, await hit.r.handle(ctx))
        } catch (e) {
          send(res, { status: 500, body: { error: (e as Error).message } })
        }
      })
    },
    transformIndexHtml: {
      order: 'pre',
      async handler(html, ctx) {
        const name = /entries\/(\w+)\.html/.exec(ctx.path)?.[1] ?? 'admin'
        const fixtures = await loadFixtures(server)
        const data = Object.assign({ ...base }, ...fixtures.map((f) => f.pages?.[name] ?? {}))
        return html.replace('<div id="app"></div>', `<script>window.__TINYICE__=${JSON.stringify(data).replace(/</g, '\\u003c')}</script>\n<div id="app"></div>`)
      },
    },
  }
}

function sse(req: IncomingMessage, res: ServerResponse, fixtures: Fixture[]) {
  res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache', Connection: 'keep-alive' })
  let n = 0
  const frame = (e: MockEvent) => res.write(`event: ${e.event}\ndata: ${JSON.stringify(e.data)}\n\n`)
  const tick = () => {
    n++
    const wob = (amp: number, p: number) => Math.sin(n / p) * amp + (Math.random() - 0.5) * amp * 0.4
    const streams = state.sources.map((s) => ({ ...s, listeners: Math.max(0, Math.round(s.base + wob(s.base * 0.12, 9))), health: Math.max(0, Math.min(100, Math.round(s.health + wob(2, 3)))) }))
    const listeners = streams.reduce((a, s) => a + s.listeners, 0)
    const out = streams.reduce((a, s) => a + s.listeners * s.bitrate * 125, 0)
    frame({ event: 'stats', data: { listeners, streams: streams.length + 2, bandwidth: out + 190_000, bandwidth_in: 190_000 + wob(8000, 5), bandwidth_out: out, uptime: Math.floor((Date.now() - state.startedAt) / 1000), goroutines: 212 + (n % 7), memory: 61_000_000 + n * 900, gc: 340 + Math.floor(n / 20) } })
    for (const s of streams) frame({ event: 'stream', data: s })
    for (const f of fixtures) {
      try { for (const e of f.events?.(n) ?? []) frame(e) }
      catch (err) { console.error(`[mock] fixture events() threw: ${(err as Error).message}`) }
    }
  }
  tick()
  const id = setInterval(tick, 1000)
  req.on('close', () => clearInterval(id))
}
