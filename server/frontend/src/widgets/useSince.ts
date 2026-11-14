import { useEffect, useRef, useState } from 'react'

/**
 * Elapsed seconds each key has been continuously in a bad state, as observed
 * by this page. The server reports no "last connected" time, so this is the
 * only honest answer to "how long has this been failing": it starts when the
 * page first sees the key down and resets when the key recovers.
 */
export function useSince(downKeys: string[]): Record<string, number> {
  const since = useRef(new Map<string, number>())
  const [now, setNow] = useState(() => Date.now())
  const sig = [...downKeys].sort().join('\n')

  for (const k of downKeys) if (!since.current.has(k)) since.current.set(k, Date.now())
  for (const k of [...since.current.keys()]) if (!downKeys.includes(k)) since.current.delete(k)

  useEffect(() => {
    if (!sig) return
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [sig])

  const out: Record<string, number> = {}
  for (const [k, t] of since.current) out[k] = Math.max(0, Math.floor((now - t) / 1000))
  return out
}
