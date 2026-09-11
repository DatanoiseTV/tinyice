import type { StatsEvent, StreamEvent, AutoDJEvent, StreamInfo } from '../types'
import { redirectToLogin } from './api'

// EventSource doesn't surface HTTP status codes — onerror just fires
// for any failure, including a 401 from a stale session post-deploy.
// To distinguish "session dead" from "server temporarily down" we
// probe the same URL with fetch() on each connection failure: a
// 401 response means we should boot the operator to /login; any
// other failure is a real network/server problem and we keep
// retrying.
async function probeAuth(url: string) {
  try {
    const res = await fetch(url, { method: 'GET', credentials: 'same-origin' })
    if (res.status === 401) {
      redirectToLogin()
    }
    // Drain so the connection isn't held open by an unread body.
    try { await res.body?.cancel() } catch {}
  } catch {
    // Real network failure — let the SSE reconnect loop keep trying.
  }
}

type SSEEventMap = {
  stats: StatsEvent
  stream: StreamEvent
  autodj: AutoDJEvent
  streams: StreamInfo[]
}

type SSECallback<K extends keyof SSEEventMap> = (data: SSEEventMap[K]) => void

export function createSSE(url: string) {
  let source: EventSource | null = null
  const listeners = new Map<string, Set<Function>>()
  // attached tracks which SSE event types we've already called
  // addEventListener for on the CURRENT EventSource. Reset on reconnect so
  // we re-attach exactly one dispatcher per event type on the new source.
  // Without this, on(...) would add a listener directly every call while
  // connect() also re-attached on every reconnect, making each subsequent
  // reconnect multiply callback invocations.
  let attached = new Set<string>()
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let reconnectDelay = 1000
  let closed = false

  function dispatch(event: string, data: unknown) {
    listeners.get(event)?.forEach((cb) => {
      try { (cb as (d: unknown) => void)(data) } catch (e) { /* swallow per-listener errors */ void e }
    })
  }

  function attachDispatcher(s: EventSource, event: string) {
    if (attached.has(event)) return
    s.addEventListener(event, (e: Event) => {
      try { dispatch(event, JSON.parse((e as MessageEvent).data)) } catch {}
    })
    attached.add(event)
  }

  function connect() {
    if (closed) return
    source = new EventSource(url)
    attached = new Set()
    source.onopen = () => { reconnectDelay = 1000 }
    source.onerror = () => {
      source?.close()
      source = null
      attached = new Set()
      if (closed) return
      // Distinguish "session expired" from "server unreachable" by
      // probing the same URL synchronously. probeAuth redirects to
      // /login on 401 and is a no-op otherwise.
      probeAuth(url)
      reconnectTimer = setTimeout(connect, reconnectDelay)
      reconnectDelay = Math.min(reconnectDelay * 2, 30000)
    }
    // Legacy untyped messages
    source.onmessage = (e) => {
      try { dispatch('message', JSON.parse(e.data)) } catch {}
    }
    // Register exactly one dispatcher per currently-known event type.
    for (const type of listeners.keys()) {
      if (type === 'message') continue
      attachDispatcher(source, type)
    }
  }

  function on<K extends keyof SSEEventMap>(event: K, callback: SSECallback<K>): () => void
  function on(event: 'message', callback: (data: unknown) => void): () => void
  function on(event: string, callback: Function): () => void {
    if (!listeners.has(event)) listeners.set(event, new Set())
    listeners.get(event)!.add(callback)
    if (source && event !== 'message') {
      attachDispatcher(source, event)
    }
    return () => { listeners.get(event)?.delete(callback) }
  }

  function close() {
    closed = true
    source?.close()
    source = null
    if (reconnectTimer) {
      clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
  }

  connect()
  return { on, close }
}
