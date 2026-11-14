import type { IncomingMessage } from 'http'

/** Parsed request handed to a fixture route. */
export interface MockCtx {
  req: IncomingMessage
  url: URL
  /** Named groups when `path` is a RegExp. */
  params: Record<string, string>
  /** JSON body, or form fields for application/x-www-form-urlencoded; {} otherwise. */
  body: Record<string, any>
}
export interface MockResult { status?: number; body?: unknown; headers?: Record<string, string> }
export interface MockRoute {
  /** Omit to match any method. */
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE'
  path: string | RegExp
  /** Return a plain value for 200 JSON, or {status, body}. Throw to produce a 500. */
  handle: (ctx: MockCtx) => unknown | MockResult | Promise<unknown | MockResult>
}
export interface MockStream {
  mount: string; title: string; artist: string; format: string; bitrate: number
  base: number; health: number; has_video?: boolean; is_transcoded?: boolean
  source_mount?: string; source_type?: string; source_bitrate?: string
}
/** One SSE frame, e.g. {event: 'autodj', data: {...}}. */
export interface MockEvent { event: string; data: unknown }

/** What a fixture file may export. */
export interface Fixture {
  default?: MockRoute[]
  /** window.__TINYICE__ per entry name (admin, player, login, ...), merged over the base page data. */
  pages?: Record<string, Record<string, unknown>>
  /** Extra SSE events to emit on every /admin/events tick (n = tick counter). */
  events?: (n: number) => MockEvent[]
}
