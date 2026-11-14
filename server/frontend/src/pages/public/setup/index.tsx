import { useEffect, useRef, useState, type FormEvent } from 'react'
import { Button } from '@/ui/controls'
import { cn } from '@/lib/cn'
import { AuthLayout, Notice } from '../shared/AuthLayout'
import { Done } from './Done'
import { AccountStep, AddressStep, AudienceStep, LookStep, MountStep, ReviewStep, StationStep, TokenStep } from './steps'
import { GUIDED, QUICK, STEP_LABEL, buildPayload, clearSaved, load, save, stepErrors, stepForField, type Answers, type Result, type StepId } from './model'

const MIN_PASSWORD = 8
const ALREADY = 'Setup is already complete on this server.'

/** POST JSON and return {status, body}; body is {} when the response is not JSON (a 404 page). */
async function post(path: string, payload: unknown) {
  const res = await fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) })
  const body = await res.json().catch(() => ({})) as Record<string, unknown>
  return { status: res.status, body }
}

const HEAD: Record<StepId, [string, string]> = {
  token: ['Set up TinyIce', 'Choose how much you want to configure now. Everything can be changed later in Settings.'],
  station: ['Your station', 'How your station introduces itself on the public pages.'],
  address: ['Your address', 'How listeners and encoders will reach this server.'],
  look: ['Look', 'Pick the accent colour of the public pages and the admin console.'],
  audience: ['Audience', 'How many people do you expect to listen at once?'],
  mount: ['Your first stream', 'Every stream lives at a mount path. Start with one.'],
  account: ['Admin account', 'This account has full control of the server.'],
  review: ['Review', 'Check your answers. Use Edit to change a group.'],
}

/** "mount: message" or "station.title: message" from the server: the field names the step; the field key names the input. */
function parseServerError(msg: string) {
  const i = msg.indexOf(':')
  const field = i > 0 ? msg.slice(0, i).trim() : ''
  const key = field.split('.').pop() ?? ''
  const map: Record<string, string> = { admin_email: 'email', accent_color: 'accent', mount: 'path' }
  return { field, key: map[key] ?? key, text: i > 0 ? msg.slice(i + 1).trim() : msg }
}

