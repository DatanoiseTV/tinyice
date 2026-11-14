import { useMemo } from 'react'
import { Link } from 'wouter'
import { useLive } from '@/lib/live'
import { codec, compact, rate, uptime, bytes } from '@/lib/format'
import { AreaChart, Bar, Sparkline } from '@/ui/charts'
import { Badge, EmptyState, PageHeader, Skeleton, Table, Td, Th } from '@/ui/display'
import { Legend, Panel, Readout, Tally } from '@/ui/console'
import { Icon, type IconName } from '@/ui/icons'
import type { StreamEvent } from '@/types'

function Kpi({ icon, label, value, unit, series, foot, loading }: { icon: IconName; label: string; value: string; unit?: string; series?: number[]; foot?: string; loading: boolean }) {
  return (
    <div className="flex min-w-0 flex-col rounded-lg border border-line bg-surface p-4">
      <div className="mb-3 flex items-center gap-2 text-faint"><Icon name={icon} size={14} /><Legend>{label}</Legend></div>
      {loading ? <Skeleton className="h-7 w-24" /> : <Readout value={value} unit={unit} />}
      <div className="mt-3 flex h-8 items-end">{series && series.length > 1 ? <Sparkline values={series} /> : <span className="text-xs text-faint">{foot}</span>}</div>
    </div>
  )
}

/** Only judge health when someone is listening: AutoDJ legitimately reports 0. */
function healthTone(s: StreamEvent): 'live' | 'warn' | 'danger' {
  if (s.listeners === 0) return 'live'
  return s.health >= 80 ? 'live' : s.health >= 50 ? 'warn' : 'danger'
}

function StreamRow({ s }: { s: StreamEvent }) {
  const tone = healthTone(s)
  const label = s.is_transcoded ? `${codec(s.source_type ?? '')} to ${codec(s.format)}` : codec(s.format)
  return (
    <tr className="transition-colors hover:bg-hover/60">
      <Td>
        <div className="flex items-center gap-2.5">
          <Tally on />
          <Link href="/admin/streams" className="font-mono text-xs text-ink hover:text-accent">{s.mount}</Link>
          {s.has_video && <Badge tone="info">Video</Badge>}
        </div>
      </Td>
      <Td className="max-w-[320px]"><div className="truncate text-muted">{s.title ? (s.artist ? `${s.artist} - ${s.title}` : s.title) : <span className="text-faint">No metadata</span>}</div></Td>
      <Td><Badge>{label}</Badge></Td>
      <Td align="right" mono>{s.bitrate ? `${s.bitrate} kbps` : '-'}</Td>
      <Td align="right" mono>{s.listeners}</Td>
      <Td className="w-40">
        <div className="flex items-center gap-2.5">
          <Bar value={s.health / 100} tone={`var(--${tone})`} className="flex-1" />
          <span className="num w-8 text-right font-mono text-xs text-muted">{Math.round(s.health)}</span>
        </div>
      </Td>
    </tr>
  )
}

