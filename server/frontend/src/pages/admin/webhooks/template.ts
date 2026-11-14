import type { EventInfo } from './types'

/**
 * Client-side approximation of the server's webhook renderer
 * (server/webhook.go renderWebhookBody): Go text/template with the funcs
 * urlencode, json, lower, upper. Supports {{.Field}}, {{.}}, func calls with
 * one argument, and if / else / end. Anything else is reported as
 * unsupported so the preview never claims more than it evaluated.
 */

type Node =
  | { t: 'text'; v: string }
  | { t: 'expr'; fn?: string; arg: string }
  | { t: 'if'; cond: string; then: Node[]; else: Node[] }

export class TemplateError extends Error {}
export class Unsupported extends Error {}

const KNOWN_FUNCS = new Set(['urlencode', 'json', 'lower', 'upper'])

function tokenize(src: string): { text?: string; action?: string }[] {
  const out: { text?: string; action?: string }[] = []
  let i = 0
  while (i < src.length) {
    const open = src.indexOf('{{', i)
    if (open < 0) { out.push({ text: src.slice(i) }); break }
    let text = src.slice(i, open)
    const close = src.indexOf('}}', open + 2)
    if (close < 0) throw new TemplateError('unclosed action: "{{" has no matching "}}"')
    let action = src.slice(open + 2, close)
    if (action.startsWith('- ')) { text = text.replace(/\s+$/, ''); action = action.slice(2) }
    let trimNext = false
    if (action.endsWith(' -')) { trimNext = true; action = action.slice(0, -2) }
    if (text) out.push({ text })
    out.push({ action: action.trim() })
    i = close + 2
    if (trimNext) while (i < src.length && /\s/.test(src[i])) i++
  }
  return out
}

export function parseTemplate(src: string): Node[] {
  const toks = tokenize(src)
  let pos = 0
  function block(stop: boolean): { nodes: Node[]; end: 'end' | 'else' | null } {
    const nodes: Node[] = []
    while (pos < toks.length) {
      const tk = toks[pos++]
      if (tk.text !== undefined) { nodes.push({ t: 'text', v: tk.text }); continue }
      const a = tk.action!
      if (a === 'end') { if (!stop) throw new TemplateError('unexpected {{end}}'); return { nodes, end: 'end' } }
      if (a === 'else') { if (!stop) throw new TemplateError('unexpected {{else}}'); return { nodes, end: 'else' } }
      if (a.startsWith('if ')) {
        const cond = a.slice(3).trim()
        if (!/^\.[A-Za-z0-9_]*$/.test(cond)) throw new Unsupported(`condition "${cond}"`)
        const then = block(true)
        let els: Node[] = []
        if (then.end === 'else') {
          const e = block(true)
          if (e.end !== 'end') throw new TemplateError('{{if}} is missing {{end}}')
          els = e.nodes
        } else if (then.end !== 'end') throw new TemplateError('{{if}} is missing {{end}}')
        nodes.push({ t: 'if', cond, then: then.nodes, else: els })
        continue
      }
      const parts = a.split(/\s+/)
      if (parts.length === 1 && /^\.[A-Za-z0-9_]*$/.test(parts[0])) { nodes.push({ t: 'expr', arg: parts[0] }); continue }
      if (parts.length === 2 && KNOWN_FUNCS.has(parts[0]) && /^\.[A-Za-z0-9_]*$/.test(parts[1])) { nodes.push({ t: 'expr', fn: parts[0], arg: parts[1] }); continue }
      throw new Unsupported(`action "{{${a}}}"`)
    }
    if (stop) throw new TemplateError('{{if}} is missing {{end}}')
    return { nodes, end: null }
  }
  return block(false).nodes
}

