import { Link } from 'wouter'
import { useQueryClient } from '@tanstack/react-query'
import { Badge } from '@/ui/display'
import { Legend, Panel } from '@/ui/console'
import { Switch } from '@/ui/controls'
import { Menu, useAction } from '@/ui/kit'
import { Icon } from '@/ui/icons'
import { dj, qk, type DjInstance } from './api'
import { useDeck, useTransport } from './deckState'
import { NowPlaying, Progress, StateBadge, TransportRow } from './DeckParts'

/** One AutoDJ on the overview: live state, transport and what plays next. The full workspace is the Studio. */
export function InstanceCard({ inst, canAdmin, onEdit, onDelete }: {
  inst: DjInstance; canAdmin: boolean; onEdit: () => void; onDelete: () => void
}) {
  const qc = useQueryClient()
  const deck = useDeck(inst)!
  const { send, isBusy } = useTransport(inst.mount)
  const { run, isBusy: busy } = useAction()
  const toggle = () => run('toggle', async () => {
    await dj.toggle(inst.mount)
    qc.invalidateQueries({ queryKey: qk.instances })
  }, inst.enabled ? `${inst.mount} stopped` : `${inst.mount} started`)
  const studio = `/admin/studio?mount=${encodeURIComponent(inst.mount)}`
  const next = deck.queue.slice(0, 3)

  return (
    <Panel
      legend="AutoDJ" title={inst.mount} lamp={!inst.enabled ? 'off' : deck.state === 'playing' ? 'live' : deck.state === 'paused' ? 'warn' : 'off'}
      actions={<>
        {canAdmin && <Switch label={`${inst.enabled ? 'Disable' : 'Enable'} ${inst.mount}`} checked={inst.enabled} onCheckedChange={toggle} disabled={busy('toggle')} />}
        {canAdmin && <Menu label={`Actions for ${inst.mount}`} actions={[
          { label: 'Edit settings', icon: 'edit', onSelect: onEdit },
          'sep',
          { label: 'Delete', icon: 'trash', danger: true, onSelect: onDelete },
        ]} />}
      </>}
    >
      <div className="flex flex-wrap items-center gap-1.5">
        <StateBadge state={inst.enabled ? deck.state : 'stopped'} />
        {!inst.enabled && <Badge tone="warn">Disabled</Badge>}
        <Badge>{(inst.format || 'mp3').toUpperCase()} {inst.bitrate} kbps</Badge>
        {!inst.visible && <span title="Not listed publicly; still streamable by URL"><Badge>Unlisted</Badge></span>}
        {inst.song_command && <Badge tone="info">Song command</Badge>}
        {inst.mpd_enabled && inst.mpd_port && <Badge tone="info">MPD {inst.mpd_port}</Badge>}
        <span className="ml-auto text-xs text-muted"><span className="num font-mono text-ink">{deck.playlistLen}</span> tracks</span>
      </div>

      <div className="mt-4"><NowPlaying deck={deck} /></div>
      <div className="mt-4"><Progress deck={deck} /></div>

      {!inst.enabled && <p className="mt-3 text-xs text-muted">Disabled. Turn the switch on to start playout; the playlist is kept.</p>}
      {inst.enabled && deck.state === 'stopped' && deck.playlistLen === 0 && !inst.song_command && (
        <p className="mt-3 text-xs text-muted">No tracks. {inst.music_dir ? <>The music directory <span className="font-mono">{inst.music_dir}</span> had nothing playable, or the playlist was cleared. </> : null}Add tracks in the Studio.</p>
      )}

      <div className="mt-4 flex flex-wrap items-center justify-between gap-3">
        <TransportRow deck={deck} send={send} isBusy={isBusy} disabled={!inst.enabled} />
        <Link href={studio} className="inline-flex h-8 items-center gap-1.5 rounded-md border border-line-strong bg-raised px-3 text-[13px] font-medium text-ink hover:bg-hover">
          <Icon name="studio" size={14} />Studio
        </Link>
      </div>

      {next.length > 0 && (
        <div className="mt-4 flex items-center gap-3 border-t border-line pt-3">
          <Legend className="shrink-0">Up next</Legend>
          <ol className="flex min-w-0 flex-1 items-center gap-2 overflow-hidden text-xs text-muted">
            {next.map((t, i) => (
              <li key={i} className="flex min-w-0 items-center gap-2">
                {i > 0 && <Icon name="chevron-right" size={12} className="shrink-0 text-faint" />}
                <span className="truncate" title={t.title}>{t.title || t.path}</span>
              </li>
            ))}
          </ol>
          {deck.queue.length > next.length && <span className="shrink-0 font-mono text-[11px] text-faint">+{deck.queue.length - next.length}</span>}
        </div>
      )}
    </Panel>
  )
}
