import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { AreaChart } from '@/ui/charts'
import { Button, Input, Switch } from '@/ui/controls'
import { Legend, Panel, Readout } from '@/ui/console'
import { Skeleton } from '@/ui/display'
import { Icon } from '@/ui/icons'
import { CopyButton, useAction, type useConfirm } from '@/ui/kit'
import { cn } from '@/lib/cn'
import { codec, compact, uptime as fmtUptime } from '@/lib/format'
import { kickListeners, kickSource, setFallback, setSong, updateMount, deleteMount } from './actions'
import { codecKind, sourceLabel, urlFor, xmlMessage, type Mount, type Sample } from './model'
import { StatusBadge, lampOf } from './parts'

function Block({ legend, children, actions }: { legend: string; children: ReactNode; actions?: ReactNode }) {
  return (
    <section className="border-t border-line px-4 py-3.5">
      <div className="mb-2.5 flex items-center justify-between gap-2"><Legend>{legend}</Legend>{actions}</div>
      {children}
    </section>
  )
}

function UrlRow({ label, url, note }: { label: string; url: string; note?: string }) {
  return (
    <div className="flex items-center gap-2 py-1">
      <div className="w-14 shrink-0 text-xs text-muted">{label}</div>
      <div className="min-w-0 flex-1" title={note}><div className="truncate font-mono text-xs text-ink">{url}</div></div>
      <CopyButton text={url} label={`Copy ${label} URL`} />
      {/^https?:/.test(url) && (
        <a href={url} target="_blank" rel="noreferrer" aria-label={`Open ${label}`} title="Open in a new tab" className="flex size-7 items-center justify-center rounded-md text-faint hover:bg-hover hover:text-ink"><Icon name="external" size={14} /></a>
      )}
    </div>
  )
}

/** "07:14:33" or "26:00:00" (HH:MM:SS from the server) to seconds. */
function uptimeSeconds(s: string): number {
  const p = s.split(':').map(Number)
  return p.length === 3 && p.every(Number.isFinite) ? p[0] * 3600 + p[1] * 60 + p[2] : NaN
}

/**
 * Everything about one mount: live readouts, a listener trace, the source,
 * now-playing and fallback editors, public addresses and the destructive actions.
 */
