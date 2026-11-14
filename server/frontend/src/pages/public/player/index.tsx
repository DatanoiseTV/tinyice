import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import './icons'
import { page } from '@/lib/api'
import { currentTheme, setTheme } from '@/lib/theme'
import { cn } from '@/lib/cn'
import { Button, Segmented } from '@/ui/controls'
import { Badge, Kbd } from '@/ui/display'
import { Icon, Logo } from '@/ui/icons'
import { Knob, Legend, Panel, Timecode, TransportButton } from '@/ui/console'
import type { PlayerData } from '@/types'
import { audienceLabel, mountPath, parseSong } from './source'
import { useNowPlaying } from './nowPlaying'
import { useAlbumArt } from './art'
import { getGraph } from './audioGraph'
import { usePlayback, type Mode, type Phase } from './usePlayback'
import { Spectrum } from './Spectrum'
import { ShareDialog } from './ShareDialog'
import { StatsPanel } from './StatsPanel'
import { shortVersion } from '@/lib/format'

type Tone = 'live' | 'warn' | 'danger' | 'off'

/** One honest status word for the lamp: it only says LIVE while audio is actually arriving. */
function statusOf(phase: Phase, sourceLive: boolean | undefined): { label: string; tone: Tone } {
  switch (phase) {
    case 'error': return { label: 'Error', tone: 'danger' }
    case 'connecting': return { label: 'Connecting', tone: 'warn' }
    case 'reconnecting': return { label: 'Reconnecting', tone: 'warn' }
    case 'buffering': return { label: 'Buffering', tone: 'warn' }
    case 'playing': return sourceLive === false ? { label: 'Source offline', tone: 'warn' } : { label: 'Live', tone: 'live' }
    default: return sourceLive === false ? { label: 'Source offline', tone: 'off' } : { label: 'On air', tone: 'live' }
  }
}

const lampClass: Record<Tone, string> = { live: 'bg-live tally', warn: 'bg-warn', danger: 'bg-danger', off: 'bg-line-strong' }
const toneText: Record<Tone, string> = { live: 'text-live', warn: 'text-warn', danger: 'text-danger', off: 'text-faint' }

function Status({ tone, label }: { tone: Tone; label: string }) {
  return (
    <span className="inline-flex items-center gap-1.5" role="status" aria-live="polite">
      <span className={cn('size-2 rounded-full', lampClass[tone])} aria-hidden />
      <Legend className={toneText[tone]}>{label}</Legend>
    </span>
  )
}

function initialMode(): Mode {
  const q = new URLSearchParams(window.location.search)
  // ?webrtc=1 is the original switch; ?mode=webrtc is what /player-webrtc/ redirects to.
  return q.get('webrtc') === '1' || q.get('mode') === 'webrtc' ? 'webrtc' : 'http'
}

function useFullscreen(target: React.RefObject<HTMLElement | null>, media: React.RefObject<HTMLMediaElement | null>) {
  const [on, setOn] = useState(false)
  useEffect(() => {
    const h = () => setOn(!!document.fullscreenElement)
    document.addEventListener('fullscreenchange', h)
    return () => document.removeEventListener('fullscreenchange', h)
  }, [])
  const toggle = useCallback(() => {
    if (document.fullscreenElement) { void document.exitFullscreen(); return }
    const t = target.current
    if (t?.requestFullscreen) void t.requestFullscreen().catch(() => {})
    else (media.current as unknown as { webkitEnterFullscreen?: () => void })?.webkitEnterFullscreen?.() // iOS Safari: video element only
  }, [media, target])
  return { on, toggle }
}

