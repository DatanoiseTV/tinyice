import { Badge } from '@/ui/display'
import { Button } from '@/ui/controls'
import { Sparkline } from '@/ui/charts'
import { Legend, Panel, Readout, Tally, Timecode } from '@/ui/console'
import { bytes } from '@/lib/format'
import type { StreamEvent } from '@/types'
import type { LinkStats, Snapshot } from './engine'

export type Quality = { label: string; tone: 'live' | 'warn' | 'danger' | 'neutral'; why: string }

/** Rates the uplink from what the server reports back over RTCP. Unknown until the first report. */
export function quality(s: LinkStats, unstable: boolean): Quality {
  if (unstable) return { label: 'Unstable', tone: 'danger', why: 'The connection is not delivering packets.' }
  if (s.rttMs === null && s.lossPct === null) return { label: 'Measuring', tone: 'neutral', why: 'Waiting for the first receiver report.' }
  const rtt = s.rttMs ?? 0, loss = s.lossPct ?? 0, jit = s.jitterMs ?? 0
  if (rtt > 300 || loss > 5) return { label: 'Poor', tone: 'danger', why: loss > 5 ? `${loss.toFixed(1)}% packet loss` : `${Math.round(rtt)} ms round trip` }
  if (rtt > 150 || loss > 1.5 || jit > 50) return { label: 'Fair', tone: 'warn', why: loss > 1.5 ? `${loss.toFixed(1)}% packet loss` : jit > 50 ? `${Math.round(jit)} ms jitter` : `${Math.round(rtt)} ms round trip` }
  return { label: 'Good', tone: 'live', why: 'Low delay, no meaningful loss.' }
}

function Cell({ label, value, unit, tone }: { label: string; value: string; unit?: string; tone?: 'warn' | 'danger' }) {
  return (
    <div className="min-w-0 rounded-md border border-line px-3 py-2">
      <Legend>{label}</Legend>
      <div className="mt-1 flex items-baseline gap-1">
        <span className={`num font-mono text-sm ${tone === 'danger' ? 'text-danger' : tone === 'warn' ? 'text-warn' : 'text-ink'}`}>{value}</span>
        {unit && <span className="font-mono text-[10px] text-faint">{unit}</span>}
      </div>
    </div>
  )
}

export function OnAirBar({ snap, onStop }: { snap: Snapshot; onStop: () => void }) {
  const recon = snap.phase === 'reconnecting'
  return (
    <div
      role="status" aria-live="polite"
      className={`mb-3 flex flex-wrap items-center gap-x-5 gap-y-2 rounded-lg border px-4 py-3 ${recon ? 'border-warn/40 bg-warn/10' : 'border-live/40 bg-live/10'}`}
    >
      <div className="flex items-center gap-2.5">
        {recon ? <span className="size-2 rounded-full bg-warn" aria-hidden /> : <Tally on />}
        <span className={`font-mono text-lg font-semibold uppercase tracking-[0.14em] ${recon ? 'text-warn' : 'text-live'}`}>{recon ? 'Reconnecting' : 'On air'}</span>
      </div>
      <span className="font-mono text-[13px] text-ink">{snap.mount}</span>
      <Timecode seconds={snap.elapsed} className="text-xl text-ink" />
      <span className="order-last w-full text-xs text-muted sm:order-none sm:w-auto sm:min-w-0 sm:flex-1">
        {recon ? `${snap.notice ?? 'Connection lost.'} Attempt ${snap.attempt} of ${snap.maxAttempts}${snap.retryIn ? `, retrying in ${snap.retryIn} s` : ', connecting'}.` : snap.unstable ? 'Connection unstable, waiting for it to recover.' : ''}
      </span>
      <Button variant="danger" size="sm" onClick={onStop} className="ml-auto">Stop broadcast</Button>
    </div>
  )
}

export function LinkPanel({ snap, server }: { snap: Snapshot; server: StreamEvent | undefined }) {
  const on = snap.phase === 'live' || snap.phase === 'reconnecting'
  const s = snap.stats
  const q = quality(s, snap.unstable)
  const rtt = s.rttMs === null ? '-' : String(Math.round(s.rttMs))
  return (
    <Panel legend="Link" title={on ? 'Uplink health' : snap.phase === 'connecting' ? 'Connecting' : 'Idle'} lamp={snap.phase === 'live' ? 'live' : on || snap.phase === 'connecting' ? 'warn' : 'off'}
      actions={on ? <Badge tone={q.tone} dot>{q.label}</Badge> : undefined}>
      {on ? (
        <div className="space-y-4">
          <div className="flex items-end justify-between gap-4">
            <div><Legend>Elapsed</Legend><div className="mt-1 text-[26px] leading-none"><Timecode seconds={snap.elapsed} /></div></div>
            <Readout label="Sent" value={bytes(s.bytesSent).split(' ')[0]} unit={bytes(s.bytesSent).split(' ')[1]} />
          </div>
          <div>
            <div className="mb-1 flex items-baseline justify-between"><Legend>Bitrate</Legend><span className="num font-mono text-xs text-ink">{s.bitrateKbps === null ? '-' : `${Math.round(s.bitrateKbps)} kbps`}</span></div>
            <Sparkline values={s.bitrateHistory} />
          </div>
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-4 [&>*]:min-w-0">
            <Cell label="Round trip" value={rtt} unit="ms" tone={s.rttMs !== null && s.rttMs > 300 ? 'danger' : s.rttMs !== null && s.rttMs > 150 ? 'warn' : undefined} />
            <Cell label="Loss" value={s.lossPct === null ? '-' : s.lossPct.toFixed(1)} unit="%" tone={s.lossPct !== null && s.lossPct > 5 ? 'danger' : s.lossPct !== null && s.lossPct > 1.5 ? 'warn' : undefined} />
            <Cell label="Jitter" value={s.jitterMs === null ? '-' : String(Math.round(s.jitterMs))} unit="ms" />
            <Cell label="Video" value={s.width ? `${s.width}x${s.height}` : 'none'} unit={s.fps ? `${Math.round(s.fps)} fps` : undefined} />
          </div>
          <p className="text-xs text-muted">{q.why}{s.limitation ? ` Encoder limited by ${s.limitation}.` : ''}</p>
          <div className="border-t border-line pt-3">
            <Legend>Server view</Legend>
            {server ? (
              <dl className="mt-2 grid grid-cols-3 gap-2 text-[13px]">
                <div><dt className="text-xs text-faint">Listeners</dt><dd className="num font-mono text-ink">{server.listeners}</dd></div>
                <div><dt className="text-xs text-faint">Health</dt><dd className="num font-mono text-ink">{Math.round(server.health)}%</dd></div>
                <div><dt className="text-xs text-faint">Bitrate</dt><dd className="num font-mono text-ink">{server.bitrate || '-'}</dd></div>
              </dl>
            ) : (
              <p className="mt-1.5 text-xs text-warn">{snap.phase === 'live' && snap.elapsed > 10 ? 'The server does not list this mount as live yet.' : 'Waiting for the server to list this mount.'}</p>
            )}
          </div>
        </div>
      ) : (
        <p className="text-[13px] text-muted">Round trip, loss, jitter, bitrate and the server's own view of the mount appear here once you are on air.</p>
      )}
    </Panel>
  )
}
