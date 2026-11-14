import { useEffect, useRef, useState } from 'react'
import { Button, Dialog, Field, Input, Switch } from '@/ui/controls'
import { Badge } from '@/ui/display'
import { Icon } from '@/ui/icons'
import { CopyButton } from '@/ui/kit'
import { cn } from '@/lib/cn'
import { validateMount } from '../streams/MountDialog'
import { listRelays, saveRelay, validateUpstream, type Relay } from './model'

/** Bytes of buffered audio a new listener starts from. Seconds are at a typical 128 kbps. */
const PRESETS = [
  { bytes: 65536, name: 'Balanced', badge: true, why: 'New listeners hear audio after about 4 seconds and short network hiccups go unnoticed. The right choice for most stations.', trade: 'About 4 seconds behind the source.' },
  { bytes: 131072, name: 'Smooth', why: 'Gives listeners about 8 seconds of head start, so phones and weak connections stutter less.', trade: 'Costs a few more seconds behind the live source.' },
  { bytes: 16384, name: 'Live as it happens', why: 'About 1 second behind the source. Best for live talk and events.', trade: 'Listeners on shaky connections may hear gaps.' },
]
const DEFAULT_BYTES = 65536

/** Turn the server's rejection text into something an operator can act on. */
export function plainError(msg: string): string {
  if (/non-routable|localhost|loopback|private/i.test(msg)) return 'That address points to this machine or a private network. Relays can only pull from public servers.'
  if (/scheme/i.test(msg)) return 'Only http:// and https:// links work.'
  if (/no host/i.test(msg)) return 'The link has no server name in it.'
  return msg.replace(/^Upstream URL rejected:\s*/i, '')
}

function suggestMount(raw: string): string {
  try {
    const p = new URL(raw.trim()).pathname.replace(/\/+$/, '')
    return p && p !== '/' && /^\/[A-Za-z0-9._~\-/]+$/.test(p) ? p : '/relay'
  } catch { return '/relay' }
}

type Phase = 'form' | 'connecting' | 'connected' | 'timeout'

