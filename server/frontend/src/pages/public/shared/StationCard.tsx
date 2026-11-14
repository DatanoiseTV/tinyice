import { useState } from 'react'
import { Badge } from '@/ui/display'
import { Legend, Tally } from '@/ui/console'
import { Icon } from '@/ui/icons'
import { CopyButton } from '@/ui/kit'
import { codec } from '@/lib/format'
import { cn } from '@/lib/cn'
import { PlayButton, useMiniPlayer } from './Player'
import { playerPath, streamUrl, type Station } from './stations'

function Poster({ s, className }: { s: Station; className?: string }) {
  const [ok, setOk] = useState(true)
  if (!s.hasVideo || !s.live || !ok) return null
  // The server keeps the last viewer-captured frame; a 404 just hides the image.
  return <img src={`${s.mount}/poster.jpg`} alt="" loading="lazy" onError={() => setOk(false)} className={cn('bg-black object-cover', className)} />
}

function Meta({ s }: { s: Station }) {
  const fmt = s.format ? codec(s.format) : ''
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {fmt && <Badge>{fmt}</Badge>}
      {s.bitrate > 0 && <Badge className="num font-mono">{s.bitrate} kbps</Badge>}
      {s.hasVideo && <Badge tone="info">Video</Badge>}
      {s.genre && <Badge>{s.genre}</Badge>}
    </div>
  )
}

function Listeners({ n }: { n: number }) {
  return (
    <span className="inline-flex items-baseline gap-1.5" title={`${n} listening`}>
      <span className="num font-mono text-[15px] font-medium text-ink">{n}</span>
      <Legend>{n === 1 ? 'listener' : 'listeners'}</Legend>
    </span>
  )
}

function Actions({ s }: { s: Station }) {
  return (
    <div className="flex items-center gap-0.5">
      <CopyButton text={streamUrl(s.mount)} label="Copy stream URL" />
      <a href={playerPath(s.mount)} aria-label={`Open player for ${s.mount}`} title="Open player" className="flex size-7 items-center justify-center rounded-md text-faint transition-colors hover:bg-hover hover:text-ink">
        <Icon name="external" size={14} />
      </a>
    </div>
  )
}

function Lines({ s, big }: { s: Station; big?: boolean }) {
  const nowPlaying = s.song || (s.live ? 'No track information' : 'Off air')
  return (
    <>
      <div className="flex items-center gap-2">
        <Tally on={s.live} />
        <span className="truncate font-mono text-[11px] text-faint">{s.mount}</span>
        {s.live && <Legend className="text-live">Live</Legend>}
      </div>
      <div className={cn('mt-1 truncate font-medium text-ink', big ? 'text-base' : 'text-[14px]', !s.song && 'text-muted')} title={nowPlaying}>{nowPlaying}</div>
      <div className="truncate text-xs text-muted">{s.name && s.name !== s.mount ? s.name : s.description || ' '}</div>
    </>
  )
}

/** Dense board row: the landing page list. */
export function StationRow({ s }: { s: Station }) {
  const p = useMiniPlayer()
  const active = p.mount === s.mount
  return (
    <li className={cn('flex items-center gap-3 border-b border-line px-4 py-3 last:border-b-0 max-sm:flex-wrap', active && 'bg-accent/[0.06]')}>
      <PlayButton station={s} size="lg" />
      <Poster s={s} className="hidden aspect-video w-24 shrink-0 rounded sm:block" />
      <div className="min-w-0 flex-1 max-sm:basis-[calc(100%-3.5rem)]">
        <Lines s={s} big />
        <div className="mt-2 md:hidden"><Meta s={s} /></div>
      </div>
      <div className="hidden shrink-0 md:block"><Meta s={s} /></div>
      <div className="flex shrink-0 items-center gap-3 max-sm:w-full max-sm:justify-between max-sm:pl-14">
        <Listeners n={s.listeners} />
        <Actions s={s} />
      </div>
    </li>
  )
}

/** Card: the Explore grid. */
export function StationCard({ s }: { s: Station }) {
  const p = useMiniPlayer()
  const active = p.mount === s.mount
  return (
    <li className={cn('flex min-w-0 flex-col overflow-hidden rounded-lg border bg-surface transition-colors', active ? 'border-accent' : 'border-line hover:border-line-strong')}>
      <Poster s={s} className="aspect-video w-full border-b border-line" />
      <div className="flex min-w-0 flex-1 flex-col p-4">
        <Lines s={s} big />
        <div className="mt-3"><Meta s={s} /></div>
        {s.description && <p className="mt-2 line-clamp-2 text-xs text-muted">{s.description}</p>}
        <div className="mt-auto flex items-center justify-between gap-3 pt-4">
          <div className="flex items-center gap-2.5"><PlayButton station={s} /><Listeners n={s.listeners} /></div>
          <Actions s={s} />
        </div>
      </div>
    </li>
  )
}
