import { useCallback, useEffect, useMemo, useRef, useState, type DragEvent, type KeyboardEvent, type MouseEvent } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Button } from '@/ui/controls'
import { EmptyState, Skeleton } from '@/ui/display'
import { Legend, Panel, Tally } from '@/ui/console'
import { Checkbox, Menu, SearchInput, useAction, useConfirm } from '@/ui/kit'
import { Icon } from '@/ui/icons'
import { toast } from '@/lib/toast'
import { cn } from '@/lib/cn'
import type { PlaylistItem } from '@/types'
import { dj, qk } from './api'
import type { Deck } from './deckState'
import { FILES_MIME, ROWS_MIME, planMoves } from './dnd'
import { useAddPaths } from './ops'
import { useVirtual } from './useVirtual'

const ROW = 34

/** Folder the track lives in, shown faint at the end of the row: enough to tell two "Intro" files apart. */
function folderOf(path: string, root: string): string {
  const rel = root && path.startsWith(root + '/') ? path.slice(root.length + 1) : path
  const parts = rel.split('/').filter(Boolean)
  return parts.length > 1 ? parts.slice(0, -1).join('/') : ''
}

export function PlaylistPanel({ mount, deck, canEdit, musicDir = '', className }: { mount: string; deck: Deck | undefined; canEdit: boolean; musicDir?: string; className?: string }) {
  const qc = useQueryClient()
  const { run, isBusy } = useAction()
  const { ask, dialog } = useConfirm()
  const q = useQuery({ queryKey: qk.playlist(mount), queryFn: () => dj.playlist(mount) })
  const items = q.data ?? []
  const [search, setSearch] = useState('')
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [anchor, setAnchor] = useState(-1)
  const [active, setActive] = useState(-1)
  const [dropAt, setDropAt] = useState<number | null>(null)
  const [focused, setFocused] = useState(false)
  const { add: addPaths } = useAddPaths(mount)
  const dragIds = useRef<number[]>([])
  const busyMoving = useRef(false)

  // Any playlist edit, from this tab or another, bumps playlist_version on the event
  // feed. The feed no longer carries the playlist itself, so a bump means refetch.
  const lastVersion = useRef<number | undefined>(undefined)
  useEffect(() => { lastVersion.current = undefined; setSelected(new Set()); setSearch(''); setAnchor(-1); setActive(-1) }, [mount])
  const version = deck?.playlistVersion
  useEffect(() => {
    if (version === undefined) return
    if (lastVersion.current !== undefined && lastVersion.current !== version && !busyMoving.current) qc.invalidateQueries({ queryKey: qk.playlist(mount) })
    lastVersion.current = version
  }, [version, mount, qc])

  // Ids are stable across reorders, so a selection survives them; drop ids that left the playlist.
  useEffect(() => {
    if (!q.data) return
    const ids = new Set(q.data.map((i) => i.id))
    setSelected((s) => { const n = new Set([...s].filter((id) => ids.has(id))); return n.size === s.size ? s : n })
  }, [q.data])

  const needle = search.trim().toLowerCase()
  const view = useMemo(() => {
    const rows = items.map((item, index) => ({ item, index }))
    return needle ? rows.filter((r) => r.item.title.toLowerCase().includes(needle) || r.item.path.toLowerCase().includes(needle)) : rows
  }, [items, needle])
  const reorderable = canEdit && !needle
  const v = useVirtual(view.length, ROW)
  const curIdx = deck ? items.findIndex((i) => i.id === deck.currentId) : -1
  const playing = deck?.state === 'playing'

  const refresh = useCallback(() => qc.invalidateQueries({ queryKey: qk.playlist(mount) }), [qc, mount])

  /* ---------- selection ---------- */
  const click = (e: MouseEvent, vi: number) => {
    const id = view[vi].item.id
    setActive(vi)
    if (e.shiftKey && anchor >= 0) {
      const [a, b] = anchor < vi ? [anchor, vi] : [vi, anchor]
      setSelected(new Set(view.slice(a, b + 1).map((r) => r.item.id)))
    } else if (e.metaKey || e.ctrlKey) {
      setSelected((s) => { const n = new Set(s); if (n.has(id)) n.delete(id); else n.add(id); return n })
      setAnchor(vi)
    } else {
      setSelected(new Set([id])); setAnchor(vi)
    }
  }
  const selectedItems = useMemo(() => items.filter((i) => selected.has(i.id)), [items, selected])
  const allSelected = view.length > 0 && view.every((r) => selected.has(r.item.id))

  /* ---------- mutations ---------- */
  const reorder = async (ids: number[], insertAt: number) => {
    const order = items.map((i) => i.id)
    const { moves, result } = planMoves(order, ids, insertAt)
    if (!moves.length) return
    busyMoving.current = true
    const byId = new Map(items.map((i) => [i.id, i]))
    qc.setQueryData<PlaylistItem[]>(qk.playlist(mount), result.map((id) => byId.get(id)!))
    try { for (const [f, t] of moves) await dj.move(mount, f, t) }
    catch (e) { toast.error((e as Error).message || 'Reorder failed') }
    finally { busyMoving.current = false; refresh() }
  }

  const removeIds = (ids: number[]) => run('remove', async () => {
    const gone = new Set(ids)
    qc.setQueryData<PlaylistItem[]>(qk.playlist(mount), (d) => d?.filter((i) => !gone.has(i.id)))
    busyMoving.current = true
    try { for (const id of ids) await dj.remove(mount, id) } finally { busyMoving.current = false; refresh() }
  })
  const removeSelected = () => {
    const ids = selectedItems.map((i) => i.id)
    if (ids.length <= 1) { void removeIds(ids); return }
    ask({ title: `Remove ${ids.length} tracks?`, body: 'They leave this playlist. The files stay in the library.', confirm: 'Remove', run: async () => { await removeIds(ids); setSelected(new Set()) } })
  }
  /** Front of the queue reverses: pushing in reverse order makes the first selected track play first. */
  const playNext = (list: PlaylistItem[]) => run('queue', async () => {
    for (const it of [...list].reverse()) await dj.playNextId(mount, it.id)
    qc.invalidateQueries({ queryKey: qk.queue(mount) })
  }, list.length === 1 ? 'Queued to play next' : `${list.length} tracks queued to play next`)
  const enqueue = (list: PlaylistItem[]) => run('queue', async () => {
    for (const it of list) await dj.enqueueId(mount, it.id, false)
    qc.invalidateQueries({ queryKey: qk.queue(mount) })
  }, list.length === 1 ? 'Added to the queue' : `${list.length} tracks added to the queue`)

  /* ---------- keyboard ---------- */
  const onKey = (e: KeyboardEvent) => {
    if (!view.length) return
    const go = (to: number, extend: boolean) => {
      const i = Math.max(0, Math.min(view.length - 1, to))
      setActive(i); v.reveal(i)
      if (extend) { const a = anchor >= 0 ? anchor : active; const [lo, hi] = a < i ? [a, i] : [i, a]; setSelected(new Set(view.slice(lo, hi + 1).map((r) => r.item.id))); if (anchor < 0) setAnchor(a) }
      else { setSelected(new Set([view[i].item.id])); setAnchor(i) }
    }
    if (e.altKey) {
      // Alt+Arrow moves the selection one place, the keyboard equivalent of dragging.
      if (reorderable && (e.key === 'ArrowUp' || e.key === 'ArrowDown') && selected.size) {
        e.preventDefault()
        const idx = items.map((i, k) => (selected.has(i.id) ? k : -1)).filter((k) => k >= 0)
        const at = e.key === 'ArrowUp' ? idx[0] - 1 : idx[idx.length - 1] + 2
        if (at >= 0 && at <= items.length) void reorder([...selected], at)
      }
      return
    }
    if (e.key === 'ArrowDown') { e.preventDefault(); go(active + 1, e.shiftKey) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); go(active < 0 ? 0 : active - 1, e.shiftKey) }
    else if (e.key === 'Home') { e.preventDefault(); go(0, e.shiftKey) }
    else if (e.key === 'End') { e.preventDefault(); go(view.length - 1, e.shiftKey) }
    else if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'a') { e.preventDefault(); setSelected(new Set(view.map((r) => r.item.id))) }
    else if (e.key === 'Escape') setSelected(new Set())
    else if ((e.key === 'Delete' || e.key === 'Backspace') && canEdit && selected.size) { e.preventDefault(); removeSelected() }
    else if (e.key === 'Enter' && selected.size) { e.preventDefault(); void playNext(selectedItems) }
  }

  /* ---------- drag and drop ---------- */
  const accepts = (e: DragEvent) => canEdit && (e.dataTransfer.types.includes(FILES_MIME) || (reorderable && e.dataTransfer.types.includes(ROWS_MIME)))
  const overRow = (e: DragEvent, vi: number) => {
    if (!accepts(e)) return
    e.preventDefault()
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
    const idx = view[vi].index + (e.clientY > r.top + r.height / 2 ? 1 : 0)
    setDropAt(needle && e.dataTransfer.types.includes(FILES_MIME) ? null : idx)
    e.dataTransfer.dropEffect = e.dataTransfer.types.includes(ROWS_MIME) ? 'move' : 'copy'
  }
  const drop = (e: DragEvent) => {
    const at = dropAt
    setDropAt(null)
    if (!accepts(e)) return
    e.preventDefault()
    const files = e.dataTransfer.getData(FILES_MIME)
    if (files) { try { void addPaths(JSON.parse(files) as string[], at ?? null) } catch { /* foreign payload */ } return }
    if (at !== null && dragIds.current.length) void reorder(dragIds.current, at)
  }
  const startDrag = (e: DragEvent, vi: number) => {
    const it = view[vi].item
    const ids = selected.has(it.id) ? items.filter((i) => selected.has(i.id)).map((i) => i.id) : [it.id]
    if (!selected.has(it.id)) { setSelected(new Set([it.id])); setAnchor(vi) }
    dragIds.current = ids
    e.dataTransfer.setData(ROWS_MIME, JSON.stringify(ids))
    e.dataTransfer.setData('text/plain', ids.length === 1 ? it.title : `${ids.length} tracks`)
    e.dataTransfer.effectAllowed = 'move'
  }

  /* ---------- render ---------- */
  const rows = view.slice(v.start, v.end)
  const dropLine = (gap: number) => dropAt === gap && <span className="pointer-events-none absolute inset-x-0 top-0 z-10 h-0.5 bg-accent" aria-hidden />

  return (
    <Panel
      legend="Playlist" title={`${items.length} track${items.length === 1 ? '' : 's'}`} flush className={className}
      lamp={q.isError ? 'danger' : undefined}
      actions={<>
        {curIdx >= 0 && <Button variant="ghost" size="sm" onClick={() => { setSearch(''); requestAnimationFrame(() => v.reveal(curIdx, true)) }} aria-label="Scroll to the playing track" title="Scroll to the playing track"><Icon name="locate" size={14} /></Button>}
        {canEdit && <Button variant="ghost" size="sm" onClick={() => run('save', () => dj.save(mount), 'Playlist saved')} loading={isBusy('save')} disabled={!items.length}><Icon name="save" size={14} /><span className="max-sm:hidden">Save</span></Button>}
        {canEdit && <Button variant="ghost" size="sm" disabled={!items.length} onClick={() => ask({ title: 'Clear the playlist?', body: 'Every track is removed from this AutoDJ. The files stay in the library, and a saved playlist file is not touched.', confirm: 'Clear', run: async () => { await run('clear', () => dj.clear(mount), 'Playlist cleared'); refresh() } })}><Icon name="trash" size={14} /><span className="max-sm:hidden">Clear</span></Button>}
      </>}
    >
      <div className="flex h-full min-h-0 flex-col">
        <div className="flex items-center gap-3 border-b border-line px-3 py-2">
          <Checkbox checked={allSelected} onChange={(c) => setSelected(c ? new Set(view.map((r) => r.item.id)) : new Set())} label="Select every track shown" disabled={!view.length} />
          <SearchInput value={search} onChange={setSearch} placeholder="Filter playlist" className="min-w-0 flex-1" />
        </div>
        {selected.size > 0 && (
          <div className="flex h-10 shrink-0 items-center gap-2 border-b border-line bg-hover/50 px-3" role="toolbar" aria-label="Selected tracks">
            <Legend className="whitespace-nowrap text-ink">{selected.size} selected</Legend>
            <div className="ml-auto flex items-center gap-1">
              <Button size="sm" variant="ghost" onClick={() => playNext(selectedItems)} loading={isBusy('queue')}><Icon name="queue-next" size={14} />Play next</Button>
              <Button size="sm" variant="ghost" onClick={() => enqueue(selectedItems)} loading={isBusy('queue')} aria-label="Add the selected tracks to the end of the queue" title="Add to the end of the queue"><Icon name="plus" size={14} /></Button>
              {canEdit && <Button size="sm" variant="danger" onClick={removeSelected} loading={isBusy('remove')}><Icon name="trash" size={14} />Remove</Button>}
              <button className="flex size-7 items-center justify-center rounded-md text-faint hover:bg-hover hover:text-ink" onClick={() => setSelected(new Set())} aria-label="Clear selection"><Icon name="x" size={14} /></button>
            </div>
          </div>
        )}

        {q.isLoading ? (
          <div className="space-y-1.5 p-3">{Array.from({ length: 8 }, (_, i) => <Skeleton key={i} className="h-7 w-full" />)}</div>
        ) : q.isError ? (
          <EmptyState icon={<Icon name="alert" size={20} />} title="Could not load the playlist" body={(q.error as Error).message} action={<Button size="sm" onClick={() => q.refetch()}>Retry</Button>} />
        ) : items.length === 0 ? (
          <div onDragOver={(e) => { if (accepts(e)) { e.preventDefault(); setDropAt(0) } }} onDragLeave={() => setDropAt(null)} onDrop={drop} className={cn('flex-1', dropAt === 0 && 'bg-accent/5')}>
            <EmptyState icon={<Icon name="list" size={20} />} title="The playlist is empty" body="Drag tracks or folders in from the library, or use the add buttons there. A scan of the music directory fills it automatically when an AutoDJ is created." />
          </div>
        ) : view.length === 0 ? (
          <EmptyState icon={<Icon name="search" size={20} />} title="No track matches" body={`Nothing in the playlist contains "${search}".`} action={<Button size="sm" onClick={() => setSearch('')}>Clear filter</Button>} />
        ) : (
          <div
            ref={v.ref} onScroll={v.onScroll} onKeyDown={onKey} onFocus={() => setFocused(true)} onBlur={() => setFocused(false)} tabIndex={0} role="listbox" aria-multiselectable aria-label="Playlist"
            aria-activedescendant={active >= 0 && view[active] ? `pl-${mount}-${view[active].item.id}` : undefined}
            onDragOver={(e) => { if (accepts(e) && e.target === e.currentTarget) { e.preventDefault(); setDropAt(items.length) } }}
            onDragLeave={(e) => { if (!e.currentTarget.contains(e.relatedTarget as Node)) setDropAt(null) }}
            onDrop={drop} onDragEnd={() => setDropAt(null)}
            className="min-h-0 flex-1 overflow-y-auto outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-accent/60"
          >
            <div style={{ height: v.padTop }} aria-hidden />
            {rows.map(({ item, index }, k) => {
              const vi = v.start + k
              const isCur = item.id === deck?.currentId && item.id >= 0
              const sel = selected.has(item.id)
              return (
                <div
                  key={item.id} id={`pl-${mount}-${item.id}`} role="option" aria-selected={sel} style={{ height: ROW }}
                  draggable={reorderable} onDragStart={(e) => startDrag(e, vi)} onDragOver={(e) => overRow(e, vi)}
                  onClick={(e) => click(e, vi)}
                  onDoubleClick={() => playNext([item])}
                  className={cn(
                    'group relative flex select-none items-center gap-2.5 border-b border-line/60 pl-2 pr-1 text-[13px]',
                    sel ? 'bg-accent/10' : 'hover:bg-hover/60', focused && active === vi && 'outline outline-1 -outline-offset-1 outline-accent/60',
                  )}
                >
                  {dropLine(index)}
                  {index === items.length - 1 && dropAt === items.length && <span className="pointer-events-none absolute inset-x-0 bottom-0 z-10 h-0.5 bg-accent" aria-hidden />}
                  {isCur && <span className="absolute inset-y-0 left-0 w-0.5 bg-accent" aria-hidden />}
                  <span className={cn('flex w-4 shrink-0 justify-center text-faint', reorderable ? 'cursor-grab' : 'opacity-0')} aria-hidden><Icon name="grip" size={14} /></span>
                  <span className="flex w-9 shrink-0 items-center justify-end gap-1.5 font-mono text-[11px] text-faint">
                    {isCur ? (playing ? <Tally on /> : <Icon name="pause" size={12} className="text-warn" />) : <span className="num">{index + 1}</span>}
                  </span>
                  <span className={cn('min-w-0 flex-1 truncate', isCur ? 'font-medium text-ink' : 'text-ink')} title={item.path}>{item.title || item.path}</span>
                  <span className="hidden max-w-[28%] shrink-0 truncate font-mono text-[11px] text-faint sm:block">{folderOf(item.path, musicDir)}</span>
                  <span className="flex shrink-0 items-center opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100 [@media(hover:none)]:opacity-100" onClick={(e) => e.stopPropagation()}>
                    <button className="flex size-7 items-center justify-center rounded-md text-faint hover:bg-hover hover:text-ink" aria-label={`Play ${item.title} next`} title="Play next" onClick={() => playNext([item])}><Icon name="queue-next" size={14} /></button>
                    <Menu label={`Actions for ${item.title}`} actions={[
                      { label: 'Play next', icon: 'queue-next', onSelect: () => playNext([item]) },
                      { label: 'Add to queue', icon: 'plus', onSelect: () => enqueue([item]) },
                      ...(reorderable ? ['sep' as const,
                        { label: 'Move to top', icon: 'arrow-up', onSelect: () => reorder([item.id], 0), disabled: index === 0 },
                        { label: 'Move to bottom', icon: 'arrow-down', onSelect: () => reorder([item.id], items.length), disabled: index === items.length - 1 }] : []),
                      ...(canEdit ? ['sep' as const, { label: 'Remove', icon: 'trash', danger: true, onSelect: () => removeIds([item.id]) }] : []),
                    ]} />
                  </span>
                </div>
              )
            })}
            <div style={{ height: v.padBottom }} aria-hidden />
          </div>
        )}
        {items.length > 0 && (
          <div className="flex h-8 shrink-0 items-center justify-between border-t border-line px-3 text-[11px] text-faint">
            <span className="truncate">{needle ? `${view.length} of ${items.length} shown` : reorderable ? 'Drag to reorder, Alt + arrows move the selection' : ''}</span>
          </div>
        )}
      </div>
      {dialog}
    </Panel>
  )
}
