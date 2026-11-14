import { Input, Switch } from '@/ui/controls'
import { Badge } from '@/ui/display'
import { page } from '@/lib/api'
import { AccentControl, LandingControl, ListenerLimit, LogoControl } from './controls'
import type { RowDef } from './Row'
import {
  BRANDING_DEFAULTS, SERVER_DEFAULTS, validateBranding, validateServer,
  type Branding, type ServerDraft, type ServerSettings,
} from './types'

export interface Ctx {
  srv: ServerDraft; srvBase: ServerDraft; setS: <K extends keyof ServerDraft>(k: K, v: ServerDraft[K]) => void
  br: Branding; brBase: Branding; setB: <K extends keyof Branding>(k: K, v: Branding[K]) => void
  server: ServerSettings; showErrors: boolean; logoVer: number; onLogoUploaded: (path: string) => void
}

const fmt = (k: string, v: string | boolean) => (typeof v === 'boolean' ? (v ? 'on' : 'off') : k === 'max_listeners' && v === '0' ? 'Unlimited' : v === '' ? 'empty' : v)
const Value = ({ children }: { children: React.ReactNode }) => <span className="num break-all font-mono text-xs text-ink">{children}</span>
const Flag = ({ on }: { on: boolean }) => <Badge tone={on ? 'accent' : 'neutral'}>{on ? 'Enabled' : 'Disabled'}</Badge>

