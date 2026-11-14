import { useCallback, useEffect, useRef, useState } from 'react'
import { connectAudio, elementVolumeWritable, getGraph, resumeAudio } from './audioGraph'
import { mountPath, pickAudioSource } from './source'
import type { HlsHandle } from './transports'

export type Phase = 'idle' | 'connecting' | 'buffering' | 'playing' | 'paused' | 'reconnecting' | 'error'
export type Mode = 'http' | 'webrtc'
export interface PlaybackError { title: string; detail?: string }
export interface PlaybackStats { bufferedSec: number; droppedFrames: number; totalFrames: number; latencySec: number }

interface Options { mount: string; hasVideo: boolean; hasHLS: boolean; initialMode: Mode }

const VOL_KEY = 'tinyice-volume'
const MAX_RETRIES = 5

const MEDIA_ERRORS: Record<number, string> = {
  1: 'MEDIA_ERR_ABORTED (user-initiated abort)',
  2: 'MEDIA_ERR_NETWORK (segment fetch failed)',
  3: 'MEDIA_ERR_DECODE (codec or bitstream rejected by decoder)',
  4: 'MEDIA_ERR_SRC_NOT_SUPPORTED (browser cannot play this format)',
}

function loadVolume(): number {
  try {
    const v = parseInt(localStorage.getItem(VOL_KEY) ?? '', 10)
    if (Number.isFinite(v)) return Math.min(100, Math.max(0, v))
  } catch { /* storage unavailable */ }
  return 80
}

/**
 * Owns the media element and every transport behind it: plain audio, HLS
 * (hls.js or native), WHEP/WebRTC. The page renders `ref`'s element and reads
 * the state; it never touches the element directly.
 */
