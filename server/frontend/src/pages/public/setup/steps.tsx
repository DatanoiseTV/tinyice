import { useEffect, type ReactNode } from 'react'
import { Legend } from '@/ui/console'
import { Switch } from '@/ui/controls'
import { Icon } from '@/ui/icons'
import { Logo } from '@/ui/icons'
import { cn } from '@/lib/cn'
import { accentForeground, applyAccent, contrastRows, parseHex, THEME_TOKENS } from '@/pages/admin/settings/contrast'
import { Notice, PasswordInput, TextInput } from '../shared/AuthLayout'
import { AUDIENCE, SWATCHES, bandwidth, checkMount, isLocalHost, normalizeMount, strength, type Answers } from './model'

export interface StepProps { a: Answers; set: (patch: Partial<Answers>) => void; errors: Record<string, string> }

export function Group({ title, children }: { title?: string; children: ReactNode }) {
  return <div className="space-y-4">{title && <Legend>{title}</Legend>}{children}</div>
}
const Help = ({ children }: { children: ReactNode }) => <p className="text-xs leading-relaxed text-muted">{children}</p>

/* ---------- 1. token and mode ---------- */
export function TokenStep({ a, set, token, setToken, errors }: StepProps & { token: string; setToken: (v: string) => void }) {
  const modes = [
    { id: 'quick' as const, label: 'Quick setup', body: 'Token and admin account only. Everything else keeps its default and can be changed later in Settings.', time: 'About a minute' },
    { id: 'guided' as const, label: 'Guided setup', body: 'Name, address, look, audience and your first stream, one question group at a time.', time: 'About five minutes' },
  ]
  return (
    <Group>
      <div role="radiogroup" aria-label="Setup path" className="grid gap-2 sm:grid-cols-2">
        {modes.map((m) => (
          <button key={m.id} type="button" role="radio" aria-checked={a.mode === m.id} onClick={() => set({ mode: m.id })}
            className={cn('rounded-md border p-3 text-left transition-colors', a.mode === m.id ? 'border-accent bg-accent/[0.06]' : 'border-line-strong hover:border-faint')}>
            <div className="flex items-center justify-between"><span className="text-[13px] font-medium text-ink">{m.label}</span>{a.mode === m.id && <Icon name="check" size={14} className="text-accent" />}</div>
            <p className="mt-1 text-xs text-muted">{m.body}</p><Legend className="mt-2 block">{m.time}</Legend>
          </button>
        ))}
      </div>
      <TextInput label="Setup token" value={token} onChange={(e) => setToken(e.target.value)} autoFocus autoComplete="off" required className="font-mono text-xs" placeholder="Paste the token" error={errors.token}
        hint={<>TinyIce prints it in the terminal or service log when it starts, on the line <code className="rounded border border-line bg-raised px-1 font-mono">Setup Token: ...</code>. Under systemd: <code className="rounded border border-line bg-raised px-1 font-mono">journalctl -u tinyice</code>. It proves you control this machine and changes on every restart until setup is done.</>} />
    </Group>
  )
}

/* ---------- 2. station ---------- */
export function StationStep({ a, set, errors }: StepProps) {
  return (
    <Group>
      <TextInput label="Station name" value={a.title} onChange={(e) => set({ title: e.target.value })} autoFocus maxLength={80} placeholder="My Radio" error={errors.title} hint="Shown in the header and browser tab of every public page." />
      <TextInput label="Tagline (optional)" value={a.subtitle} onChange={(e) => set({ subtitle: e.target.value })} maxLength={160} placeholder="Jazz and ambient from Leipzig" error={errors.subtitle} hint="One line under the name on the front page." />
      <TextInput label="Location (optional)" value={a.location} onChange={(e) => set({ location: e.target.value })} maxLength={80} placeholder="Leipzig, Germany" error={errors.location} hint="City and country. Published in the Icecast-compatible status document at /status-json.xsl, which anyone can read." />
      <TextInput label="Contact email (optional)" type="email" value={a.email} onChange={(e) => set({ email: e.target.value })} maxLength={120} autoComplete="email" placeholder="studio@example.org" error={errors.email} hint="Also published in /status-json.xsl, so leave it empty if you do not want it public." />
    </Group>
  )
}

