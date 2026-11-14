import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Button, Dialog } from '@/ui/controls'
import { EmptyState, Skeleton } from '@/ui/display'
import { Legend } from '@/ui/console'
import { Icon } from '@/ui/icons'
import { dj } from './api'

/** A directory with no tracks of its own is not "empty" if it has subfolders: that is what a library's parent looks like. */
function contents(tracks: number, dirs: number): string {
  if (tracks < 0) return ''
  if (tracks > 0) return `${tracks} track${tracks === 1 ? '' : 's'}`
  if (dirs > 0) return `${dirs} folder${dirs === 1 ? '' : 's'}`
  return 'empty'
}

/**
 * Picks a music directory without typing a path. It can only show what
 * /api/autodj/browse returns, which is confined to the configured media roots:
 * "browse" never means "browse the filesystem".
 */
export function DirectoryBrowser({ open, onOpenChange, initialPath, onPick }: {
  open: boolean; onOpenChange: (o: boolean) => void; initialPath?: string; onPick: (path: string) => void
}) {
  const [path, setPath] = useState('')
  useEffect(() => { if (open) setPath(initialPath ?? '') }, [open, initialPath])
  const q = useQuery({
    queryKey: ['dj', 'browse', path], queryFn: () => dj.browse(path), enabled: open, retry: false,
    // A stale or removed initial path is not worth an error: fall back to the roots.
  })
  useEffect(() => { if (q.isError && path) setPath('') }, [q.isError, path])
  const d = q.data
  const top = !d?.path

  return (
    <Dialog
      open={open} onOpenChange={onOpenChange} title="Choose music directory" width="max-w-lg"
      description={top ? 'Configured media roots' : d?.path}
      footer={<>
        <Button variant="ghost" onClick={() => onOpenChange(false)}>Cancel</Button>
        <Button variant="primary" disabled={top || !d} onClick={() => { if (d) { onPick(d.path); onOpenChange(false) } }}>Use this directory</Button>
      </>}
    >
      <div className="-mx-1 max-h-[50vh] min-h-48 overflow-y-auto">
        {q.isLoading ? (
          <div className="space-y-1.5 p-1">{Array.from({ length: 5 }, (_, i) => <Skeleton key={i} className="h-8 w-full" />)}</div>
        ) : q.isError ? (
          <EmptyState icon={<Icon name="alert" size={20} />} title="Cannot list media roots" body={(q.error as Error).message} action={<Button size="sm" onClick={() => q.refetch()}>Retry</Button>} />
        ) : d && (
          <ul className="space-y-0.5">
            {d.parent && (
              <li><button className="flex h-9 w-full items-center gap-2.5 rounded-md px-2 text-left text-[13px] text-muted hover:bg-hover hover:text-ink" onClick={() => setPath(d.parent)}><Icon name="chevron-left" size={14} />Up one level</button></li>
            )}
            {!top && d.parent === '' && (
              <li><button className="flex h-9 w-full items-center gap-2.5 rounded-md px-2 text-left text-[13px] text-muted hover:bg-hover hover:text-ink" onClick={() => setPath('')}><Icon name="chevron-left" size={14} />All media roots</button></li>
            )}
            {d.entries.map((e) => (
              <li key={e.path}>
                <button className="flex h-9 w-full items-center gap-2.5 rounded-md px-2 text-left hover:bg-hover" onClick={() => setPath(e.path)}>
                  <Icon name="folder" size={15} className="shrink-0 text-accent" />
                  <span className="min-w-0 flex-1 truncate font-mono text-xs text-ink" title={e.path}>{e.name}</span>
                  <Legend className="shrink-0">{contents(e.track_count, e.dir_count)}</Legend>
                </button>
              </li>
            ))}
            {d.entries.length === 0 && (
              <li className="px-2 py-6 text-center text-xs text-muted">
                {top ? 'No media roots are configured and none could be derived. Set "media_roots" in the server configuration.' : 'No subfolders here. This directory can still be chosen.'}
              </li>
            )}
            {d.truncated && <li className="px-2 py-2 text-xs text-faint">Only the first entries are shown; this directory has too many subfolders to list.</li>}
          </ul>
        )}
      </div>
      {d && !top && <p className="mt-3 text-xs text-muted">{contents(d.track_count, d.dir_count)} directly in this folder</p>}
    </Dialog>
  )
}
