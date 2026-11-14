import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Icon } from '@/ui/icons'
import { Fader, Legend } from '@/ui/console'
import { Button } from '@/ui/controls'
import { cn } from '@/lib/cn'
import { playerPath, type Station } from './stations'
import './icons'

type Status = 'idle' | 'loading' | 'playing' | 'error'
interface Ctx {
  mount: string | null
  status: Status
  volume: number
  setVolume: (v: number) => void
  toggle: (mount: string) => void
  stop: () => void
}
const PlayerCtx = createContext<Ctx>({ mount: null, status: 'idle', volume: 0.8, setVolume: () => {}, toggle: () => {}, stop: () => {} })
export const useMiniPlayer = () => useContext(PlayerCtx)

const VOL_KEY = 'tinyice-public-volume'
function storedVolume(): number {
  try { const v = parseFloat(localStorage.getItem(VOL_KEY) ?? ''); if (v >= 0 && v <= 1) return v } catch { /* storage may be unavailable */ }
  return 0.8
}

/**
 * One shared <audio> element for the whole page. A live stream is never
 * paused (the buffer would be stale on resume): "pause" drops the connection
 * and "play" opens a fresh one.
 */
export function PlayerProvider({ children }: { children: ReactNode }) {
  const audio = useRef<HTMLAudioElement | null>(null)
  const [mount, setMount] = useState<string | null>(null)
  const [status, setStatus] = useState<Status>('idle')
  const [volume, setVol] = useState(storedVolume)

  const stop = useCallback(() => {
    const a = audio.current
    if (a) { a.pause(); a.removeAttribute('src'); a.load() }
    setMount(null)
    setStatus('idle')
  }, [])

  const toggle = useCallback((m: string) => {
    const a = audio.current
    if (!a) return
    if (mount === m && status !== 'error') { stop(); return }
    setMount(m)
    setStatus('loading')
    a.src = m
    a.volume = volume
    a.play().catch((e: DOMException) => { if (e.name !== 'AbortError') setStatus('error') })
  }, [mount, status, stop, volume])

  const setVolume = useCallback((v: number) => {
    setVol(v)
    if (audio.current) audio.current.volume = v
    try { localStorage.setItem(VOL_KEY, String(v)) } catch { /* ignore */ }
  }, [])

  const value = useMemo(() => ({ mount, status, volume, setVolume, toggle, stop }), [mount, status, volume, setVolume, toggle, stop])
  return (
    <PlayerCtx.Provider value={value}>
      {children}
      <audio
        ref={audio}
        preload="none"
        onPlaying={() => setStatus('playing')}
        onWaiting={() => setStatus((s) => (s === 'idle' ? s : 'loading'))}
        onError={(e) => { if (e.currentTarget.getAttribute('src')) setStatus('error') }}
        onEnded={stop}
      />
    </PlayerCtx.Provider>
  )
}

/** Play/stop button used by every station row and card. */
export function PlayButton({ station, size = 'md' }: { station: Station; size?: 'md' | 'lg' }) {
  const p = useMiniPlayer()
  const active = p.mount === station.mount
  const busy = active && p.status === 'loading'
  const playing = active && p.status !== 'error' && p.status !== 'idle'
  return (
    <button
      type="button"
      onClick={() => p.toggle(station.mount)}
      disabled={!station.live}
      aria-pressed={playing}
      aria-label={playing ? `Stop ${station.mount}` : `Listen to ${station.mount}`}
      className={cn(
        'flex shrink-0 items-center justify-center rounded-md border transition-colors disabled:pointer-events-none disabled:opacity-40',
        size === 'lg' ? 'size-11' : 'size-9',
        playing ? 'border-accent bg-accent text-accent-fg' : 'border-line-strong bg-raised text-ink hover:border-accent hover:text-accent',
      )}
    >
      <Icon name={busy ? 'refresh' : playing ? 'stop' : 'play'} size={size === 'lg' ? 18 : 16} className={busy ? 'animate-spin' : ''} />
    </button>
  )
}

/** Docked bar shown while something plays. Reads live now-playing from the station list. */
export function MiniPlayer({ stations }: { stations: Station[] }) {
  const p = useMiniPlayer()

  useEffect(() => {
    if (!p.mount || !('mediaSession' in navigator)) return
    const s = stations.find((x) => x.mount === p.mount)
    if (s) navigator.mediaSession.metadata = new MediaMetadata({ title: s.song || s.mount, artist: s.name || s.mount })
  }, [p.mount, stations])

  if (!p.mount) return null
  const s = stations.find((x) => x.mount === p.mount)
  const err = p.status === 'error'
  return (
    <div role="region" aria-label="Player" className="fixed inset-x-0 bottom-0 z-40 border-t border-line-strong bg-surface">
      <div className="mx-auto flex max-w-[1080px] items-center gap-3 px-5 py-2.5">
        <span className={cn('size-2 shrink-0 rounded-full', err ? 'bg-danger' : p.status === 'playing' ? 'bg-live tally' : 'bg-warn')} aria-hidden />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2"><Legend>{err ? 'Cannot play' : p.status === 'playing' ? 'Playing' : 'Connecting'}</Legend><span className="truncate font-mono text-[11px] text-faint">{p.mount}</span></div>
          <div className="truncate text-[13px] font-medium text-ink" aria-live="polite">
            {err ? 'This stream cannot be played inline in your browser.' : s?.song || s?.name || 'No track information'}
          </div>
        </div>
        {err && <a href={playerPath(p.mount)} className="shrink-0 text-xs text-accent hover:underline">Open full player</a>}
        <div className="hidden w-28 shrink-0 sm:block"><Fader value={p.volume} onChange={p.setVolume} label="Volume" /></div>
        <Button variant="secondary" size="icon" onClick={p.stop} aria-label="Stop playback"><Icon name="stop" size={14} /></Button>
      </div>
    </div>
  )
}
