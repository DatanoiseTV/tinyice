import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Button, Switch } from '@/ui/controls'
import { Badge, EmptyState, PageHeader, Skeleton } from '@/ui/display'
import { Panel, Readout, Tally } from '@/ui/console'
import { Icon, registerIcons } from '@/ui/icons'
import { Menu, useAction, useConfirm } from '@/ui/kit'
import { useLive } from '@/lib/live'
import { bytes as fmtBytes, compact } from '@/lib/format'
import { api } from '@/lib/api'
import type { ApiMount } from './streams/model'
import { TranscoderDialog } from './transcoders/TranscoderDialog'
import { TC_KEY, TC_STATS_KEY, deleteTranscoder, listTranscoders, toggleTranscoder, transcoderStats, type Transcoder } from './transcoders/model'

registerIcons({ 'arrow-flow': { d: 'M3 10h12 M11 6l4 4-4 4' } })

/** Go duration ("1h2m3s", "OFF") shown as the server wrote it, with OFF mapped to nothing. */
const dur = (s: string) => (s && s !== 'OFF' ? s : '')

function MountChip({ mount, live, note }: { mount: string; live: boolean; note?: string }) {
  return (
    <div className="flex min-w-0 items-center gap-2 rounded-md border border-line bg-bg px-2.5 py-1.5">
      <Tally on={live} />
      <div className="min-w-0"><div className="truncate font-mono text-xs text-ink">{mount}</div>{note && <div className="num truncate text-[11px] text-faint">{note}</div>}</div>
    </div>
  )
}

function Row({ t, inputLive, outputListeners, kbps, frames, byteCount, uptime, onEdit, onToggle, onDelete, busy }: {
  t: Transcoder; inputLive: boolean; outputListeners?: number; kbps?: number; frames: number; byteCount: number; uptime: string
  onEdit: () => void; onToggle: () => void; onDelete: () => void; busy: boolean
}) {
  const state = !t.enabled ? 'disabled' : t.active ? 'running' : inputLive ? 'starting' : 'waiting'
  const label = { disabled: 'Disabled', running: 'Running', starting: 'Starting', waiting: 'Waiting for input' }[state]
  return (
    <li className="px-4 py-3.5">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-3">
        <div className="min-w-0 flex-1 basis-44 sm:w-52 sm:flex-none">
          <div className="flex items-center gap-2">
            <span className="inline-flex" title={label}>
              {state === 'running' ? <Tally on /> : state === 'disabled' ? <Tally on={false} /> : <span className="size-2 rounded-full bg-warn" />}
              <span className="sr-only">{label}</span>
            </span>
            <span className={`truncate text-[13px] font-medium ${state === 'disabled' ? 'text-faint' : 'text-ink'}`}>{t.name}</span>
          </div>
          <div className={`mt-0.5 pl-4 text-xs ${state === 'running' ? 'text-live' : state === 'disabled' ? 'text-faint' : 'text-warn'}`}>{label}{state === 'running' && dur(uptime) ? <span className="num font-mono text-faint"> · {dur(uptime)}</span> : null}</div>
        </div>

        <div className="order-3 flex min-w-0 basis-full items-center gap-2 max-sm:flex-col max-sm:items-stretch sm:order-2 sm:flex-1 sm:basis-80">
          <MountChip mount={t.input} live={inputLive} />
          <Icon name="arrow-flow" size={16} className="shrink-0 self-center text-faint max-sm:rotate-90" />
          <Badge tone="accent" className="shrink-0 self-center">{t.format.toUpperCase()} {t.bitrate} kbps</Badge>
          <Icon name="arrow-flow" size={16} className="shrink-0 self-center text-faint max-sm:rotate-90" />
          <MountChip mount={t.output} live={t.active} note={t.active && outputListeners !== undefined ? `${outputListeners} listening` : undefined} />
        </div>

        <div className="order-3 flex items-center gap-5 text-xs max-xl:hidden">
          <div className="w-24"><div className="text-faint">Encoding</div><div className="num font-mono text-ink">{t.active && kbps !== undefined ? `${kbps.toFixed(1)} kbps` : '-'}</div></div>
          <div className="w-24"><div className="text-faint">Output</div><div className="num font-mono text-ink">{t.active ? fmtBytes(byteCount) : '-'}</div></div>
          <div className="w-20"><div className="text-faint">Frames</div><div className="num font-mono text-ink">{t.active ? compact(frames) : '-'}</div></div>
        </div>

        <div className="order-2 ml-auto flex items-center gap-2 sm:order-4">
          <Switch label={`Run ${t.name}`} checked={t.enabled} disabled={busy} onCheckedChange={onToggle} />
          <Menu label={`Actions for ${t.name}`} actions={[
            { label: 'Edit', icon: 'edit', onSelect: onEdit },
            'sep',
            { label: 'Delete transcoder', icon: 'trash', danger: true, onSelect: onDelete },
          ]} />
        </div>
      </div>
      {state === 'waiting' && <p className="mt-2 pl-4 text-xs text-muted" role="status">It starts by itself when <span className="font-mono">{t.input}</span> has a live source.</p>}
    </li>
  )
}

