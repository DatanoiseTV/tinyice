import { useEffect, useRef, useState } from 'react'
import { page } from '@/lib/api'
import { cn } from '@/lib/cn'
import { Fader, Legend } from '@/ui/console'
import { Icon, Logo } from '@/ui/icons'
import type { PlayerData } from '@/types'
import { useNowPlaying } from '../player/nowPlaying'
import { usePlayback } from '../player/usePlayback'
import { audienceLabel, mountPath, parseSong } from '../player/source'
import { getGraph } from '../player/audioGraph'
import { Spectrum } from '../player/Spectrum'

/*
 * Compact iframe player. Everything heavy stays out of this bundle: usePlayback
 * only imports hls.js / WHEP code for video mounts (dynamic), and the embed
 * is always audio. No third-party requests are made from here (the full
 * player does the cover lookup; an embed on someone else's site should not).
 *
 * Layout is chosen from the frame's measured size, because the same page is
 * used at 300x120, 400x200 and 100%x80 and CSS alone cannot restructure it.
 */
type Tier = 'row' | 'card' | 'tall'

function useSize(ref: React.RefObject<HTMLElement | null>) {
  const [size, setSize] = useState({ w: 400, h: 120 })
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const ro = new ResizeObserver(([e]) => setSize({ w: Math.round(e.contentRect.width), h: Math.round(e.contentRect.height) }))
    ro.observe(el)
    return () => ro.disconnect()
  }, [ref])
  return size
}

/** Four bars that move while audio is arriving; static otherwise. */
function Bars({ on }: { on: boolean }) {
  return (
    <span className="flex h-4 items-end gap-[2px]" aria-hidden>
      {[0, 1, 2, 3].map((i) => (
        <span key={i} className={cn('w-[3px] origin-bottom rounded-[1px] bg-accent', on ? 'animate-[tally_.9s_ease-in-out_infinite]' : 'opacity-40')}
          style={{ height: on ? `${55 + ((i * 37) % 45)}%` : '30%', animationDelay: `${i * 0.12}s` }} />
      ))}
    </span>
  )
}

