import { parseHex } from '@/pages/admin/settings/contrast'

/** Everything the wizard asks, except secrets (token, password) which are never stored. */
export interface Answers {
  mode: 'quick' | 'guided'
  title: string
  subtitle: string
  location: string
  email: string
  hostname: string
  baseUrl: string
  accent: string
  audience: AudienceId
  directory: boolean
  mount: string
  username: string
}

export type StepId = 'token' | 'station' | 'address' | 'look' | 'audience' | 'mount' | 'account' | 'review'
export const GUIDED: StepId[] = ['token', 'station', 'address', 'look', 'audience', 'mount', 'account', 'review']
export const QUICK: StepId[] = ['token', 'account']
export const STEP_LABEL: Record<StepId, string> = {
  token: 'Token', station: 'Station', address: 'Address', look: 'Look', audience: 'Audience', mount: 'Stream', account: 'Admin', review: 'Review',
}

export type AudienceId = 'friends' | 'small' | 'busy' | 'event' | 'unlimited'
export const AUDIENCE: { id: AudienceId; label: string; max: number; why: string }[] = [
  { id: 'friends', label: 'Just friends', max: 25, why: 'A private or test station. Keeps a stray link from using your upload.' },
  { id: 'small', label: 'Small station', max: 100, why: 'A hobby or community station with a regular audience.' },
  { id: 'busy', label: 'Busy station', max: 500, why: 'A popular station, if your connection can carry it.' },
  { id: 'event', label: 'Big event', max: 2000, why: 'A one-off broadcast. Check your bandwidth first.' },
  { id: 'unlimited', label: 'Unlimited', max: 0, why: 'No cap. Your connection is the only limit.' },
]

/** About 16 KB/s per listener at 128 kbps (128 / 8). */
export function bandwidth(listeners: number, kbps = 128): string {
  const kBs = listeners * (kbps / 8)
  return kBs >= 1024 ? `${(kBs / 1024).toFixed(1)} MB/s` : `${Math.round(kBs)} KB/s`
}

export const SWATCHES = ['#ff6a00', '#e5484d', '#f5a623', '#2fbf71', '#12a594', '#3e8bff', '#6e56cf', '#e93d82']
export const isLocalHost = (h: string) => h === 'localhost' || h === '127.0.0.1' || h === '[::1]' || h === '::1' || h.endsWith('.localhost')

export function defaults(): Answers {
  return {
    mode: 'guided', title: '', subtitle: '', location: '', email: '',
    hostname: location.hostname, baseUrl: location.origin,
    accent: '#ff6a00', audience: 'small', directory: false, mount: '/live', username: 'admin',
  }
}

const KEY = 'tinyice-setup'
/** Answers and the furthest step reached survive a reload. Never put a secret in here. */
export function load(): { a: Answers; step: StepId } {
  try {
    const raw = JSON.parse(sessionStorage.getItem(KEY) ?? 'null')
    if (raw && raw.a) return { a: { ...defaults(), ...raw.a }, step: raw.step ?? 'token' }
  } catch { /* storage may be unavailable */ }
  return { a: defaults(), step: 'token' }
}
export function save(a: Answers, step: StepId) {
  try { sessionStorage.setItem(KEY, JSON.stringify({ a, step })) } catch { /* ignore */ }
}
export function clearSaved() { try { sessionStorage.removeItem(KEY) } catch { /* ignore */ } }

