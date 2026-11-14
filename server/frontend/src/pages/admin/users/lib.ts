import { useCallback, useEffect, useRef, useState } from 'react'
import { page } from '@/lib/api'

/** Role of the signed-in operator. UI gating only: every handler re-checks on the server. */
export function useRole(): string { return (page().user?.role as string | undefined) ?? '' }
export function useMe(): string { return page().user?.username ?? '' }

/**
 * Fetch-on-mount with optional polling and refetch on window focus.
 * `key` re-fetches when it changes (filters, page number).
 * `enabled: false` skips the request entirely (used so a role that the
 * server would answer with 401/403 never makes the call: a 401 sends the
 * browser to /login).
 */
export function useLoad<T>(fn: () => Promise<T>, { enabled = true, every = 0, key = '' }: { enabled?: boolean; every?: number; key?: string } = {}) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(enabled)
  const fnRef = useRef(fn)
  fnRef.current = fn
  const alive = useRef(true)
  const reload = useCallback(async () => {
    try { const d = await fnRef.current(); if (alive.current) { setData(d); setError(null) } }
    catch (e) { if (alive.current) setError((e as Error).message || 'Request failed') }
    finally { if (alive.current) setLoading(false) }
  }, [])
  useEffect(() => {
    alive.current = true
    if (!enabled) { setLoading(false); return }
    void reload()
    const t = every ? setInterval(reload, every) : undefined
    const onFocus = () => { void reload() }
    window.addEventListener('focus', onFocus)
    return () => { alive.current = false; clearInterval(t); window.removeEventListener('focus', onFocus) }
  }, [enabled, every, key, reload])
  return { data, error, loading, reload }
}

/** Go's zero time.Time and empty strings both mean "never". */
export const isNever = (iso?: string | null) => !iso || iso.startsWith('0001-')

export function ago(iso?: string | null, now = Date.now()): string {
  if (isNever(iso)) return 'never'
  const diff = now - new Date(iso as string).getTime()
  const future = diff < 0
  const s = Math.abs(diff) / 1000
  if (s < 45) return future ? 'in moments' : 'just now'
  const units: [number, string][] = [[60, 'min'], [3600, 'h'], [86400, 'd'], [86400 * 30, 'mo'], [86400 * 365, 'y']]
  let label = `${Math.round(s / 60)} min`
  for (let i = 0; i < units.length; i++) {
    const next = units[i + 1]?.[0] ?? Infinity
    if (s < next) { label = `${Math.floor(s / units[i][0])} ${units[i][1]}`; break }
  }
  return future ? `in ${label}` : `${label} ago`
}

export const absTime = (iso?: string | null) => (isNever(iso) ? '' : new Date(iso as string).toLocaleString())
