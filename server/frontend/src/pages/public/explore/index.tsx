import { useEffect, useMemo, useRef, useState } from 'react'
import { Panel, Tally } from '@/ui/console'
import { Button, NativeSelect, Segmented } from '@/ui/controls'
import { EmptyState } from '@/ui/display'
import { Icon } from '@/ui/icons'
import { SearchInput } from '@/ui/kit'
import { PublicShell } from '../shared/Chrome'
import { MiniPlayer, PlayerProvider, useMiniPlayer } from '../shared/Player'
import { StationCard } from '../shared/StationCard'
import { useStations } from '../shared/stations'

type Filter = 'all' | 'live' | 'video'
type Sort = 'listeners' | 'name'

function initialQuery() {
  try { return new URLSearchParams(location.search).get('q') ?? '' } catch { return '' }
}

function Browser() {
  const { stations, connected } = useStations()
  const [q, setQ] = useState(initialQuery)
  const [filter, setFilter] = useState<Filter>('all')
  const [sort, setSort] = useState<Sort>('listeners')
  const box = useRef<HTMLDivElement>(null)
  const p = useMiniPlayer()

  // Keep the query shareable without adding history entries per keystroke.
  useEffect(() => {
    const u = new URL(location.href)
    if (q) u.searchParams.set('q', q); else u.searchParams.delete('q')
    history.replaceState(null, '', u)
  }, [q])

  // "/" jumps to search, like the admin command palette convention.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if (e.key === '/' && !e.metaKey && !e.ctrlKey && !/^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName) && !t.isContentEditable) {
        e.preventDefault()
        box.current?.querySelector('input')?.focus()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const shown = useMemo(() => {
    const n = q.trim().toLowerCase()
    const list = stations.filter((s) =>
      (filter === 'all' || (filter === 'live' && s.live) || (filter === 'video' && s.hasVideo)) &&
      (!n || [s.mount, s.song, s.name, s.genre, s.description].some((f) => f.toLowerCase().includes(n))))
    return sort === 'name' ? [...list].sort((a, b) => a.mount.localeCompare(b.mount)) : list
  }, [stations, q, filter, sort])

  const live = stations.filter((s) => s.live).length
  const clear = () => { setQ(''); setFilter('all') }

  return (
    <>
      <div className="mb-5 flex flex-wrap items-end justify-between gap-3 pt-8">
        <div>
          <h1 className="text-lg font-semibold tracking-tight text-ink">Explore</h1>
          <p className="mt-0.5 flex items-center gap-2 text-[13px] text-muted">
            <Tally on={connected && live > 0} />
            <span aria-live="polite">{live} live of {stations.length} listed</span>
          </p>
        </div>
      </div>

      <div className="mb-4 flex flex-wrap items-center gap-2">
        <div ref={box} className="min-w-0 flex-1 basis-64"><SearchInput value={q} onChange={setQ} placeholder="Search mount, track or station ( / )" /></div>
        <Segmented<Filter> label="Filter" value={filter} onChange={setFilter} options={[{ value: 'all', label: 'All' }, { value: 'live', label: 'Live' }, { value: 'video', label: 'Video' }]} />
        <NativeSelect aria-label="Sort by" value={sort} onChange={(e) => setSort(e.target.value as Sort)} className="w-40!">
          <option value="listeners">Most listeners</option>
          <option value="name">Mount name</option>
        </NativeSelect>
      </div>

      {shown.length === 0 ? (
        <Panel legend="Streams" flush>
          <EmptyState icon={<Icon name={q || filter !== 'all' ? 'search' : 'streams'} size={20} />}
            title={q || filter !== 'all' ? 'No streams match' : 'No streams are listed'}
            body={q || filter !== 'all' ? 'Nothing matches the current search and filter.' : 'This server has no public mounts right now. The list updates by itself.'}
            action={q || filter !== 'all' ? <Button size="sm" onClick={clear}>Clear search and filter</Button> : <a href="/" className="text-xs text-accent hover:underline">Back to the station page</a>} />
        </Panel>
      ) : (
        <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">{shown.map((s) => <StationCard key={s.mount} s={s} />)}</ul>
      )}
      <div className={p.mount ? 'h-20' : 'h-12'} aria-hidden />
      <MiniPlayer stations={stations} />
    </>
  )
}

export function App() {
  return (
    <PublicShell active="explore">
      <main className="mx-auto max-w-[1080px] px-5">
        <PlayerProvider><Browser /></PlayerProvider>
      </main>
    </PublicShell>
  )
}

