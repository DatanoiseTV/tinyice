import { useCallback, useRef, useState, type KeyboardEvent, type PointerEvent, type ReactNode } from 'react'
import { cn } from '@/lib/cn'
import { Icon, type IconName } from './icons'

/** Etched console legend: tiny mono caps used for section captions and units. */
export function Legend({ children, className }: { children: ReactNode; className?: string }) {
  return <span className={cn('font-mono text-[10px] font-medium uppercase tracking-[0.09em] text-faint', className)}>{children}</span>
}

/**
 * Rack panel: a card whose header is a legend strip with an optional status
 * lamp. This is the product's signature container; use it for every
 * top-level block of a page.
 */
export function Panel({ legend, title, lamp, actions, children, className, flush }: {
  legend: string; title?: ReactNode; lamp?: 'live' | 'warn' | 'danger' | 'off'; actions?: ReactNode; children?: ReactNode; className?: string; flush?: boolean
}) {
  const lampColor = { live: 'bg-live tally', warn: 'bg-warn', danger: 'bg-danger', off: 'bg-line-strong' }
  return (
    <section className={cn('flex min-w-0 flex-col rounded-lg border border-line bg-surface', className)}>
      <header className="flex h-10 shrink-0 items-center gap-2.5 border-b border-line px-4">
        {lamp && <span className={cn('size-1.5 rounded-full', lampColor[lamp])} aria-hidden />}
        <Legend>{legend}</Legend>
        {title && <span className="truncate text-[13px] font-medium text-ink">{title}</span>}
        {actions && <div className="ml-auto flex items-center gap-2">{actions}</div>}
      </header>
      <div className={cn('min-h-0 flex-1', !flush && 'p-4')}>{children}</div>
    </section>
  )
}

/** Big readout: tabular mono number with an etched unit. */
export function Readout({ value, unit, label, tone, className }: { value: ReactNode; unit?: string; label?: string; tone?: 'live' | 'warn' | 'danger'; className?: string }) {
  return (
    <div className={className}>
      {label && <Legend>{label}</Legend>}
      <div className="mt-1 flex items-baseline gap-1.5">
        <span className={cn('num font-mono text-[26px] font-medium leading-none tracking-tight', tone ? `text-${tone}` : 'text-ink')}>{value}</span>
        {unit && <Legend>{unit}</Legend>}
      </div>
    </div>
  )
}

/** m:ss readout with dimmed leading zeros, like a deck display. */
export function Timecode({ seconds, className }: { seconds: number; className?: string }) {
  const s = Math.max(0, Math.floor(seconds || 0))
  const txt = `${String(Math.floor(s / 60)).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`
  const lead = txt.match(/^[0:]*/)![0].replace(/:$/, '').length
  return (
    <span className={cn('num font-mono tabular-nums', className)} aria-label={`${Math.floor(s / 60)} minutes ${s % 60} seconds`}>
      <span className="text-faint/70">{txt.slice(0, Math.min(lead, txt.length - 1))}</span>{txt.slice(Math.min(lead, txt.length - 1))}
    </span>
  )
}

/** On-air tally: a lamp with an etched caption. */
export function Tally({ on, label }: { on: boolean; label?: string }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <span className={cn('size-2 rounded-full', on ? 'bg-live tally' : 'bg-line-strong')} />
      {label && <Legend className={on ? 'text-live' : undefined}>{label}</Legend>}
    </span>
  )
}

/* ---------- Knob ---------- */
const A0 = -135, A1 = 135 // sweep in degrees, 0 = straight up
const pol = (cx: number, cy: number, r: number, deg: number) => { const a = ((deg - 90) * Math.PI) / 180; return [cx + r * Math.cos(a), cy + r * Math.sin(a)] as const }
const arc = (cx: number, cy: number, r: number, from: number, to: number) => {
  const [x0, y0] = pol(cx, cy, r, from), [x1, y1] = pol(cx, cy, r, to)
  return `M${x0.toFixed(2)} ${y0.toFixed(2)} A${r} ${r} 0 ${to - from > 180 ? 1 : 0} 1 ${x1.toFixed(2)} ${y1.toFixed(2)}`
}

/**
 * Rotary control. Drag up/right to raise, wheel, arrow keys (shift = x10),
 * Home/End, double-click to reset. `value` is 0..1; callers map units.
 */
