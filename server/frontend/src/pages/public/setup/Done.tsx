import { useState } from 'react'
import { startRegistration } from '@simplewebauthn/browser'
import { Button } from '@/ui/controls'
import { Legend } from '@/ui/console'
import { Icon, type IconName } from '@/ui/icons'
import { Checkbox, CodeBlock, CopyButton } from '@/ui/kit'
import { cn } from '@/lib/cn'
import { Notice } from '../shared/AuthLayout'
import { type Answers, type Result } from './model'

const GROUPS: [keyof NonNullable<Result['applied']>, string][] = [['station', 'Station name and details'], ['address', 'Address'], ['appearance', 'Accent colour'], ['audience', 'Audience limits']]

export function Done({ a, result }: { a: Answers; result: Result }) {
  const [saved, setSaved] = useState(false)
  const [passkey, setPasskey] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const mount = result.mount || '/live'
  const host = (a.mode === 'guided' && a.hostname) || location.hostname
  const port = location.port || (location.protocol === 'https:' ? '443' : '80')
  const tls = location.protocol === 'https:'
  const applied = result.applied ?? {}

  async function registerPasskey() {
    setBusy(true); setError('')
    try {
      const begin = await fetch('/api/passkey/register/begin', { method: 'POST' })
      if (!begin.ok) throw new Error((await begin.json().catch(() => ({}))).error || 'Could not start passkey registration')
      const options = await begin.json()
      const credential = await startRegistration({ optionsJSON: options.publicKey ?? options })
      const finish = await fetch('/api/passkey/register/finish?name=Setup+Passkey', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(credential) })
      if (!finish.ok) throw new Error((await finish.json().catch(() => ({}))).error || 'Passkey registration failed')
      setPasskey(true)
    } catch (e) {
      if ((e as Error).name !== 'NotAllowedError') setError((e as Error).message)
    }
    setBusy(false)
  }

  const ffmpeg = `ffmpeg -re -i input.mp3 -c:a libmp3lame -b:a 128k -content_type audio/mpeg \\\n  -f mp3 "icecast://source:${result.live_mount_pass}@${host}:${port}${mount}"`
  const table: [string, string][] = [['Server type', 'Icecast'], ['Address', host], ['Port', port], ['Mount', mount], ['User', 'source'], ['Password', result.live_mount_pass], ['TLS', tls ? 'on' : 'off']]
  const next: { icon: IconName; label: string; body: string; href: string }[] = [
    { icon: 'dashboard', label: 'Open the dashboard', body: 'Live listeners, streams and bandwidth.', href: '/admin' },
    { icon: 'golive', label: 'Go live from the browser', body: 'Broadcast from your microphone, no encoder needed.', href: '/admin/golive' },
    { icon: 'autodj', label: 'Create an AutoDJ', body: 'Play a folder of music around the clock.', href: '/admin/autodj' },
    { icon: 'users', label: 'Add a teammate', body: 'Give someone their own login.', href: '/admin/users' },
    { icon: 'security', label: 'Review security', body: 'Bans, whitelist and sign-in options.', href: '/admin/security' },
  ]

  return (
    <div className="space-y-5">
      <ul className="space-y-1.5" aria-label="What the server confirmed">
        <li className="flex items-center gap-2 text-xs text-live"><Icon name="check" size={12} />Admin account created</li>
        <li className="flex items-center gap-2 text-xs text-live"><Icon name="check" size={12} />Source passwords generated for {mount}</li>
        {a.mode === 'guided' && GROUPS.map(([k, label]) => applied[k]
          ? <li key={k} className="flex items-center gap-2 text-xs text-live"><Icon name="check" size={12} />{label} saved</li>
          : <li key={k} className="flex items-center gap-2 text-xs text-faint"><Icon name="minus" size={12} />{label}: not changed (default kept)</li>)}
        {a.mode === 'quick' && <li className="flex items-center gap-2 text-xs text-faint"><Icon name="minus" size={12} />Everything else keeps its default. Change it under Settings.</li>}
      </ul>

      <div>
        <Legend>Source passwords, shown once</Legend>
        <div className="mt-2 rounded-md border border-line bg-bg">
          {([['Mount password', result.live_mount_pass, mount], ['Default source password', result.default_source_pass, '']] as const).map(([k, v, m]) => (
            <div key={k} className="flex items-center gap-2 border-b border-line px-3 py-2.5 last:border-b-0">
              <div className="min-w-0 flex-1"><Legend>{k}</Legend>{m && <span className="ml-2 font-mono text-[11px] text-muted">{m}</span>}<div className="mt-0.5 select-all break-all font-mono text-[13px] text-ink">{v}</div></div>
              <CopyButton text={v} label={`Copy ${k.toLowerCase()}`} />
            </div>
          ))}
        </div>
        <p className="mt-2 text-xs text-muted">They are stored hashed and cannot be shown again. New ones can be set under Streams in the admin console.</p>
      </div>

      <div className="space-y-3">
        <Legend>Connect an encoder</Legend>
        <CodeBlock label="ffmpeg">{ffmpeg}</CodeBlock>
        <div className="rounded-md border border-line">
          <div className="flex h-8 items-center justify-between border-b border-line px-3"><Legend>BUTT, Mixxx, OBS</Legend></div>
          <dl className="divide-y divide-line text-xs">{table.map(([k, v]) => <div key={k} className="flex justify-between gap-3 px-3 py-1.5"><dt className="text-muted">{k}</dt><dd className="break-all text-right font-mono text-ink">{v}</dd></div>)}</dl>
        </div>
      </div>

      {typeof PublicKeyCredential !== 'undefined' && (passkey
        ? <Notice tone="live">Passkey registered. You can sign in with it next time.</Notice>
        : <div className="rounded-md border border-line p-3"><div className="text-[13px] font-medium text-ink">Add a passkey (optional)</div><p className="mb-2.5 mt-0.5 text-xs text-muted">Sign in with your device instead of typing a password. You can add one later under Security.</p><Button type="button" onClick={registerPasskey} loading={busy} className="h-9 w-full justify-center"><Icon name="fingerprint" size={16} />Register a passkey</Button></div>)}
      {error && <Notice>{error}</Notice>}

      <label className="flex cursor-pointer items-center gap-2.5 text-[13px] text-ink"><Checkbox checked={saved} onChange={setSaved} label="I have saved these passwords" />I have saved these passwords</label>

      <div>
        <Legend>Next steps</Legend>
        <ul className={cn('mt-2 divide-y divide-line rounded-md border border-line', !saved && 'opacity-60')}>
          {next.map((n, i) => (
            <li key={n.href}>
              <a href={saved ? n.href : undefined} aria-disabled={!saved} tabIndex={saved ? undefined : -1} className={cn('flex items-center gap-3 px-3 py-2.5 transition-colors', saved ? 'hover:bg-hover' : 'cursor-not-allowed')}>
                <Icon name={n.icon} size={16} className={i === 0 ? 'text-accent' : 'text-faint'} />
                <span className="min-w-0 flex-1"><span className="block text-[13px] font-medium text-ink">{n.label}</span><span className="block text-xs text-muted">{n.body}</span></span>
                <Icon name="chevron-right" size={14} className="text-faint" />
              </a>
            </li>
          ))}
        </ul>
        {!saved && <p className="mt-1.5 text-xs text-faint">Tick the box above to continue.</p>}
      </div>
    </div>
  )
}