/* ---------- 3. address ---------- */
export function AddressStep({ a, set, errors }: StepProps) {
  const insecure = location.protocol === 'http:' && !isLocalHost(location.hostname)
  return (
    <Group>
      <TextInput label="Hostname" value={a.hostname} onChange={(e) => set({ hostname: e.target.value })} autoFocus placeholder="radio.example.org" error={errors.hostname} className="font-mono text-xs"
        hint="The name or IP address listeners and encoders connect to, without http:// or a port. Used in stream links the server hands out and in the status document." />
      <TextInput label="Public address" value={a.baseUrl} onChange={(e) => set({ baseUrl: e.target.value })} inputMode="url" placeholder="https://radio.example.org" error={errors.base_url} className="font-mono text-xs"
        hint="The full address of this server as the internet sees it, without a path. Used for embeds, share links and directory listing. Both are filled in from the address you are using now; change them if listeners will use another name." />
      {insecure && (
        <Notice tone="warn">
          You are on plain http. Use HTTPS before you invite listeners: passwords and sessions cross the network unencrypted, and browsers block microphone broadcasting and some features on http pages. See the <a href="/developers" className="underline">documentation</a> and the server's HTTPS options.
        </Notice>
      )}
    </Group>
  )
}

/* ---------- 4. look ---------- */
function Preview({ theme, accent, title }: { theme: 'dark' | 'light'; accent: string; title: string }) {
  const t = THEME_TOKENS[theme]
  const style = { ...t, '--accent': accent, '--accent-fg': accentForeground(accent), '--live': theme === 'dark' ? '#2fbf71' : '#14a15a' } as unknown as React.CSSProperties
  return (
    <div style={style} className="overflow-hidden rounded-md border border-line-strong bg-bg" aria-hidden>
      <div className="flex h-9 items-center gap-2 border-b border-line px-3"><Logo size={18} /><span className="truncate text-xs font-semibold text-ink">{title || 'My Radio'}</span><span className="ml-auto text-[11px] text-accent">Admin</span></div>
      <div className="space-y-2.5 p-3">
        <div className="flex items-center gap-2"><span className="flex size-8 items-center justify-center rounded-md bg-accent text-accent-fg"><Icon name="play" size={14} /></span><div className="min-w-0"><div className="text-xs font-medium text-ink">Morning Show</div><div className="text-[11px] text-muted"><span className="text-accent">/live</span> · 142 listeners</div></div></div>
        <span className="inline-flex h-7 items-center rounded-md bg-accent px-3 text-xs font-medium text-accent-fg">Listen</span>
      </div>
    </div>
  )
}
export function LookStep({ a, set, errors }: StepProps) {
  // Recolour the wizard itself while choosing, so the whole page previews the accent.
  useEffect(() => { if (parseHex(a.accent)) applyAccent(a.accent) }, [a.accent])
  const bad = contrastRows(parseHex(a.accent) ? a.accent : '#ff6a00').filter((r) => !r.pass)
  return (
    <Group>
      <div>
        <Legend>Accent colour</Legend>
        <div className="mt-2 flex flex-wrap items-center gap-2" role="radiogroup" aria-label="Accent swatches">
          {SWATCHES.map((c) => (
            <button key={c} type="button" role="radio" aria-checked={a.accent.toLowerCase() === c} aria-label={c} onClick={() => set({ accent: c })}
              className={cn('flex size-8 items-center justify-center rounded-md border-2 transition-transform hover:scale-105', a.accent.toLowerCase() === c ? 'border-ink' : 'border-transparent')} style={{ background: c }}>
              {a.accent.toLowerCase() === c && <Icon name="check" size={14} style={{ color: accentForeground(c) }} />}
            </button>
          ))}
        </div>
        <div className="mt-3 flex items-center gap-2">
          <input type="color" aria-label="Pick a colour" value={parseHex(a.accent) ? a.accent : '#ff6a00'} onChange={(e) => set({ accent: e.target.value })} className="h-9 w-12 cursor-pointer rounded-md border border-line-strong bg-bg p-1" />
          <TextInput label="" aria-label="Hex colour" value={a.accent} onChange={(e) => set({ accent: e.target.value.startsWith('#') ? e.target.value : `#${e.target.value}` })} maxLength={7} className="w-28 font-mono text-xs" />
        </div>
        {errors.accent && <p className="mt-1 text-xs text-danger">{errors.accent}</p>}
      </div>
      <div className="grid gap-2 sm:grid-cols-2"><Preview theme="dark" accent={a.accent} title={a.title} /><Preview theme="light" accent={a.accent} title={a.title} /></div>
      {bad.length > 0
        ? <Notice tone="warn">Low contrast: {bad.map((r) => `${r.label.toLowerCase()} ${r.ratio.toFixed(1)}:1 (needs ${r.need === 'text' ? '4.5' : '3'})`).join('; ')}. It will still work, but some visitors may struggle to read it. Pick a darker or lighter shade.</Notice>
        : <Help>Contrast is readable on both themes.</Help>}
      <Help>To use your own logo, upload it later under Settings, Branding.</Help>
    </Group>
  )
}