/* ---------- validation (the server re-checks everything; these give the message early) ---------- */
const RESERVED = ['/admin', '/api', '/login', '/logout', '/setup', '/explore', '/developers', '/player', '/embed', '/events', '/webrtc', '/kiosk', '/assets', '/branding', '/metrics', '/auth', '/status-json.xsl', '/player-webrtc']
export function normalizeMount(raw: string) { const v = raw.trim(); return v.startsWith('/') ? v : `/${v}` }
export function checkMount(raw: string): string | null {
  const m = normalizeMount(raw)
  if (m === '/') return 'Enter a mount path such as /live.'
  if (!/^\/[A-Za-z0-9._-]{1,64}$/.test(m)) return 'Use 1 to 64 letters, digits, dots, dashes or underscores after the slash. No spaces and no further slashes.'
  if (RESERVED.includes(m.toLowerCase())) return `${m} is used by TinyIce itself. Pick another name.`
  return null
}
export function checkEmail(v: string): string | null {
  return v === '' || /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(v) ? null : 'That does not look like an email address.'
}
export function checkHostname(v: string): string | null {
  if (!v) return null
  if (v.length > 253) return 'Too long.'
  if (/[/\s:]/.test(v.replace(/^\[.*\]$/, ''))) return 'Enter the name only, without http://, a port or a path.'
  return null
}
export function checkBaseUrl(v: string): string | null {
  if (!v) return null
  try {
    const u = new URL(v)
    if (u.protocol !== 'http:' && u.protocol !== 'https:') return 'The address must start with http:// or https://.'
    if (u.pathname !== '/' || u.search || u.hash) return 'Leave out any path.'
    if (v.endsWith('/')) return 'Remove the trailing slash.'
    return null
  } catch { return 'Enter a full address such as https://radio.example.org.' }
}
export const checkAccent = (v: string) => (parseHex(v) && v.startsWith('#') ? null : 'Use a six digit hex colour such as #ff6a00.')

/** Per-step problems, keyed by field, for the steps that have any. */
export function stepErrors(step: StepId, a: Answers): Record<string, string> {
  const e: Record<string, string> = {}
  const put = (k: string, m: string | null) => { if (m) e[k] = m }
  if (step === 'station') { if (a.title.length > 80) e.title = 'At most 80 characters.'; if (a.subtitle.length > 160) e.subtitle = 'At most 160 characters.'; if (a.location.length > 80) e.location = 'At most 80 characters.'; put('email', checkEmail(a.email)); if (a.email.length > 120) e.email = 'At most 120 characters.' }
  if (step === 'address') { put('hostname', checkHostname(a.hostname)); put('base_url', checkBaseUrl(a.baseUrl)) }
  if (step === 'look') put('accent', checkAccent(a.accent))
  if (step === 'mount') put('path', checkMount(a.mount))
  return e
}

/** Which step a server error belongs to, from its "<field>: message" prefix. */
export function stepForField(field: string): StepId | null {
  if (/^(station|title|subtitle|location|admin_email)/.test(field)) return 'station'
  if (/^(address|hostname|base_url)/.test(field)) return 'address'
  if (/^(appearance|accent)/.test(field)) return 'look'
  if (/^(audience|max_listeners|directory)/.test(field)) return 'audience'
  if (/^(mount|path)/.test(field)) return 'mount'
  if (/^(username|password)/.test(field)) return 'account'
  if (/^token/.test(field)) return 'token'
  return null
}

export function buildPayload(a: Answers, token: string, password: string) {
  const body: Record<string, unknown> = { token, username: a.username.trim(), password }
  if (a.mode === 'guided') {
    const station = { title: a.title.trim(), subtitle: a.subtitle.trim(), location: a.location.trim(), admin_email: a.email.trim() }
    if (Object.values(station).some(Boolean)) body.station = station
    body.address = { hostname: a.hostname.trim(), base_url: a.baseUrl.trim() }
    body.appearance = { accent_color: a.accent.toLowerCase() }
    body.audience = { max_listeners: AUDIENCE.find((x) => x.id === a.audience)!.max, directory_listing: a.directory }
    body.mount = { path: normalizeMount(a.mount) }
  }
  return body
}

export interface Applied { station: boolean; address: boolean; appearance: boolean; audience: boolean }
export interface Result { default_source_pass: string; live_mount_pass: string; mount?: string; applied?: Partial<Applied> }

/** 0..4 score and a plain label; length is what matters, variety gives a small credit. */
export function strength(p: string): { score: number; label: string } {
  if (!p) return { score: 0, label: '' }
  let s = p.length >= 8 ? 1 : 0
  if (p.length >= 12) s++
  if (p.length >= 16) s++
  if (/[a-z]/.test(p) && /[A-Z]/.test(p) && /\d/.test(p) || /[^A-Za-z0-9]/.test(p) && p.length >= 10) s++
  if (p.length < 8) return { score: 0, label: 'Too short' }
  s = Math.min(4, s)
  return { score: s, label: ['Too short', 'Acceptable', 'Good', 'Strong', 'Very strong'][s] }
}
