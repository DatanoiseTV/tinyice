import type { Fixture, MockCtx } from '../types'

/**
 * Mock for the Access area: users, pending sign-ups, bans, allow list,
 * lockouts, audit log and API tokens. Shapes and status codes mirror
 * server/handlers_api_v2.go, handlers_pending_users.go and
 * handlers_admin.go. Notably the real server does NOT validate IP strings or
 * role names, so neither does this mock: the UI has to.
 */

const H = 3600_000
const ago = (ms: number) => new Date(Date.now() - ms).toISOString()
const rfc = (ms: number) => new Date(Date.now() - ms).toISOString().replace(/\.\d+Z$/, 'Z')

interface MUser { username: string; role: string; mounts: string[] }
interface MPending { id: string; email: string; name: string; provider: string; requested_at: string; denied_at?: string }
interface MToken { id: string; name: string; username: string; created_at: string; last_used_at: string; last_used_ip: string; expires_at: string; prefix: string }
interface MAudit { id: number; timestamp: string; username: string; action: string; resource_type: string; resource_id: string; detail: string; ip: string }
interface MStat { ip: string; count: number; locked: boolean; expires_in: string }

const CATS: Record<string, { exact?: string[]; prefixes?: string[] }> = {
  auth: { exact: ['login', 'logout', 'login_failed', 'setup_complete', 'passkey_registered'], prefixes: ['token_'] },
  streams: { exact: ['stream_kicked', 'listeners_kicked_all'], prefixes: ['mount_'] },
  autodj: { prefixes: ['autodj_'] }, relays: { prefixes: ['relay_'] }, transcoders: { prefixes: ['transcoder_'] },
  webhooks: { prefixes: ['webhook_'] }, users: { prefixes: ['user_', 'pending_'] }, security: { prefixes: ['ip_'] },
  settings: { prefixes: ['settings_', 'branding_', 'logo_'] },
}
const inCat = (action: string, cat: string) => {
  const r = CATS[cat]
  return !!r && (r.exact?.includes(action) || r.prefixes?.some((p) => action.startsWith(p)) || false)
}

function seedAudit(): MAudit[] {
  const rows: Omit<MAudit, 'id'>[] = []
  const add = (h: number, username: string, action: string, resource_type: string, resource_id = '', detail = '', ip = '203.0.113.10') =>
    rows.push({ timestamp: ago(h * H), username, action, resource_type, resource_id, detail, ip })
  add(0.1, 'admin', 'login', 'auth', 'admin', '', '203.0.113.10')
  add(0.3, 'admin', 'mount_updated', 'stream', '/live', '', '203.0.113.10')
  add(1.2, 'maria', 'login', 'user', 'maria', 'passkey', '198.51.100.24')
  add(1.4, 'system', 'login_failed', 'auth', 'root', 'invalid password', '185.220.101.7')
  add(1.4, 'system', 'login_failed', 'auth', 'root', 'invalid password', '185.220.101.7')
  add(1.5, 'system', 'login_failed', 'auth', 'admin', 'invalid password', '185.220.101.7')
  add(2.1, 'admin', 'ip_banned', 'security', '185.220.101.0/24', '', '203.0.113.10')
  add(3.5, 'admin', 'token_created', 'auth', 'ci-deploy', '', '203.0.113.10')
  add(5, 'maria', 'autodj_updated', 'autodj', 'Weekday mix', '', '198.51.100.24')
  add(8, 'admin', 'user_created', 'user', 'tobias', 'dj', '203.0.113.10')
  add(9, 'admin', 'pending_approved', 'user', 'tobias', 'tobias@radio.example', '203.0.113.10')
  add(14, 'admin', 'settings_updated', 'settings', 'security', 'trusted_proxies', '203.0.113.10')
  add(20, 'admin', 'webhook_created', 'webhook', 'Discord notifier', '', '203.0.113.10')
  add(26, 'admin', 'logout', 'auth', 'admin', '', '203.0.113.10')
  add(27, 'admin', 'token_revoked', 'auth', '9c1f0a22b7d34e51', '', '203.0.113.10')
  add(30, 'admin', 'user_updated', 'user', 'maria', 'role admin', '203.0.113.10')
  add(34, 'maria', 'mount_created', 'stream', '/jazz', '', '198.51.100.24')
  add(40, 'admin', 'ip_whitelisted', 'security', '10.0.0.0/8', '', '203.0.113.10')
  add(52, 'admin', 'transcoder_created', 'transcoder', 'live-to-opus', '', '203.0.113.10')
  add(60, 'admin', 'pending_denied', 'user', 'spam@mailinator.example', '', '203.0.113.10')
  add(75, 'admin', 'user_deleted', 'user', 'old-dj', '', '203.0.113.10')
  add(98, 'admin', 'relay_deleted', 'relay', 'backup-relay', '', '203.0.113.10')
  add(120, 'admin', 'branding_updated', 'branding', '', 'accent colour', '203.0.113.10')
  add(150, 'admin', 'passkey_registered', 'user', 'admin', '', '203.0.113.10')
  add(190, 'admin', 'mount_disabled', 'stream', '/talk', '', '203.0.113.10')
  add(260, 'admin', 'setup_complete', 'server', 'setup', '', '203.0.113.10')
  return rows.sort((a, b) => b.timestamp.localeCompare(a.timestamp)).map((r, i) => ({ ...r, id: 1000 - i }))
}

