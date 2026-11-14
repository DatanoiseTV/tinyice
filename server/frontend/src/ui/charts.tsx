import { useId, useMemo, useRef, useState } from 'react'
import { useLayoutEffect } from 'react'
import { cn } from '@/lib/cn'

/** Track an element's pixel width so SVG text is drawn 1:1 instead of being stretched. */
function useWidth<T extends Element>(initial = 640) {
  const ref = useRef<T>(null)
  const [w, setW] = useState(initial)
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    setW(Math.round(el.getBoundingClientRect().width) || initial)
    const ro = new ResizeObserver(([e]) => setW(Math.round(e.contentRect.width) || initial))
    ro.observe(el)
    return () => ro.disconnect()
  }, [initial])
  return [ref, w] as const
}

/** 1/2/5 x 10^n step so gridlines land on round numbers. */
function niceStep(raw: number) {
  const mag = 10 ** Math.floor(Math.log10(raw || 1))
  const f = raw / mag
  return (f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10) * mag
}
function niceStep1024(raw: number) {
  const mag = 1024 ** Math.max(0, Math.floor(Math.log(raw || 1) / Math.log(1024)))
  const f = raw / mag
  return (f <= 1 ? 1 : f <= 2 ? 2 : f <= 4 ? 4 : f <= 8 ? 8 : f <= 16 ? 16 : f <= 32 ? 32 : f <= 64 ? 64 : f <= 128 ? 128 : f <= 256 ? 256 : f <= 512 ? 512 : 1024) * mag
}
/** Fit the axis to the data; only anchor at zero when the data actually spans most of it. */
function axis(values: number[], ticks = 4, base = 10) {
  const lo = Math.min(...values), hi = Math.max(...values)
  const span = hi - lo
  const from = lo > 0 && span < hi * 0.5 ? Math.max(0, lo - Math.max(span, hi * 0.02) * 0.5) : Math.min(0, lo)
  const raw = (hi - from || 1) * 1.08 / ticks
  // base 1024 for byte quantities so ticks read 1 MB, 2 MB, 4 MB rather than 1.91 MB.
  const step = base === 1024 ? niceStep1024(raw) : niceStep(raw)
  const min = Math.floor(from / step) * step
  const max = Math.max(min + step * ticks, Math.ceil(hi / step) * step)
  return { min, max, step }
}

/** Map values onto an SVG path. `min`/`max` default to the data range, padded. */
function scale(values: number[], w: number, h: number, pad = 2, floor?: number, ceil?: number) {
  const lo = floor ?? Math.min(...values)
  let hi = ceil ?? Math.max(...values)
  if (hi === lo) hi = lo + 1
  const step = values.length > 1 ? w / (values.length - 1) : 0
  return values.map((v, i) => [i * step, pad + (h - pad * 2) * (1 - (v - lo) / (hi - lo))] as const)
}
const line = (pts: readonly (readonly [number, number])[]) => pts.map(([x, y], i) => `${i ? 'L' : 'M'}${x.toFixed(1)} ${y.toFixed(1)}`).join(' ')

/** Tiny trend line for KPI tiles. */
export function Sparkline({ values, className, color = 'var(--accent)' }: { values: number[]; className?: string; color?: string }) {
  const id = useId()
  const w = 120, h = 32
  const d = useMemo(() => {
    if (values.length < 2) return null
    const pts = scale(values, w, h, 3)
    return { stroke: line(pts), fill: `${line(pts)} L${w} ${h} L0 ${h} Z` }
  }, [values])
  return (
    <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" className={cn('h-8 w-full overflow-visible', className)} aria-hidden>
      <defs>
        <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stopColor={color} stopOpacity=".22" />
          <stop offset="1" stopColor={color} stopOpacity="0" />
        </linearGradient>
      </defs>
      {d && (<><path d={d.fill} fill={`url(#${id})`} /><path d={d.stroke} fill="none" stroke={color} strokeWidth="1.5" vectorEffect="non-scaling-stroke" strokeLinejoin="round" /></>)}
    </svg>
  )
}

export interface Series { name: string; values: number[]; color: string }

/**
 * Time-series area chart with gridlines, y labels and a hover crosshair
 * that reads out every series at the pointer. Pure SVG; no chart library.
 */