export function Embed() {
  const data = page<Partial<PlayerData>>()
  const mount = data.mount ?? ''
  const playback = usePlayback({ mount, hasVideo: false, hasHLS: true, initialMode: 'http' })
  const { song, name, audience, sourceLive } = useNowPlaying(data)
  const { phase, error, toggle, retry, volume, setVolume, muted } = playback
  const rootRef = useRef<HTMLDivElement>(null)
  const { w, h } = useSize(rootRef)
  const tier: Tier = h < 100 ? 'row' : h < 170 ? 'card' : 'tall'
  const narrow = w < 330

  const { artist, track } = parseSong(song)
  const active = phase === 'playing' || phase === 'buffering' || phase === 'connecting' || phase === 'reconnecting'
  const busy = phase === 'buffering' || phase === 'connecting' || phase === 'reconnecting'
  const offline = sourceLive === false
  const status = error ? 'Error' : busy ? (phase === 'buffering' ? 'Buffering' : phase === 'reconnecting' ? 'Reconnecting' : 'Connecting') : phase === 'playing' ? 'Live' : offline ? 'Offline' : 'On air'
  const lamp = error ? 'bg-danger' : busy || (offline && phase === 'playing') ? 'bg-warn' : offline ? 'bg-line-strong' : 'bg-live tally'
  const textTone = error ? 'text-danger' : busy ? 'text-warn' : offline ? 'text-faint' : 'text-live'

  const playBtn = (size: number) => (
    <button
      type="button" onClick={error ? retry : toggle} aria-label={error ? 'Retry' : active ? 'Pause' : 'Play'}
      style={{ width: size, height: size }}
      className="flex shrink-0 items-center justify-center rounded-full bg-accent text-accent-fg transition-[filter] hover:brightness-110"
    >
      <Icon name={error ? 'refresh' : busy ? 'refresh' : active ? 'pause' : 'play'} size={Math.round(size * 0.42)} className={busy ? 'animate-spin' : ''} />
    </button>
  )

  const lampEl = (
    <span className="inline-flex shrink-0 items-center gap-1.5" role="status" aria-live="polite">
      <span className={cn('size-1.5 rounded-full', lamp)} aria-hidden />
      <Legend className={textTone}>{status}</Legend>
    </span>
  )

  const titleText = track || <span className="text-faint">No track information</span>
  const volumeFader = !narrow && (
    <div className="flex items-center gap-2" title="Volume">
      <button type="button" aria-label={muted || volume === 0 ? 'Unmute' : 'Mute'} onClick={playback.toggleMute} className="text-faint hover:text-ink">
        <Icon name={muted || volume === 0 ? 'mute' : 'volume'} size={16} />
      </button>
      <div className="w-20"><Fader value={muted ? 0 : volume / 100} onChange={(v) => setVolume(v * 100)} label="Volume" /></div>
    </div>
  )

  // The audio element is always present so the ref is stable across tiers.
  const audioEl = <audio ref={playback.bind as (el: HTMLAudioElement | null) => void} crossOrigin="anonymous" preload="none" />

  if (!mount) return <div className="flex h-dvh items-center justify-center text-xs text-faint">No stream.</div>

  return (
    <div ref={rootRef} className="relative flex h-dvh w-full min-w-0 flex-col overflow-hidden bg-surface text-ink">
      {audioEl}

      {tier === 'row' && (
        <div className="flex flex-1 items-center gap-3 px-3">
          {playBtn(40)}
          <div className="min-w-0 flex-1">
            <p className="truncate text-sm font-medium leading-tight">{error ? error.title : titleText}</p>
            <p className="mt-0.5 truncate text-xs text-muted">{artist || name || ' '}</p>
          </div>
          {!narrow && lampEl}
          <Bars on={phase === 'playing'} />
        </div>
      )}

      {tier === 'card' && (
        <>
          <div className="flex flex-1 items-center gap-3.5 px-4">
            {playBtn(56)}
            <div className="min-w-0 flex-1">
              <div className="mb-1 flex items-center gap-2.5">{lampEl}{!narrow && <Legend className="truncate">{name}</Legend>}</div>
              <p className="line-clamp-2 break-words text-[15px] font-semibold leading-snug">{error ? error.title : titleText}</p>
              {artist && <p className="mt-0.5 truncate text-xs text-muted">{artist}</p>}
            </div>
            {volumeFader}
          </div>
          <div className="flex h-7 shrink-0 items-center justify-between border-t border-line px-4 text-[11px] text-faint">
            <span className="inline-flex items-center gap-1.5"><Icon name="headphones" size={12} /><span className="num font-mono">{audience}</span> {audienceLabel(audience, false)}</span>
            <a href={`/player${mountPath(mount)}`} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1.5 hover:text-ink">
              <Logo size={12} />TinyIce<Icon name="external" size={11} />
            </a>
          </div>
        </>
      )}

      {tier === 'tall' && (
        <>
          <div className="flex h-9 shrink-0 items-center gap-2.5 border-b border-line px-4">
            {lampEl}
            <Legend className="min-w-0 flex-1 truncate">{name || mountPath(mount)}</Legend>
            <span className="inline-flex shrink-0 items-center gap-1.5 text-[11px] text-faint"><Icon name="headphones" size={12} /><span className="num font-mono">{audience}</span></span>
          </div>
          <div className="flex min-h-0 flex-1 flex-col justify-center gap-1 px-4">
            <p className="line-clamp-2 break-words text-lg font-semibold leading-tight">{error ? error.title : titleText}</p>
            {(artist || error?.detail) && <p className="truncate text-[13px] text-muted">{error?.detail ?? artist}</p>}
          </div>
          <Spectrum analyser={() => getGraph()?.analyser ?? null} active={phase === 'playing'} className="mx-4 h-9 w-[calc(100%-2rem)] shrink-0" />
          <div className="flex h-14 shrink-0 items-center gap-3 px-4">
            {playBtn(40)}
            <div className="flex-1" />
            {volumeFader}
            <a href={`/player${mountPath(mount)}`} target="_blank" rel="noopener noreferrer" aria-label="Open the full player" title="Open the full player"
              className="flex size-8 items-center justify-center rounded-md text-faint hover:bg-hover hover:text-ink"><Icon name="external" size={14} /></a>
          </div>
        </>
      )}

      {tier === 'row' && phase === 'playing' && <div className="absolute inset-x-0 bottom-0 h-0.5 bg-accent" aria-hidden />}
    </div>
  )
}
