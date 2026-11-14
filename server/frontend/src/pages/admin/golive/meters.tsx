import { useEffect, useState } from 'react'
import { LevelMeter } from '@/ui/charts'
import { Legend } from '@/ui/console'
import { cn } from '@/lib/cn'
import { broadcaster } from './engine'

/**
 * dBFS to 0..1 along the console scale: 0.70 sits at -9 dBFS (LevelMeter turns amber there)
 * and 0.88 at -3 dBFS (red), so speech peaking around -12 reads green and anything within
 * 3 dB of full scale is visibly hot.
 */
const KNOTS: [number, number][] = [[-60, 0], [-9, 0.7], [-3, 0.88], [0, 1]]
export function dbToMeter(db: number): number {
  if (db <= KNOTS[0][0]) return 0
  for (let i = 1; i < KNOTS.length; i++) {
    const [d1, v1] = KNOTS[i], [d0, v0] = KNOTS[i - 1]
    if (db <= d1) return v0 + ((db - d0) / (d1 - d0)) * (v1 - v0)
  }
  return 1
}
const TICKS = [-48, -36, -24, -12, -6, -3, 0]
const toDb = (lin: number) => (lin > 1e-5 ? 20 * Math.log10(lin) : -90)
const FALL_DB_PER_S = 24
const HOLD_MS = 1500

interface Row { disp: number; hold: number; holdAt: number }
interface View { l: number; r: number; hl: number; hr: number; clipped: boolean; reduction: number }
const ZERO: View = { l: -90, r: -90, hl: -90, hr: -90, clipped: false, reduction: 0 }

function fmt(db: number) { return db <= -60 ? '-inf' : db.toFixed(1) }

function Channel({ name, disp, hold }: { name: string; disp: number; hold: number }) {
  return (
    <div className="flex items-center gap-3">
      <span className="w-3 font-mono text-[11px] text-muted" aria-hidden>{name}</span>
      <div className="relative min-w-0 flex-1">
        <LevelMeter value={dbToMeter(disp)} segments={48} className="h-3.5" />
        <span className="absolute inset-y-0 w-0.5 bg-ink" style={{ left: `calc(${dbToMeter(hold) * 100}% - 1px)`, opacity: hold > -60 ? 1 : 0 }} aria-hidden />
      </div>
      <span className={cn('num w-11 text-right font-mono text-[11px]', hold > -1 ? 'text-danger' : hold > -3 ? 'text-warn' : 'text-ink')} aria-label={`${name === 'L' ? 'Left' : 'Right'} peak ${fmt(hold)} dBFS`}>{fmt(hold)}</span>
    </div>
  )
}

/**
 * Stereo peak meters with hold, clip latch and limiter reduction. It owns its own
 * render loop so the ~30 Hz level updates never re-render the page around it
 * (the old page worked around the same cost by writing levels to the DOM by hand,
 * which also kept the Go Live button from flickering).
 */
export function Meters({ active }: { active: boolean }) {
  const [v, setV] = useState<View>(ZERO)
  useEffect(() => {
    if (!active) { setV(ZERO); return }
    let raf = 0, frame = 0, last = performance.now()
    const rows: Record<'l' | 'r', Row> = { l: { disp: -90, hold: -90, holdAt: 0 }, r: { disp: -90, hold: -90, holdAt: 0 } }
    const step = (row: Row, db: number, dt: number, now: number) => {
      row.disp = Math.max(db, row.disp - FALL_DB_PER_S * dt)
      if (db >= row.hold) { row.hold = db; row.holdAt = now }
      else if (now - row.holdAt > HOLD_MS) row.hold = Math.max(db, row.hold - 18 * dt)
    }
    const loop = (now: number) => {
      raf = requestAnimationFrame(loop)
      if (frame++ & 1) return
      const lv = broadcaster.readLevels()
      if (!lv) return
      const dt = Math.min(0.1, (now - last) / 1000); last = now
      step(rows.l, toDb(lv.peakL), dt, now); step(rows.r, toDb(lv.peakR), dt, now)
      setV({ l: rows.l.disp, r: rows.r.disp, hl: rows.l.hold, hr: rows.r.hold, clipped: lv.clipped, reduction: lv.reductionDb })
    }
    raf = requestAnimationFrame(loop)
    return () => cancelAnimationFrame(raf)
  }, [active])

  return (
    <div>
      <div className="space-y-2.5" role="group" aria-label="Input peak levels in dBFS">
        <Channel name="L" disp={v.l} hold={v.hl} />
        <Channel name="R" disp={v.r} hold={v.hr} />
      </div>
      <div className="relative mt-1.5 ml-6 mr-14 h-4" aria-hidden>
        {TICKS.map((t) => (
          <span key={t} className="absolute top-0 -translate-x-1/2 font-mono text-[9px] text-faint" style={{ left: `${dbToMeter(t) * 100}%` }}>{t === 0 ? '0' : t}</span>
        ))}
      </div>
      <div className="mt-3 flex items-center gap-3">
        <button
          type="button" onClick={() => broadcaster.resetClip()} disabled={!active}
          aria-label={v.clipped ? 'Clipping detected. Reset clip indicator' : 'Clip indicator'}
          className={cn('inline-flex h-7 items-center gap-2 rounded-md border px-2.5 transition-colors disabled:pointer-events-none', v.clipped ? 'border-danger/50 bg-danger/10 text-danger' : 'border-line text-faint')}
        >
          <span className={cn('size-2 rounded-full', v.clipped ? 'bg-danger' : 'bg-line-strong')} aria-hidden />
          <Legend className={v.clipped ? 'text-danger' : undefined}>{v.clipped ? 'Clip - reset' : 'Clip'}</Legend>
        </button>
        <span className="inline-flex h-7 items-center gap-2 rounded-md border border-line px-2.5" title="Gain reduction applied by the limiter">
          <Legend>Limiter</Legend>
          <span className={cn('num w-14 text-right font-mono text-[11px]', v.reduction < -0.5 ? 'text-warn' : 'text-muted')}>{v.reduction < -0.05 ? `${v.reduction.toFixed(1)} dB` : '0.0 dB'}</span>
        </span>
        <span className="ml-auto hidden text-xs text-faint sm:block">Peak, dBFS, measured after the gain stage</span>
      </div>
      <p role="status" aria-live="polite" className="sr-only">{v.clipped ? 'Input is clipping. Lower the input gain.' : ''}</p>
    </div>
  )
}