const S = ((globalThis as any).__accessMock ??= {
  users: [
    { username: 'admin', role: 'superadmin', mounts: [] },
    { username: 'maria', role: 'admin', mounts: ['/jazz', '/radio'] },
    { username: 'tobias', role: 'dj', mounts: ['/live'] },
    { username: 'night-shift', role: 'dj', mounts: ['/talk', '/live', '/radio', '/jazz'] },
  ] as MUser[],
  pending: [
    { id: 'p_7f3a', email: 'lena.hoffmann@stadtfunk.example', name: 'Lena Hoffmann', provider: 'google', requested_at: ago(3 * H) },
    { id: 'p_91bc', email: 'dev@pirate-fm.example', name: '', provider: 'github', requested_at: ago(26 * H) },
    { id: 'p_c20d', email: 'k.okafor@campus.example', name: 'Kelechi Okafor', provider: 'oidc', requested_at: ago(5 * 24 * H) },
  ] as MPending[],
  bans: ['185.220.101.0/24', '198.51.100.77', '2001:db8:bad::/48'] as string[],
  whitelist: ['10.0.0.0/8'] as string[],
  tokens: [
    { id: 'dd8a01b05d0eeb7a', name: 'ci-deploy', username: 'admin', created_at: rfc(3.5 * H), last_used_at: rfc(0.2 * H), last_used_ip: '203.0.113.55', expires_at: new Date(Date.now() + 60 * 24 * H).toISOString(), prefix: 'ti_dd8a...' },
    { id: '4be2f19a77c0d3e8', name: 'now-playing-bridge', username: 'maria', created_at: rfc(40 * 24 * H), last_used_at: rfc(2 * 24 * H), last_used_ip: '198.51.100.24', expires_at: '', prefix: 'ti_4be2...' },
    { id: '1aa09c55e2b6f740', name: 'old-script', username: 'admin', created_at: rfc(200 * 24 * H), last_used_at: '', last_used_ip: '', expires_at: new Date(Date.now() - 10 * 24 * H).toISOString(), prefix: 'ti_1aa0...' },
    { id: '8c3d27f1a09be654', name: 'grafana', username: 'admin', created_at: rfc(9 * 24 * H), last_used_at: rfc(12 * 60_000), last_used_ip: '10.0.0.12', expires_at: new Date(Date.now() + 6 * 24 * H).toISOString(), prefix: 'ti_8c3d...' },
  ] as MToken[],
  audit: seedAudit(),
  nextAudit: 2000,
  authFails: [
    { ip: '185.220.101.7', count: 14, locked: true, expires_in: '11m42s' },
    { ip: '45.155.205.233', count: 5, locked: true, expires_in: '3m9s' },
    { ip: '198.51.100.24', count: 2, locked: false, expires_in: '0s' },
  ] as MStat[],
  scanners: [
    { ip: '92.118.39.14', count: 37, locked: false, expires_in: '0s' },
    { ip: '141.98.11.22', count: 9, locked: true, expires_in: '8m31s' },
  ] as MStat[],
})

