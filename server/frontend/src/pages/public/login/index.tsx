import { useState, type FormEvent } from 'react'
import { startAuthentication } from '@simplewebauthn/browser'
import { Button } from '@/ui/controls'
import { Icon } from '@/ui/icons'
import { page } from '@/lib/api'
import type { TinyIceBase } from '@/types'
import { AuthLayout, Notice, PasswordInput, TextInput } from '../shared/AuthLayout'

const data = page<Partial<TinyIceBase> & { error?: string }>()

/**
 * Auth redirects carry where the user was headed (/kiosk sends ?next=). Only
 * same-origin absolute paths are followed; the server applies the same rule
 * (safeNextPath in server/helpers.go) to its own redirect.
 */
function safePath(raw: string): string {
  if (!raw.startsWith('/') || raw.startsWith('//') || raw.startsWith('/\\')) return '/admin'
  return raw
}
const nextParam = () => new URLSearchParams(location.search).get('next') || ''
const nextPath = () => safePath(nextParam())

/** Lockout text from checkAuthLimit: "too many failed attempts. locked out until HH:MM:SS". */
function describe(msg: string): { text: string; locked: boolean } {
  const m = /locked out until (\d{2}:\d{2}:\d{2})/i.exec(msg)
  if (m) return { locked: true, text: `Too many failed attempts. Sign-in from this address is locked until ${m[1]} (server time). Try again after that.` }
  return { locked: false, text: msg.charAt(0).toUpperCase() + msg.slice(1) }
}

function PasskeyButton({ onError }: { onError: (m: string) => void }) {
  const [busy, setBusy] = useState(false)
  async function run() {
    setBusy(true); onError('')
    try {
      const begin = await fetch('/api/passkey/login/begin', { method: 'POST' })
      if (!begin.ok) throw new Error((await begin.json().catch(() => ({}))).error || 'Could not start passkey sign-in')
      const opts = await begin.json()
      const credential = await startAuthentication({ optionsJSON: opts.publicKey })
      const finish = await fetch(`/api/passkey/login/finish?challengeKey=${encodeURIComponent(opts.challengeKey)}`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(credential),
      })
      if (!finish.ok) throw new Error((await finish.json().catch(() => ({}))).error || 'Passkey sign-in failed')
      const result = await finish.json()
      // The server always answers "/admin"; a ?next= from the redirect that brought us here wins, after the same-origin check.
      location.assign(nextParam() ? nextPath() : safePath(result.redirect || '/admin'))
    } catch (e) {
      const err = e as Error
      onError(err.name === 'NotAllowedError' ? 'Passkey sign-in was cancelled or timed out.' : err.message || 'Passkey sign-in failed')
      setBusy(false)
    }
  }
  return (
    <Button type="button" onClick={run} loading={busy} className="h-10 w-full justify-center text-sm"><Icon name="fingerprint" size={16} />Sign in with a passkey</Button>
  )
}

export function App() {
  const hasPasskeys = !!data.passkeysEnabled && typeof PublicKeyCredential !== 'undefined'
  const providers = data.oidcProviders ?? []
  const hasAlt = hasPasskeys || providers.length > 0
  const [error, setError] = useState(data.error ?? '')
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    if (busy) return
    setError(''); setBusy(true)
    const form = new FormData(e.currentTarget)
    form.set('next', nextPath())
    try {
      const res = await fetch('/login', { method: 'POST', body: new URLSearchParams(form as unknown as Record<string, string>), headers: { Accept: 'application/json' } })
      if (res.ok || res.redirected) { location.assign(nextPath()); return }
      const body = await res.json().catch(() => ({}))
      setError(body.error || 'Invalid username or password')
    } catch {
      setError('Could not reach the server. Check your connection and try again.')
    }
    setBusy(false)
  }

  const err = error ? describe(error) : null
  return (
    <AuthLayout heading="Sign in" sub="Operator access to the admin console.">
      <div className="space-y-4">
        {hasPasskeys && <PasskeyButton onError={setError} />}
        {providers.map((p) => (
          <a key={p.id} href={`/auth/${encodeURIComponent(p.id)}`} className="flex h-10 w-full items-center justify-center gap-2.5 rounded-md border border-line-strong bg-raised text-sm font-medium text-ink transition-colors hover:bg-hover">
            <span className="flex size-5 items-center justify-center rounded bg-hover font-mono text-[11px] text-muted" aria-hidden>{p.name.charAt(0).toUpperCase()}</span>
            Continue with {p.name}
          </a>
        ))}
        {hasAlt && <div className="flex items-center gap-3 text-xs text-faint" role="separator"><span className="h-px flex-1 bg-line" />or use a password<span className="h-px flex-1 bg-line" /></div>}

        {/* Plain POST works without JavaScript; the handler above upgrades it to a fetch with inline errors. */}
        <form method="post" action="/login" onSubmit={submit} className="space-y-4">
          <input type="hidden" name="next" value={nextPath()} />
          <TextInput label="Username" name="username" autoComplete="username" required autoFocus />
          <PasswordInput label="Password" name="password" autoComplete="current-password" required error={!!err && !err.locked} />
          {err && <Notice tone={err.locked ? 'warn' : 'danger'}>{err.text}</Notice>}
          <Button type="submit" variant="primary" loading={busy} className="h-10 w-full justify-center text-sm">{busy ? 'Signing in' : 'Sign in'}</Button>
        </form>

        {providers.length > 0 && <p className="text-center text-xs text-faint">No account yet? Continue with a provider above to request access.</p>}
      </div>
      <p className="mt-5 border-t border-line pt-4 text-center text-xs"><a href="/" className="text-muted hover:text-ink">Back to the station page</a></p>
    </AuthLayout>
  )
}