function lookup(ctx: Record<string, unknown>, ref: string): { found: boolean; value: unknown } {
  if (ref === '.') return { found: true, value: ctx }
  const k = ref.slice(1)
  return k in ctx ? { found: true, value: ctx[k] } : { found: false, value: undefined }
}
const truthy = (v: unknown) => !(v === undefined || v === null || v === '' || v === 0 || v === false)
const goString = (v: unknown) => (typeof v === 'object' && v !== null ? JSON.stringify(v) : String(v))
/** Go url.QueryEscape: spaces become "+" and !'()* are escaped. */
const queryEscape = (s: string) => encodeURIComponent(s).replace(/%20/g, '+').replace(/[!'()*]/g, (c) => '%' + c.charCodeAt(0).toString(16).toUpperCase())

function run(nodes: Node[], ctx: Record<string, unknown>): string {
  let out = ''
  for (const n of nodes) {
    if (n.t === 'text') out += n.v
    else if (n.t === 'if') out += run(truthy(lookup(ctx, n.cond).value) ? n.then : n.else, ctx)
    else {
      const r = lookup(ctx, n.arg)
      if (!n.fn) { out += r.found ? goString(r.value) : '<no value>'; continue }
      if (n.fn === 'json') out += JSON.stringify(r.found ? r.value : null)
      else {
        const s = r.found ? goString(r.value) : '<no value>'
        out += n.fn === 'lower' ? s.toLowerCase() : n.fn === 'upper' ? s.toUpperCase() : queryEscape(s)
      }
    }
  }
  return out
}

export type Rendered = { ok: true; text: string } | { ok: false; kind: 'error' | 'unsupported'; message: string }

export function renderTemplate(src: string, ctx: Record<string, unknown>): Rendered {
  try { return { ok: true, text: run(parseTemplate(src), ctx) } }
  catch (e) {
    if (e instanceof Unsupported) return { ok: false, kind: 'unsupported', message: `Preview cannot evaluate ${e.message}; the server will.` }
    if (e instanceof TemplateError) return { ok: false, kind: 'error', message: e.message }
    throw e
  }
}

export const snakeToCamel = (s: string) => s.split('_').map((p) => (p ? p[0].toUpperCase() + p.slice(1) : p)).join('')

export interface PreviewEnv { hostname: string; baseUrl: string; version: string }

/** Template context for an event, built like renderWebhookBody: globals, MountURL/PlayerURL, payload keys raw and CamelCased. */
export function buildContext(ev: EventInfo, env: PreviewEnv, now = new Date()): Record<string, unknown> {
  const iso = now.toISOString().replace(/\.\d+Z$/, 'Z')
  const base = env.baseUrl.replace(/\/+$/, '')
  const ctx: Record<string, unknown> = {
    Event: ev.name, Timestamp: iso, UnixTimestamp: Math.floor(now.getTime() / 1000),
    Date: iso.slice(0, 10), Time: iso.slice(11, 19), Hostname: env.hostname, BaseURL: base, Version: env.version,
  }
  const mount = ev.sample.mount
  if (base && typeof mount === 'string' && mount) {
    const p = mount.startsWith('/') ? mount : '/' + mount
    ctx.MountURL = base + p
    ctx.PlayerURL = base + '/player' + p
  }
  for (const [k, v] of Object.entries(ev.sample)) { ctx[k] = v; ctx[snakeToCamel(k)] = v }
  return ctx
}

function sortKeys(v: unknown): unknown {
  if (Array.isArray(v)) return v.map(sortKeys)
  if (v && typeof v === 'object') return Object.fromEntries(Object.entries(v as object).sort(([a], [b]) => (a < b ? -1 : 1)).map(([k, x]) => [k, sortKeys(x)]))
  return v
}

export interface RequestPreview {
  method: string
  url: string
  headers: [string, string][]
  body: string
  notes: string[]
  problem?: { kind: 'error' | 'unsupported'; message: string }
}

/** What deliverWebhook would send for this event. Mirrors the GET/HEAD query-append rule and the default envelope. */
export function previewRequest(
  w: { id?: string; url: string; method: string; content_type: string; headers: Record<string, string>; body_template: string },
  ev: EventInfo, env: PreviewEnv,
): RequestPreview {
  const ctx = buildContext(ev, env)
  const method = (w.method || 'POST').toUpperCase()
  const notes: string[] = []
  if (!env.baseUrl) notes.push('No public base URL is set in Settings, so MountURL and PlayerURL are empty.')
  let body = ''
  let problem: RequestPreview['problem']
  if (!w.body_template.trim()) {
    body = JSON.stringify(sortKeys({ event: ev.name, timestamp: ctx.Timestamp, hostname: env.hostname, data: ev.sample }), null, 2)
    notes.push('Default envelope. The server sends it compact; shown indented here.')
  } else {
    const r = renderTemplate(w.body_template, ctx)
    if (r.ok) body = r.text; else problem = { kind: r.kind, message: r.message }
  }
  let url = w.url
  const isQuery = method === 'GET' || method === 'HEAD'
  if (isQuery) {
    const frag = body.trim()
    if (frag && w.body_template) url += (url.includes('?') ? '&' : '?') + frag
    body = ''
    notes.push('GET and HEAD carry no body; a template is appended to the URL as a query string.')
  }
  const headers: [string, string][] = [
    ['Content-Type', w.content_type || 'application/json'], ['User-Agent', 'TinyIce-Webhook/2.0'],
    ['X-TinyIce-Event', ev.name], ['X-TinyIce-Webhook-ID', w.id || '(assigned on save)'],
  ]
  for (const [k, v] of Object.entries(w.headers)) {
    if (!k) continue
    if (v.includes('{{')) { const r = renderTemplate(v, ctx); headers.push([k, r.ok ? r.text : v]) } else headers.push([k, v])
  }
  return { method, url, headers, body, notes, problem }
}

const SENSITIVE = /auth|token|secret|key|passw|signature|cookie/i
export const isSensitiveHeader = (name: string) => SENSITIVE.test(name)

/** Hide credentials embedded in a webhook URL (Discord/Slack tokens live in the path). */
export function maskUrl(u: string): string {
  try {
    const p = new URL(u)
    const segs = p.pathname.split('/').map((s) => (s.length >= 16 && (/\d/.test(s) || s.length >= 24) ? s.slice(0, 4) + '…' : s))
    return p.host + segs.join('/') + (p.search ? '?…' : '')
  } catch { return u }
}

/** True for hosts the server's outbound guard refuses (loopback, private, link-local). */
export function isPrivateHost(host: string): boolean {
  const h = host.replace(/^\[|\]$/g, '').toLowerCase()
  if (h === 'localhost' || h.endsWith('.localhost') || h === '::1' || h === '0.0.0.0') return true
  const m = /^(\d+)\.(\d+)\.\d+\.\d+$/.exec(h)
  if (!m) return false
  const a = +m[1], b = +m[2]
  return a === 10 || a === 127 || (a === 169 && b === 254) || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168)
}
