import { useEffect, useMemo, useState } from 'react'
import * as RDialog from '@radix-ui/react-dialog'
import { useQuery } from '@tanstack/react-query'
import { Button, Segmented, Select } from '@/ui/controls'
import { Badge, EmptyState, PageHeader, Skeleton, Table, Td, Th } from '@/ui/display'
import { Bar, Sparkline } from '@/ui/charts'
import { Legend, Panel, Readout, Tally } from '@/ui/console'
import { Icon } from '@/ui/icons'
import { Checkbox, Menu, SearchInput, useAction, useConfirm, type MenuAction } from '@/ui/kit'
import { codec, compact } from '@/lib/format'
import { cn } from '@/lib/cn'
import { toast } from '@/lib/toast'
import { ConnectGuide } from './streams/ConnectGuide'
import { MountDetail } from './streams/Detail'
import { MountDialog } from './streams/MountDialog'
import { deleteMount, getLowLatency, hotSwap, kickAllListeners, kickListeners, kickSource, setLowLatency, updateMount } from './streams/actions'
import { isSuperadmin, urlFor, useMounts, type Mount } from './streams/model'
import { StatusBadge, statusLabel } from './streams/parts'

type Filter = 'all' | 'live' | 'offline' | 'disabled'

function useMedia(query: string) {
  const [on, setOn] = useState(() => window.matchMedia(query).matches)
  useEffect(() => {
    const mq = window.matchMedia(query)
    const h = () => setOn(mq.matches)
    mq.addEventListener('change', h)
    h()
    return () => mq.removeEventListener('change', h)
  }, [query])
  return on
}

function Lamp({ m }: { m: Mount }) {
  return (
    <span title={statusLabel(m.status)} className="inline-flex">
      {m.status === 'stalled' ? <span className="size-2 rounded-full bg-warn" /> : <Tally on={m.status === 'live'} />}
      <span className="sr-only">{statusLabel(m.status)}</span>
    </span>
  )
}

/** Right-hand sheet for viewports too narrow to show the detail beside the list. */
function Drawer({ open, onClose, children, label }: { open: boolean; onClose: () => void; children: React.ReactNode; label: string }) {
  return (
    <RDialog.Root open={open} onOpenChange={(o) => !o && onClose()}>
      <RDialog.Portal>
        <RDialog.Overlay className="fixed inset-0 z-50 bg-black/60 animate-[overlay-in_.15s_ease-out]" />
        <RDialog.Content aria-describedby={undefined} className="fixed inset-y-0 right-0 z-50 w-[min(460px,100vw)] border-l border-line-strong bg-bg p-2 animate-[pop-in_.14s_ease-out] focus:outline-none">
          <RDialog.Title className="sr-only">{label}</RDialog.Title>
          <div className="h-full [&>section]:h-full">{children}</div>
        </RDialog.Content>
      </RDialog.Portal>
    </RDialog.Root>
  )
}

function Stat({ label, value, unit, tone, foot }: { label: string; value: number | string; unit?: string; tone?: 'warn'; foot?: string }) {
  return (
    <div className="min-w-0 rounded-lg border border-line bg-surface px-4 py-3">
      <Readout label={label} value={value} unit={unit} tone={tone} />
      <div className="mt-1.5 h-4 truncate text-xs text-faint">{foot}</div>
    </div>
  )
}

