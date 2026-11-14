import { useMemo } from 'react'
import { Link } from 'wouter'
import { ArrowDownToLine, ArrowUpFromLine, Clock, Headphones, Radio } from 'lucide-react'
import { useLive } from '@/lib/live'
import { codec, compact, rate, uptime, bytes } from '@/lib/format'
import { AreaChart, Bar, Sparkline } from '@/ui/charts'
import { Badge, Card, CardHeader, EmptyState, PageHeader, Skeleton, Table, Td, Th, Tally } from '@/ui/display'
import type { StreamEvent } from '@/types'

function Kpi({ icon, label, value, unit, series, foot, loading }: { icon: React.ReactNode; label: string; value: string; unit?: string; series?: number[]; foot?: string; loading: boolean }) {
  return (
    <Card className="flex flex-col p-4">
      <div className="flex items-center gap-2 text-xs text-muted">{icon}{label}</div>
      {loading ? <Skeleton className="mt-3 h-8 w-24" /> : (
        <div className="mt-2 flex items-baseline gap-1.5">
          <span className="num text-[26px] font-semibold leading-none tracking-tight text-ink">{value}</span>
          {unit && <span className="text-xs text-faint">{unit}</span>}
        </div>
      )}
      <div className="mt-3 flex h-8 items-end">{series && series.length > 1 ? <Sparkline values={series} /> : <span className="text-xs text-faint">{foot}</span>}</div>
    </Card>
  )
}

/** Source is connected but stalling. AutoDJ legitimately reports health 0, so only judge when someone listens. */
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
          <Link href={`/admin/streams`} className="font-mono text-xs text-ink hover:text-accent">{s.mount}</Link>
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

export function Dashboard() {
  const { stats, streams, history, connected } = useLive()
  const loading = !stats
  const total = streams.reduce((a, s) => a + s.listeners, 0)
  const times = useMemo(() => history.map((h) => h.t), [history])
  const listeners = useMemo(() => history.map((h) => h.listeners), [history])
  const out = useMemo(() => history.map((h) => h.out), [history])
  const inn = useMemo(() => history.map((h) => h.in), [history])

  return (
    <>
      <PageHeader title="Dashboard" sub="Live view of everything this server is carrying right now." />

      <div className="grid grid-cols-2 gap-3 lg:grid-cols-5 [&>*]:min-w-0">
        <Kpi loading={loading} icon={<Headphones className="size-3.5" />} label="Listeners" value={String(stats?.listeners ?? 0)} series={listeners} />
        <Kpi loading={loading} icon={<Radio className="size-3.5" />} label="Live streams" value={String(streams.length)} unit={`of ${stats?.streams ?? 0}`} foot={`${streams.filter((s) => s.listeners > 0).length} with listeners`} />
        <Kpi loading={loading} icon={<ArrowUpFromLine className="size-3.5" />} label="Outbound" value={rate(stats?.bandwidth_out ?? 0)} series={out} />
        <Kpi loading={loading} icon={<ArrowDownToLine className="size-3.5" />} label="Inbound" value={rate(stats?.bandwidth_in ?? 0)} series={inn} />
        <Kpi loading={loading} icon={<Clock className="size-3.5" />} label="Uptime" value={uptime(stats?.uptime ?? 0)} foot={stats ? `Since ${new Date(Date.now() - stats.uptime * 1000).toLocaleDateString([], { month: 'short', day: 'numeric' })}` : ''} />
      </div>

      <div className="mt-3 grid gap-3 xl:grid-cols-3 [&>*]:min-w-0">
        <Card className="xl:col-span-2">
          <CardHeader title="Listeners" sub="Last 5 minutes, live" actions={<Badge tone={connected ? 'live' : 'warn'} dot>{connected ? 'Live' : 'Connecting'}</Badge>} />
          <div className="p-4">
            {history.length < 2 ? <Skeleton className="h-[220px] w-full" /> : <AreaChart times={times} series={[{ name: 'Listeners', values: listeners, color: 'var(--accent)' }]} format={compact} />}
          </div>
        </Card>
        <Card>
          <CardHeader title="Bandwidth" sub="Last 5 minutes, live" />
          <div className="p-4">
            {history.length < 2 ? <Skeleton className="h-[220px] w-full" /> : (
              <AreaChart bytes times={times} height={220} format={(n) => rate(n).replace('/s', '')}
                series={[{ name: 'Out', values: out, color: 'var(--info)' }, { name: 'In', values: inn, color: 'var(--live)' }]} />
            )}
          </div>
        </Card>
      </div>

      <div className="mt-3 grid gap-3 xl:grid-cols-4 [&>*]:min-w-0">
        <Card className="xl:col-span-3">
          <CardHeader title="Live streams" sub={`${streams.length} source${streams.length === 1 ? '' : 's'} connected`} actions={<Link href="/admin/streams" className="text-xs text-muted hover:text-ink">Manage</Link>} />
          {loading ? <div className="space-y-2 p-4">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-9 w-full" />)}</div>
            : streams.length === 0 ? <EmptyState icon={<Radio className="size-5" />} title="No live streams" body="Nothing is connected. Point an encoder at a mount, start an AutoDJ, or go live from the browser." action={<Link href="/admin/golive" className="text-xs text-accent hover:underline">Go live</Link>} />
            : (
              <Table>
                <thead><tr><Th>Mount</Th><Th>Now playing</Th><Th>Format</Th><Th align="right">Bitrate</Th><Th align="right">Listeners</Th><Th>Health</Th></tr></thead>
                <tbody>{streams.map((s) => <StreamRow key={s.mount} s={s} />)}</tbody>
              </Table>
            )}
        </Card>
        <Card>
          <CardHeader title="Runtime" sub="Go process" />
          <dl className="divide-y divide-line text-[13px]">
            {[
              ['Memory', stats ? bytes(stats.memory) : '-'],
              ['Goroutines', stats ? String(stats.goroutines) : '-'],
              ['GC cycles', stats ? String(stats.gc) : '-'],
              ['Total throughput', stats ? rate(stats.bandwidth) : '-'],
            ].map(([k, v]) => (
              <div key={k} className="flex items-center justify-between px-4 py-2.5"><dt className="text-muted">{k}</dt><dd className="num font-mono text-xs text-ink">{v}</dd></div>
            ))}
          </dl>
          {streams.length > 0 && (
            <div className="border-t border-line px-4 py-3">
              <div className="mb-2 text-xs text-faint">Listener share</div>
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
        </Card>
      </div>
    </>
  )
}
