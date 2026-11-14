import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Link } from 'wouter'
import { page } from '@/lib/api'
import { toast } from '@/lib/toast'
import { Button } from '@/ui/controls'
import { EmptyState, PageHeader, Skeleton } from '@/ui/display'
import { useConfirm } from '@/ui/kit'
import { Icon } from '@/ui/icons'
import { dj, qk, useInstances, type DjInstance } from './autodj/api'
import { InstanceCard } from './autodj/InstanceCard'
import { InstanceForm } from './autodj/InstanceForm'
import './autodj/deckState' // registers the page's extra icons

export default function AutoDJPage() {
  const qc = useQueryClient()
  const q = useInstances()
  const { ask, dialog } = useConfirm()
  // Creating, editing and deleting register shell commands and directories, so the server restricts them to superadmin.
  const canAdmin = page().user?.role === 'superadmin'
  const [form, setForm] = useState<{ open: boolean; instance?: DjInstance }>({ open: false })
  const list = q.data ?? []

  const remove = (i: DjInstance) => ask({
    title: `Delete ${i.mount}?`,
    body: 'The AutoDJ, its playlist and its queue are removed and there is no undo. The music files on disk are not touched.',
    run: async () => {
      try { await dj.remove_instance(i.mount); toast.success(`Deleted ${i.mount}`); qc.invalidateQueries({ queryKey: qk.instances }) }
      catch (e) { toast.error((e as Error).message || `Could not delete ${i.mount}`) }
    },
  })

  return (
    <>
      <PageHeader
        title="AutoDJ" sub="Automated playout from a music directory. Open the Studio to edit a playlist."
        actions={canAdmin && <Button variant="primary" onClick={() => setForm({ open: true })}><Icon name="plus" size={14} />New AutoDJ</Button>}
      />

      {q.isError && (
        <div role="alert" className="mb-3 flex items-center gap-3 rounded-lg border border-danger/30 bg-danger/10 px-4 py-2.5 text-[13px] text-danger">
          <Icon name="alert" size={16} /><span className="flex-1">{(q.error as Error).message || 'Could not load AutoDJ instances'}{list.length > 0 ? ' Showing the last known state.' : ''}</span>
          <Button size="sm" onClick={() => q.refetch()}>Retry</Button>
        </div>
      )}

      {q.isLoading ? (
        <div className="grid gap-3 xl:grid-cols-2">{[0, 1].map((i) => <Skeleton key={i} className="h-72 w-full" />)}</div>
      ) : list.length === 0 && !q.isError ? (
        <div className="rounded-lg border border-line bg-surface">
          <EmptyState
            icon={<Icon name="autodj" size={20} />} title="No AutoDJ configured"
            body="An AutoDJ plays the audio files of a directory into a mount, with a playlist, a queue and optional external song control."
            action={canAdmin ? <Button variant="primary" onClick={() => setForm({ open: true })}><Icon name="plus" size={14} />New AutoDJ</Button> : <span className="text-xs text-muted">Ask a superadmin to create one.</span>}
          />
        </div>
      ) : (
        <div className="grid gap-3 xl:grid-cols-2 [&>*]:min-w-0">
          {list.map((i) => <InstanceCard key={i.mount} inst={i} canAdmin={canAdmin} onEdit={() => setForm({ open: true, instance: i })} onDelete={() => remove(i)} />)}
        </div>
      )}
      {list.length > 0 && <p className="mt-4 text-xs text-faint">Studio: <Link href={`/admin/studio?mount=${encodeURIComponent(list[0].mount)}`} className="text-muted hover:text-ink">open the library, queue and playlist editor</Link>. Space, N and P control the deck there.</p>}

      <InstanceForm open={form.open} onOpenChange={(o) => setForm((f) => ({ ...f, open: o }))} instance={form.instance} existingMounts={list.map((i) => i.mount)} />
      {dialog}
    </>
  )
}
