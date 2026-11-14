/** Wire shapes of /api/webhooks*, mirrored from config.WebhookConfig and handlers_api_webhooks.go. */
export interface Webhook {
  id: string
  name?: string
  url: string
  method?: string
  events: string[]
  headers?: Record<string, string>
  body_template?: string
  content_type?: string
  enabled: boolean
}

/** Body accepted by POST /api/webhooks and PUT /api/webhooks?id=. */
export interface WebhookBody {
  name: string
  url: string
  method: string
  events: string[]
  headers: Record<string, string>
  body_template: string
  content_type: string
  enabled: boolean
}

export interface EventInfo {
  name: string
  description: string
  sample: Record<string, unknown>
  placeholders: string[]
}

export interface Preset {
  id: string
  name: string
  description: string
  url_hint: string
  events: string[]
  method: string
  content_type: string
  headers?: Record<string, string>
  body: string
}

export interface FuncDoc { name: string; description: string; example: string }

export interface WebhookMeta {
  events: EventInfo[]
  global_placeholders: string[]
  presets: Preset[]
  funcs: FuncDoc[]
}

/** POST /api/webhooks/test. Only status and event are sent today; the rest is read if a future server adds it. */
export interface TestResult {
  status: string
  event: string
  http_status?: number
  latency_ms?: number
  response_excerpt?: string
  error?: string
}

export const METHODS = ['POST', 'PUT', 'PATCH', 'GET', 'DELETE', 'HEAD'] as const

export function toBody(w: Webhook): WebhookBody {
  return {
    name: w.name ?? '',
    url: w.url,
    method: w.method || 'POST',
    events: w.events,
    headers: w.headers ?? {},
    body_template: w.body_template ?? '',
    content_type: w.content_type ?? '',
    enabled: w.enabled,
  }
}