export function buildRows(c: Ctx): RowDef[] {
  const se = validateServer(c.srv), be = validateBranding(c.br)
  const sd = (k: keyof ServerDraft) => {
    const dirty = c.srv[k] !== c.srvBase[k]
    return {
      dirty, saved: fmt(k, c.srvBase[k]), error: se[k] && (c.showErrors || dirty) ? se[k] : undefined,
      defaultText: fmt(k, SERVER_DEFAULTS[k]), isDefault: c.srv[k] === SERVER_DEFAULTS[k], onReset: () => c.setS(k, SERVER_DEFAULTS[k] as never),
    }
  }
  const bd = (k: keyof Branding) => {
    const dirty = c.br[k] !== c.brBase[k]
    return {
      dirty, saved: fmt(k, c.brBase[k]), error: be[k] && (c.showErrors || dirty) ? be[k] : undefined,
      defaultText: fmt(k, BRANDING_DEFAULTS[k]), isDefault: c.br[k] === BRANDING_DEFAULTS[k], onReset: () => c.setB(k, BRANDING_DEFAULTS[k] as never),
    }
  }
  const text = (k: keyof ServerDraft, label: string, placeholder: string, extra: { type?: string; mono?: boolean } = {}) => (
    <Input type={extra.type} mono={extra.mono} value={c.srv[k] as string} onChange={(e) => c.setS(k, e.target.value as never)} placeholder={placeholder} aria-label={label} aria-invalid={!!sd(k).error} />
  )
  const btext = (k: keyof Branding, label: string, placeholder: string) => (
    <Input value={c.br[k]} onChange={(e) => c.setB(k, e.target.value as never)} placeholder={placeholder} aria-label={label} aria-invalid={!!bd(k).error} />
  )
  const toggle = (k: 'low_latency_mode' | 'directory_listing' | 'audit_enabled', label: string) => (
    <div className="flex sm:justify-end"><Switch label={label} checked={c.srv[k]} onCheckedChange={(v) => c.setS(k, v)} /></div>
  )
  const s = c.server
  const p = page()
  const providers = p.oidcProviders ?? []
  const ro = (id: string, section: RowDef['section'], label: string, summary: string, control: React.ReactNode, restart = true, keywords = ''): RowDef =>
    ({ id, section, label, summary, summaryText: summary, control, restart, keywords })

  return [
    // Station
    { id: 'page_title', section: 'station', label: 'Station name', summary: 'Shown in the top bar, on the landing page and in the browser tab.', summaryText: 'name title brand shown in the top bar landing page browser tab',
      keywords: 'title site name', ...bd('page_title'), control: btext('page_title', 'Station name', 'TinyIce') },
    { id: 'page_subtitle', section: 'station', label: 'Tagline', summary: 'One short line shown above the title on the landing page.', summaryText: 'tagline subtitle slogan line above title landing page',
      keywords: 'subtitle', ...bd('page_subtitle'), control: btext('page_subtitle', 'Tagline', 'Live Streaming Server powered by Go') },
    { id: 'location', section: 'station', label: 'Location', summary: 'Where the station is based, for example a city.', summaryText: 'location city country place where based',
      useWhen: 'you want listeners and directories to see where you broadcast from.', ...sd('location'), control: text('location', 'Location', 'Berlin, DE') },
    { id: 'admin_email', section: 'station', label: 'Contact e-mail', summary: 'The address people should write to about this station.', summaryText: 'contact e-mail email address admin',
      keywords: 'admin email', ...sd('admin_email'), control: text('admin_email', 'Contact e-mail', 'ops@example.net', { type: 'email' }) },

    // Look and feel
    { id: 'logo', section: 'look', label: 'Logo', summary: 'Replaces the initials in the top bar and on the landing page.', summaryText: 'logo image picture icon brand upload',
      useWhen: 'you have a square image, about 128 by 128 pixels.', defaultText: 'none (initials)', dirty: c.br.logo_path !== c.brBase.logo_path, saved: c.brBase.logo_path ? 'uploaded logo' : 'none',
      isDefault: !c.br.logo_path, onReset: () => c.setB('logo_path', ''), stack: true,
      control: <LogoControl draft={c.br} saved={c.brBase} setPath={(v) => c.setB('logo_path', v)} logoVer={c.logoVer} onUploaded={c.onLogoUploaded} /> },
    { id: 'accent_color', section: 'look', label: 'Accent colour', summary: 'The colour of buttons, links, the active tab and focus rings. The whole interface previews it while you edit; nothing is saved until you save.', summaryText: 'accent colour color brand theme buttons links contrast',
      keywords: 'color theme brand', ...bd('accent_color'), stack: true,
      control: <AccentControl value={c.br.accent_color} saved={c.brBase.accent_color} onChange={(v) => c.setB('accent_color', v)} invalid={!!bd('accent_color').error} /> },
    { id: 'landing_markdown', section: 'look', label: 'Landing page text', summary: 'An introduction shown on the public landing page above the list of live streams. Supports Markdown.', summaryText: 'landing page text intro welcome markdown schedule about',
      useWhen: 'you want to tell visitors about your schedule or link elsewhere. Scripts are stripped before display.', keywords: 'markdown welcome home',
      ...bd('landing_markdown'), defaultText: 'empty', stack: true, control: <LandingControl value={c.br.landing_markdown} onChange={(v) => c.setB('landing_markdown', v)} /> },

    // Listeners
    { id: 'max_listeners', section: 'listeners', label: 'Listener limit per stream',
      summary: 'The most people one stream accepts at a time. It counts direct stream connections (the plain MP3 or Ogg URL players use); HLS and WebRTC viewers are not counted. At the limit, new listeners get "Server Full" (HTTP 503) while current listeners keep playing.',
      summaryText: 'listener limit per stream max listeners capacity server full 503 unlimited bandwidth',
      useWhen: 'your upload bandwidth or CPU is limited, so one popular stream cannot starve the others.', keywords: 'max capacity cap audience',
      ...sd('max_listeners'), stack: true, control: <ListenerLimit label="Listener limit per stream" value={c.srv.max_listeners} onChange={(v) => c.setS('max_listeners', v)} /> },
    { id: 'low_latency_mode', section: 'listeners', label: 'Low-latency mode',
      summary: 'Listeners hear the broadcast sooner after it is sent, but players on slow or unstable connections are more likely to stutter or drop out.', summaryText: 'low latency mode delay buffer stutter dropouts faster live',
      useWhen: 'live interaction such as call-ins or chat matters more than rock-solid playback. Applied to the running server as soon as you save.', keywords: 'delay buffering',
      ...sd('low_latency_mode'), control: toggle('low_latency_mode', 'Low-latency mode') },
    { id: 'directory_listing', section: 'listeners', label: 'Public directory listing',
      summary: 'Announces your public streams to a stream directory so new listeners can find you.', summaryText: 'public directory listing yellow pages announce discoverable xiph',
      useWhen: 'you run a public station and want to be discoverable. Keep it off for private or test streams.', keywords: 'icecast directory yp discover',
      ...sd('directory_listing'), control: toggle('directory_listing', 'Public directory listing') },

    // Security and logging
    { id: 'audit_enabled', section: 'security', label: 'Admin activity log',
      summary: 'Keeps a record of what administrators do, such as setting changes. You can read it on the Security page.', summaryText: 'admin activity log audit record actions history',
      useWhen: 'more than one person has admin access, or you need to know who changed what.', recommended: 'on', keywords: 'audit logging',
      ...sd('audit_enabled'), control: toggle('audit_enabled', 'Admin activity log') },

    // Address and network
    { id: 'hostname', section: 'network', label: 'Host name', summary: 'The name this server goes by. It is sent in webhook messages as the host.', summaryText: 'host name hostname server name domain webhook',
      useWhen: 'you run several servers and want to tell them apart in notifications.', ...sd('hostname'), control: text('hostname', 'Host name', 'radio.example.net', { mono: true }) },
    { id: 'base_url', section: 'network', label: 'Public address', summary: 'The web address listeners use to reach you, including https://. Used to build stream links in webhook messages.', summaryText: 'public address url base url link listeners reach https webhook mount player',
      useWhen: 'webhook messages should contain clickable stream and player links. Leave empty to omit them.', keywords: 'base url domain',
      ...sd('base_url'), control: text('base_url', 'Public address', 'https://radio.example.net', { mono: true }) },
    ro('bind', 'network', 'Listening address', 'The network interface and port the server accepts connections on.', <Value>{s.bind_host}:{s.port}</Value>, true, 'bind port interface host'),
    ro('https', 'network', 'HTTPS', 'Encrypts connections between listeners and the server.', <Flag on={s.use_https} />, true, 'tls ssl secure'),
    ro('https_port', 'network', 'HTTPS port', 'The port used for encrypted connections.', <Value>{s.https_port || '443'}</Value>, true, 'tls ssl port'),
    ro('auto_https', 'network', 'Automatic certificates', 'Gets and renews HTTPS certificates for your domains by itself (ACME).', <Flag on={s.auto_https} />, true, 'acme lets encrypt certificate'),
    ro('acme_email', 'network', 'Certificate e-mail', 'Contact address registered with the certificate authority.', <Value>{s.acme_email || 'not set'}</Value>, true, 'acme'),
    ro('domains', 'network', 'Certificate domains', 'Host names the automatic certificate covers.', s.domains && s.domains.length ? <div className="flex flex-wrap gap-1">{s.domains.map((d) => <Badge key={d}>{d}</Badge>)}</div> : <Value>none</Value>, true, 'acme domains'),
    ro('directory_server', 'network', 'Directory server', 'Where public streams are announced when the public directory listing is on.', <Value>{s.directory_server || 'not set'}</Value>, true, 'yp directory'),

    // Sign-in
    ro('login_password', 'signin', 'Password sign-in', 'Local accounts. Always available.', <Badge>Always on</Badge>, false, 'login'),
    ro('login_admin', 'signin', 'Admin account', 'The account created during setup.', <Value>{s.admin_user || 'none'}</Value>, false, 'administrator user'),
    ro('login_passkeys', 'signin', 'Passkeys', 'Sign in with a fingerprint, face or device key instead of a password.', <Badge tone={p.passkeysEnabled ? 'accent' : 'neutral'}>{p.passkeysEnabled ? 'Offered' : 'Not offered'}</Badge>, false, 'webauthn passwordless'),
    ro('login_sso', 'signin', 'Single sign-on', 'Providers shown as buttons on the login page.', providers.length ? <div className="flex flex-wrap gap-1">{providers.map((o) => <Badge key={o.id} tone="accent">{o.name}</Badge>)}</div> : <Badge>None configured</Badge>, true, 'oidc google github login sso'),
  ]
}