/* ---------- 5. audience ---------- */
export function AudienceStep({ a, set }: StepProps) {
  return (
    <Group>
      <div role="radiogroup" aria-label="Expected audience" className="space-y-2">
        {AUDIENCE.map((o) => (
          <button key={o.id} type="button" role="radio" aria-checked={a.audience === o.id} onClick={() => set({ audience: o.id })}
            className={cn('flex w-full items-start gap-3 rounded-md border p-3 text-left transition-colors', a.audience === o.id ? 'border-accent bg-accent/[0.06]' : 'border-line-strong hover:border-faint')}>
            <span className={cn('mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-full border', a.audience === o.id ? 'border-accent' : 'border-line-strong')}>{a.audience === o.id && <span className="size-2 rounded-full bg-accent" />}</span>
            <span className="min-w-0 flex-1">
              <span className="flex items-baseline justify-between gap-2"><span className="text-[13px] font-medium text-ink">{o.label}</span><span className="num font-mono text-[11px] text-muted">{o.max ? `up to ${o.max} per stream` : 'no limit'}</span></span>
              <span className="block text-xs text-muted">{o.why}</span>
              {o.max > 0 && <span className="num mt-0.5 block font-mono text-[11px] text-faint">Full house at 128 kbps: about {bandwidth(o.max)} upload</span>}
            </span>
          </button>
        ))}
      </div>
      <Help>The limit applies to each stream. Each listener uses about 16 KB/s at 128 kbps (128 / 8). Make sure your upload bandwidth can carry the audience you choose.</Help>
      <div className="flex items-start justify-between gap-4 rounded-md border border-line p-3">
        <div className="min-w-0">
          <div className="text-[13px] font-medium text-ink">List my station in public directories</div>
          <p className="mt-1 text-xs text-muted">Announces your public streams (name, genre, listener count and listen address) to the Xiph directory so people can find you. Anything announced is public and may be copied by other sites. Off by default. The announcement task starts with the server, so it may need a restart to begin.</p>
        </div>
        <Switch label="List my station in public directories" checked={a.directory} onCheckedChange={(v) => set({ directory: v })} />
      </div>
    </Group>
  )
}

/* ---------- 6. first stream ---------- */
export function MountStep({ a, set, errors }: StepProps) {
  const m = normalizeMount(a.mount)
  const ok = !checkMount(a.mount)
  const port = location.port || (location.protocol === 'https:' ? '443' : '80')
  const rows: [string, string][] = [['Server', a.hostname || location.hostname], ['Port', port], ['Mount', ok ? m : '...'], ['User', 'source'], ['Password', 'created when you finish']]
  return (
    <Group>
      <TextInput label="Mount path" value={a.mount} onChange={(e) => set({ mount: e.target.value })} autoFocus className="font-mono text-xs" placeholder="/live" error={errors.path}
        hint="The address of your first stream, for example /live. Listeners open this path; encoders publish to it. You can add more mounts later." />
      <div>
        <Legend>How an encoder will connect</Legend>
        <dl className="mt-2 divide-y divide-line rounded-md border border-line text-xs">
          {rows.map(([k, v]) => <div key={k} className="flex justify-between gap-3 px-3 py-1.5"><dt className="text-muted">{k}</dt><dd className="break-all font-mono text-ink">{v}</dd></div>)}
        </dl>
        <div className="mt-2"><Help>This is a preview. A source password for the mount is generated when you finish and shown once.</Help></div>
      </div>
    </Group>
  )
}

