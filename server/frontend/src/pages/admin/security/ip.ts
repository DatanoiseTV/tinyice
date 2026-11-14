export type IpKind = 'IPv4' | 'IPv6' | 'IPv4 range' | 'IPv6 range'
export type IpParse = { ok: true; value: string; kind: IpKind; prefix?: number; broad: boolean; whole: boolean } | { ok: false; error: string }

const V4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/

/** The browser's URL parser is a strict IPv6 literal validator and also compresses and lowercases it. */
function canonicalV6(s: string): string | null {
  if (!/^[0-9a-fA-F:.]+$/.test(s) || !s.includes(':')) return null
  try {
    const host = new URL(`http://[${s}]/`).hostname.slice(1, -1)
    // Go prints IPv4-mapped addresses with a dotted tail; URL rewrites them to hex groups. Keep the input then.
    return s.includes('.') ? s.toLowerCase() : host
  } catch { return null }
}

/**
 * The server stores whatever string it is sent and never validates it, then
 * matches plain addresses by exact string equality against Go's canonical
 * textual form. A typo or a non-canonical IPv6 spelling is therefore stored
 * silently and never matches anything. Validate and canonicalise here.
 */
export function parseIp(raw: string): IpParse {
  const input = raw.trim()
  if (!input) return { ok: false, error: 'Enter an address or range' }
  const [addr, prefixText, ...rest] = input.split('/')
  if (rest.length) return { ok: false, error: 'Only one "/" is allowed' }
  const isV6 = addr.includes(':')
  if (isV6) {
    const v6 = canonicalV6(addr)
    if (!v6) return { ok: false, error: 'Not a valid IPv6 address' }
    if (prefixText === undefined) return { ok: true, value: v6, kind: 'IPv6', broad: false, whole: false }
    if (!/^\d{1,3}$/.test(prefixText) || Number(prefixText) > 128) return { ok: false, error: 'IPv6 prefix must be 0 to 128' }
    const p = Number(prefixText)
    return { ok: true, value: `${v6}/${p}`, kind: 'IPv6 range', prefix: p, broad: p < 32, whole: p === 0 }
  }
  if (!V4.test(addr)) return { ok: false, error: 'Not a valid IPv4 or IPv6 address' }
  if (prefixText === undefined) return { ok: true, value: addr, kind: 'IPv4', broad: false, whole: false }
  if (!/^\d{1,2}$/.test(prefixText) || Number(prefixText) > 32) return { ok: false, error: 'IPv4 prefix must be 0 to 32' }
  const p = Number(prefixText)
  return { ok: true, value: `${addr}/${p}`, kind: 'IPv4 range', prefix: p, broad: p < 16, whole: p === 0 }
}

export const kindOf = (s: string): IpKind | 'Invalid' => { const p = parseIp(s); return p.ok ? p.kind : 'Invalid' }