export function Player() {
  const data = page<Partial<PlayerData>>()
  const mount = data.mount ?? ''
  const hasVideo = !!data.hasVideo
  const hasHLS = data.hasHLS
  const playback = usePlayback({ mount, hasVideo, hasHLS: hasHLS !== false, initialMode: initialMode() })
  const { phase, error, mode, setMode, volume, setVolume, muted, toggleMute, elapsed, toggle, retry, elRef } = playback

  const { song, name, audience, sourceLive, source } = useNowPlaying(data)
  const [showStats, setShowStats] = useState(false)
  const [share, setShare] = useState(false)
  const [theme, setThemeState] = useState(currentTheme())

  const { artist: songArtist, track } = useMemo(() => parseSong(song), [song])
  const art = useAlbumArt(songArtist, track)
  const [artOk, setArtOk] = useState(true)
  const [artLoaded, setArtLoaded] = useState(false)
  useEffect(() => { setArtOk(true); setArtLoaded(false) }, [art])

  useEffect(() => { if (song) document.title = `${track || song} - ${name || 'TinyIce'}` }, [song, track, name])

  // Lock-screen / headset / notification controls.
  useEffect(() => {
    if (!('mediaSession' in navigator)) return
    const ms = navigator.mediaSession
    ms.metadata = new MediaMetadata({
      title: track || song || name || 'Live stream',
      artist: songArtist || name,
      album: name,
      artwork: art && artOk ? [{ src: art, sizes: '500x500' }] : [{ src: data.branding?.logoUrl || '/assets/logo.png', sizes: '512x512' }],
    })
  }, [song, track, songArtist, name, art, artOk])
  useEffect(() => {
    if (!('mediaSession' in navigator)) return
    const ms = navigator.mediaSession
    ms.playbackState = phase === 'playing' || phase === 'buffering' ? 'playing' : 'paused'
    ms.setActionHandler('play', () => { if (phase !== 'playing') playback.start() })
    ms.setActionHandler('pause', () => playback.stop())
    ms.setActionHandler('stop', () => playback.stop())
    return () => { for (const a of ['play', 'pause', 'stop'] as const) ms.setActionHandler(a, null) }
  }, [phase, playback])

  const stageRef = useRef<HTMLDivElement>(null)
  const fs = useFullscreen(stageRef, elRef)

  // Keyboard: space/k play-pause, m mute, up/down volume, f fullscreen, s share, i stats.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return
      const t = e.target as HTMLElement | null
      if (t?.closest('input, textarea, select, [role=dialog]')) return
      const onControl = !!t?.closest('button, [role=slider], a')
      switch (e.key) {
        case ' ': case 'k': if (e.key === ' ' && onControl) return; e.preventDefault(); toggle(); break
        case 'm': toggleMute(); break
        case 'ArrowUp': if (onControl && t?.closest('[role=slider]')) return; e.preventDefault(); setVolume(volume + 5); break
        case 'ArrowDown': if (onControl && t?.closest('[role=slider]')) return; e.preventDefault(); setVolume(volume - 5); break
        case 'f': if (hasVideo) fs.toggle(); break
        case 's': setShare(true); break
        case 'i': setShowStats((v) => !v); break
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [fs, hasVideo, setVolume, toggle, toggleMute, volume])

  if (!mount) {
    return <div className="flex min-h-dvh items-center justify-center p-6 text-muted">No stream selected.</div>
  }

  const status = statusOf(phase, sourceLive)
  const active = phase === 'playing' || phase === 'buffering' || phase === 'connecting' || phase === 'reconnecting'
  const showModeToggle = hasVideo ? hasHLS !== false : /opus|ogg/i.test(data.format ?? '') && data.hasWebRTC !== false
  const meta = (
    <div className="flex flex-wrap items-center gap-2">
      {source.audioCodec !== '-' && <Badge>{source.audioCodec}</Badge>}
      {source.audioKbps > 0 && <Badge className="num font-mono">{source.audioKbps} kbps</Badge>}
      {hasVideo && source.videoWidth > 0 && <Badge tone="info">{source.videoWidth}x{source.videoHeight}</Badge>}
      <span className="inline-flex items-center gap-1.5 text-xs text-muted" title={`${audience} ${audienceLabel(audience, hasVideo)} right now`}>
        <Icon name="headphones" size={14} className="text-faint" />
        <span className="num font-mono text-ink">{audience}</span> {audienceLabel(audience, hasVideo)}
      </span>
    </div>
  )

  const volumeControl = (
    <div className="flex items-center gap-1">
      <TransportButton icon={muted || volume === 0 ? 'mute' : 'volume'} label={muted || volume === 0 ? 'Unmute' : 'Mute'} onClick={toggleMute} active={muted} size="sm" />
      <Knob value={volume / 100} onChange={(v) => setVolume(v * 100)} label="Volume" display={muted ? 'Muted' : `${volume}%`} size={48} defaultValue={0.8} step={0.05} />
    </div>
  )

  const modeToggle = showModeToggle && (
    <Segmented<Mode> label="Transport" value={mode} onChange={setMode}
      options={[{ value: 'http', label: hasVideo ? 'HLS' : 'HTTP' }, { value: 'webrtc', label: 'WebRTC' }]} />
  )

  const errorBanner = error && (
    <div role="alert" className="flex items-start gap-3 rounded-md border border-danger/40 bg-danger/10 p-3">
      <Icon name="alert" className="mt-0.5 shrink-0 text-danger" />
      <div className="min-w-0 flex-1">
        <div className="text-[13px] font-medium text-ink">{error.title}</div>
        {error.detail && <div className="mt-0.5 break-words font-mono text-[11px] text-muted">{error.detail}</div>}
      </div>
      <Button size="sm" onClick={retry}><Icon name="refresh" size={14} />Retry</Button>
    </div>
  )

  const header = (
    <header className="flex h-12 shrink-0 items-center gap-3 border-b border-line px-4 sm:px-5">
      <a href="/" className="flex items-center gap-2.5 rounded-md" aria-label="Home">
        <Logo size={22} />
        <span className="hidden text-[13px] font-semibold text-ink sm:inline">{data.pageTitle || 'TinyIce'}</span>
      </a>
      <div className="ml-auto flex items-center gap-1.5">
        <Button size="sm" onClick={() => setShare(true)}><Icon name="player-share" size={14} /><span className="max-sm:hidden">Share</span></Button>
        <TransportButton icon="player-sliders" label="Stream stats" onClick={() => setShowStats((v) => !v)} active={showStats} size="sm" />
        <TransportButton icon={theme === 'dark' ? 'sun' : 'moon'} label={theme === 'dark' ? 'Light theme' : 'Dark theme'} size="sm"
          onClick={() => { const t = theme === 'dark' ? 'light' : 'dark'; setTheme(t); setThemeState(t) }} />
      </div>
    </header>
  )

  const footer = (
    <footer className="mt-auto flex flex-wrap items-center gap-x-5 gap-y-1.5 border-t border-line px-4 py-3 text-xs text-faint sm:px-5">
      <span className="font-mono">{mountPath(mount)}</span>
      <span className="hidden items-center gap-3 md:flex">
        <span className="inline-flex items-center gap-1.5"><Kbd>Space</Kbd>play</span>
        <span className="inline-flex items-center gap-1.5"><Kbd>M</Kbd>mute</span>
        <span className="inline-flex items-center gap-1.5"><Kbd>Up</Kbd><Kbd>Down</Kbd>volume</span>
        {hasVideo && <span className="inline-flex items-center gap-1.5"><Kbd>F</Kbd>fullscreen</span>}
      </span>
      <span className="ml-auto">Powered by TinyIce {shortVersion(data.version)}</span>
    </footer>
  )

  const titleBlock = (
    <div className="min-w-0">
      <h1 className="break-words text-[clamp(1.5rem,4.2vw,2.5rem)] font-semibold leading-[1.1] tracking-tight text-ink">
        {track || <span className="text-faint">No track information</span>}
      </h1>
      {songArtist && <p className="mt-1.5 break-words text-lg text-muted">{songArtist}</p>}
      {name && <p className="mt-2"><Legend>{name}</Legend></p>}
    </div>
  )

  const shareDialog = <ShareDialog open={share} onOpenChange={setShare} mount={mount} name={name || mount} />
  const statsPanel = showStats && <StatsPanel source={source} playback={playback.stats} transport={playback.transport} isVideo={hasVideo} />

  /* ---------------------------- video layout ---------------------------- */
  if (hasVideo) {
    return (
      <div className="flex min-h-dvh flex-col bg-bg">
        {header}
        <main className="mx-auto flex w-full max-w-[1280px] flex-1 flex-col gap-4 p-4 sm:p-5">
          <div ref={stageRef} className="group relative aspect-video w-full overflow-hidden rounded-lg border border-line bg-black">
            <video
              ref={playback.bind as (el: HTMLVideoElement | null) => void}
              crossOrigin="anonymous" preload="metadata" playsInline aria-label={`${name || mount} video`}
              className="absolute inset-0 size-full bg-black object-contain"
              onClick={toggle}
            />
            {phase !== 'playing' && phase !== 'buffering' && !error && (
              <button onClick={toggle} aria-label="Play" className="absolute inset-0 flex items-center justify-center bg-black/30 transition-colors hover:bg-black/20">
                <span className="flex size-[72px] items-center justify-center rounded-full bg-accent text-accent-fg"><Icon name="play" size={30} /></span>
              </button>
            )}
            {(phase === 'buffering' || phase === 'connecting' || phase === 'reconnecting') && (
              <div className="pointer-events-none absolute inset-0 flex items-center justify-center" aria-hidden>
                <Icon name="refresh" size={36} className="animate-spin text-white/80" />
              </div>
            )}
            {error && <div className="absolute inset-x-3 bottom-3">{errorBanner}</div>}
            <div className="pointer-events-none absolute left-3 top-3 rounded-md bg-black/55 px-2 py-1"><Status tone={status.tone} label={status.label} /></div>
          </div>

          <div className="flex flex-wrap items-center gap-3 rounded-lg border border-line bg-surface p-3">
            <TransportButton icon={active ? 'pause' : 'play'} label={active ? 'Pause' : 'Play'} onClick={toggle} primary size="lg" />
            <div className="min-w-0">
              <Legend>Session</Legend>
              <div className="text-sm text-ink"><Timecode seconds={elapsed} /></div>
            </div>
            <div className="ml-auto flex flex-wrap items-center gap-3">
              {modeToggle}
              {volumeControl}
              <TransportButton icon={fs.on ? 'player-collapse' : 'player-expand'} label={fs.on ? 'Exit fullscreen' : 'Fullscreen'} onClick={fs.toggle} size="sm" />
            </div>
          </div>

          <div className="flex flex-wrap items-start justify-between gap-4">
            {titleBlock}
            {meta}
          </div>
          {statsPanel}
        </main>
        {footer}
        {shareDialog}
      </div>
    )
  }

  /* ---------------------------- audio layout ---------------------------- */
  const analyser = () => getGraph()?.analyser ?? null
  return (
    <div className="flex min-h-dvh flex-col bg-bg">
      {header}
      <audio ref={playback.bind as (el: HTMLAudioElement | null) => void} crossOrigin="anonymous" preload="none" />
      <main className="mx-auto grid w-full max-w-[1080px] flex-1 content-center gap-5 p-4 sm:p-6 lg:grid-cols-[minmax(0,360px)_minmax(0,1fr)] [&>*]:min-w-0">
        <div className="relative mx-auto aspect-square w-full max-w-[240px] overflow-hidden rounded-lg border border-line bg-surface lg:mx-0 lg:max-w-[420px]">
          {art && artOk && (
            <img src={art} alt={`Cover art for ${track}`} onLoad={() => setArtLoaded(true)} onError={() => setArtOk(false)}
              className={cn('absolute inset-0 z-10 size-full object-cover transition-opacity duration-300', artLoaded ? 'opacity-100' : 'opacity-0')} />
          )}
          <div className="flex size-full flex-col justify-between p-4">
              <Legend>Cover art</Legend>
              <div className="flex flex-col items-center gap-4 text-center">
                <Logo size={88} />
                <div className="min-w-0">
                  <div className="truncate text-base font-semibold text-ink">{name || mountPath(mount)}</div>
                  <Legend className="mt-1 block">{mountPath(mount)}</Legend>
                </div>
              </div>
              <span className="text-xs text-faint">{songArtist ? (art ? 'Loading cover...' : 'No cover found for this track.') : 'Covers are matched from "Artist - Title" metadata.'}</span>
          </div>
        </div>

        <div className="flex flex-col gap-4">
          <Panel legend="Now playing" lamp={status.tone} actions={<Status tone={status.tone} label={status.label} />}>
            <div className="space-y-5">
              {titleBlock}
              {meta}
              <Spectrum analyser={analyser} active={phase === 'playing'} className="h-16 w-full" />
              <div className="flex flex-wrap items-center gap-x-5 gap-y-4">
                <TransportButton icon={active ? 'pause' : 'play'} label={active ? 'Pause' : 'Play'} onClick={toggle} primary size="lg" />
                <div className="min-w-[72px]">
                  <Legend>{phase === 'playing' ? 'Listening' : 'Stopped'}</Legend>
                  <div className="text-lg text-ink"><Timecode seconds={elapsed} /></div>
                </div>
                <div className="ml-auto flex flex-wrap items-center gap-4">
                  {modeToggle}
                  {volumeControl}
                </div>
              </div>
              {errorBanner}
              {sourceLive === false && !error && (
                <p className="rounded-md border border-line bg-raised px-3 py-2 text-xs text-muted">
                  The source is not connected right now. Playback starts when it comes back; this page updates on its own.
                </p>
              )}
            </div>
          </Panel>
          {statsPanel}
        </div>
      </main>
      {footer}
      {shareDialog}
    </div>
  )
}
