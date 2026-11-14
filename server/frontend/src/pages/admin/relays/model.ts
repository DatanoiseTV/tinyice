import { api, adminPost } from '@/lib/api'

/** GET /api/relays row (apiGetRelays). `active` means a stream with source "relay-pull" exists on the mount. */
export interface Relay { url: string; mount: string; burst_size: number; enabled: boolean; active: boolean }

export const RELAYS_KEY = ['admin', 'relays'] as const
export const listRelays = () => api.get<Relay[]>('/api/relays')
/** Create, or update when the mount already exists. An empty password keeps the stored one. */
export const saveRelay = (b: { url: string; mount: string; password?: string; burst_size: number }) => api.post('/api/relays', b)
export const toggleRelay = (mount: string) => api.post<{ status: string; enabled: boolean }>('/api/relays/toggle', { mount })
export const deleteRelay = (mount: string) => api.del(`/api/relays?mount=${encodeURIComponent(mount)}`)
/** Legacy form endpoint: re-dials the upstream with the stored settings. */
export const restartRelay = (mount: string) => adminPost('/admin/restart-relay', { mount })

const PRIVATE_V4 = /^(127\.|10\.|192\.168\.|169\.254\.|0\.|172\.(1[6-9]|2\d|3[01])\.|100\.(6[4-9]|[7-9]\d|1[01]\d|12[0-7])\.)/

/** Client-side mirror of validateOutboundURL (server/helpers.go). The server stays authoritative. */
export function validateUpstream(raw: string): string | null {
  const v = raw.trim()
  if (!v) return 'Paste the link to the stream.'
  let u: URL
  try { u = new URL(v) } catch { return 'That does not look like a link. Start it with https:// or http://.' }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return `Only http:// and https:// links work.`
  if (!u.hostname) return 'The link has no server name in it.'
  const h = u.hostname.toLowerCase()
  if (h === 'localhost' || h.endsWith('.localhost') || h === '[::1]' || PRIVATE_V4.test(h)) return 'That address points to this machine or a private network. Relays can only pull from public servers.'
  return null
}

/** host[:port]/path without credentials, so a password embedded in the URL is never rendered. */
export function displayUrl(raw: string): { scheme: string; host: string; path: string; hadAuth: boolean } {
  try {
    const u = new URL(raw)
    return { scheme: u.protocol.replace(':', ''), host: u.host, path: u.pathname + u.search, hadAuth: !!(u.username || u.password) }
  } catch { return { scheme: '', host: raw, path: '', hadAuth: false } }
}
