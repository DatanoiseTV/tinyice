import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Button } from '@/ui/controls'
import { Panel } from '@/ui/console'
import { useAction, useConfirm } from '@/ui/kit'
import { Icon } from '@/ui/icons'
import { dj, qk } from './api'
import type { Deck } from './deckState'

/**
 * Tracks that jump the playlist. They play before the playlist resumes, front
 * first. The event feed carries the queue on every frame, so no polling here;
 * the REST read only runs until the first frame arrives.
 */
export function QueuePanel({ mount, deck, canEdit, className }: { mount: string; deck: Deck | undefined; canEdit: boolean; className?: string }) {
  const qc = useQueryClient()
  const { run, isBusy } = useAction()
  const { ask, dialog } = useConfirm()
  const q = useQuery({ queryKey: qk.queue(mount), queryFn: () => dj.queue(mount), enabled: !deck?.live })
  const items = deck?.live ? deck.queue : q.data ?? deck?.queue ?? []

  return (
    <Panel
      legend="Queue" title={items.length ? `${items.length} up next` : 'Empty'} flush className={className} lamp={items.length ? 'live' : 'off'}
      actions={canEdit && <Button variant="ghost" size="sm" disabled={!items.length} loading={isBusy('clearq')} onClick={() => ask({
        title: 'Clear the queue?', body: 'Queued tracks are dropped. The playlist is not touched.', confirm: 'Clear',
        run: async () => { await run('clearq', () => dj.clearQueue(mount), 'Queue cleared'); qc.invalidateQueries({ queryKey: qk.queue(mount) }) },
      })}><Icon name="trash" size={14} />Clear</Button>}
    >
      {items.length === 0 ? (
        <p className="px-4 py-4 text-[13px] text-muted">Nothing queued. Use Play next on a playlist or library track; queued tracks play before the playlist continues.</p>
      ) : (
        <ol className="max-h-64 divide-y divide-line/60 overflow-y-auto" aria-label="Queued tracks">
          {items.map((it, i) => (
            <li key={`${i}:${it.path}`} className="flex h-9 items-center gap-3 px-4 text-[13px]">
              <span className="num w-5 shrink-0 text-right font-mono text-[11px] text-faint">{i + 1}</span>
              <span className="min-w-0 flex-1 truncate text-ink" title={it.path}>{it.title || it.path}</span>
              {i === 0 && <span className="shrink-0 font-mono text-[10px] uppercase tracking-wider text-accent">Next</span>}
            </li>
          ))}
        </ol>
      )}
      {dialog}
    </Panel>
  )
}
