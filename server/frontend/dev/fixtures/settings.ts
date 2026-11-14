import type { Fixture, MockRoute } from '../types'

/**
 * Fixtures for the Webhooks and Settings admin pages. Shapes are copied from
 * the reference server (GET /api/settings, /api/branding, /api/webhooks,
 * /api/webhooks/meta). Validation messages mirror validateWebhookBody and
 * logoPathAllowed in server/handlers_api_webhooks.go and handlers_api_v2.go.
 */

interface Hook {
  id: string; name?: string; url: string; method?: string; events: string[]; headers?: Record<string, string>
  body_template?: string; content_type?: string; enabled: boolean
}

// Verbatim response of GET /api/webhooks/meta on the reference server.
const META = {"events":[{"name":"now_playing","description":"A new track started playing on an AutoDJ mount.","sample":{"album":"Selected Ambient Works 85-92","artist":"Aphex Twin","bitrate":128,"duration_seconds":285.4,"file":"/music/aphex-twin/saw1/01-xtal.flac","format":"mp3","mount":"/live","name":"My Station","title":"Xtal"},"placeholders":["Title","Album","File","Format","DurationSeconds","Artist","Bitrate","Mount","Name","MountURL","PlayerURL"]},{"name":"source_connect","description":"An external source (broadcaster) connected to a mount.","sample":{"ip":"203.0.113.42:51234","mount":"/live","name":"BUTT","ua":"BUTT/0.1.40"},"placeholders":["Mount","Ip","Ua","Name","MountURL","PlayerURL"]},{"name":"source_disconnect","description":"An external source disconnected from a mount.","sample":{"ip":"203.0.113.42:51234","mount":"/live","reason":"client closed connection","seconds":1834},"placeholders":["Mount","Ip","Reason","Seconds","MountURL","PlayerURL"]},{"name":"metadata_update","description":"Mount metadata (title/artist/song) changed.","sample":{"artist":"Aphex Twin","mount":"/live","song":"Xtal","title":"Aphex Twin - Xtal"},"placeholders":["Title","Artist","Song","Mount","MountURL","PlayerURL"]},{"name":"security_lockout","description":"An IP was locked out for repeated auth failures.","sample":{"duration":"15m","ip":"198.51.100.7","reason":"too many failed login attempts"},"placeholders":["Ip","Reason","Duration"]}],"funcs":[{"description":"URL-encode a string for query params or URLs.","example":"{{urlencode .Title}}","name":"urlencode"},{"description":"Marshal any value to a JSON literal.","example":"{{json .}}","name":"json"},{"description":"Lowercase a string.","example":"{{lower .Mount}}","name":"lower"},{"description":"Uppercase a string.","example":"{{upper .Event}}","name":"upper"}],"global_placeholders":["Event","Timestamp","UnixTimestamp","Date","Time","Hostname","BaseURL","Version"],"presets":[{"id":"generic_json","name":"Generic JSON envelope","description":"Default tinyice envelope with event, timestamp, hostname and payload — works with anything that accepts JSON.","url_hint":"https://example.com/webhooks/tinyice","events":["now_playing","source_connect","source_disconnect","metadata_update","security_lockout"],"method":"POST","content_type":"application/json","body":""},{"id":"discord","name":"Discord channel webhook","description":"Posts a one-line message to a Discord channel using an incoming webhook. Create the webhook in: Server Settings → Integrations → Webhooks.","url_hint":"https://discord.com/api/webhooks/\u003cchannel-id\u003e/\u003ctoken\u003e","events":["now_playing"],"method":"POST","content_type":"application/json","body":"{\n  \"username\": \"TinyIce\",\n  \"content\": \":musical_note: Now playing on **{{.Mount}}**: **{{.Artist}} – {{.Title}}**{{if .MountURL}} — [Listen]({{.MountURL}}){{end}}\"\n}"},{"id":"slack","name":"Slack incoming webhook","description":"Posts to a Slack channel via an Incoming Webhook app. The text field is required; blocks/attachments can be added.","url_hint":"https://hooks.slack.com/services/T.../B.../...","events":["now_playing","source_connect","source_disconnect"],"method":"POST","content_type":"application/json","body":"{\n  \"text\": \":musical_note: *{{.Mount}}* — {{.Artist}} – {{.Title}}{{if .MountURL}} (\u003c{{.MountURL}}|Listen\u003e){{end}}\"\n}"},{"id":"mattermost","name":"Mattermost incoming webhook","description":"Same payload shape as Slack. Configure under Integrations → Incoming Webhooks in Mattermost.","url_hint":"https://mattermost.example.com/hooks/\u003cid\u003e","events":["now_playing"],"method":"POST","content_type":"application/json","body":"{\n  \"username\": \"TinyIce\",\n  \"text\": \"🎵 **{{.Mount}}** — {{.Artist}} – {{.Title}}\"\n}"},{"id":"msteams","name":"Microsoft Teams (MessageCard)","description":"Posts a legacy MessageCard via an Incoming Webhook connector. Works with classic Teams channel webhooks.","url_hint":"https://outlook.office.com/webhook/...","events":["now_playing"],"method":"POST","content_type":"application/json","body":"{\n  \"@type\": \"MessageCard\",\n  \"@context\": \"https://schema.org/extensions\",\n  \"summary\": \"TinyIce now playing\",\n  \"themeColor\": \"0078D4\",\n  \"title\": \"Now playing on {{.Mount}}\",\n  \"text\": \"**{{.Artist}}** – {{.Title}}\"\n}"},{"id":"telegram","name":"Telegram bot — sendMessage","description":"Sends a message via a Telegram bot. URL must include your bot token. Replace \u003cCHAT_ID\u003e with the target chat.","url_hint":"https://api.telegram.org/bot\u003cTOKEN\u003e/sendMessage","events":["now_playing"],"method":"POST","content_type":"application/json","body":"{\n  \"chat_id\": \"\u003cCHAT_ID\u003e\",\n  \"text\": \"🎵 {{.Mount}} — {{.Artist}} – {{.Title}}\",\n  \"parse_mode\": \"Markdown\"\n}"},{"id":"ntfy","name":"ntfy.sh push notification","description":"Publishes a push notification to an ntfy topic. Plain-text body; topic is the URL path.","url_hint":"https://ntfy.sh/\u003cyour-topic\u003e","events":["now_playing"],"method":"POST","content_type":"text/plain","headers":{"Priority":"default","Tags":"musical_note","Title":"TinyIce — {{.Mount}}"},"body":"{{.Artist}} – {{.Title}}"},{"id":"pushover","name":"Pushover — messages.json","description":"Sends a Pushover notification. Replace \u003cAPP_TOKEN\u003e and \u003cUSER_KEY\u003e with the values from your Pushover console.","url_hint":"https://api.pushover.net/1/messages.json","events":["now_playing"],"method":"POST","content_type":"application/x-www-form-urlencoded","body":"token=\u003cAPP_TOKEN\u003e\u0026user=\u003cUSER_KEY\u003e\u0026title={{urlencode .Mount}}\u0026message={{urlencode .Artist}}+-+{{urlencode .Title}}"},{"id":"tunein_air","name":"TuneIn AIR — Playing.ashx","description":"Reports the now-playing track to TuneIn AIR. Replace \u003cPARTNER_ID\u003e, \u003cPARTNER_KEY\u003e and \u003cSTATION_ID\u003e with your partner credentials.","url_hint":"https://air.radiotime.com/Playing.ashx","events":["now_playing"],"method":"GET","content_type":"application/x-www-form-urlencoded","body":"partnerId=\u003cPARTNER_ID\u003e\u0026partnerKey=\u003cPARTNER_KEY\u003e\u0026id=\u003cSTATION_ID\u003e\u0026title={{urlencode .Title}}\u0026artist={{urlencode .Artist}}"},{"id":"webhook_site","name":"webhook.site (debug echo)","description":"Sends the full JSON payload to a webhook.site URL — useful for debugging your template before pointing it at a real receiver.","url_hint":"https://webhook.site/\u003cyour-uuid\u003e","events":["now_playing","source_connect","source_disconnect","metadata_update","security_lockout"],"method":"POST","content_type":"application/json","body":"{\n  \"event\": \"{{.Event}}\",\n  \"timestamp\": \"{{.Timestamp}}\",\n  \"hostname\": \"{{.Hostname}}\",\n  \"payload\": {{json .}}\n}"}]}