export function AreaChart({ series, times, height = 220, format = (n) => String(Math.round(n)), className, bytes }: {
  series: Series[]; times: number[]; height?: number; format?: (n: number) => string; className?: string; bytes?: boolean
}) {
  const [wrap, W] = useWidth<HTMLDivElement>()
  const [hover, setHover] = useState<number | null>(null)
  const id = useId()
  const padL = 52, padR = 6, padB = 22, padT = 8
  const H = height
  const n = times.length
  const { min, max, step } = useMemo(() => axis(series.flatMap((s) => s.values), 4, bytes ? 1024 : 10), [series, bytes])
  const innerW = W - padL - padR, innerH = H - padB - padT
  const x = (i: number) => padL + (n > 1 ? (i / (n - 1)) * innerW : 0)
  const y = (v: number) => padT + innerH * (1 - (v - min) / (max - min))
  const ticks: number[] = []
  for (let v = min; v <= max + step / 2; v += step) ticks.push(v)

  const onMove = (e: React.PointerEvent) => {
    const r = wrap.current!.getBoundingClientRect()
    setHover(Math.max(0, Math.min(n - 1, Math.round(((e.clientX - r.left - padL) / innerW) * (n - 1)))))
  }
  const t = (ms: number) => new Date(ms).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
  const mono = { fontSize: 10, fill: 'var(--faint)', fontFamily: 'var(--font-mono)' } as const

  return (
    <div ref={wrap} className={cn('relative', className)} onPointerMove={onMove} onPointerLeave={() => setHover(null)}>
      <svg width={W} height={H} role="img" aria-label="Time series chart" className="block">
        <defs>
          {series.map((s, i) => (
            <linearGradient key={i} id={`${id}-${i}`} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0" stopColor={s.color} stopOpacity=".18" /><stop offset="1" stopColor={s.color} stopOpacity="0" />
            </linearGradient>
          ))}
        </defs>
        {ticks.map((v, k) => (
          <g key={v}>
            <line x1={padL} x2={W - padR} y1={y(v)} y2={y(v)} stroke="var(--border)" strokeDasharray={k ? '2 4' : undefined} />
            <text x={padL - 8} y={y(v) + 3} textAnchor="end" {...mono}>{format(v)}</text>
          </g>
        ))}
        {n > 1 && series.map((s, i) => {
          const pts = s.values.map((v, j) => [x(j), y(v)] as const)
          return (
            <g key={s.name}>
              <path d={`${line(pts)} L${x(n - 1)} ${y(min)} L${x(0)} ${y(min)} Z`} fill={`url(#${id}-${i})`} />
              <path d={line(pts)} fill="none" stroke={s.color} strokeWidth="1.6" strokeLinejoin="round" />
            </g>
          )
        })}
        {n > 1 && (<>
          <text x={padL} y={H - 6} {...mono}>{t(times[0])}</text>
          <text x={W - padR} y={H - 6} textAnchor="end" {...mono}>{t(times[n - 1])}</text>
        </>)}
        {hover !== null && n > 0 && (<>
          <line x1={x(hover)} x2={x(hover)} y1={padT} y2={padT + innerH} stroke="var(--border-strong)" />
          {series.map((s) => <circle key={s.name} cx={x(hover)} cy={y(s.values[hover])} r="3.5" fill="var(--surface)" stroke={s.color} strokeWidth="1.6" />)}
        </>)}
      </svg>
      {hover !== null && n > 0 && (
        <div className="pointer-events-none absolute top-2 rounded-md border border-line-strong bg-raised px-2.5 py-1.5 text-xs" style={{ left: Math.min(Math.max(x(hover) + 12, 8), W - 160) }}>
          <div className="mb-1 font-mono text-[10px] text-faint">{t(times[hover])}</div>
          {series.map((s) => (
            <div key={s.name} className="flex items-center justify-between gap-4">
              <span className="flex items-center gap-1.5 text-muted"><span className="size-1.5 rounded-full" style={{ background: s.color }} />{s.name}</span>
              <span className="num font-mono text-ink">{format(s.values[hover])}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

/**
 * Segmented LED level meter (broadcast style). `value` is 0..1; the last
 * segments tint warn/danger like a console PPM so a hot stream is obvious.
 */
export function LevelMeter({ value, segments = 24, className }: { value: number; segments?: number; className?: string }) {
  const lit = Math.round(Math.max(0, Math.min(1, value)) * segments)
  return (
    <div className={cn('flex h-2.5 gap-px', className)} role="meter" aria-valuenow={Math.round(value * 100)} aria-valuemin={0} aria-valuemax={100}>
      {Array.from({ length: segments }, (_, i) => {
        const f = i / segments
        const on = i < lit
        const c = f > 0.88 ? 'var(--danger)' : f > 0.7 ? 'var(--warn)' : 'var(--live)'
        return <span key={i} className="h-full flex-1 rounded-[1px] transition-colors duration-100" style={{ background: on ? c : 'var(--border)' }} />
      })}
    </div>
  )
}

/** Horizontal bar with a label, for capacity-style readouts. */
export function Bar({ value, tone = 'var(--accent)', className }: { value: number; tone?: string; className?: string }) {
  return (
    <div className={cn('h-1.5 overflow-hidden rounded-full bg-hover', className)}>
      <div className="h-full rounded-full transition-[width] duration-300" style={{ width: `${Math.max(0, Math.min(1, value)) * 100}%`, background: tone }} />
    </div>
  )
}
