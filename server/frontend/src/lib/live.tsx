import { createContext, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { createSSE } from './sse'
import { LiveStreamTracker } from './liveStreams'
import type { StatsEvent, StreamEvent, AutoDJEvent, GeoCity } from '../types'

/** Samples kept per series: 5 minutes at the server's ~1 s stats tick. */
const HISTORY = 300

export interface Sample { t: number; listeners: number; out: number; in: number }
interface Live {
  connected: boolean
  stats: StatsEvent | null
  streams: StreamEvent[]
  autodj: Record<string, AutoDJEvent>
  geo: GeoCity[]
  history: Sample[]
}

const Ctx = createContext<Live>({ connected: false, stats: null, streams: [], autodj: {}, geo: [], history: [] })
export const useLive = () => useContext(Ctx)

/**
 * One /admin/events connection for the whole admin app. Pages read from this
 * context instead of each opening their own EventSource (the old UI opened a
 * connection per page mount).
 */
export function LiveProvider({ children }: { children: ReactNode }) {
  const [connected, setConnected] = useState(false)
  const [stats, setStats] = useState<StatsEvent | null>(null)
  const [streams, setStreams] = useState<StreamEvent[]>([])
  const [autodj, setAutodj] = useState<Record<string, AutoDJEvent>>({})
  const [geo, setGeo] = useState<GeoCity[]>([])
  const [history, setHistory] = useState<Sample[]>([])
  const tracker = useRef(new LiveStreamTracker<StreamEvent>())

  useEffect(() => {
    const sse = createSSE('/admin/events')
    const offs = [
      sse.on('stats', (d) => {
        setConnected(true)
        setStats(d)
        setHistory((h) => [...h.slice(-(HISTORY - 1)), { t: Date.now(), listeners: d.listeners, out: d.bandwidth_out, in: d.bandwidth_in }])
      }),
      sse.on('stream', (d) => setStreams((s) => tracker.current.upsert(s, d))),
      sse.on('autodj', (d) => setAutodj((m) => ({ ...m, [d.mount]: d }))),
      sse.on('geo', (d) => setGeo(d ?? [])),
    ]
    // Mounts that stop emitting have gone away; prune them even when no event arrives.
    const sweep = setInterval(() => setStreams((s) => tracker.current.prune(s)), 2000)
    return () => { clearInterval(sweep); offs.forEach((f) => f()); sse.close() }
  }, [])

  const value = useMemo(() => ({ connected, stats, streams, autodj, geo, history }), [connected, stats, streams, autodj, geo, history])
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}
