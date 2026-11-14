import { useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Button, Switch } from '@/ui/controls'
import { Badge, EmptyState, PageHeader, Skeleton } from '@/ui/display'
import { Panel, Readout, Tally } from '@/ui/console'
import { Icon } from '@/ui/icons'
import { CopyButton, Menu, useAction, useConfirm } from '@/ui/kit'
import { useLive } from '@/lib/live'
import { uptime, codec } from '@/lib/format'
import type { StreamEvent } from '@/types'
import { useSince } from '@/widgets/useSince'
import { RelayDialog } from './relays/RelayDialog'
import { RELAYS_KEY, deleteRelay, displayUrl, listRelays, restartRelay, toggleRelay, type Relay } from './relays/model'

/** After this long without a connection the row stops saying "connecting" and says what to check. */
const STALL_AFTER_S = 20

type State = 'connected' | 'connecting' | 'stalled' | 'disabled'

function RelayRow({ r, down, onEdit, onToggle, onRestart, onDelete, busy }: {
  r: Relay; down?: number; onEdit: () => void; onToggle: () => void; onRestart: () => void; onDelete: () => void; busy: boolean
}) {
  const { streams } = useLive()
  const live = streams.find((s) => s.mount === r.mount) as (StreamEvent & { uptime?: string }) | undefined
  const state: State = !r.enabled ? 'disabled' : r.active ? 'connected' : (down ?? 0) >= STALL_AFTER_S ? 'stalled' : 'connecting'
  const u = displayUrl(r.url)
  const label = { connected: 'Connected', connecting: 'Connecting', stalled: 'Not connecting', disabled: 'Disabled' }[state]
  return (
    <li className="px-4 py-3.5">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <div className="flex min-w-0 flex-1 basis-64 items-center gap-3">
          <span className="inline-flex shrink-0" title={label}>
            {state === 'connected' ? <Tally on /> : state === 'disabled' ? <Tally on={false} /> : <span className="size-2 rounded-full bg-warn" />}
            <span className="sr-only">{label}</span>
          </span>
          <div className="min-w-0">
            <div className="flex items-center gap-1.5"><span className={`truncate font-mono text-[13px] ${state === 'disabled' ? 'text-faint' : 'text-ink'}`}>{r.mount}</span><CopyButton text={`${window.location.origin}${r.mount}`} label={`Copy ${r.mount} URL`} /></div>
            <div className="flex min-w-0 items-center gap-1.5 text-xs text-muted">
              <Icon name="arrow-down" size={12} className="shrink-0 text-faint" />
              <Badge className="shrink-0">{u.scheme || 'url'}</Badge>
              <span className="truncate font-mono" title={`${u.host}${u.path}`}>{u.host}<span className="text-faint">{u.path === '/' ? '' : u.path}</span></span>
              {u.hadAuth && <Badge tone="warn" className="shrink-0">Credentials in URL</Badge>}
            </div>
          </div>
        </div>

        <div className="flex items-center gap-5 text-xs">
          <div className="w-28">
            <div className={state === 'connected' ? 'text-live' : state === 'stalled' ? 'text-warn' : 'text-muted'}>{label}</div>
            <div className="num font-mono text-faint">{state === 'connecting' || state === 'stalled' ? `${uptime(down ?? 0)} waiting` : state === 'connected' && live?.uptime ? `up ${live.uptime}` : ' '}</div>
          </div>
          <div className="w-24 max-sm:hidden">
            {live ? <><div className="num font-mono text-ink">{live.listeners} <span className="text-faint">listening</span></div><div className="num font-mono text-faint">{codec(live.format)} {Number(live.bitrate) ? `${live.bitrate}k` : ''}</div></> : <div className="text-faint">-</div>}
          </div>

        </div>

        <div className="ml-auto flex items-center gap-2">
          <Switch label={`Enable relay ${r.mount}`} checked={r.enabled} disabled={busy} onCheckedChange={onToggle} />
          <Menu label={`Actions for relay ${r.mount}`} actions={[
            { label: 'Edit', icon: 'edit', onSelect: onEdit },
            { label: 'Reconnect now', icon: 'refresh', disabled: !r.enabled, onSelect: onRestart },
            'sep',
            { label: 'Delete relay', icon: 'trash', danger: true, onSelect: onDelete },
          ]} />
        </div>
      </div>
      {state === 'stalled' && (
        <p className="mt-2.5 flex gap-2 rounded-md border border-warn/25 bg-warn/10 px-3 py-2 text-xs text-ink" role="status">
          <Icon name="alert" size={14} className="mt-px shrink-0 text-warn" />
          <span>No connection for {uptime(down ?? 0)}. The server does not report why a relay fails. Check that <span className="font-mono">{u.host}</span> is reachable from this machine, the path exists, and the password is right. It keeps retrying on its own.</span>
        </p>
      )}
    </li>
  )
}