export function App() {
  const saved = useRef(load())
  const [a, setA] = useState<Answers>(saved.current.a)
  const [step, setStep] = useState<StepId>('token')
  const [reached, setReached] = useState(0)
  const [token, setToken] = useState('')
  const [tokenOk, setTokenOk] = useState(false)
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [banner, setBanner] = useState('')
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<Result | null>(null)
  const [already, setAlready] = useState(false)
  const body = useRef<HTMLDivElement>(null)

  const steps = a.mode === 'quick' ? QUICK : GUIDED
  const idx = Math.max(0, steps.indexOf(step))
  const set = (patch: Partial<Answers>) => { setA((p) => ({ ...p, ...patch })); setErrors({}); setBanner('') }

  // Answers (never the token or password) survive a reload.
  useEffect(() => { if (tokenOk) save(a, step); else save(a, saved.current.step) }, [a, step, tokenOk])
  useEffect(() => { body.current?.querySelector<HTMLElement>('input:not([type=hidden]):not([type=color])')?.focus() }, [step])

  const go = (s: StepId) => { setErrors({}); setBanner(''); setStep(s); setReached((r) => Math.max(r, steps.indexOf(s))) }

  async function verify() {
    setBusy(true); setBanner('')
    try {
      const { status, body: b } = await post('/setup/verify-token', { token: token.trim() })
      if (b.valid) {
        setTokenOk(true)
        const resume = saved.current.step
        const want = steps.includes(resume) && resume !== 'token' ? resume : steps[1]
        go(want === 'review' && !password ? 'account' : want)
      } else if (status === 404) setAlready(true)
      else if (status === 403) setErrors({ token: 'That setup token does not match. Copy it again from the terminal where TinyIce was started.' })
      else setBanner(typeof b.error === 'string' ? b.error : 'The token could not be verified.')
    } catch { setBanner('Could not reach the server. Check your connection and try again.') }
    setBusy(false)
  }

  async function finish() {
    setBusy(true); setBanner('')
    try {
      const { status, body: b } = await post('/setup/complete', buildPayload(a, token.trim(), password))
      if (b.success) { setResult(b as unknown as Result); setPassword(''); setConfirm(''); setToken(''); clearSaved(); setBusy(false); return }
      if (status === 404) { setAlready(true); setBusy(false); return }
      if (status === 403) { setTokenOk(false); setStep('token'); setErrors({ token: 'The setup token was rejected. Copy it again from the terminal; it changes when the server restarts.' }); setBusy(false); return }
      const msg = typeof b.error === 'string' ? b.error : 'Setup failed.'
      const pe = parseServerError(msg)
      const target = stepForField(pe.field)
      setBanner(target && steps.includes(target) && pe.key ? '' : msg)
      if (target && steps.includes(target)) { setStep(target); setErrors(pe.key ? { [pe.key]: pe.text } : {}) }
      else if (a.mode === 'quick') setStep('account')
    } catch { setBanner('Could not reach the server. Check your connection and try again.') }
    setBusy(false)
  }

  async function next(e?: FormEvent) {
    e?.preventDefault()
    if (busy) return
    if (step === 'token') { if (!token.trim()) { setErrors({ token: 'Paste the setup token first.' }); return } if (tokenOk) { go(steps[1]); return } await verify(); return }
    const errs = stepErrors(step, a)
    if (step === 'account') {
      if (!a.username.trim()) errs.username = 'Enter a username.'
      if (password.length < MIN_PASSWORD) errs.password = `The password must be at least ${MIN_PASSWORD} characters.`
      else if (password !== confirm) errs.password = 'The two passwords do not match.'
    }
    if (Object.keys(errs).length) { setErrors(errs); return }
    if (step === 'review' || (step === 'account' && a.mode === 'quick')) { await finish(); return }
    go(steps[idx + 1])
  }

  const stepProps = { a, set, errors }
  const [heading, sub] = HEAD[step]

  if (already) return <AuthLayout heading="Setup is complete" sub="This server already has an administrator."><Notice tone="warn">{ALREADY}</Notice><a href="/login" className="mt-4 block"><Button variant="primary" className="h-10 w-full justify-center text-sm">Go to sign in</Button></a></AuthLayout>
  if (result) return <AuthLayout heading="Your station is ready" sub="Keep the source passwords below: they are shown only once." width="max-w-[560px]"><Done a={a} result={result} /></AuthLayout>

  const last = step === 'review' || (step === 'account' && a.mode === 'quick')
  return (
    <AuthLayout heading={heading} sub={sub} width="max-w-[560px]">
      <nav aria-label="Setup progress" className="mb-5">
        <ol className="flex items-center gap-1.5">
          {steps.map((s, i) => {
            const can = tokenOk && i <= reached && s !== 'token' && !busy
            return (
              <li key={s} className="min-w-0 flex-1" aria-current={s === step ? 'step' : undefined}>
                <button type="button" disabled={!can} onClick={() => go(s)} aria-label={`Step ${i + 1}: ${STEP_LABEL[s]}`} className="group flex w-full flex-col gap-1.5 text-left disabled:cursor-default">
                  <span className={cn('h-0.5 w-full rounded-full', i <= idx ? 'bg-accent' : i <= reached ? 'bg-accent/40' : 'bg-line-strong')} />
                  <span className={cn('hidden truncate font-mono text-[10px] uppercase tracking-[0.09em] md:block', s === step ? 'text-ink' : 'text-faint', can && 'group-hover:text-ink')}>{STEP_LABEL[s]}</span>
                </button>
              </li>
            )
          })}
        </ol>
        <p className="mt-2 font-mono text-[10px] uppercase tracking-[0.09em] text-faint md:hidden">Step {idx + 1} of {steps.length} · {STEP_LABEL[step]}</p>
      </nav>

      <form onSubmit={next} noValidate>
        <div ref={body} key={step}>
          {step === 'token' && <TokenStep {...stepProps} token={token} setToken={(v) => { setToken(v); setTokenOk(false); setErrors({}) }} />}
          {step === 'station' && <StationStep {...stepProps} />}
          {step === 'address' && <AddressStep {...stepProps} />}
          {step === 'look' && <LookStep {...stepProps} />}
          {step === 'audience' && <AudienceStep {...stepProps} />}
          {step === 'mount' && <MountStep {...stepProps} />}
          {step === 'account' && <AccountStep {...stepProps} password={password} setPassword={(v) => { setPassword(v); setErrors({}) }} confirm={confirm} setConfirm={(v) => { setConfirm(v); setErrors({}) }} min={MIN_PASSWORD} />}
          {step === 'review' && <ReviewStep a={a} goto={go} />}
        </div>
        {banner && <div className="mt-4"><Notice>{banner}</Notice></div>}
        <div className="mt-6 flex gap-2">
          {idx > 0 && <Button type="button" variant="ghost" onClick={() => go(steps[idx - 1])} className="h-10">Back</Button>}
          <Button type="submit" variant="primary" loading={busy} className="h-10 flex-1 justify-center text-sm">
            {step === 'token' ? (tokenOk ? 'Continue' : 'Verify token and continue') : last ? (a.mode === 'quick' ? 'Create account' : 'Create station') : 'Next'}
          </Button>
        </div>
        {step === 'token' && <p className="mt-3 text-center text-xs text-faint">{a.mode === 'guided' ? 'Your answers stay in this browser tab if you reload. Passwords and the token never do.' : 'Two steps: token, then your admin account.'}</p>}
      </form>
    </AuthLayout>
  )
}