export function Knob({ value, onChange, label, display, size = 56, defaultValue, step = 0.02, disabled }: {
  value: number; onChange: (v: number) => void; label: string; display?: string; size?: number; defaultValue?: number; step?: number; disabled?: boolean
}) {
  const drag = useRef<{ y: number; x: number; v: number } | null>(null)
  const clamp = (v: number) => Math.max(0, Math.min(1, v))
  const move = useCallback((e: PointerEvent) => {
    if (!drag.current) return
    const d = (drag.current.y - e.clientY) + (e.clientX - drag.current.x)
    onChange(clamp(drag.current.v + d / (e.shiftKey ? 600 : 150)))
  }, [onChange])
  const onKey = (e: KeyboardEvent) => {
    const k = e.shiftKey ? step * 5 : step
    const m: Record<string, number> = { ArrowUp: value + k, ArrowRight: value + k, ArrowDown: value - k, ArrowLeft: value - k, Home: 0, End: 1 }
    if (e.key in m) { e.preventDefault(); onChange(clamp(m[e.key])) }
  }
  const c = size / 2, r = c - 5
  const ang = A0 + (A1 - A0) * value
  const [ix, iy] = pol(c, c, r - 7, ang), [ox, oy] = pol(c, c, r - 1, ang)
  return (
    <div className="inline-flex select-none flex-col items-center gap-1" style={{ width: size + 8 }}>
      <svg
        width={size} height={size} role="slider" tabIndex={disabled ? -1 : 0} aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(value * 100)} aria-valuetext={display}
        className={cn('touch-none cursor-ns-resize rounded-full', disabled && 'pointer-events-none opacity-40')}
        onPointerDown={(e) => { (e.currentTarget as Element).setPointerCapture(e.pointerId); drag.current = { y: e.clientY, x: e.clientX, v: value } }}
        onPointerMove={move} onPointerUp={() => { drag.current = null }} onPointerCancel={() => { drag.current = null }}
        onWheel={(e) => onChange(clamp(value - Math.sign(e.deltaY) * step))}
        onDoubleClick={() => defaultValue !== undefined && onChange(defaultValue)} onKeyDown={onKey}
      >
        <path d={arc(c, c, r, A0, A1)} stroke="var(--border-strong)" strokeWidth="3" strokeLinecap="round" fill="none" />
        {value > 0.001 && <path d={arc(c, c, r, A0, ang)} stroke="var(--accent)" strokeWidth="3" strokeLinecap="round" fill="none" />}
        <circle cx={c} cy={c} r={r - 5} fill="var(--raised)" stroke="var(--border-strong)" />
        <line x1={ix} y1={iy} x2={ox} y2={oy} stroke="var(--ink, var(--text))" strokeWidth="2" strokeLinecap="round" />
      </svg>
      <Legend>{label}</Legend>
      {display && <span className="num font-mono text-[11px] text-ink">{display}</span>}
    </div>
  )
}

/* ---------- Fader / scrubber ---------- */
/** Linear slider with a console-style cap. Horizontal by default; `vertical` for channel strips. */
export function Fader({ value, onChange, label, vertical, length = 160, disabled, onCommit }: {
  value: number; onChange: (v: number) => void; label: string; vertical?: boolean; length?: number; disabled?: boolean; onCommit?: (v: number) => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  const [active, setActive] = useState(false)
  const last = useRef(value)
  const at = (e: PointerEvent) => {
    const r = ref.current!.getBoundingClientRect()
    const t = vertical ? 1 - (e.clientY - r.top) / r.height : (e.clientX - r.left) / r.width
    const v = Math.max(0, Math.min(1, t)); last.current = v; onChange(v)
  }
  const onKey = (e: KeyboardEvent) => {
    const k = e.shiftKey ? 0.1 : 0.02
    const m: Record<string, number> = { ArrowUp: value + k, ArrowRight: value + k, ArrowDown: value - k, ArrowLeft: value - k, Home: 0, End: 1 }
    if (e.key in m) { e.preventDefault(); const v = Math.max(0, Math.min(1, m[e.key])); onChange(v); onCommit?.(v) }
  }
  const pct = `${value * 100}%`
  return (
    <div
      ref={ref} role="slider" tabIndex={disabled ? -1 : 0} aria-label={label} aria-orientation={vertical ? 'vertical' : 'horizontal'} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(value * 100)}
      className={cn('relative touch-none select-none', vertical ? 'w-6' : 'h-6', disabled && 'pointer-events-none opacity-40')}
      style={vertical ? { height: length } : { width: '100%' }}
      onPointerDown={(e) => { (e.currentTarget as Element).setPointerCapture(e.pointerId); setActive(true); at(e) }}
      onPointerMove={(e) => active && at(e)} onPointerUp={() => { setActive(false); onCommit?.(last.current) }} onPointerCancel={() => setActive(false)} onKeyDown={onKey}
    >
      <div className={cn('absolute rounded-full bg-line-strong', vertical ? 'inset-y-0 left-1/2 w-[3px] -translate-x-1/2' : 'inset-x-0 top-1/2 h-[3px] -translate-y-1/2')} />
      <div className={cn('absolute rounded-full bg-accent', vertical ? 'bottom-0 left-1/2 w-[3px] -translate-x-1/2' : 'left-0 top-1/2 h-[3px] -translate-y-1/2')} style={vertical ? { height: pct } : { width: pct }} />
      <div
        className={cn('absolute rounded-[3px] border border-line-strong bg-raised shadow-[0_1px_0_var(--border)]', vertical ? 'left-1/2 h-3 w-5 -translate-x-1/2 translate-y-1/2' : 'top-1/2 h-5 w-3 -translate-x-1/2 -translate-y-1/2', active && 'border-accent')}
        style={vertical ? { bottom: pct } : { left: pct }}
      >
        <div className={cn('absolute bg-accent', vertical ? 'inset-x-0.5 top-1/2 h-px' : 'inset-y-0.5 left-1/2 w-px')} />
      </div>
    </div>
  )
}

/* ---------- Transport ---------- */
export function TransportButton({ icon, label, onClick, primary, active, disabled, size = 'md' }: {
  icon: IconName; label: string; onClick?: () => void; primary?: boolean; active?: boolean; disabled?: boolean; size?: 'sm' | 'md' | 'lg'
}) {
  const dim = { sm: 'size-8', md: 'size-9', lg: 'size-12' }[size]
  return (
    <button
      type="button" onClick={onClick} disabled={disabled} aria-label={label} aria-pressed={active} title={label}
      className={cn(
        'flex shrink-0 items-center justify-center rounded-md border transition-colors disabled:pointer-events-none disabled:opacity-40', dim,
        primary ? 'border-transparent bg-accent text-accent-fg hover:brightness-110' : active ? 'border-accent/50 bg-accent/10 text-accent' : 'border-line-strong bg-raised text-muted hover:bg-hover hover:text-ink',
      )}
    >
      <Icon name={icon} size={size === 'lg' ? 20 : 16} />
    </button>
  )
}
