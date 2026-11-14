import { useState, type InputHTMLAttributes, type ReactNode } from 'react'
import { Icon } from '@/ui/icons'
import { Input } from '@/ui/controls'
import { cn } from '@/lib/cn'
import { Toasts } from '@/app/Toasts'
import { Brand, ThemeToggle } from './Chrome'
import { pageMeta } from './stations'
import './icons'

/** Centered single-card layout for login and setup. */
export function AuthLayout({ children, heading, sub, width = 'max-w-[400px]' }: { children: ReactNode; heading: string; sub?: ReactNode; width?: string }) {
  const m = pageMeta()
  return (
    <div className="relative flex min-h-full flex-col items-center justify-center bg-bg px-5 py-10 text-ink">
      <div className="absolute right-3 top-3"><ThemeToggle /></div>
      <div className={cn('w-full', width)}>
        <a href="/" className="mb-7 flex items-center justify-center gap-2.5" aria-label={`${m.title} home`}>
          <Brand size={32} />
          <span className="text-[15px] font-semibold tracking-tight">{m.title}</span>
        </a>
        <div className="rounded-lg border border-line bg-surface p-6">
          <h1 className="text-lg font-semibold tracking-tight">{heading}</h1>
          {sub && <p className="mt-1 text-[13px] text-muted">{sub}</p>}
          <div className="mt-5">{children}</div>
        </div>
        <p className="mt-5 text-center font-mono text-[11px] text-faint">TinyIce {m.version || 'dev'}</p>
      </div>
      <Toasts />
    </div>
  )
}

/** Inline error/notice. `role=alert` so a screen reader announces it when it appears. */
export function Notice({ tone = 'danger', children }: { tone?: 'danger' | 'warn' | 'live'; children: ReactNode }) {
  const t = { danger: 'border-danger/30 bg-danger/10 text-danger', warn: 'border-warn/30 bg-warn/10 text-warn', live: 'border-live/30 bg-live/10 text-live' }[tone]
  return (
    <div role={tone === 'live' ? 'status' : 'alert'} className={cn('flex items-start gap-2 rounded-md border px-3 py-2 text-[13px]', t)}>
      <Icon name={tone === 'live' ? 'check' : tone === 'warn' ? 'lock' : 'alert'} size={14} className="mt-0.5 shrink-0" />
      <span className="min-w-0 break-words">{children}</span>
    </div>
  )
}

/** Password field with a show/hide toggle and a caps-lock hint. */
export function PasswordInput({ label, error, hint, ...p }: { label: string; error?: boolean; hint?: ReactNode } & Omit<InputHTMLAttributes<HTMLInputElement>, 'type'>) {
  const [show, setShow] = useState(false)
  const [caps, setCaps] = useState(false)
  return (
    <label className="block space-y-1.5">
      <span className="text-xs font-medium text-muted">{label}</span>
      <div className="relative">
        <Input
          {...p}
          type={show ? 'text' : 'password'}
          aria-invalid={error || undefined}
          autoCapitalize="none" autoCorrect="off" spellCheck={false}
          onKeyUp={(e) => setCaps(e.getModifierState?.('CapsLock') ?? false)}
          onBlur={(e) => { setCaps(false); p.onBlur?.(e) }}
          className={cn('h-10 pr-10 text-sm', error && 'border-danger')}
        />
        <button type="button" onClick={() => setShow((s) => !s)} aria-pressed={show} aria-label={show ? 'Hide password' : 'Show password'}
          className="absolute right-1 top-1/2 flex size-8 -translate-y-1/2 items-center justify-center rounded text-faint hover:text-ink">
          <Icon name={show ? 'eye-off' : 'eye'} size={16} />
        </button>
      </div>
      {caps && <span className="block text-xs text-warn">Caps Lock is on.</span>}
      {hint && !caps && <span className="block text-xs text-faint">{hint}</span>}
    </label>
  )
}

/** Text field matching PasswordInput's size. `error` replaces the hint and marks the field invalid. */
export function TextInput({ label, hint, error, ...p }: { label: string; hint?: ReactNode; error?: string | null } & InputHTMLAttributes<HTMLInputElement>) {
  return (
    <label className="block space-y-1.5">
      {label && <span className="text-xs font-medium text-muted">{label}</span>}
      <Input {...p} aria-invalid={error ? true : undefined} autoCapitalize="none" autoCorrect="off" spellCheck={false} className={cn('h-10 text-sm', error && 'border-danger', p.className)} />
      {error ? <span className="block text-xs text-danger">{error}</span> : hint ? <span className="block text-xs leading-relaxed text-faint">{hint}</span> : null}
    </label>
  )
}
