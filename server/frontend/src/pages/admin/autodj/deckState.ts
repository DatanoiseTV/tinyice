import { useCallback, useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useLive } from '@/lib/live'
import { useAction } from '@/ui/kit'
import { registerIcons } from '@/ui/icons'
import { autoDJState, type AutoDJState, type PlaylistItem } from '@/types'
import { dj, qk, type DjInstance, type Transport } from './api'

registerIcons({
  save: { d: 'M4.5 4.5h9l2 2v9h-11z M7 4.5v3.5h5V4.5 M7 15.5v-4h6v4' },
  'queue-next': { d: 'M3 5h9 M3 9h6 M3 13h5 M13 11v6 M10 14h6' },
  locate: { d: 'M10 6a4 4 0 1 0 0 8 4 4 0 0 0 0-8z M10 2.5V5 M10 15v2.5 M2.5 10H5 M15 10h2.5' },
})

/** Live view of one AutoDJ: the SSE frame when there is one, the REST row until the first frame arrives. */
export interface Deck {
  mount: string
  name: string
  state: AutoDJState
  song: string
  /** Seconds into the track. Held at the last playing value while paused: the server reports 0 then. */
  position: number
  /** 0 when the decoder cannot report a length up front (most non-MP3 input). */
  duration: number
  /** Playlist id of the playing track; -1 for queue entries and external-command tracks. */
  currentId: number
  playlistPos: number
  playlistLen: number
  shuffle: boolean
  loop: boolean
  queue: PlaylistItem[]
  /** Bumps on every playlist mutation (any tab); refetch the playlist when it changes. */
  playlistVersion: number | undefined
  live: boolean
}

export function splitSong(song: string): { artist: string; title: string } {
  const i = song.indexOf(' - ')
  return i > 0 ? { artist: song.slice(0, i), title: song.slice(i + 3) } : { artist: '', title: song }
}

export function useDeck(inst: DjInstance | undefined): Deck | undefined {
  const { autodj } = useLive()
  const held = useRef({ song: '', pos: 0 })
  if (!inst) return undefined
  const ev = autodj[inst.mount]
  const state = autoDJState(ev ? ev.state : inst.state)
  const song = ev ? ev.song : inst.current_song
  const raw = ev ? ev.position : inst.position
  let position = raw || 0
  if (state === 'playing') held.current = { song, pos: position }
  else if (state === 'paused') position = held.current.song === song ? held.current.pos : 0
  else position = 0
  return {
    mount: inst.mount,
    name: ev?.name ?? inst.name,
    state,
    song,
    position,
    duration: (ev ? ev.duration : inst.duration) || 0,
    currentId: ev ? ev.current_id : inst.current_id,
    playlistPos: ev ? ev.playlist_pos : inst.playlist_pos,
    playlistLen: ev ? ev.playlist_len : inst.playlist_len,
    shuffle: ev ? ev.shuffle : inst.shuffle,
    loop: ev ? ev.loop : inst.loop,
    queue: (ev ? ev.queue : inst.queue) ?? [],
    playlistVersion: ev?.playlist_version,
    live: !!ev,
  }
}

/** Transport commands with a busy flag, toast on failure, and a refetch of what they change. */
export function useTransport(mount: string | undefined) {
  const qc = useQueryClient()
  const { run, isBusy } = useAction()
  const send = useCallback((a: Transport) => {
    if (!mount) return Promise.resolve(undefined)
    return run(`t:${a}`, async () => {
      await dj.transport(mount, a)
      qc.invalidateQueries({ queryKey: qk.instances })
      qc.invalidateQueries({ queryKey: qk.queue(mount) })
    })
  }, [mount, qc, run])
  return { send, isBusy }
}

/** Debounced volume: the knob fires on every pointer move, the server only needs the settled value. */
export function useVolume(mount: string | undefined) {
  const key = mount ? `tinyice-dj-vol:${mount}` : ''
  const read = () => { try { const v = Number(localStorage.getItem(key)); return key && localStorage.getItem(key) !== null && isFinite(v) ? Math.max(0, Math.min(100, v)) : 100 } catch { return 100 } }
  const [percent, setPercent] = useState(read)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const { run } = useAction()
  useEffect(() => { setPercent(read()) }, [key]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => () => clearTimeout(timer.current), [])
  const set = useCallback((p: number) => {
    const v = Math.round(Math.max(0, Math.min(100, p)))
    setPercent(v)
    try { localStorage.setItem(key, String(v)) } catch { /* private mode */ }
    clearTimeout(timer.current)
    timer.current = setTimeout(() => { if (mount) run('volume', () => dj.volume(mount, v)) }, 150)
  }, [key, mount, run])
  return { percent, set }
}

/** Keys that belong to the focused control, not to the deck. */
function typing(t: EventTarget | null): boolean {
  const el = t as HTMLElement | null
  if (!el || !el.tagName) return false
  if (el.isContentEditable) return true
  if (['INPUT', 'TEXTAREA', 'SELECT', 'BUTTON', 'A', 'AUDIO'].includes(el.tagName)) return true
  const role = el.getAttribute('role')
  return role === 'slider' || role === 'checkbox' || role === 'radio' || role === 'menuitem' || !!el.closest('[role="dialog"]')
}

/** Space toggles play/pause, N skips, P goes back. Ignored while a control that uses those keys has focus. */
export function useDeckHotkeys(deck: Deck | undefined, send: (a: Transport) => void) {
  useEffect(() => {
    if (!deck) return
    const h = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || typing(e.target)) return
      if (e.key === ' ') { e.preventDefault(); send(deck.state === 'playing' ? 'pause' : 'play') }
      else if (e.key === 'n') send('next')
      else if (e.key === 'p') send('prev')
    }
    addEventListener('keydown', h)
    return () => removeEventListener('keydown', h)
  }, [deck, send])
}

export const mm = (s: number) => `${Math.floor(Math.max(0, s) / 60)}:${String(Math.floor(Math.max(0, s) % 60)).padStart(2, '0')}`