export default function Relays() {
  const qc = useQueryClient()
  const q = useQuery({ queryKey: RELAYS_KEY, queryFn: listRelays, refetchInterval: 4000, staleTime: 0 })
  const { run, busy } = useAction()
  const { ask, dialog } = useConfirm()
  const [dlg, setDlg] = useState<{ open: boolean; edit: Relay | null }>({ open: false, edit: null })
  const relays = q.data ?? []
  const refresh = () => qc.invalidateQueries({ queryKey: RELAYS_KEY })
  const since = useSince(relays.filter((r) => r.enabled && !r.active).map((r) => r.mount))

  const counts = useMemo(() => ({
    connected: relays.filter((r) => r.active).length,
    waiting: relays.filter((r) => r.enabled && !r.active).length,
    off: relays.filter((r) => !r.enabled).length,
  }), [relays])

  return (
    <>
      <PageHeader title="Relays" sub="Pull a stream from another server and republish it on a local mount."
        actions={<Button variant="primary" onClick={() => setDlg({ open: true, edit: null })}><Icon name="plus" size={14} />Add relay</Button>} />

      <div className="mb-3 grid grid-cols-3 gap-3 [&>*]:min-w-0">
        {[['Connected', counts.connected, undefined], ['Waiting', counts.waiting, counts.waiting ? 'warn' : undefined], ['Disabled', counts.off, undefined]].map(([l, v, tone]) => (
          <div key={l as string} className="rounded-lg border border-line bg-surface px-4 py-3"><Readout label={l as string} value={q.isLoading ? '-' : (v as number)} tone={tone as 'warn' | undefined} /></div>
        ))}
      </div>

      <Panel legend="Relays" title={q.isLoading ? undefined : `${relays.length}`} lamp={counts.connected ? 'live' : counts.waiting ? 'warn' : 'off'} flush>
        {q.isLoading ? (
          <div className="space-y-2 p-4">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-12 w-full" />)}</div>
        ) : q.error && !relays.length ? (
          <EmptyState icon={<Icon name="alert" size={20} />} title="Could not load the relays" body={(q.error as Error).message} action={<Button onClick={() => void q.refetch()}><Icon name="refresh" size={14} />Retry</Button>} />
        ) : relays.length === 0 ? (
          <EmptyState icon={<Icon name="relays" size={20} />} title="No relays configured"
            body="Add the URL of a stream on another server and a local mount. Listeners connect to this server, so the upstream carries one connection however many people listen here."
            action={<Button variant="primary" onClick={() => setDlg({ open: true, edit: null })}><Icon name="plus" size={14} />Add relay</Button>} />
        ) : (
          <ul className="divide-y divide-line">
            {relays.map((r) => (
              <RelayRow key={r.mount} r={r} down={since[r.mount]} busy={busy === `t:${r.mount}`}
                onEdit={() => setDlg({ open: true, edit: r })}
                onToggle={() => void run(`t:${r.mount}`, async () => { await toggleRelay(r.mount); await refresh() }, r.enabled ? `${r.mount} relay stopped` : `${r.mount} relay started`)}
                onRestart={() => void run(`r:${r.mount}`, async () => { await restartRelay(r.mount); await refresh() }, 'Reconnecting')}
                onDelete={() => ask({ title: `Delete the relay on ${r.mount}?`, body: `It stops pulling from ${displayUrl(r.url).host}. The mount itself is not deleted.`, run: () => run('del', async () => { await deleteRelay(r.mount); await refresh() }, `Relay ${r.mount} deleted`) })} />
            ))}
          </ul>
        )}
      </Panel>

      <RelayDialog open={dlg.open} edit={dlg.edit} existing={relays.map((r) => r.mount)} onOpenChange={(o) => setDlg((d) => ({ ...d, open: o }))} onDone={refresh} />
      {dialog}
    </>
  )
}