const EVENTS = META.events.map((e) => e.name)

const hooks: Hook[] = [
  {
    id: 'wh_5e1c0a7d93b2', name: 'Discord, #now-playing', method: 'POST', content_type: 'application/json', enabled: true, events: ['now_playing'],
    url: 'https://discord.com/api/webhooks/1104455667788990011/xV3kQ9mTnRzP2aLhGdYw8sUoBfJc',
    body_template: '{\n  "username": "TinyIce",\n  "content": ":musical_note: Now playing on **{{.Mount}}**: **{{.Artist}} – {{.Title}}**{{if .MountURL}} — [Listen]({{.MountURL}}){{end}}"\n}',
  },
  {
    id: 'wh_a90f13c2e7d4', name: 'ntfy alerts', method: 'POST', content_type: 'text/plain', enabled: true, events: ['source_disconnect', 'security_lockout'],
    url: 'https://ntfy.example.net/tinyice-alerts', headers: { Authorization: 'Bearer tk_7f3a91c0d2e84b5aa1', Title: '{{.Event}} on {{.Hostname}}' },
    body_template: '{{.Ip}} {{.Reason}}',
  },
  {
    id: 'wh_c2d8e4f1a6b0', method: 'GET', content_type: 'application/json', enabled: false, events: ['now_playing'],
    url: 'http://air.radiotime.com/Playing.ashx', body_template: 'partnerId=XXXX&partnerKey=YYYY&id=s12345&title={{urlencode .Title}}&artist={{urlencode .Artist}}',
  },
]

