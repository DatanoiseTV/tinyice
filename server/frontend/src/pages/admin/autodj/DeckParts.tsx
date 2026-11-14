import { Badge } from '@/ui/display'
import { Legend, Timecode, TransportButton } from '@/ui/console'
import { cn } from '@/lib/cn'
import { mm, splitSong, type Deck } from './deckState'
import type { Transport } from './api'

export function StateBadge({ state }: { state: Deck['state'] }) {
  return state === 'playing' ? <Badge tone="live" dot>Playing</Badge> : state === 'paused' ? <Badge tone="warn" dot>Paused</Badge> : <Badge>Stopped</Badge>
}

/** Track title over artist. "Artist - Title" arrives as one string; the AutoDJ has no separate tags. */
export function NowPlaying({ deck, big }: { deck: Deck; big?: boolean }) {
  const { artist, title } = splitSong(deck.song)
  const idle = deck.state === 'stopped' && !deck.song
  return (
    <div className="min-w-0" aria-live="polite">
      <div className={cn('truncate font-semibold tracking-tight', big ? 'text-xl' : 'text-[15px]', idle ? 'text-faint' : 'text-ink')} title={deck.song}>{idle ? 'Nothing loaded' : title || 'Unknown track'}</div>
      <div className="truncate text-[13px] text-muted">{idle ? `${deck.playlistLen} track${deck.playlistLen === 1 ? '' : 's'} in the playlist` : artist || deck.name}</div>
    </div>
  )
}

/**
 * Elapsed / length with a progress rail. The server has no seek endpoint, so this
 * is a readout, not a scrubber. Length is 0 for input whose decoder cannot report
 * it up front; then only elapsed time is shown rather than a bar that never moves.
 */
export function Progress({ deck, big }: { deck: Deck; big?: boolean }) {
  const known = deck.duration > 0
  const pct = known ? Math.min(100, (deck.position / deck.duration) * 100) : 0
  const live = deck.state === 'playing'
  return (
    <div>
      <div className="flex items-end justify-between gap-3">
        <Timecode seconds={deck.position} className={cn(big ? 'text-[44px] leading-none' : 'text-2xl leading-none', deck.state === 'stopped' ? 'text-faint' : 'text-ink')} />
        <div className="pb-0.5 text-right">
          <Legend className="block">{known ? 'Remaining' : 'Length'}</Legend>
          <span className="num font-mono text-[13px] text-muted">{known ? `-${mm(deck.duration - deck.position)}` : 'unknown'}</span>
          {known && <span className="num block font-mono text-[11px] text-faint">of {mm(deck.duration)}</span>}
        </div>
      </div>
      <div
        role="progressbar" aria-label="Track progress" aria-valuemin={0} aria-valuemax={known ? Math.round(deck.duration) : undefined}
        aria-valuenow={known ? Math.round(deck.position) : undefined} aria-valuetext={known ? `${mm(deck.position)} of ${mm(deck.duration)}` : `${mm(deck.position)} elapsed`}
        className={cn('mt-3 overflow-hidden rounded-full bg-line-strong', big ? 'h-2' : 'h-1.5')}
      >
        {known && <div className={cn('h-full rounded-full bg-accent motion-safe:transition-[width] motion-safe:duration-700 motion-safe:ease-linear', !live && 'opacity-50')} style={{ width: `${pct}%` }} />}
      </div>
    </div>
  )
}

export function TransportRow({ deck, send, isBusy, size = 'md', disabled }: {
  deck: Deck; send: (a: Transport) => void; isBusy: (k: string) => boolean; size?: 'md' | 'lg'; disabled?: boolean
}) {
  const playing = deck.state === 'playing'
  const empty = deck.playlistLen === 0 && deck.queue.length === 0
  return (
    <div className="flex items-center gap-2" role="group" aria-label="Transport">
      <TransportButton icon="prev" label="Previous track (P)" onClick={() => send('prev')} disabled={disabled || empty || isBusy('t:prev')} size={size === 'lg' ? 'md' : 'sm'} />
      <TransportButton icon={playing ? 'pause' : 'play'} label={playing ? 'Pause (Space)' : 'Play (Space)'} primary onClick={() => send(playing ? 'pause' : 'play')} disabled={disabled || empty || isBusy('t:play') || isBusy('t:pause')} size={size === 'lg' ? 'lg' : 'md'} />
      <TransportButton icon="next" label="Next track (N)" onClick={() => send('next')} disabled={disabled || empty || isBusy('t:next')} size={size === 'lg' ? 'md' : 'sm'} />
      <span className="mx-1 h-5 w-px bg-line" aria-hidden />
      <TransportButton icon="shuffle" label={deck.shuffle ? 'Shuffle on' : 'Shuffle off'} active={deck.shuffle} onClick={() => send('shuffle')} disabled={disabled || isBusy('t:shuffle')} size="sm" />
      <TransportButton icon="loop" label={deck.loop ? 'Loop on' : 'Loop off'} active={deck.loop} onClick={() => send('loop')} disabled={disabled || isBusy('t:loop')} size="sm" />
    </div>
  )
}
