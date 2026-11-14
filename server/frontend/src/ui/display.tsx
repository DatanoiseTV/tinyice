import type { HTMLAttributes, ReactNode } from 'react'
import { cn } from '@/lib/cn'

export function Card({ className, ...p }: HTMLAttributes<HTMLDivElement>) {
  return <section className={cn('rounded-lg border border-line bg-surface', className)} {...p} />
}
export function CardHeader({ title, sub, actions }: { title: ReactNode; sub?: ReactNode; actions?: ReactNode }) {
  return (
    <header className="flex min-h-12 items-center justify-between gap-3 border-b border-line px-4 py-2.5">
      <div className="min-w-0">
        <h2 className="truncate text-[13px] font-semibold text-ink">{title}</h2>
        {sub && <p className="truncate text-xs text-faint">{sub}</p>}
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </header>
  )
}

export function PageHeader({ title, sub, actions }: { title: string; sub?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mb-5 flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 className="text-lg font-semibold tracking-tight text-ink">{title}</h1>
        {sub && <p className="mt-0.5 text-[13px] text-muted">{sub}</p>}
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  )
}

type Tone = 'neutral' | 'live' | 'warn' | 'danger' | 'info' | 'accent'
const tones: Record<Tone, string> = {
  neutral: 'bg-hover text-muted border-line',
  live: 'bg-live/10 text-live border-live/25',
  warn: 'bg-warn/10 text-warn border-warn/25',
  danger: 'bg-danger/10 text-danger border-danger/25',
  info: 'bg-info/10 text-info border-info/25',
  accent: 'bg-accent/10 text-accent border-accent/25',
}
export function Badge({ tone = 'neutral', dot, children, className }: { tone?: Tone; dot?: boolean; children: ReactNode; className?: string }) {
  return (
    <span className={cn('inline-flex h-5 items-center gap-1.5 rounded border px-1.5 text-[11px] font-medium leading-none', tones[tone], className)}>
      {dot && <span className="size-1.5 rounded-full bg-current" />}
      {children}
    </span>
  )
}

export function Kbd({ children }: { children: ReactNode }) {
  return <kbd className="inline-flex h-[18px] min-w-[18px] items-center justify-center rounded border border-line-strong bg-raised px-1 font-mono text-[10px] text-muted">{children}</kbd>
}

export function Skeleton({ className }: { className?: string }) {
  return <div className={cn('skeleton', className)} />
}

export function EmptyState({ icon, title, body, action }: { icon: ReactNode; title: string; body?: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center px-6 py-12 text-center">
      <div className="mb-3 flex size-10 items-center justify-center rounded-lg border border-line bg-raised text-faint">{icon}</div>
      <p className="text-[13px] font-medium text-ink">{title}</p>
      {body && <p className="mt-1 max-w-sm text-xs text-muted">{body}</p>}
      {action && <div className="mt-4">{action}</div>}
    </div>
  )
}

/* ---------- Table ---------- */
export function Table({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn('overflow-x-auto', className)}><table className="w-full border-collapse text-[13px]">{children}</table></div>
}
export function Th({ children, className, align = 'left' }: { children?: ReactNode; className?: string; align?: 'left' | 'right' }) {
  return <th className={cn('h-9 whitespace-nowrap border-b border-line px-4 text-xs font-medium text-faint', align === 'right' ? 'text-right' : 'text-left', className)}>{children}</th>
}
export function Td({ children, className, align = 'left', mono }: { children?: ReactNode; className?: string; align?: 'left' | 'right'; mono?: boolean }) {
  return <td className={cn('h-11 border-b border-line px-4 text-ink', align === 'right' && 'text-right', mono && 'whitespace-nowrap font-mono text-xs num', className)}>{children}</td>
}