export default function Streams() {
  const { mounts, history, loading, error, refresh, refetch } = useMounts()
  const { run } = useAction()
  const { ask, dialog } = useConfirm()
  const wide = useMedia('(min-width: 1280px)')
  const admin = isSuperadmin()

  const [q, setQ] = useState('')
  const [filter, setFilter] = useState<Filter>('all')
  const [sel, setSel] = useState<string | null>(null)
  const [checked, setChecked] = useState<Set<string>>(new Set())
  const [dlg, setDlg] = useState<{ open: boolean; edit: Mount | null }>({ open: false, edit: null })
  const [fallbacks, setFallbacks] = useState<Record<string, string>>({})
  const [guideMount, setGuideMount] = useState('')

  const latency = useQuery({ queryKey: ['admin', 'low-latency'], queryFn: getLowLatency, enabled: admin, retry: false })

  const counts = useMemo(() => ({
    all: mounts.length,
    live: mounts.filter((m) => m.isLive).length,
    offline: mounts.filter((m) => m.status === 'offline').length,
    disabled: mounts.filter((m) => m.status === 'disabled').length,
  }), [mounts])
  const totalListeners = mounts.reduce((a, m) => a + m.listeners, 0)
  const stalled = mounts.filter((m) => m.status === 'stalled')

  const rows = useMemo(() => {
    const needle = q.trim().toLowerCase()
    return mounts.filter((m) => {
      if (filter === 'live' && !m.isLive) return false
      if (filter === 'offline' && m.status !== 'offline') return false
      if (filter === 'disabled' && m.status !== 'disabled') return false
      return !needle || `${m.mount} ${m.song} ${m.name} ${m.source_ip}`.toLowerCase().includes(needle)
    })
  }, [mounts, q, filter])

  // Drop selections that no longer exist (mount removed here or elsewhere).
  useEffect(() => {
    if (sel && mounts.length && !mounts.some((m) => m.mount === sel)) setSel(null)
    setChecked((c) => { const n = new Set([...c].filter((x) => mounts.some((m) => m.mount === x))); return n.size === c.size ? c : n })
  }, [mounts, sel])

  const selected = mounts.find((m) => m.mount === sel) ?? null
  // With the detail panel beside it the list is narrow; drop the secondary columns below 1400px.
  const tight = wide && !!selected
  const allChecked = rows.length > 0 && rows.every((m) => checked.has(m.mount))
  const picked = mounts.filter((m) => checked.has(m.mount))

  const toggleAll = (v: boolean) => setChecked(v ? new Set(rows.map((m) => m.mount)) : new Set())
  const toggleOne = (mount: string, v: boolean) => setChecked((c) => { const n = new Set(c); if (v) n.add(mount); else n.delete(mount); return n })

  /** Run one call per mount, tolerate partial failure and say exactly how it went. */
  const bulk = (key: string, list: Mount[], fn: (m: Mount) => Promise<unknown>, verb: string) => run(key, async () => {
    const failed: string[] = []
    for (const m of list) { try { await fn(m) } catch { failed.push(m.mount) } }
    refresh()
    if (failed.length === list.length) throw new Error(`${verb} failed for every selected mount`)
    if (failed.length) throw new Error(`${verb}: ${list.length - failed.length} done, failed for ${failed.join(', ')}`)
    setChecked(new Set())
  }, `${verb}: ${list.length} mount${list.length === 1 ? '' : 's'}`)

  const one = (key: string, fn: () => Promise<unknown>, ok: string) => run(key, async () => { await fn(); refresh() }, ok)

  const rowMenu = (m: Mount): (MenuAction | 'sep')[] => [
    { label: 'Details', icon: 'eye', onSelect: () => setSel(m.mount) },
    { label: 'Edit', icon: 'edit', onSelect: () => setDlg({ open: true, edit: m }) },
    { label: m.enabled ? 'Disable' : 'Enable', icon: m.enabled ? 'pause' : 'play', onSelect: () => void one('toggle', () => updateMount(m.mount, { enabled: !m.enabled }), m.enabled ? 'Mount disabled' : 'Mount enabled') },
    { label: m.visible ? 'Hide from listing' : 'Show in listing', icon: 'globe', onSelect: () => void one('vis', () => updateMount(m.mount, { visible: !m.visible }), m.visible ? 'Mount hidden' : 'Mount listed') },
    { label: 'Copy stream URL', icon: 'copy', onSelect: () => void navigator.clipboard.writeText(urlFor(m.mount)).then(() => toast.success('Stream URL copied'), () => toast.error('Clipboard unavailable')) },
    'sep',
    { label: 'Kick source', icon: 'ban', disabled: !m.isLive, onSelect: () => ask({ title: `Kick the source on ${m.mount}?`, body: 'The encoder is disconnected and listeners are cut until it reconnects.', confirm: 'Kick source', run: () => one('kick', () => kickSource(m.mount), 'Source kicked') }) },
    { label: 'Kick listeners', icon: 'users', disabled: m.listeners === 0, onSelect: () => ask({ title: `Disconnect ${m.listeners} listeners on ${m.mount}?`, body: 'Players are cut and will normally reconnect by themselves.', confirm: 'Kick listeners', run: () => one('kickl', () => kickListeners(m.mount), 'Listeners kicked') }) },
    'sep',
    { label: 'Remove mount', icon: 'trash', danger: true, onSelect: () => ask({ title: `Remove ${m.mount}?`, body: 'The mount, its password and settings are deleted and any source is disconnected.', confirm: 'Remove mount', run: () => one('del', () => deleteMount(m.mount), `${m.mount} removed`) }) },
  ]

  const serverMenu: (MenuAction | 'sep')[] = [
    ...(latency.data ? [{
      label: latency.data.low_latency_mode ? 'Low-latency mode: on, turn off' : 'Low-latency mode: off, turn on', icon: 'bolt',
      onSelect: () => void run('latency', async () => { await setLowLatency(!latency.data!.low_latency_mode); await latency.refetch() }, 'Low-latency mode changed'),
    } satisfies MenuAction] : []),
    { label: 'Kick all listeners', icon: 'users', onSelect: () => ask({ title: 'Disconnect every listener on every mount?', body: 'Sources stay connected. Players normally reconnect by themselves.', confirm: 'Kick everyone', run: () => run('kickall', () => kickAllListeners(), 'All listeners kicked') }) },
    'sep',
    { label: 'Hot swap server', icon: 'refresh', danger: true, onSelect: () => ask({ title: 'Hot swap the server process?', body: 'Starts a fresh copy of the binary, waits 5 seconds, then stops this one. Use it after replacing the executable. Admin pages reconnect on their own.', confirm: 'Hot swap', run: () => run('hotswap', async () => { try { await hotSwap() } catch { /* the old process exits mid-request */ } }, 'Hot swap started') }) },
  ]

  const detailFor = (drawer: boolean) => selected && (
    <MountDetail drawer={drawer}
      m={selected} history={history(selected.mount)} fallback={fallbacks[selected.mount]}
      onFallback={(v) => setFallbacks((f) => ({ ...f, [selected.mount]: v }))}
      onEdit={() => setDlg({ open: true, edit: selected })} onClose={() => setSel(null)}
      afterChange={refresh} confirm={ask} canEditFallback
    />
  )

  const mountOptions = mounts.map((m) => ({ value: m.mount, label: m.mount }))
  const guideTarget = guideMount && mounts.some((m) => m.mount === guideMount) ? guideMount : mounts[0]?.mount ?? '/live'

  return (
    <>
      <PageHeader title="Streams" sub="Every mount on this server, whether or not an encoder is connected."
        actions={<>
          {admin && <Menu label="Server actions" actions={serverMenu} />}
          <Button variant="primary" onClick={() => setDlg({ open: true, edit: null })}><Icon name="plus" size={14} />Add mount</Button>
        </>} />

      <div className="mb-3 grid grid-cols-2 gap-3 lg:grid-cols-4 [&>*]:min-w-0">
        <Stat label="On air" value={loading ? '-' : counts.live} unit={`of ${counts.all}`} foot={`${counts.offline} waiting for a source${counts.disabled ? `, ${counts.disabled} disabled` : ''}`} />
        <Stat label="Listeners" value={loading ? '-' : totalListeners} foot={mounts.length ? `Busiest ${[...mounts].sort((a, b) => b.listeners - a.listeners)[0].mount}` : ''} />
        <Stat label="Needs attention" value={loading ? '-' : stalled.length} tone={stalled.length ? 'warn' : undefined} foot={stalled.length ? stalled.map((s) => s.mount).join(', ') : 'No mount is stalling'} />
        <Stat label="Video" value={loading ? '-' : mounts.filter((m) => m.has_video).length} unit="mounts" foot="With a picture sub-mount" />
      </div>

      <div className={cn('grid gap-3 [&>*]:min-w-0', wide && selected ? 'grid-cols-[minmax(0,1fr)_430px]' : 'grid-cols-1')}>
        <Panel legend="Mounts" title={loading ? undefined : `${rows.length}${rows.length !== mounts.length ? ` of ${mounts.length}` : ''}`} flush lamp={counts.live ? 'live' : 'off'}>
          <div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2.5">
            <SearchInput value={q} onChange={setQ} placeholder="Search mount, track or source" className="w-full sm:w-72" />
            <Segmented<Filter> label="Filter mounts" value={filter} onChange={setFilter} options={[
              { value: 'all', label: `All ${counts.all}` }, { value: 'live', label: `On air ${counts.live}` }, { value: 'offline', label: `Offline ${counts.offline}` }, ...(counts.disabled ? [{ value: 'disabled' as const, label: `Disabled ${counts.disabled}` }] : []),
            ]} />
          </div>

          {picked.length > 0 && (
            <div className="flex flex-wrap items-center gap-2 border-b border-line bg-hover/50 px-4 py-2" role="region" aria-label="Bulk actions">
              <span className="num mr-1 text-xs text-ink">{picked.length} selected</span>
              <Button size="sm" onClick={() => void bulk('b-en', picked.filter((m) => !m.enabled), (m) => updateMount(m.mount, { enabled: true }), 'Enabled')} disabled={!picked.some((m) => !m.enabled)}>Enable</Button>
              <Button size="sm" onClick={() => ask({ title: `Disable ${picked.length} mount${picked.length === 1 ? '' : 's'}?`, body: 'Their sources are disconnected and new ones refused.', confirm: 'Disable', run: () => bulk('b-dis', picked.filter((m) => m.enabled), (m) => updateMount(m.mount, { enabled: false }), 'Disabled') })} disabled={!picked.some((m) => m.enabled)}>Disable</Button>
              <Button size="sm" onClick={() => void bulk('b-vis', picked, (m) => updateMount(m.mount, { visible: !picked.every((x) => x.visible) }), 'Listing changed')}>{picked.every((m) => m.visible) ? 'Hide' : 'List'}</Button>
              <Button size="sm" onClick={() => ask({ title: `Disconnect listeners on ${picked.length} mount${picked.length === 1 ? '' : 's'}?`, confirm: 'Kick listeners', run: () => bulk('b-kl', picked.filter((m) => m.listeners > 0), (m) => kickListeners(m.mount), 'Listeners kicked') })} disabled={!picked.some((m) => m.listeners > 0)}>Kick listeners</Button>
              <Button size="sm" variant="danger" onClick={() => ask({ title: `Remove ${picked.length} mount${picked.length === 1 ? '' : 's'}?`, body: picked.map((m) => m.mount).join(', '), confirm: 'Remove', run: () => bulk('b-del', picked, (m) => deleteMount(m.mount), 'Removed') })}>Remove</Button>
              <button className="ml-auto text-xs text-muted hover:text-ink" onClick={() => setChecked(new Set())}>Clear</button>
            </div>
          )}

          {loading ? (
            <div className="space-y-2 p-4">{[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-9 w-full" />)}</div>
          ) : error && !mounts.length ? (
            <EmptyState icon={<Icon name="alert" size={20} />} title="Could not load the mounts" body={error.message} action={<Button onClick={() => void refetch()}><Icon name="refresh" size={14} />Retry</Button>} />
          ) : mounts.length === 0 ? (
            <EmptyState icon={<Icon name="streams" size={20} />} title="No mounts yet" body="A mount is an address an encoder publishes to and listeners open. Create one, then connect an encoder with the settings shown on the next screen."
              action={<Button variant="primary" onClick={() => setDlg({ open: true, edit: null })}><Icon name="plus" size={14} />Add mount</Button>} />
          ) : rows.length === 0 ? (
            <EmptyState icon={<Icon name="search" size={20} />} title="No mount matches" body="Nothing fits the current search and filter." action={<Button onClick={() => { setQ(''); setFilter('all') }}>Clear filters</Button>} />
          ) : (
            <Table>
              <thead>
                <tr>
                  <Th className="w-10 pr-0"><Checkbox label="Select all mounts" checked={allChecked} onChange={toggleAll} /></Th>
                  <Th>Mount</Th>
                  <Th className="max-md:hidden">Now playing</Th>
                  <Th className={cn('max-lg:hidden', tight && 'max-[1400px]:hidden')}>Format</Th>
                  <Th align="right">Listeners</Th>
                  <Th className={cn('w-32 max-xl:hidden', tight && 'max-[1400px]:hidden')}>Health</Th>
                  <Th className="w-10"><span className="sr-only">Actions</span></Th>
                </tr>
              </thead>
              <tbody>
                {rows.map((m) => {
                  const active = m.mount === sel
                  const hist = history(m.mount).map((s) => s.v)
                  return (
                    <tr key={m.mount} className={cn('transition-colors hover:bg-hover/60', active && 'bg-hover/60 shadow-[inset_2px_0_0_var(--accent)]')}>
                      <Td className="w-10 pr-0"><Checkbox label={`Select ${m.mount}`} checked={checked.has(m.mount)} onChange={(v) => toggleOne(m.mount, v)} /></Td>
                      <Td>
                        <button onClick={() => setSel(active && wide ? null : m.mount)} aria-pressed={active} className="flex w-full min-w-0 items-center gap-2.5 text-left">
                          <Lamp m={m} />
                          <span className="min-w-0">
                            <span className="flex items-center gap-1.5 whitespace-nowrap">
                              <span className={cn('font-mono text-xs', m.status === 'disabled' ? 'text-faint line-through' : 'text-ink')}>{m.mount}</span>
                              {m.has_video && <Badge tone="info">Video</Badge>}
                              {!m.visible && <Badge>Unlisted</Badge>}
                              {m.status === 'stalled' && <Badge tone="warn">Stalling</Badge>}
                            </span>
                            <span className="block truncate text-[11px] text-faint">{m.status === 'disabled' ? 'Disabled' : m.isLive ? (m.name || m.source_ip) : 'Waiting for a source'}</span>
                          </span>
                        </button>
                      </Td>
                      <Td className="max-w-[300px] max-md:hidden"><div className="truncate text-muted">{m.song || <span className="text-faint">{m.isLive ? 'No metadata' : '-'}</span>}</div></Td>
                      <Td className={cn('max-lg:hidden', tight && 'max-[1400px]:hidden')}>{m.isLive ? <span className="flex items-center gap-1.5"><Badge>{codec(m.codecType)}</Badge>{m.kbps > 0 && <span className="num font-mono text-xs text-muted">{m.kbps}k</span>}</span> : <span className="text-faint">-</span>}</Td>
                      <Td align="right">
                        <div className="flex items-center justify-end gap-3">
                          {m.isLive && <div className={cn('w-16 max-sm:hidden [&>svg]:h-5', tight && 'max-[1400px]:hidden')}><Sparkline values={hist} color={m.status === 'stalled' ? 'var(--warn)' : 'var(--accent)'} /></div>}
                          <span className="num w-10 text-right font-mono text-xs">{compact(m.listeners)}</span>
                        </div>
                      </Td>
                      <Td className={cn('max-xl:hidden', tight && 'max-[1400px]:hidden')}>
                        {m.isLive ? (
                          <div className="flex items-center gap-2.5" title={m.listeners === 0 ? 'Health is only meaningful while someone is listening' : undefined}>
                            <Bar value={m.listeners === 0 ? 1 : m.health / 100} tone={m.listeners === 0 ? 'var(--line-strong)' : `var(--${m.health >= 80 ? 'live' : m.health >= 50 ? 'warn' : 'danger'})`} className="flex-1" />
                            <span className="num w-9 text-right font-mono text-xs text-muted">{m.listeners === 0 ? 'idle' : Math.round(m.health)}</span>
                          </div>
                        ) : <span className="text-faint">-</span>}
                      </Td>
                      <Td className="w-10 pr-2"><Menu label={`Actions for ${m.mount}`} actions={rowMenu(m)} /></Td>
                    </tr>
                  )
                })}
              </tbody>
            </Table>
          )}
          {!loading && mounts.length > 0 && <div className="flex items-center justify-between border-t border-line px-4 py-2"><Legend>Select a row for details</Legend><StatusLegend /></div>}
        </Panel>

        {wide && selected && <div className="sticky top-16 self-start">{detailFor(false)}</div>}
      </div>
      {!wide && <Drawer open={!!selected} onClose={() => setSel(null)} label={`Details for ${selected?.mount ?? 'mount'}`}>{detailFor(true)}</Drawer>}

      <div className="mt-3">
        <Panel legend="Connect an encoder" title={mounts.length ? undefined : 'Create a mount first, then use these settings'}
          actions={mounts.length > 0 ? <Select value={guideTarget} onValueChange={setGuideMount} options={mountOptions} className="w-44" /> : undefined}>
          <ConnectGuide mount={guideTarget} />
        </Panel>
      </div>

      <MountDialog open={dlg.open} edit={dlg.edit} existing={mounts.map((m) => m.mount)} onOpenChange={(o) => setDlg((d) => ({ ...d, open: o }))} onDone={refresh} />
      {dialog}
    </>
  )
}

function StatusLegend() {
  return (
    <span className="flex items-center gap-3 max-sm:hidden">
      <StatusBadge status="live" /><StatusBadge status="stalled" /><StatusBadge status="offline" />
    </span>
  )
}