const settings = {
  acme_email: '', admin_email: 'ops@example.net', admin_user: 'admin', audit_enabled: false, auto_https: false,
  base_url: 'https://radio.example.net', bind_host: '0.0.0.0', directory_listing: false, directory_server: 'http://dir.xiph.org/cgi-bin/yp-cgi',
  domains: null as string[] | null, hostname: 'radio.example.net', https_port: '443', location: 'Berlin, DE', low_latency_mode: false,
  max_listeners: 100, port: '8000', use_https: false,
}

const branding = {
  accent_color: '#ff6a00', landing_markdown: '## Schedule\n\n- **Mon-Fri** 08:00-22:00 live shows\n- **Weekends** AutoDJ\n\n[Listen now](/explore)',
  logo_path: '', page_subtitle: 'Live Streaming Server powered by Go', page_title: 'TinyIce',
}

// A body with a string `status` field would be read as the mock's own result envelope, so wrap it.
const ok = (body: unknown) => ({ status: 200, body })
const err = (status: number, error: string) => ({ status, body: { error } })
const q = (url: URL, k: string) => url.searchParams.get(k) ?? ''
const PRIVATE = /^(localhost|127\.|10\.|192\.168\.|169\.254\.|172\.(1[6-9]|2\d|3[01])\.|\[?::1\]?$)/i

function validate(b: Record<string, any>): string | null {
  const url = String(b.url ?? '').trim()
  if (!url) return 'URL is required'
  try {
    const u = new URL(url)
    if (!/^https?:$/.test(u.protocol)) return 'URL rejected: scheme must be http or https'
    if (PRIVATE.test(u.hostname)) return `URL rejected: URL points at a non-routable address: loopback address (${u.hostname})`
  } catch { return 'URL rejected: invalid URL' }
  if (!Array.isArray(b.events) || b.events.length === 0) return 'at least one event is required'
  for (const e of b.events) if (!EVENTS.includes(e)) return 'unknown event: ' + e
  if (b.method && !['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD'].includes(String(b.method).toUpperCase())) return 'unsupported HTTP method: ' + b.method
  return null
}

function apply(h: Hook, b: Record<string, any>) {
  h.name = String(b.name ?? '').trim() || undefined
  h.url = String(b.url).trim()
  h.method = String(b.method ?? '').trim().toUpperCase() || undefined
  h.events = b.events
  h.headers = b.headers && Object.keys(b.headers).length ? b.headers : undefined
  h.body_template = b.body_template || undefined
  h.content_type = String(b.content_type ?? '').trim() || undefined
  h.enabled = !!b.enabled
}

