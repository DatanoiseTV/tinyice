export function uptime(seconds: number): string {
  if (!isFinite(seconds) || seconds < 0) return '-'
  if (seconds < 60) return `${Math.floor(seconds)}s`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${Math.floor(seconds % 60)}s`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`
  return `${Math.floor(seconds / 86400)}d ${Math.floor((seconds % 86400) / 3600)}h`
}

/** Bytes/second as a bit rate would be misleading here: the server reports bytes. */
export function rate(bytesPerSec: number): string {
  if (bytesPerSec < 1024) return `${Math.round(bytesPerSec)} B/s`
  if (bytesPerSec < 1048576) return `${(bytesPerSec / 1024).toFixed(1)} KB/s`
  return `${(bytesPerSec / 1048576).toFixed(2)} MB/s`
}

export function bytes(n: number): string {
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++ }
  return `${n >= 100 || i === 0 ? Math.round(n) : n.toFixed(1)} ${u[i]}`
}

export function compact(n: number): string {
  return new Intl.NumberFormat('en', { notation: 'compact', maximumFractionDigits: 1 }).format(n)
}

/** "audio/mpeg" -> "MP3". */
export function codec(ct: string): string {
  const l = (ct || '').toLowerCase()
  if (l.includes('mpeg') || l.includes('mp3')) return 'MP3'
  if (l.includes('opus')) return 'Opus'
  if (l.includes('aac')) return 'AAC'
  if (l.includes('flac')) return 'FLAC'
  if (l.includes('vorbis')) return 'Vorbis'
  if (l.includes('ogg')) return 'Ogg'
  return l.startsWith('audio/') ? l.slice(6).toUpperCase() : l.toUpperCase() || '-'
}

export function clock(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds))
  const m = Math.floor(s / 60)
  return `${m}:${String(s % 60).padStart(2, '0')}`
}

/** "v0.11.1-0.2026...+dirty" (a Go pseudo-version from an untagged build) to "v0.11.1 dev"; a release tag stays as is. */
export function shortVersion(v?: string): string {
  const ver = v ?? ''
  const m = /^v?(\d+\.\d+\.\d+)/.exec(ver)
  if (!m) return ver || 'dev'
  return `v${m[1]}${/-0\.\d{14}|dirty|dev/.test(ver) ? ' dev' : ''}`
}

/**
 * The server fills metadata it does not have with placeholders ("N/A",
 * "Unnamed Stream", "No description", relay/relay.go). They are not content, so
 * show nothing instead of the placeholder.
 */
const PLACEHOLDERS = new Set(['', 'n/a', '-', 'unnamed stream', 'no description'])
export function real(v: string | null | undefined): string {
  const t = (v ?? '').trim()
  return PLACEHOLDERS.has(t.toLowerCase()) ? '' : t
}
