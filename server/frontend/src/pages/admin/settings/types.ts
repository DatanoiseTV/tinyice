import type { SectionId } from './Row'

/** GET /api/settings (superadmin only), as returned by the reference server. */
export interface ServerSettings {
  bind_host: string
  /** Strings on the wire, not numbers. */
  port: string
  https_port: string
  hostname: string
  base_url: string
  location: string
  admin_email: string
  admin_user: string
  low_latency_mode: boolean
  max_listeners: number
  use_https: boolean
  auto_https: boolean
  acme_email: string
  domains: string[] | null
  directory_listing: boolean
  directory_server: string
  audit_enabled: boolean
}

/** GET /api/branding (any signed-in user); PUT needs superadmin. */
export interface Branding {
  page_title: string
  page_subtitle: string
  accent_color: string
  logo_path: string
  landing_markdown: string
}

/** The fields PUT /api/settings accepts (apiUpdateSettings); every other field is read-only. */
export interface ServerDraft {
  hostname: string
  base_url: string
  location: string
  admin_email: string
  /** Kept as text while typing; validated to an integer on save. */
  max_listeners: string
  low_latency_mode: boolean
  directory_listing: boolean
  audit_enabled: boolean
}

export const toServerDraft = (s: ServerSettings): ServerDraft => ({
  hostname: s.hostname, base_url: s.base_url, location: s.location, admin_email: s.admin_email,
  max_listeners: String(s.max_listeners), low_latency_mode: s.low_latency_mode, directory_listing: s.directory_listing, audit_enabled: s.audit_enabled,
})


/** Which section each editable field lives in, for change counts and error jumps. */
export const SERVER_SECTION: Record<keyof ServerDraft, SectionId> = {
  hostname: 'network', base_url: 'network', location: 'station', admin_email: 'station', max_listeners: 'listeners',
  low_latency_mode: 'listeners', directory_listing: 'listeners', audit_enabled: 'security',
}
export const BRANDING_SECTION: Record<keyof Branding, SectionId> = {
  page_title: 'station', page_subtitle: 'station', accent_color: 'look', logo_path: 'look', landing_markdown: 'look',
}

/** Values the server starts with (config defaults and the branding the shell falls back to). */
export const SERVER_DEFAULTS: ServerDraft = {
  hostname: '', base_url: '', location: '', admin_email: '', max_listeners: '100',
  low_latency_mode: false, directory_listing: false, audit_enabled: false,
}
export const BRANDING_DEFAULTS: Branding = {
  page_title: 'TinyIce', page_subtitle: 'Live Streaming Server powered by Go', accent_color: '#ff6a00', logo_path: '', landing_markdown: '',
}

export function validateServer(d: ServerDraft): Partial<Record<keyof ServerDraft, string>> {
  const e: Partial<Record<keyof ServerDraft, string>> = {}
  const host = d.hostname.trim()
  if (host && !/^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:\d{1,5})?$/.test(host)) e.hostname = 'A host name only: letters, digits, dots and hyphens, no scheme or path'
  const base = d.base_url.trim()
  if (base) {
    try {
      const u = new URL(base)
      if (u.protocol !== 'http:' && u.protocol !== 'https:') e.base_url = 'Use an http:// or https:// URL'
      else if (u.search || u.hash) e.base_url = 'Remove the query string or fragment'
    } catch { e.base_url = 'Not a valid URL, for example https://radio.example.net' }
  }
  if (d.admin_email.trim() && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(d.admin_email.trim())) e.admin_email = 'Not a valid e-mail address'
  if (d.location.length > 120) e.location = 'At most 120 characters'
  const n = Number(d.max_listeners)
  if (!/^\d+$/.test(d.max_listeners.trim()) || !Number.isSafeInteger(n)) e.max_listeners = 'Enter a whole number, 0 or more'
  else if (n > 1_000_000) e.max_listeners = 'At most 1,000,000'
  return e
}

export function validateBranding(b: Branding): Partial<Record<keyof Branding, string>> {
  const e: Partial<Record<keyof Branding, string>> = {}
  if (!/^#[0-9a-fA-F]{6}$/.test(b.accent_color)) e.accent_color = 'Six-digit hex colour such as #ff6a00'
  if (b.page_title.length > 60) e.page_title = 'At most 60 characters'
  if (b.page_subtitle.length > 120) e.page_subtitle = 'At most 120 characters'
  return e
}