const routes: MockRoute[] = [
  { method: 'GET', path: '/api/webhooks/meta', handle: () => META },
  { method: 'GET', path: '/api/webhooks', handle: () => hooks },
  {
    method: 'POST', path: '/api/webhooks', handle: ({ body }) => {
      const e = validate(body); if (e) return err(400, e)
      const h = { id: 'wh_' + Math.random().toString(16).slice(2, 14), enabled: true } as Hook
      apply(h, body); hooks.push(h); return h
    },
  },
  {
    method: 'PUT', path: '/api/webhooks', handle: ({ url, body }) => {
      const h = hooks.find((x) => x.id === q(url, 'id')); if (!h) return err(404, 'Webhook not found')
      const e = validate(body); if (e) return err(400, e)
      apply(h, body); return h
    },
  },
  {
    method: 'DELETE', path: '/api/webhooks', handle: ({ url }) => {
      const i = hooks.findIndex((x) => x.id === q(url, 'id')); if (i < 0) return err(404, 'Webhook not found')
      hooks.splice(i, 1); return ok({ status: 'deleted' })
    },
  },
  {
    // The real endpoint only queues the delivery; the receiver's answer goes to the server log.
    method: 'POST', path: '/api/webhooks/test', handle: ({ url }) => {
      if (!hooks.some((x) => x.id === q(url, 'id'))) return err(404, 'Webhook not found')
      return ok({ event: q(url, 'event') || 'now_playing', status: 'queued' })
    },
  },
  { method: 'GET', path: '/api/settings', handle: () => settings },
  {
    method: 'PUT', path: '/api/settings', handle: ({ body }) => {
      // Contract: integer 0..1000000 (0 = unlimited), persisted. Anything else is a 400 and changes nothing.
      let pending = settings.max_listeners
      if ('max_listeners' in body) {
        const n = body.max_listeners
        if (typeof n !== 'number' || !Number.isInteger(n) || n < 0 || n > 1_000_000) return err(400, 'max_listeners must be an integer between 0 and 1000000')
        pending = n
      }
      // Same whitelist as apiUpdateSettings; everything else is silently ignored.
      for (const k of ['hostname', 'base_url', 'location', 'admin_email'] as const) if (k in body) settings[k] = String(body[k])
      for (const k of ['low_latency_mode', 'directory_listing', 'audit_enabled'] as const) if (typeof body[k] === 'boolean') settings[k] = body[k]
      settings.max_listeners = pending
      return ok({ status: 'updated' })
    },
  },
  { method: 'GET', path: '/api/branding', handle: () => branding },
  {
    method: 'PUT', path: '/api/branding', handle: ({ body }) => {
      if (typeof body.logo_path === 'string' && body.logo_path !== '' && body.logo_path !== branding.logo_path) return err(400, 'logo_path must be empty or a file uploaded via /api/branding/logo')
      for (const k of ['page_title', 'page_subtitle', 'accent_color', 'landing_markdown', 'logo_path'] as const) if (typeof body[k] === 'string') branding[k] = body[k]
      return ok({ status: 'updated' })
    },
  },
  {
    method: 'POST', path: '/api/branding/logo', handle: () => { branding.logo_path = 'branding/logo.png'; return ok({ status: 'ok', path: branding.logo_path }) },
  },
  {
    // Stand-in image for the uploaded logo (the mock does not store uploads).
    method: 'GET', path: '/branding/logo', handle: () => branding.logo_path
      ? { status: 200, headers: { 'Content-Type': 'image/svg+xml' }, body: '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" rx="12" fill="#3a6df0"/><path d="M16 40V24M26 48V16M36 52V12M46 42V22" stroke="#fff" stroke-width="5" stroke-linecap="round"/></svg>' }
      : { status: 404, body: { error: 'not found' } },
  },
]

const fixture: Fixture = {
  default: routes,
  pages: { admin: { user: { username: 'admin', role: 'superadmin' } } },
}
export const pages = fixture.pages
export default fixture.default