export function usePlayback({ mount, hasVideo, hasHLS, initialMode }: Options) {
  const path = mountPath(mount)
  const elRef = useRef<HTMLMediaElement | null>(null)
  const hlsRef = useRef<HlsHandle | null>(null)
  const whepRef = useRef<(() => void) | null>(null)
  const attachedRef = useRef(false)
  const intent = useRef<'play' | 'pause'>('pause')
  const gen = useRef(0)
  const retries = useRef(0)
  const retryTimer = useRef<ReturnType<typeof setTimeout>>(undefined)

  const [phase, setPhase] = useState<Phase>('idle')
  const [error, setError] = useState<PlaybackError | null>(null)
  const [mode, setModeState] = useState<Mode>(hasVideo && hasHLS === false ? 'webrtc' : initialMode)
  const [volume, setVolumeState] = useState(loadVolume)
  const [muted, setMuted] = useState(false)
  const [elapsed, setElapsed] = useState(0)
  const [stats, setStats] = useState<PlaybackStats>({ bufferedSec: 0, droppedFrames: 0, totalFrames: 0, latencySec: 0 })
  const modeRef = useRef(mode)
  modeRef.current = mode

  const transport = whepRef.current ? 'WebRTC' : hlsRef.current || hasVideo ? 'HLS' : mode === 'webrtc' ? 'WebRTC' : 'Icecast'

  const teardown = useCallback(() => {
    clearTimeout(retryTimer.current)
    hlsRef.current?.destroy()
    hlsRef.current = null
    whepRef.current?.()
    whepRef.current = null
    attachedRef.current = false
    const el = elRef.current
    if (el) {
      el.removeAttribute('src')
      el.srcObject = null
      // load() releases the connection; assigning src='' would raise an
      // error event on some browsers.
      try { el.load() } catch { /* nothing to release */ }
    }
  }, [])

  const fail = useCallback((title: string, detail?: string) => {
    intent.current = 'pause'
    gen.current++
    setError({ title, detail })
    setPhase('error')
  }, [])

  /** Video mounts attach their source up front so play() runs inside the user gesture (iOS). */
  const attachVideo = useCallback(async (): Promise<boolean> => {
    const el = elRef.current
    if (!el) return false
    if (attachedRef.current) return true
    attachedRef.current = true
    const myGen = gen.current
    const { attachHLS, attachWHEP, resolveHLSUrl } = await import('./transports')
    if (myGen !== gen.current) { attachedRef.current = false; return false }
    // hasHLS === false: the server will not build a playlist for this mount
    // (Opus from a WebRTC publisher), so WHEP is the only transport. Do not
    // fall back to a playlist that cannot be parsed.
    const mustWebRTC = hasHLS === false
    if (modeRef.current === 'webrtc' || mustWebRTC) {
      const cleanup = await attachWHEP(path, el)
      if (myGen !== gen.current) { cleanup?.(); attachedRef.current = false; return false }
      if (cleanup) { whepRef.current = cleanup; return true }
      attachedRef.current = false
      if (mustWebRTC) {
        fail('The stream is not available', 'WebRTC negotiation failed. The source may be offline.')
        return false
      }
    }
    const url = await resolveHLSUrl(path)
    if (myGen !== gen.current) { attachedRef.current = false; return false }
    hlsRef.current = await attachHLS(url, el)
    // iOS Safari with preload=none can ignore a freshly set src and sit idle
    // with nothing buffered; an explicit load() makes it fetch the playlist.
    try { el.load() } catch { /* no src */ }
    return true
  }, [fail, hasHLS, path])

  const start = useCallback(async () => {
    const el = elRef.current
    if (!el) return
    const myGen = ++gen.current
    intent.current = 'play'
    setError(null)
    setPhase(retries.current > 0 ? 'reconnecting' : 'connecting')
    try {
      if (hasVideo) {
        if (!(await attachVideo())) return
      } else if (modeRef.current === 'webrtc') {
        if (!attachedRef.current) {
          const { attachWHEP } = await import('./transports')
          const cleanup = await attachWHEP(path, el)
          if (myGen !== gen.current) { cleanup?.(); return }
          if (!cleanup) { fail('WebRTC playback failed', 'The server rejected the session. Switch to HTTP.'); return }
          whepRef.current = cleanup
          attachedRef.current = true
        }
      } else if (!el.getAttribute('src')) {
        // The AudioContext must be created and resumed inside the gesture
        // frame, before play(), or iOS keeps it suspended and playback is
        // silent until the next tap. Order: connect, resume, set src, play.
        connectAudio(el)
        await resumeAudio()
        const src = await pickAudioSource(path)
        if (myGen !== gen.current) return
        el.src = src
      }
      await el.play()
    } catch (err) {
      if (myGen !== gen.current) return
      const e = err as DOMException
      if (e?.name === 'AbortError') return // superseded by a newer load
      if (e?.name === 'NotAllowedError') {
        intent.current = 'pause'
        setPhase('paused')
        return
      }
      fail('Playback failed', e?.message || String(err))
    }
  }, [attachVideo, fail, hasVideo, path])

  const stop = useCallback(() => {
    gen.current++
    intent.current = 'pause'
    retries.current = 0
    clearTimeout(retryTimer.current)
    const el = elRef.current
    el?.pause()
    // A live HTTP stream keeps downloading while paused and would resume
    // minutes stale: drop the connection and rejoin at the live edge.
    if (!hasVideo) teardown()
    setPhase('paused')
  }, [hasVideo, teardown])

  const toggle = useCallback(() => {
    if (intent.current === 'play') stop()
    else void start()
  }, [start, stop])

  const retry = useCallback(() => {
    retries.current = 0
    if (hasVideo) teardown()
    void start()
  }, [hasVideo, start, teardown])

  const reconnect = useCallback((why: string) => {
    if (intent.current !== 'play') return
    if (retries.current >= MAX_RETRIES) { fail('Connection lost', `${why}. Gave up after ${MAX_RETRIES} attempts.`); return }
    const delay = Math.min(8000, 1000 * 2 ** retries.current)
    retries.current++
    setPhase('reconnecting')
    gen.current++
    teardown()
    retryTimer.current = setTimeout(() => void start(), delay)
  }, [fail, start, teardown])

  const setMode = useCallback((m: Mode) => {
    if (m === modeRef.current) return
    modeRef.current = m
    setModeState(m)
    const wasPlaying = intent.current === 'play'
    gen.current++
    teardown()
    if (hasVideo) void attachVideo()
    if (wasPlaying) void start()
  }, [attachVideo, hasVideo, start, teardown])

  // Element events.
  const bind = useCallback((el: HTMLMediaElement | null) => {
    elRef.current = el
  }, [])
  useEffect(() => {
    const el = elRef.current
    if (!el) return
    const on = (type: string, fn: () => void) => { el.addEventListener(type, fn); return () => el.removeEventListener(type, fn) }
    const offs = [
      on('playing', () => { retries.current = 0; setError(null); setPhase('playing') }),
      on('waiting', () => { if (intent.current === 'play') setPhase('buffering') }),
      on('stalled', () => { if (intent.current === 'play' && el.readyState < 3) setPhase('buffering') }),
      on('pause', () => {
        // Native pause (media key, headset unplugged, OS control).
        if (intent.current === 'play' && !el.ended && (el.getAttribute('src') !== null || el.srcObject)) { intent.current = 'pause'; setPhase('paused') }
      }),
      on('ended', () => reconnect('The stream ended')),
      on('error', () => {
        if (intent.current !== 'play' || (!el.getAttribute('src') && !el.srcObject)) return
        const err = el.error
        const detail = err ? `${MEDIA_ERRORS[err.code] ?? `code=${err.code}`}${err.message ? `: ${err.message}` : ''}` : undefined
        // Format and decode problems will not fix themselves; network ones may.
        if (err && (err.code === 3 || err.code === 4) && hasVideo) fail('This browser cannot play the stream', detail)
        else reconnect(detail ?? 'The stream failed')
      }),
      on('suspend', () => {
        // suspend also fires for "enough data", so only flag the iOS case
        // where nothing was ever loaded and no source is acceptable.
        if (el.readyState === 0 && el.networkState === 3 && intent.current === 'play' && hasVideo) {
          fail('The browser rejected the stream', 'NO_SOURCE (browser rejected the playlist)')
        }
      }),
    ]
    return () => offs.forEach((f) => f())
  }, [fail, hasVideo, reconnect])

  // Video mounts attach on mount.
  useEffect(() => {
    if (hasVideo) void attachVideo()
  }, [attachVideo, hasVideo])

  // Release the hls.js worker and the peer connection when the page goes.
  useEffect(() => () => { gen.current++; teardown() }, [teardown])

  // Volume. Prefer the element's own volume (what a test or the OS mixer
  // sees); fall back to the gain node where volume is read-only (iOS).
  const effective = muted ? 0 : volume / 100
  useEffect(() => {
    const el = elRef.current
    if (!el) return
    if (elementVolumeWritable()) {
      el.volume = effective
      const g = getGraph()
      if (g) g.gain.gain.value = 1
    } else {
      const g = getGraph()
      if (g) g.gain.gain.value = effective
    }
  }, [effective, phase])

  const setVolume = useCallback((v: number) => {
    const c = Math.round(Math.min(100, Math.max(0, v)))
    setVolumeState(c)
    setMuted(false)
    try { localStorage.setItem(VOL_KEY, String(c)) } catch { /* ignore */ }
  }, [])
  const toggleMute = useCallback(() => {
    // Unmuting a slider that sits at 0 must still produce sound.
    if (muted || volume === 0) { setMuted(false); if (volume === 0) setVolumeState(80) }
    else setMuted(true)
  }, [muted, volume])

  // Session clock: time since the listener most recently pressed play.
  const active = phase === 'playing' || phase === 'buffering'
  useEffect(() => {
    if (!active) { setElapsed(0); return }
    const start = Date.now()
    const id = setInterval(() => setElapsed(Math.floor((Date.now() - start) / 1000)), 1000)
    return () => clearInterval(id)
  }, [active])

  // Playback health once a second while playing.
  useEffect(() => {
    if (phase !== 'playing') return
    const id = setInterval(() => {
      const el = elRef.current as (HTMLMediaElement & { getVideoPlaybackQuality?: () => VideoPlaybackQuality }) | null
      if (!el) return
      let bufferedSec = 0
      try { if (el.buffered.length > 0) bufferedSec = el.buffered.end(el.buffered.length - 1) - el.currentTime } catch { /* ignore */ }
      let dropped = 0, total = 0
      if (typeof el.getVideoPlaybackQuality === 'function') {
        try { const q = el.getVideoPlaybackQuality(); dropped = q.droppedVideoFrames; total = q.totalVideoFrames } catch { /* ignore */ }
      }
      const latency = typeof hlsRef.current?.latency === 'number' ? hlsRef.current.latency : 0
      setStats({ bufferedSec: Math.max(0, bufferedSec), droppedFrames: dropped, totalFrames: total, latencySec: latency })
    }, 1000)
    return () => clearInterval(id)
  }, [phase])

  return { bind, elRef, phase, error, mode, setMode, transport, volume, setVolume, muted, toggleMute, effective, elapsed, stats, toggle, start, stop, retry }
}
