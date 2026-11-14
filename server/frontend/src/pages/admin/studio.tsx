import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link } from 'wouter'
import { page } from '@/lib/api'
import { Button, Select } from '@/ui/controls'
import { EmptyState, PageHeader, Skeleton } from '@/ui/display'
import { Icon } from '@/ui/icons'
import { Tally } from '@/ui/console'
import { useInstances } from './autodj/api'
import { useDeck, useDeckHotkeys, useTransport } from './autodj/deckState'
import { useAddPaths } from './autodj/ops'
import { Library } from './autodj/Library'
import { PlaylistPanel } from './autodj/PlaylistPanel'
import { QueuePanel } from './autodj/QueuePanel'
import { StudioDeck } from './studio/StudioDeck'

const initialMount = () => new URLSearchParams(window.location.search).get('mount') ?? ''

/** Library, deck, queue and playlist of one AutoDJ. Served at /admin/studio?mount=/radio. */
export default function StudioPage() {
  const q = useInstances()
  const list = q.data ?? []
  const [want, setWant] = useState(initialMount)
  // The URL names the mount, but only a mount that exists can be edited: fall back to the first one.
  const inst = useMemo(() => list.find((i) => i.mount === want) ?? list[0], [list, want])
  const mount = inst?.mount
  const deck = useDeck(inst)
  const { send, isBusy } = useTransport(mount)
  const { add, adding } = useAddPaths(mount)
  useDeckHotkeys(deck, send)
  // Any logged-in user passes the per-mount check on these endpoints for mounts they hold, so editing is not superadmin-only.
  const canEdit = !!page().user

  useEffect(() => {
    if (!mount) return
    const u = new URL(window.location.href)
    if (u.searchParams.get('mount') !== mount) { u.searchParams.set('mount', mount); window.history.replaceState(null, '', u.toString()) }
  }, [mount])
  const pick = useCallback((m: string) => setWant(m), [])

  if (q.isLoading) {
    return (
      <>
        <PageHeader title="Studio" />
        <div className="grid gap-3 xl:grid-cols-[320px_minmax(0,1fr)_380px]"><Skeleton className="h-[560px]" /><Skeleton className="h-[560px]" /><Skeleton className="h-[560px]" /></div>
      </>
    )
  }
  if (q.isError && !list.length) {
    return (
      <>
        <PageHeader title="Studio" />
        <div className="rounded-lg border border-line bg-surface"><EmptyState icon={<Icon name="alert" size={20} />} title="Could not load the AutoDJ list" body={(q.error as Error).message} action={<Button size="sm" onClick={() => q.refetch()}>Retry</Button>} /></div>
      </>
    )
  }
  if (!inst || !deck || !mount) {
    return (
      <>
        <PageHeader title="Studio" />
        <div className="rounded-lg border border-line bg-surface"><EmptyState icon={<Icon name="studio" size={20} />} title="No AutoDJ to work on" body="The Studio edits the library, queue and playlist of an AutoDJ. Create one first." action={<Link href="/admin/autodj" className="inline-flex h-8 items-center rounded-md bg-accent px-3 text-[13px] font-medium text-accent-fg hover:brightness-110">Go to AutoDJ</Link>} /></div>
      </>
    )
  }

  return (
    <>
      <PageHeader
        title="Studio"
        sub={<span className="inline-flex items-center gap-2"><Tally on={deck.state === 'playing'} />{deck.state === 'playing' ? 'On air' : deck.state === 'paused' ? 'Paused' : 'Stopped'} on <span className="font-mono text-ink">{mount}</span>, {inst.format.toUpperCase()} {inst.bitrate} kbps</span>}
        actions={<>
          <Link href="/admin/autodj" className="inline-flex h-8 items-center gap-1.5 rounded-md px-3 text-[13px] text-muted hover:bg-hover hover:text-ink"><Icon name="chevron-left" size={14} />AutoDJ</Link>
          {list.length > 1 && <Select value={mount} onValueChange={pick} options={list.map((i) => ({ value: i.mount, label: `${i.mount}  ${i.name}` }))} className="w-52" />}
        </>}
      />
      {!inst.enabled && <div role="status" className="mb-3 rounded-lg border border-warn/30 bg-warn/10 px-4 py-2.5 text-[13px] text-warn">This AutoDJ is disabled, so nothing plays. Enable it on the AutoDJ page; the playlist can still be edited here.</div>}
      <div className="grid gap-3 xl:grid-cols-[320px_minmax(0,1fr)_380px] [&>*]:min-w-0">
        <Library mount={mount} canEdit={canEdit} onAdd={(p) => add(p)} adding={adding} className="h-[520px] xl:h-[calc(100vh-11rem)] xl:min-h-[560px] max-xl:order-3" />
        <div className="space-y-3 max-xl:order-1">
          <StudioDeck inst={inst} deck={deck} send={send} isBusy={isBusy} canEdit={canEdit} />
          <QueuePanel mount={mount} deck={deck} canEdit={canEdit} />
        </div>
        <PlaylistPanel mount={mount} deck={deck} canEdit={canEdit} musicDir={inst.music_dir} className="h-[520px] xl:h-[calc(100vh-11rem)] xl:min-h-[560px] max-xl:order-2" />
      </div>
    </>
  )
}