export default function Transcoders() {
  const qc = useQueryClient()
  const { streams } = useLive()
  const q = useQuery({ queryKey: TC_KEY, queryFn: listTranscoders, refetchInterval: 4000, staleTime: 0 })
  const stats = useQuery({ queryKey: TC_STATS_KEY, queryFn: transcoderStats, refetchInterval: 2000, staleTime: 0 })
  const mountsQ = useQuery({ queryKey: ['admin', 'streams', 'names'], queryFn: () => api.get<ApiMount[]>('/api/streams'), staleTime: 30_000 })
  const { run, busy } = useAction()
  const { ask, dialog } = useConfirm()
  const [dlg, setDlg] = useState<{ open: boolean; edit: Transcoder | null }>({ open: false, edit: null })
  const list = q.data ?? []
  const refresh = () => Promise.all([qc.invalidateQueries({ queryKey: TC_KEY }), qc.invalidateQueries({ queryKey: TC_STATS_KEY })])

  // Measured output rate from consecutive byte counters; the configured bitrate is a target, this is the truth.
  // Live counters come from /admin/transcoder-stats; when that is unavailable the same numbers
  // are read from the config list, which carries them too.
  const prev = useRef<Record<string, { bytes: number; t: number }>>({})
  const [rates, setRates] = useState<Record<string, number>>({})
  const tick = stats.data ? stats.dataUpdatedAt : q.dataUpdatedAt
  useEffect(() => {
    const now = Date.now()
    const next: Record<string, number> = {}
    for (const t of q.data ?? []) {
      const s = stats.data?.find((x) => x.name === t.name)
      const active = s?.active ?? t.active
      const total = s?.bytes ?? t.bytes_encoded
      const p = prev.current[t.name]
      if (active && p && total >= p.bytes && now > p.t) next[t.name] = ((total - p.bytes) * 8) / ((now - p.t) / 1000) / 1000
      prev.current[t.name] = { bytes: total, t: now }
    }
    setRates(next)
  }, [tick]) // eslint-disable-line react-hooks/exhaustive-deps

  const live = useMemo(() => new Map(streams.map((s) => [s.mount, s])), [streams])
  const stat = useMemo(() => new Map((stats.data ?? []).map((s) => [s.name, s])), [stats.data])
  const running = list.filter((t) => (stat.get(t.name)?.active ?? t.active)).length
  const totalBytes = list.reduce((a, t) => a + (stat.get(t.name)?.bytes ?? t.bytes_encoded), 0)
  const mountNames = (mountsQ.data ?? []).map((m) => m.mount)

  return (
    <>
      <PageHeader title="Transcoders" sub="Decode a mount and publish it again in another format or bitrate."
        actions={<Button variant="primary" onClick={() => setDlg({ open: true, edit: null })}><Icon name="plus" size={14} />Add transcoder</Button>} />

      <div className="mb-3 grid grid-cols-3 gap-3 [&>*]:min-w-0">
        {[['Running', q.isLoading ? '-' : running, `of ${list.length}`], ['Waiting', q.isLoading ? '-' : list.filter((t) => t.enabled && !(stat.get(t.name)?.active ?? t.active)).length, undefined], ['Encoded', fmtBytes(totalBytes).split(' ')[0], fmtBytes(totalBytes).split(' ')[1]]].map(([l, v, u]) => (
          <div key={l as string} className="rounded-lg border border-line bg-surface px-4 py-3"><Readout label={l as string} value={v as string | number} unit={u as string | undefined} /></div>
        ))}
      </div>

      <Panel legend="Transcoders" title={q.isLoading ? undefined : `${list.length}`} lamp={running ? 'live' : 'off'} flush>
        {q.isLoading ? (
          <div className="space-y-2 p-4">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-14 w-full" />)}</div>
        ) : q.error && !list.length ? (
          <EmptyState icon={<Icon name="alert" size={20} />} title="Could not load the transcoders" body={(q.error as Error).message} action={<Button onClick={() => void q.refetch()}><Icon name="refresh" size={14} />Retry</Button>} />
        ) : list.length === 0 ? (
          <EmptyState icon={<Icon name="transcoders" size={20} />} title="No transcoders yet"
            body="A transcoder reads one mount and writes a new one, for example a 64 kbps Opus copy of /live for mobile listeners. Each running transcoder uses CPU while its input has a source."
            action={<Button variant="primary" onClick={() => setDlg({ open: true, edit: null })}><Icon name="plus" size={14} />Add transcoder</Button>} />
        ) : (
          <ul className="divide-y divide-line">
            {list.map((t) => {
              const s = stat.get(t.name)
              const merged = { ...t, active: s?.active ?? t.active }
              return (
                <Row key={t.name} t={merged} inputLive={live.has(t.input)} outputListeners={live.get(t.output)?.listeners}
                  kbps={rates[t.name]} frames={s?.frames ?? t.frames_processed} byteCount={s?.bytes ?? t.bytes_encoded} uptime={s?.uptime ?? t.uptime}
                  busy={busy === `t:${t.name}`} onEdit={() => setDlg({ open: true, edit: t })}
                  onToggle={() => void run(`t:${t.name}`, async () => { await toggleTranscoder(t.name); await refresh() }, t.enabled ? `${t.name} stopped` : `${t.name} started`)}
                  onDelete={() => ask({ title: `Delete ${t.name}?`, body: `${t.output} stops being published. The input mount is not touched.`, run: () => run('del', async () => { await deleteTranscoder(t.name); await refresh() }, `${t.name} deleted`) })} />
              )
            })}
          </ul>
        )}
      </Panel>

      <TranscoderDialog open={dlg.open} edit={dlg.edit} all={list} mounts={mountNames} onOpenChange={(o) => setDlg((d) => ({ ...d, open: o }))} onDone={refresh} />
      {dialog}
    </>
  )
}
