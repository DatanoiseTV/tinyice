import { useEffect, useMemo, useState, type DragEvent, type KeyboardEvent, type MouseEvent } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Button } from '@/ui/controls'
import { EmptyState, Skeleton } from '@/ui/display'
import { Legend, Panel } from '@/ui/console'
import { Checkbox, SearchInput, useAction, useConfirm } from '@/ui/kit'
import { Icon } from '@/ui/icons'
import { cn } from '@/lib/cn'
import type { FileInfo } from '@/types'
import { dj, qk } from './api'
import { FILES_MIME } from './dnd'
import { useVirtual } from './useVirtual'

const ROW = 34

/** Library browser for one AutoDJ's music directory (GET /api/autodj/{mount}/files). */
export function Library({ mount, canEdit, onAdd, adding, className }: {
  mount: string
  canEdit: boolean
  /** Append paths (files or whole folders) to the playlist. */
  onAdd: (paths: string[]) => Promise<void> | void
  adding?: boolean
  className?: string
}) {
  const qc = useQueryClient()
  const { run, isBusy } = useAction()
  const { ask, dialog } = useConfirm()
  const [path, setPath] = useState('')
  const [search, setSearch] = useState('')
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [anchor, setAnchor] = useState(-1)
  const q = useQuery({ queryKey: qk.files(mount, path), queryFn: () => dj.files(mount, path), placeholderData: (p) => p })
  useEffect(() => { setPath(''); setSearch(''); setSelected(new Set()); setAnchor(-1) }, [mount])
  useEffect(() => { setSelected(new Set()); setAnchor(-1) }, [path])

  const needle = search.trim().toLowerCase()
  const files = useMemo(() => {
    const all = q.data ?? []
    const hit = needle ? all.filter((f) => f.name.toLowerCase().includes(needle) || f.title.toLowerCase().includes(needle)) : all
    // Folders first, then playlists, then tracks; the server lists playlists at the root ahead of everything.
    const rank = (f: FileInfo) => (f.is_dir ? 0 : f.is_pls ? 1 : 2)
    return hit.map((f, i) => ({ f, i })).sort((a, b) => rank(a.f) - rank(b.f) || a.i - b.i).map((x) => x.f)
  }, [q.data, needle])
  const v = useVirtual(files.length, ROW)
  const tracks = files.filter((f) => !f.is_dir && !f.is_pls)
  const crumbs = useMemo(() => {
    const parts = path.split('/').filter(Boolean)
    return [{ name: 'Library', path: '' }, ...parts.map((p, i) => ({ name: p, path: parts.slice(0, i + 1).join('/') }))]
  }, [path])

  const click = (e: MouseEvent, i: number) => {
    const f = files[i]
    if (f.is_dir) { setPath(f.path); setSearch(''); return }
    if (f.is_pls) return
    if (e.shiftKey && anchor >= 0) {
      const [a, b] = anchor < i ? [anchor, i] : [i, anchor]
      setSelected(new Set(files.slice(a, b + 1).filter((x) => !x.is_dir && !x.is_pls).map((x) => x.path)))
    } else if (e.metaKey || e.ctrlKey) {
      setSelected((s) => { const n = new Set(s); if (n.has(f.path)) n.delete(f.path); else n.add(f.path); return n })
      setAnchor(i)
    } else { setSelected(new Set([f.path])); setAnchor(i) }
  }
  const onKey = (e: KeyboardEvent) => {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'a') { e.preventDefault(); setSelected(new Set(tracks.map((t) => t.path))) }
    else if (e.key === 'Escape') setSelected(new Set())
    else if (e.key === 'Backspace' && path && !e.metaKey) { e.preventDefault(); setPath(path.split('/').slice(0, -1).join('/')) }
    else if (e.key === 'Enter' && canEdit && selected.size) { e.preventDefault(); void Promise.resolve(onAdd([...selected])).then(() => setSelected(new Set())) }
  }
  const drag = (e: DragEvent, f: FileInfo) => {
    const paths = selected.has(f.path) ? [...selected] : [f.path]
    e.dataTransfer.setData(FILES_MIME, JSON.stringify(paths))
    e.dataTransfer.setData('text/plain', paths.length === 1 ? f.title : `${paths.length} files`)
    e.dataTransfer.effectAllowed = 'copy'
  }
  const queue = (paths: string[], front: boolean) => run('queue', async () => {
    // The queue endpoint validates against the music directory with filepath.Abs, so a relative path would
    // resolve against the server's working directory and be refused: send the absolute path the listing carries.
    const abs = new Map((q.data ?? []).map((f) => [f.path, f.abs_path]))
    const list = paths.map((p) => abs.get(p) ?? p)
    // Front insertion reverses, so push in reverse to keep the first selected track playing first.
    for (const p of front ? [...list].reverse() : list) await dj.enqueuePath(mount, p, front)
    qc.invalidateQueries({ queryKey: qk.queue(mount) })
  }, front ? 'Queued to play next' : 'Added to the queue')

  const loadPls = (f: FileInfo) => ask({
    title: `Load ${f.name}?`, confirm: 'Load',
    body: 'This replaces the AutoDJ playlist with the contents of the file. Unsaved changes to the current playlist are lost.',
    run: async () => { await run('load', () => dj.load(mount, f.name), `Loaded ${f.name}`); qc.invalidateQueries({ queryKey: qk.playlist(mount) }); qc.invalidateQueries({ queryKey: qk.instances }) },
  })

  const rows = files.slice(v.start, v.end)
  return (
    <Panel legend="Library" title={path ? path : 'Music directory'} flush className={className} lamp={q.isError ? 'danger' : undefined}>
      <div className="flex h-full min-h-0 flex-col">
        <div className="space-y-2 border-b border-line px-3 py-2">
          <SearchInput value={search} onChange={setSearch} placeholder="Filter this folder" />
          <nav aria-label="Folder" className="flex items-center gap-1 overflow-x-auto whitespace-nowrap font-mono text-[11px] text-faint">
            {crumbs.map((c, i) => (
              <span key={c.path || 'root'} className="flex items-center gap-1">
                {i > 0 && <span aria-hidden>/</span>}
                <button className={cn('rounded px-1 py-0.5 hover:bg-hover hover:text-ink', i === crumbs.length - 1 && 'text-ink')} aria-current={i === crumbs.length - 1 ? 'location' : undefined} onClick={() => { setPath(c.path); setSearch('') }}>{c.name}</button>
              </span>
            ))}
          </nav>
        </div>

        {q.isLoading ? (
          <div className="space-y-1.5 p-3">{Array.from({ length: 8 }, (_, i) => <Skeleton key={i} className="h-7 w-full" />)}</div>
        ) : q.isError ? (
          <EmptyState icon={<Icon name="alert" size={20} />} title="Could not read the folder" body={(q.error as Error).message} action={<Button size="sm" onClick={() => q.refetch()}>Retry</Button>} />
        ) : files.length === 0 ? (
          <EmptyState icon={<Icon name="folder" size={20} />} title={needle ? 'No file matches' : 'This folder has no audio'} body={needle ? `Nothing here contains "${search}".` : 'Only folders and playable audio files are listed.'} action={needle ? <Button size="sm" onClick={() => setSearch('')}>Clear filter</Button> : path ? <Button size="sm" onClick={() => setPath(path.split('/').slice(0, -1).join('/'))}>Up one level</Button> : undefined} />
        ) : (
          <div ref={v.ref} onScroll={v.onScroll} onKeyDown={onKey} tabIndex={0} role="listbox" aria-multiselectable aria-label="Library files" className="min-h-0 flex-1 overflow-y-auto outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-accent/60">
            <div style={{ height: v.padTop }} aria-hidden />
            {rows.map((f, k) => {
              const i = v.start + k
              const sel = selected.has(f.path)
              return (
                <div
                  key={f.path + (f.is_pls ? ':pls' : '')} role="option" aria-selected={sel} style={{ height: ROW }}
                  draggable={canEdit && !f.is_pls} onDragStart={(e) => drag(e, f)}
                  onClick={(e) => click(e, i)} onDoubleClick={() => canEdit && !f.is_dir && !f.is_pls && void onAdd([f.path])}
                  className={cn('group flex select-none items-center gap-2.5 border-b border-line/60 pl-2 pr-1 text-[13px]', sel ? 'bg-accent/10' : 'hover:bg-hover/60', f.is_dir && 'cursor-pointer')}
                >
                  <Icon name={f.is_dir ? 'folder' : f.is_pls ? 'list' : 'music'} size={15} className={cn('shrink-0', f.is_dir ? 'text-accent' : 'text-faint')} />
                  <span className="min-w-0 flex-1 truncate text-ink" title={f.is_dir ? f.path : `${f.name}\n${f.abs_path}`}>{f.is_dir ? f.name : f.is_pls ? f.name : f.title || f.name}</span>
                  {f.is_pls && <span className="shrink-0 font-mono text-[10px] uppercase tracking-wider text-faint">pls</span>}
                  <span className="flex shrink-0 items-center opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100 [@media(hover:none)]:opacity-100" onClick={(e) => e.stopPropagation()}>
                    {f.is_pls ? (
                      canEdit && <Button size="sm" variant="ghost" onClick={() => loadPls(f)} loading={isBusy('load')}>Load</Button>
                    ) : (
                      <>
                        {!f.is_dir && canEdit && <button className="flex size-7 items-center justify-center rounded-md text-faint hover:bg-hover hover:text-ink" aria-label={`Play ${f.title} next`} title="Play next" onClick={() => queue([f.path], true)}><Icon name="queue-next" size={14} /></button>}
                        {canEdit && <button className="flex size-7 items-center justify-center rounded-md text-faint hover:bg-hover hover:text-ink" aria-label={f.is_dir ? `Add the folder ${f.name} to the playlist` : `Add ${f.title} to the playlist`} title={f.is_dir ? 'Add the whole folder' : 'Add to playlist'} onClick={() => void onAdd([f.path])}><Icon name="plus" size={14} /></button>}
                      </>
                    )}
                  </span>
                </div>
              )
            })}
            <div style={{ height: v.padBottom }} aria-hidden />
          </div>
        )}

        <div className="flex min-h-11 shrink-0 flex-wrap items-center gap-2 border-t border-line px-3 py-2">
          {selected.size > 0 ? (
            <>
              <Checkbox checked onChange={() => setSelected(new Set())} label="Clear selection" />
              <Legend className="text-ink">{selected.size} selected</Legend>
              {canEdit && (
                <span className="ml-auto flex gap-1.5">
                  <Button size="sm" variant="ghost" onClick={async () => { await queue([...selected], true); setSelected(new Set()) }} loading={isBusy('queue')}><Icon name="queue-next" size={14} />Next</Button>
                  <Button size="sm" variant="primary" onClick={async () => { await onAdd([...selected]); setSelected(new Set()) }} loading={adding}><Icon name="plus" size={14} />Add</Button>
                </span>
              )}
            </>
          ) : (
            <>
              <span className="text-[11px] text-faint">{tracks.length} track{tracks.length === 1 ? '' : 's'}{needle ? ' match' : ' here'}</span>
              {canEdit && <Button size="sm" className="ml-auto" disabled={!tracks.length} onClick={() => void onAdd(tracks.map((t) => t.path))}><Icon name="plus" size={14} />Add all</Button>}
            </>
          )}
        </div>
      </div>
      {dialog}
    </Panel>
  )
}