const err = (status: number, error: string) => ({ status, body: { error } })
/** The signed-in mock user is always the page's `admin` superadmin. */
const ME = 'admin'

function audit(username: string, action: string, resource_type: string, resource_id: string, detail = '') {
  S.audit.unshift({ id: S.nextAudit++, timestamp: new Date().toISOString(), username, action, resource_type, resource_id, detail, ip: '127.0.0.1' })
}
/** Real mutating /api routes require JSON; mimic the CSRF 403 for anything else. */
const jsonOnly = (c: MCtx) => (String(c.req.headers['content-type'] ?? '').includes('json') ? null : err(403, 'Forbidden'))
type MCtx = MockCtx

const fixture: Fixture = {
  default: [
    { method: 'GET', path: '/api/users', handle: () => [...S.users].reverse() },
    { method: 'POST', path: '/api/users', handle: (c) => {
      const bad = jsonOnly(c); if (bad) return bad
      const { username, password, role } = c.body
      if (!username || !password) return err(400, 'Username and password are required')
      S.users.push({ username, role: role || 'admin', mounts: [] })
      audit(ME, 'user_created', 'user', username, role || 'admin')
      return { status: 'created', username }
    } },
    { method: 'PUT', path: '/api/users', handle: (c) => {
      const bad = jsonOnly(c); if (bad) return bad
      const u = S.users.find((x: MUser) => x.username === c.body.username)
      if (!c.body.username) return err(400, 'Username is required')
      if (!u) return err(404, 'User not found')
      if (c.body.role) u.role = c.body.role
      audit(ME, 'user_updated', 'user', u.username)
      return { status: 'updated' }
    } },
    { method: 'DELETE', path: '/api/users', handle: (c) => {
      const bad = jsonOnly(c); if (bad) return bad
      const name = c.url.searchParams.get('username')
      if (!name) return err(400, 'Username is required')
      if (name === ME) return err(400, 'Cannot delete yourself')
      if (!S.users.some((x: MUser) => x.username === name)) return err(404, 'User not found')
      S.users = S.users.filter((x: MUser) => x.username !== name)
      audit(ME, 'user_deleted', 'user', name)
      return { status: 'deleted' }
    } },

    { method: 'GET', path: '/api/pending-users', handle: () => S.pending.filter((p: MPending) => !p.denied_at) },
    { method: 'POST', path: '/api/pending-users/approve', handle: (c) => {
      const bad = jsonOnly(c); if (bad) return bad
      const { id, username, role } = c.body
      if (!username) return err(400, 'Username is required')
      const r = role || 'dj'
      if (!['superadmin', 'admin', 'dj'].includes(r)) return err(400, 'Invalid role')
      const p = S.pending.find((x: MPending) => x.id === id)
      if (!p) return err(404, 'Pending user not found')
      if (S.users.some((u: MUser) => u.username === username)) return err(409, 'Username already taken')
      S.users.push({ username, role: r, mounts: [] })
      S.pending = S.pending.filter((x: MPending) => x.id !== id)
      audit(ME, 'pending_approved', 'user', username, p.email)
      return { success: true, username }
    } },
    { method: 'POST', path: '/api/pending-users/deny', handle: (c) => {
      const bad = jsonOnly(c); if (bad) return bad
      const p = S.pending.find((x: MPending) => x.id === c.body.id)
      if (!p) return err(404, 'Pending user not found')
      p.denied_at = new Date().toISOString()
      audit(ME, 'pending_denied', 'user', p.email)
      return { success: true }
    } },

    { method: 'GET', path: '/api/security/bans', handle: () => S.bans },
    { method: 'POST', path: '/api/security/bans', handle: (c) => {
      const bad = jsonOnly(c); if (bad) return bad
      if (!c.body.ip) return err(400, 'IP is required')
      if (!S.bans.includes(c.body.ip)) S.bans.push(c.body.ip)
      audit(ME, 'ip_banned', 'security', c.body.ip)
      return { status: 'added', ip: c.body.ip }
    } },
    { method: 'DELETE', path: '/api/security/bans', handle: (c) => {
      const bad = jsonOnly(c); if (bad) return bad
      const ip = c.url.searchParams.get('ip'); if (!ip) return err(400, 'IP is required')
      S.bans = S.bans.filter((b: string) => b !== ip)
      audit(ME, 'ip_unbanned', 'security', ip)
      return { status: 'removed', ip }
    } },
    { method: 'GET', path: '/api/security/whitelist', handle: () => S.whitelist },
    { method: 'POST', path: '/api/security/whitelist', handle: (c) => {
      const bad = jsonOnly(c); if (bad) return bad
      if (!c.body.ip) return err(400, 'IP is required')
      if (!S.whitelist.includes(c.body.ip)) S.whitelist.push(c.body.ip)
      audit(ME, 'ip_whitelisted', 'security', c.body.ip)
      return { status: 'added', ip: c.body.ip }
    } },
    { method: 'DELETE', path: '/api/security/whitelist', handle: (c) => {
      const bad = jsonOnly(c); if (bad) return bad
      const ip = c.url.searchParams.get('ip'); if (!ip) return err(400, 'IP is required')
      S.whitelist = S.whitelist.filter((b: string) => b !== ip)
      audit(ME, 'ip_unwhitelisted', 'security', ip)
      return { status: 'removed', ip }
    } },
    { method: 'GET', path: '/api/security/audit', handle: (c) => {
      const page = Math.max(1, parseInt(c.url.searchParams.get('page') ?? '1', 10) || 1)
      const limit = Math.min(100, parseInt(c.url.searchParams.get('limit') ?? '25', 10) || 25)
      const cat = c.url.searchParams.get('category') ?? ''
      let rows: MAudit[] = S.audit
      if (cat) rows = CATS[cat] ? rows.filter((r) => inCat(r.action, cat)) : []
      return { entries: rows.slice((page - 1) * limit, page * limit), total: rows.length, page, limit }
    } },

    { method: 'GET', path: '/admin/security-stats', handle: () => ({ auth_fails: S.authFails, scanners: S.scanners }) },
    { method: 'POST', path: '/admin/clear-auth-lockout', handle: (c) => { S.authFails = S.authFails.filter((s: MStat) => s.ip !== c.body.ip); return '' } },
    { method: 'POST', path: '/admin/clear-scan-lockout', handle: (c) => { S.scanners = S.scanners.filter((s: MStat) => s.ip !== c.body.ip); return '' } },

    { method: 'GET', path: '/api/tokens', handle: () => S.tokens },
    { method: 'POST', path: '/api/tokens', handle: (c) => {
      const bad = jsonOnly(c); if (bad) return bad
      if (!c.body.name) return err(400, 'Name is required')
      const raw = 'ti_' + Array.from({ length: 64 }, () => Math.floor(Math.random() * 16).toString(16)).join('')
      const id = raw.slice(3, 19)
      S.tokens.push({ id, name: c.body.name, username: ME, created_at: rfc(0), last_used_at: '', last_used_ip: '', expires_at: c.body.expires_at ?? '', prefix: `ti_${raw.slice(3, 7)}...` })
      audit(ME, 'token_created', 'auth', c.body.name)
      return { id, token: raw, name: c.body.name }
    } },
    { method: 'DELETE', path: '/api/tokens', handle: (c) => {
      const bad = jsonOnly(c); if (bad) return bad
      const id = c.url.searchParams.get('id'); if (!id) return err(400, 'ID is required')
      if (!S.tokens.some((t: MToken) => t.id === id)) return err(404, 'Token not found')
      S.tokens = S.tokens.filter((t: MToken) => t.id !== id)
      audit(ME, 'token_revoked', 'auth', id)
      return { status: 'deleted' }
    } },
  ],
}
export default fixture.default
