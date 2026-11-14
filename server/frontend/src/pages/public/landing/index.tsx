import { useMemo } from 'react'
import { Panel, Legend, Readout, Tally } from '@/ui/console'
import { EmptyState } from '@/ui/display'
import { Icon } from '@/ui/icons'
import { compact } from '@/lib/format'
import { PublicShell } from '../shared/Chrome'
import { MiniPlayer, PlayerProvider, useMiniPlayer } from '../shared/Player'
import { StationRow } from '../shared/StationCard'
import { pageMeta, useStations } from '../shared/stations'
import { MARKDOWN_CLASS, renderSafeMarkdown } from '../shared/markdown'

function Hero({ live, listeners, total, connected }: { live: number; listeners: number; total: number; connected: boolean }) {
  const m = pageMeta()
  const html = useMemo(() => (m.markdown ? renderSafeMarkdown(m.markdown) : ''), [m.markdown])
  return (
    <section className="grid gap-6 pb-8 pt-10 lg:grid-cols-[minmax(0,1fr)_300px] lg:gap-10 lg:pt-14">
      <div className="min-w-0">
        <Legend>Station</Legend>
        <h1 className="mt-2 text-[32px] font-semibold leading-[1.1] tracking-tight text-ink sm:text-[44px]">{m.title}</h1>
        {m.subtitle && <p className="mt-3 max-w-xl text-base text-muted">{m.subtitle}</p>}
        {html && <div className={`mt-6 max-w-xl ${MARKDOWN_CLASS}`} dangerouslySetInnerHTML={{ __html: html }} />}
      </div>
      <Panel legend="Right now" lamp={!connected ? 'warn' : live > 0 ? 'live' : 'off'} className="self-start" flush>
        <div className="grid grid-cols-2 divide-x divide-line">
          <div className="p-4"><Readout label="On air" value={live} unit={live === 1 ? 'stream' : 'streams'} /></div>
          <div className="p-4"><Readout label="Listening" value={compact(listeners)} /></div>
        </div>
        <div className="flex items-center gap-2 border-t border-line px-4 py-2.5 text-xs text-muted">
          <Tally on={connected && live > 0} />
          <span aria-live="polite">{!connected ? 'Reconnecting to the live feed' : live > 0 ? `${total} mount${total === 1 ? '' : 's'} listed, updating live` : 'Nothing on air'}</span>
        </div>
      </Panel>
    </section>
  )
}

function Board() {
  const { stations, connected } = useStations()
  const live = stations.filter((s) => s.live)
  const off = stations.filter((s) => !s.live)
  const listeners = live.reduce((a, s) => a + s.listeners, 0)
  return (
    <PlayerProvider>
      <Hero live={live.length} listeners={listeners} total={stations.length} connected={connected} />
      <div className="space-y-3 pb-16">
        <Panel legend="On air" title={live.length ? `${live.length} live` : undefined} lamp={live.length ? 'live' : 'off'} flush>
          {live.length === 0 ? (
            <EmptyState icon={<Icon name="streams" size={20} />} title="Nothing is on air right now"
              body={off.length ? 'The mounts below have no source connected. This page updates by itself when one goes live.' : 'No streams are listed yet. This page updates by itself when one goes live.'}
              action={<a href="/explore" className="text-xs text-accent hover:underline">Open Explore</a>} />
          ) : <ul>{live.map((s) => <StationRow key={s.mount} s={s} />)}</ul>}
        </Panel>
        {off.length > 0 && (
          <Panel legend="Off air" title={`${off.length} mount${off.length === 1 ? '' : 's'}`} lamp="off" flush>
            <ul>{off.map((s) => <StationRow key={s.mount} s={s} />)}</ul>
          </Panel>
        )}
      </div>
      <PlayerPad />
      <MiniPlayer stations={stations} />
    </PlayerProvider>
  )
}

/** Keeps the last row clear of the docked player. */
function PlayerPad() {
  const p = useMiniPlayer()
  return <div className={p.mount ? 'h-16' : 'h-0'} aria-hidden />
}

export function App() {
  return (
    <PublicShell>
      <main className="mx-auto max-w-[1080px] px-5">
        <Board />
      </main>
    </PublicShell>
  )
}