/** Add a relay, or edit one. Adding ends on a live connection check. */
export function RelayDialog({ open, onOpenChange, edit, existing, onDone }: {
  open: boolean; onOpenChange: (o: boolean) => void; edit: Relay | null; existing: string[]; onDone: () => void
}) {
  const [url, setUrl] = useState('')
  const [mount, setMount] = useState('')
  const [mountTouched, setMountTouched] = useState(false)
  const [needPw, setNeedPw] = useState(false)
  const [password, setPassword] = useState('')
  const [burst, setBurst] = useState(DEFAULT_BYTES)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [touched, setTouched] = useState(false)
  const [phase, setPhase] = useState<Phase>('form')
  const [savedMount, setSavedMount] = useState('')
  const timer = useRef<ReturnType<typeof setInterval>>(undefined)

  useEffect(() => {
    if (!open) return
    clearInterval(timer.current)
    setUrl(edit?.url ?? ''); setMount(edit?.mount ?? ''); setMountTouched(!!edit); setNeedPw(false); setPassword('')
    setBurst(edit?.burst_size || DEFAULT_BYTES); setError(null); setTouched(false); setPhase('form'); setSavedMount('')
  }, [open, edit])
  useEffect(() => () => clearInterval(timer.current), [])

  const onUrl = (v: string) => {
    setUrl(v)
    if (!edit && !mountTouched) setMount(suggestMount(v))
  }

  const urlErr = validateUpstream(url)
  const mountErr = edit ? null : validateMount(mount, existing)
  const kept = edit && edit.burst_size && !PRESETS.some((p) => p.bytes === edit.burst_size) ? edit.burst_size : null
  const normMount = (m: string) => (m.trim().startsWith('/') ? m.trim() : `/${m.trim()}`)

  const watch = (m: string) => {
    setPhase('connecting')
    const t0 = Date.now()
    timer.current = setInterval(async () => {
      let ok = false
      try { ok = !!(await listRelays()).find((r) => r.mount === m)?.active } catch { /* keep polling */ }
      if (ok) { clearInterval(timer.current); setPhase('connected'); onDone() }
      else if (Date.now() - t0 >= 20000) { clearInterval(timer.current); setPhase('timeout') }
    }, 2000)
  }

  const submit = async () => {
    setTouched(true); setError(null)
    if (urlErr || mountErr) return
    setBusy(true)
    try {
      const m = edit ? edit.mount : normMount(mount)
      await saveRelay({ url: url.trim(), mount: m, password: needPw && password ? password : undefined, burst_size: burst })
      onDone()
      if (edit) onOpenChange(false)
      else { setSavedMount(m); watch(m) }
    } catch (e) { setError(plainError((e as Error).message || 'Save failed')) }
    finally { setBusy(false) }
  }

  const origin = window.location.origin
  const link = `${origin}${savedMount}`

  if (phase !== 'form') {
    return (
      <Dialog open={open} onOpenChange={onOpenChange} title={`Relay ${savedMount}`}
        footer={<Button variant={phase === 'connected' ? 'primary' : 'secondary'} onClick={() => onOpenChange(false)}>Close</Button>}>
        <div role="status" aria-live="polite" className="space-y-3">
          {phase === 'connecting' && <p className="flex items-center gap-2 text-[13px] text-ink"><Icon name="refresh" size={14} className="animate-spin text-muted" />Connecting...</p>}
          {phase === 'connected' && <p className="flex items-center gap-2 text-[13px] text-ink"><span className="size-2 rounded-full bg-live tally" />Connected, listeners can tune in.</p>}
          {phase === 'timeout' && <p className="flex gap-2 text-[13px] text-ink"><Icon name="alert" size={14} className="mt-0.5 shrink-0 text-warn" />Could not connect yet. Check the address and password; TinyIce keeps retrying.</p>}
          <div className="flex items-center gap-2 rounded-md border border-line bg-bg px-3 py-2">
            <span className="min-w-0 flex-1 truncate font-mono text-xs text-ink">{link}</span>
            <CopyButton text={link} label="Copy link" />
            <a href={`${origin}/player${savedMount}`} target="_blank" rel="noreferrer" className="flex h-7 items-center gap-1.5 rounded-md px-2 text-xs text-muted hover:bg-hover hover:text-ink"><Icon name="external" size={14} />Open player</a>
          </div>
        </div>
      </Dialog>
    )
  }

  const cards = [...(kept ? [{ bytes: kept, name: 'Current setting (kept)', badge: false, why: 'This relay uses a custom head start. It stays as it is unless you pick another option.', trade: '' }] : []), ...PRESETS]
  return (
    <Dialog open={open} onOpenChange={(o) => !busy && onOpenChange(o)} title={edit ? `Edit relay ${edit.mount}` : 'Add relay'} width="max-w-xl"
      description="A relay re-broadcasts a stream from another server, so your listeners connect here."
      footer={<>
        <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>Cancel</Button>
        <Button variant="primary" loading={busy} onClick={submit}>{edit ? 'Save and reconnect' : 'Add relay'}</Button>
      </>}>
      <form className="max-h-[66vh] space-y-5 overflow-y-auto pr-1" onSubmit={(e) => { e.preventDefault(); void submit() }}>
        <button type="submit" className="hidden" aria-hidden tabIndex={-1} />
        <Field label="1. Where is the stream?" error={touched && urlErr ? plainError(urlErr) : null} hint="Paste the link to the stream, for example https://radio.example.org:8000/jazz.">
          <Input mono value={url} onChange={(e) => onUrl(e.target.value)} onBlur={() => setUrl((v) => v.trim())} placeholder="https://radio.example.org:8000/jazz" autoFocus autoComplete="off" spellCheck={false} aria-invalid={!!(touched && urlErr)} />
        </Field>
        <Field label="2. What should it be called here?" error={touched ? mountErr : null}
          hint={<>Listeners will find it at <span className="break-all font-mono">{origin}{mount ? normMount(mount) : '/...'}</span></>}>
          <Input mono value={mount} onChange={(e) => { setMountTouched(true); setMount(e.target.value) }} placeholder="/jazz" disabled={!!edit} autoComplete="off" spellCheck={false} aria-invalid={!!(touched && mountErr)} />
        </Field>
        <div>
          <div className="flex items-center justify-between gap-4">
            <div><div className="text-xs font-medium text-muted">3. Does the source need a password?</div><div className="text-xs text-faint">{edit ? 'Turn on to replace the saved password.' : 'Most public streams do not.'}</div></div>
            <Switch label="The source needs a password" checked={needPw} onCheckedChange={setNeedPw} />
          </div>
          {needPw && <div className="mt-2"><Input type="password" mono value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" aria-label="Source password" placeholder={edit ? 'New password' : 'Password'} /><p className="mt-1 text-xs text-faint">Saved on this server and never shown again.</p></div>}
        </div>
        <fieldset>
          <legend className="mb-1.5 text-xs font-medium text-muted">4. Listener experience</legend>
          <div role="radiogroup" aria-label="Listener experience" className="space-y-2">
            {cards.map((c) => {
              const on = burst === c.bytes
              return (
                <button key={c.name} type="button" role="radio" aria-checked={on} onClick={() => setBurst(c.bytes)}
                  className={cn('flex w-full gap-3 rounded-md border p-3 text-left transition-colors', on ? 'border-accent bg-accent/5' : 'border-line hover:border-line-strong hover:bg-hover')}>
                  <span className={cn('mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-full border', on ? 'border-accent' : 'border-line-strong')}>{on && <span className="size-2 rounded-full bg-accent" />}</span>
                  <span className="min-w-0">
                    <span className="flex items-center gap-2 text-[13px] font-medium text-ink">{c.name}{c.badge && !edit && <Badge tone="accent">Recommended</Badge>}{c.badge && edit && <Badge tone="accent">Recommended</Badge>}</span>
                    <span className="mt-0.5 block text-xs text-muted">{c.why}</span>
                    {c.trade && <span className="mt-0.5 block text-xs text-faint">{c.trade}</span>}
                  </span>
                </button>
              )
            })}
          </div>
          <p className="mt-1.5 text-[11px] text-faint">Times assume a typical 128 kbps stream.</p>
        </fieldset>
        {error && <p role="alert" className="text-xs text-danger">{error}</p>}
      </form>
    </Dialog>
  )
}