/* ---------- 7. account ---------- */
export function AccountStep({ a, set, password, setPassword, confirm, setConfirm, min, errors }: StepProps & { password: string; setPassword: (v: string) => void; confirm: string; setConfirm: (v: string) => void; min: number }) {
  const lengthOk = password.length >= min
  const matchOk = password !== '' && password === confirm
  const s = strength(password)
  return (
    <Group>
      <TextInput label="Username" value={a.username} onChange={(e) => set({ username: e.target.value })} autoComplete="username" autoFocus required error={errors.username} hint="This account has full control of the server. You can add teammates with limited access later." />
      <PasswordInput label="Password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" required minLength={min} error={!!errors.password} />
      <div className="flex items-center gap-2" aria-live="polite">
        <div className="flex flex-1 gap-1" aria-hidden>{[1, 2, 3, 4].map((i) => <span key={i} className={cn('h-1 flex-1 rounded-full', s.score >= i ? (s.score <= 1 ? 'bg-warn' : 'bg-live') : 'bg-line-strong')} />)}</div>
        <span className="w-24 text-right text-xs text-muted">{s.label}</span>
      </div>
      <PasswordInput label="Confirm password" value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" required error={confirm !== '' && !matchOk} />
      <ul aria-label="Password requirements" className="space-y-1">
        <li className={cn('flex items-center gap-2 text-xs', lengthOk ? 'text-live' : 'text-faint')}><Icon name={lengthOk ? 'check' : 'minus'} size={12} />At least {min} characters ({password.length} so far)</li>
        <li className={cn('flex items-center gap-2 text-xs', matchOk ? 'text-live' : 'text-faint')}><Icon name={matchOk ? 'check' : 'minus'} size={12} />Both entries match</li>
      </ul>
      {errors.password && <p className="text-xs text-danger">{errors.password}</p>}
      <Help>Longer is better than clever: a few random words beat a short mix of symbols. You can add a passkey after the account is created.</Help>
    </Group>
  )
}

/* ---------- 8. review ---------- */
export function ReviewStep({ a, goto }: { a: Answers; goto: (s: 'station' | 'address' | 'look' | 'audience' | 'mount' | 'account') => void }) {
  const aud = AUDIENCE.find((x) => x.id === a.audience)!
  const none = <span className="text-faint">not set</span>
  const groups: { id: Parameters<typeof goto>[0]; title: string; rows: [string, ReactNode][] }[] = [
    { id: 'station', title: 'Station', rows: [['Name', a.title || none], ['Tagline', a.subtitle || none], ['Location', a.location || none], ['Email', a.email || none]] },
    { id: 'address', title: 'Address', rows: [['Hostname', a.hostname || none], ['Public address', a.baseUrl || none]] },
    { id: 'look', title: 'Look', rows: [['Accent', <span className="inline-flex items-center gap-2"><span className="size-3 rounded-sm border border-line-strong" style={{ background: a.accent }} />{a.accent}</span>]] },
    { id: 'audience', title: 'Audience', rows: [['Listener limit', aud.max ? `${aud.label}: ${aud.max} per stream` : 'Unlimited'], ['Public directories', a.directory ? 'Listed' : 'Not listed']] },
    { id: 'mount', title: 'First stream', rows: [['Mount', normalizeMount(a.mount)]] },
    { id: 'account', title: 'Admin account', rows: [['Username', a.username], ['Password', 'hidden']] },
  ]
  return (
    <div className="space-y-3">
      {groups.map((g) => (
        <section key={g.id} className="rounded-md border border-line">
          <header className="flex items-center justify-between border-b border-line px-3 py-1.5"><Legend>{g.title}</Legend><button type="button" onClick={() => goto(g.id)} className="text-xs text-accent hover:underline" aria-label={`Edit ${g.title}`}>Edit</button></header>
          <dl className="divide-y divide-line text-xs">{g.rows.map(([k, v]) => <div key={k} className="flex justify-between gap-4 px-3 py-1.5"><dt className="shrink-0 text-muted">{k}</dt><dd className="min-w-0 break-words text-right text-ink">{v}</dd></div>)}</dl>
        </section>
      ))}
      <Help>Nothing is stored until you press Create station. The next screen shows what the server confirmed.</Help>
    </div>
  )
}