export function MountDetail({ m, history, fallback, onFallback, onEdit, onClose, afterChange, confirm, canEditFallback = true, drawer }: {
  m: Mount; history: Sample[]; fallback?: string; onFallback: (v: string) => void; onEdit: () => void; onClose?: () => void
  afterChange: () => void; confirm: ReturnType<typeof useConfirm>['ask']; canEditFallback?: boolean; drawer?: boolean
}) {
  const { run, isBusy } = useAction()
  const [song, setSongText] = useState(m.song)
  const [editing, setEditing] = useState(false)
  const [fb, setFb] = useState(fallback ?? m.fallback ?? '')

  // Follow the live title until the operator starts typing; never clobber their edit.
  useEffect(() => { if (!editing) setSongText(m.song) }, [m.song, m.mount, editing])
  useEffect(() => { setEditing(false); setFb(fallback ?? m.fallback ?? '') }, [m.mount]) // eslint-disable-line react-hooks/exhaustive-deps

  const times = useMemo(() => history.map((h) => h.t), [history])
  const values = useMemo(() => history.map((h) => h.v), [history])
  const peak = values.length ? Math.max(...values) : 0
  const kind = codecKind(m)
  const up = uptimeSeconds(m.uptime)

  const act = (key: string, fn: () => Promise<unknown>, ok: string) => run(key, async () => { await fn(); afterChange() }, ok)

  const sendSong = async () => {
    const v = song.trim()
    if (!v) return
    await run('song', async () => {
      try { await setSong(m.mount, v) } catch (e) { throw new Error(xmlMessage(e)) }
      setEditing(false)
      afterChange()
    }, 'Now playing updated')
  }
  const sendFallback = (value: string) => run('fallback', async () => {
    const v = value.trim()
    await setFallback(m.mount, v)
    onFallback(v ? (v.startsWith('/') ? v : `/${v}`) : '')
    if (!v) setFb('')
  }, value.trim() ? 'Fallback saved' : 'Fallback cleared')

  const embed = `<iframe src="${urlFor(`/embed${m.mount}`)}" width="100%" height="120" frameborder="0" allow="autoplay"></iframe>`

  return (
    <Panel legend="Mount" title={<span className="font-mono text-xs">{m.mount}</span>} lamp={lampOf(m.status)} flush
      actions={<>
        <Button size="sm" onClick={onEdit}><Icon name="edit" size={14} />Edit</Button>
        {onClose && <button onClick={onClose} aria-label="Close details" className="flex size-7 items-center justify-center rounded-md text-faint hover:bg-hover hover:text-ink"><Icon name="x" /></button>}
      </>}>
      <div className={cn('overflow-y-auto', drawer ? 'max-h-[calc(100dvh-3.5rem)]' : 'max-h-[calc(100dvh-7.5rem)]')}>
        <div className="grid grid-cols-2 gap-x-4 gap-y-4 px-4 py-4">
          <Readout label="Listeners" value={m.listeners} />
          <Readout label="Bitrate" value={m.kbps || '-'} unit={m.kbps ? 'kbps' : undefined} />
          <Readout label="Uptime" value={Number.isFinite(up) ? fmtUptime(up) : (m.uptime || '-')} className="[&_span.num]:text-lg" />
          <Readout label="Health" value={m.isLive ? Math.round(m.health) : '-'} unit={m.isLive ? '%' : undefined} tone={m.status === 'stalled' ? 'warn' : undefined} className="[&_span.num]:text-lg" />
        </div>

        <Block legend="Listeners" actions={<span className="num font-mono text-[11px] text-faint">{values.length > 1 ? `peak ${compact(peak)} in ${Math.round((times[times.length - 1] - times[0]) / 60000) || '<1'} min` : ''}</span>}>
          {values.length >= 2
            ? <AreaChart series={[{ name: 'Listeners', values, color: 'var(--accent)' }]} times={times} height={120} format={compact} />
            : <Skeleton className="h-[120px] w-full" />}
        </Block>

        <Block legend="Source">
          <dl className="space-y-1.5 text-[13px]">
            {[
              ['Status', <StatusBadge key="s" status={m.status} />],
              ['Source', <span key="src" className="font-mono text-xs">{sourceLabel(m.source_ip)}</span>],
              ['Format', m.codecType ? <span key="f" className="font-mono text-xs">{codec(m.codecType)} <span className="text-faint">{m.codecType}</span></span> : '-'],
              ['Name', m.name || '-'],
              ...(m.has_video && m.video_width ? [['Video', <span key="v" className="num font-mono text-xs">{m.video_width}x{m.video_height}{m.video_fps ? ` · ${m.video_fps.toFixed(0)} fps` : ''}{m.video_kbps ? ` · ${m.video_kbps} kbps` : ''}{m.video_gop ? ` · GOP ${m.video_gop.toFixed(1)} s` : ''}</span>]] : []),
            ].map(([k, v]) => <div key={k as string} className="flex items-start justify-between gap-4"><dt className="shrink-0 text-muted">{k}</dt><dd className="min-w-0 break-words text-right text-ink">{v}</dd></div>)}
          </dl>
          <div className="mt-3 space-y-2.5 border-t border-line pt-3">
            <div className="flex items-center justify-between gap-4">
              <div><div className="text-[13px] text-ink">Accept sources</div><div className="text-xs text-muted">Off drops the source and refuses new ones.</div></div>
              <Switch label="Accept sources" checked={m.enabled} disabled={isBusy('enabled')} onCheckedChange={(v) => act('enabled', () => updateMount(m.mount, { enabled: v }), v ? 'Mount enabled' : 'Mount disabled')} />
            </div>
            <div className="flex items-center justify-between gap-4">
              <div><div className="text-[13px] text-ink">Public listing</div><div className="text-xs text-muted">Show on the landing page and the directory.</div></div>
              <Switch label="Public listing" checked={m.visible} disabled={isBusy('visible')} onCheckedChange={(v) => act('visible', () => updateMount(m.mount, { visible: v }), v ? 'Mount is listed' : 'Mount is hidden')} />
            </div>
          </div>
        </Block>

        <Block legend="Now playing">
          <form className="flex gap-2" onSubmit={(e) => { e.preventDefault(); void sendSong() }}>
            <Input value={song} onChange={(e) => { setEditing(true); setSongText(e.target.value) }} placeholder={m.isLive ? 'Artist - Title' : 'Needs a live source'} disabled={!m.isLive} aria-label="Now playing text" maxLength={300} />
            <Button type="submit" variant="primary" loading={isBusy('song')} disabled={!m.isLive || !song.trim() || song.trim() === m.song}>Update</Button>
          </form>
          <p className="mt-1.5 text-xs text-faint">Replaces the title shown to listeners. The encoder overwrites it with its next metadata update.</p>
        </Block>

        {canEditFallback && (
          <Block legend="Fallback">
            <form className="flex gap-2" onSubmit={(e) => { e.preventDefault(); void sendFallback(fb) }}>
              <Input mono value={fb} onChange={(e) => setFb(e.target.value)} placeholder="/backup" aria-label="Fallback mount" spellCheck={false} />
              <Button type="submit" loading={isBusy('fallback')}>Save</Button>
              {(fallback || m.fallback) && <Button type="button" variant="ghost" onClick={() => void sendFallback('')}>Clear</Button>}
            </form>
            <p className="mt-1.5 text-xs text-faint">When this mount has no source, new listeners are served from the fallback mount. An empty value removes it. The server does not report the stored value, so this field shows only what was set in this session.</p>
          </Block>
        )}

        <Block legend="Addresses">
          <UrlRow label="Stream" url={urlFor(m.mount)} />
          <UrlRow label="Player" url={urlFor(`/player${m.mount}`)} />
          {kind !== 'opus' && kind !== 'other' && <UrlRow label="HLS" url={urlFor(`${m.mount}/playlist.m3u8`)} note="MPEG-TS cannot carry Opus, so Opus mounts have no HLS playlist." />}
          {kind === 'opus' && <UrlRow label="WHEP" url={urlFor(`${m.mount}/whep`)} note="Opus and WebRTC mounts are played over WHEP." />}
          <div className="flex items-center gap-2 py-1">
            <div className="w-14 shrink-0 text-xs text-muted">Embed</div>
            <div className="min-w-0 flex-1"><div className="truncate font-mono text-xs text-ink">{embed}</div></div>
            <CopyButton text={embed} label="Copy embed code" />
          </div>
        </Block>

        <Block legend="Actions">
          <div className="flex flex-wrap gap-2">
            <Button disabled={!m.isLive} onClick={() => confirm({ title: `Kick the source on ${m.mount}?`, body: 'The encoder is disconnected. It may reconnect on its own, and listeners are cut until it does.', confirm: 'Kick source', run: () => act('kick-src', () => kickSource(m.mount), 'Source kicked') })}><Icon name="ban" size={14} />Kick source</Button>
            <Button disabled={m.listeners === 0} onClick={() => confirm({ title: `Disconnect ${m.listeners} listener${m.listeners === 1 ? '' : 's'} on ${m.mount}?`, body: 'Players are cut and will normally reconnect by themselves.', confirm: 'Kick listeners', run: () => act('kick-lis', () => kickListeners(m.mount), 'Listeners kicked') })}><Icon name="users" size={14} />Kick listeners</Button>
            <Button variant="danger" onClick={() => confirm({ title: `Remove ${m.mount}?`, body: 'The mount, its password and its settings are deleted and any source is disconnected.', confirm: 'Remove mount', run: () => act('delete', () => deleteMount(m.mount), `${m.mount} removed`).then(() => onClose?.()) })}><Icon name="trash" size={14} />Remove</Button>
          </div>
        </Block>
      </div>
    </Panel>
  )
}