export default function Dashboard() {
  const { stats, streams, history, connected } = useLive()
  const loading = !stats
  const total = streams.reduce((a, s) => a + s.listeners, 0)
  const times = useMemo(() => history.map((h) => h.t), [history])
  const listeners = useMemo(() => history.map((h) => h.listeners), [history])
  const out = useMemo(() => history.map((h) => h.out), [history])
  const inn = useMemo(() => history.map((h) => h.in), [history])
  const ready = history.length >= 2

  return (
    <>
      <PageHeader title="Dashboard" sub="Live view of everything this server is carrying right now." />

      <div className="grid grid-cols-2 gap-3 lg:grid-cols-5 [&>*]:min-w-0">
        <Kpi loading={loading} icon="headphones" label="Listeners" value={String(stats?.listeners ?? 0)} series={listeners} />
        <Kpi loading={loading} icon="streams" label="Live streams" value={String(streams.length)} unit={`of ${stats?.streams ?? 0}`} foot={`${streams.filter((s) => s.listeners > 0).length} with listeners`} />
        <Kpi loading={loading} icon="arrow-up" label="Outbound" value={rate(stats?.bandwidth_out ?? 0)} series={out} />
        <Kpi loading={loading} icon="arrow-down" label="Inbound" value={rate(stats?.bandwidth_in ?? 0)} series={inn} />
        <Kpi loading={loading} icon="clock" label="Uptime" value={uptime(stats?.uptime ?? 0)} foot={stats ? `Since ${new Date(Date.now() - stats.uptime * 1000).toLocaleDateString([], { month: 'short', day: 'numeric' })}` : ''} />
      </div>

      <div className="mt-3 grid gap-3 xl:grid-cols-3 [&>*]:min-w-0">
        <Panel legend="Listeners" title="Last 5 minutes" lamp={connected ? 'live' : 'warn'} className="xl:col-span-2">
          {ready ? <AreaChart times={times} series={[{ name: 'Listeners', values: listeners, color: 'var(--accent)' }]} format={compact} /> : <Skeleton className="h-[220px] w-full" />}
        </Panel>
        <Panel legend="Bandwidth" title="Last 5 minutes" lamp={connected ? 'live' : 'warn'}>
          {ready ? <AreaChart bytes times={times} format={(n) => rate(n).replace('/s', '')} series={[{ name: 'Out', values: out, color: 'var(--info)' }, { name: 'In', values: inn, color: 'var(--live)' }]} /> : <Skeleton className="h-[220px] w-full" />}
        </Panel>
      </div>

      <div className="mt-3 grid gap-3 xl:grid-cols-4 [&>*]:min-w-0">
        <Panel legend="On air" title={`${streams.length} source${streams.length === 1 ? '' : 's'}`} lamp={streams.length ? 'live' : 'off'} flush className="xl:col-span-3"
          actions={<Link href="/admin/streams" className="text-xs text-muted hover:text-ink">Manage</Link>}>
          {loading ? <div className="space-y-2 p-4">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-9 w-full" />)}</div>
            : streams.length === 0 ? <EmptyState icon={<Icon name="streams" size={20} />} title="Nothing is on air" body="Point an encoder at a mount, start an AutoDJ, or go live from the browser." action={<Link href="/admin/golive" className="text-xs text-accent hover:underline">Go live</Link>} />
            : (
              <Table>
                <thead><tr><Th>Mount</Th><Th>Now playing</Th><Th>Format</Th><Th align="right">Bitrate</Th><Th align="right">Listeners</Th><Th>Health</Th></tr></thead>
                <tbody>{streams.map((s) => <StreamRow key={s.mount} s={s} />)}</tbody>
              </Table>
            )}
        </Panel>
        <Panel legend="Runtime" title="Go process" flush>
          <dl className="divide-y divide-line text-[13px]">
            {[['Memory', stats ? bytes(stats.memory) : '-'], ['Goroutines', stats ? String(stats.goroutines) : '-'], ['GC cycles', stats ? String(stats.gc) : '-'], ['Throughput', stats ? rate(stats.bandwidth) : '-']].map(([k, v]) => (
              <div key={k} className="flex items-center justify-between px-4 py-2.5"><dt className="text-muted">{k}</dt><dd className="num font-mono text-xs text-ink">{v}</dd></div>
            ))}
          </dl>
          {streams.length > 0 && (
            <div className="border-t border-line px-4 py-3">
              <Legend className="mb-2 block">Listener share</Legend>
              <div className="space-y-2">
                {[...streams].sort((a, b) => b.listeners - a.listeners).slice(0, 5).map((s) => (
                  <div key={s.mount}>
                    <div className="mb-1 flex justify-between font-mono text-[11px]"><span className="text-muted">{s.mount}</span><span className="num text-ink">{s.listeners}</span></div>
                    <Bar value={total ? s.listeners / total : 0} />
                  </div>
                ))}
              </div>
            </div>
          )}
        </Panel>
      </div>
    </>
  )
}
