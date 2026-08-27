import { useEffect, useRef, useCallback } from 'preact/hooks'
import { signal } from '@preact/signals'
import { Visualizer } from '@/components/Visualizer'
import { ModeToggle } from '@/components/ModeToggle'
import { VolumeKnob } from '@/components/VolumeKnob'
import { createSSE } from '@/lib/sse'
import { connectAudio, getFrequencyData, resumeAudio } from '@/lib/audio'
import { useAlbumArt } from '@/hooks/useAlbumArt'
import type { PlayerData } from '@/types'

const data = (window.__TINYICE__ ?? {}) as Partial<PlayerData>

const playing = signal(false)
const playbackError = signal('')
const title = signal(data.title || 'Untitled')
const artist = signal(data.artist || 'Unknown Artist')
const mode = signal<'http' | 'webrtc'>('http')
const volume = signal(80)
const listeners = signal(data.listeners || 0)
// Seconds of continuous playback since the user most recently pressed play.
// Resets to 0 on pause so the number always reflects "how long the current
// listening session has been running" rather than page-open time.
const elapsed = signal(0)
const showStats = signal(false)

// Live stream info (refreshed from SSE) — these reflect what the source is
// currently producing, independent of how well our player is keeping up.
type StreamStats = {
  audioCodec: string
  audioBitrateKbps: number
  videoCodec: string
  videoWidth: number
  videoHeight: number
  videoFPS: number
  videoGOP: number
  videoKbps: number
}
const initialBitrate = (() => {
  const v = data.bitrate as unknown
  if (typeof v === 'number') return v > 0 ? v : 0
  if (typeof v === 'string') {
    const n = parseInt(v, 10)
    return Number.isFinite(n) ? n : 0
  }
  return 0
})()
const streamStats = signal<StreamStats>({
  audioCodec: codecFromMime(data.format),
  audioBitrateKbps: initialBitrate,
  videoCodec: data.hasVideo ? 'H264' : '—',
  videoWidth: 0,
  videoHeight: 0,
  videoFPS: 0,
  videoGOP: 0,
  videoKbps: 0,
})

// Client-side playback health (sampled from the <video>/<audio> element
// every second). Buffered seconds is forward-looking; dropped frames is a
// running counter from the browser's playback quality stats; latency is
// (live edge − currentTime) when hls.js exposes it.
type PlaybackStats = {
  bufferedSec: number
  droppedFrames: number
  totalFrames: number
  latencySec: number
  transport: string
}
const playbackStats = signal<PlaybackStats>({
  bufferedSec: 0,
  droppedFrames: 0,
  totalFrames: 0,
  latencySec: 0,
  transport: '—',
})

function formatElapsed(secs: number): string {
  secs = Math.max(0, Math.floor(secs))
  const h = Math.floor(secs / 3600)
  const m = Math.floor((secs % 3600) / 60)
  const s = secs % 60
  const pad = (n: number) => n.toString().padStart(2, '0')
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`
}

function codecFromMime(mime: string | undefined): string {
  if (!mime) return '—'
  const lower = mime.toLowerCase()
  if (lower.includes('opus')) return 'OPUS'
  if (lower.includes('mp3') || lower.includes('mpeg')) return 'MP3'
  if (lower.includes('aac')) return 'AAC'
  if (lower.includes('vorbis')) return 'VORBIS'
  if (lower.includes('flac')) return 'FLAC'
  return mime.toUpperCase()
}

function audienceLabel(n: number, isVideo: boolean): string {
  if (isVideo) return n === 1 ? 'viewer' : 'viewers'
  return n === 1 ? 'listener' : 'listeners'
}

function StatsPanel({ isVideo }: { isVideo: boolean }) {
  const s = streamStats.value
  const p = playbackStats.value
  const droppedPct = p.totalFrames > 0 ? ((p.droppedFrames / p.totalFrames) * 100) : 0
  return (
    <div class="fixed bottom-20 right-5 z-30 w-[280px] rounded-lg border border-border bg-surface-base/95 backdrop-blur p-3 shadow-2xl font-mono text-[11px] text-text-secondary">
      <div class="flex items-center justify-between mb-2">
        <span class="text-[9px] tracking-widest text-text-tertiary uppercase">STREAM STATS</span>
        <button
          onClick={() => (showStats.value = false)}
          class="text-text-tertiary hover:text-text-primary"
          aria-label="Close stats"
        >
          ×
        </button>
      </div>
      <div class="grid grid-cols-2 gap-x-3 gap-y-1.5 tabular-nums">
        <div class="text-text-tertiary">Transport</div><div class="text-text-primary">{p.transport}</div>
        <div class="text-text-tertiary">Audio codec</div><div class="text-text-primary">{s.audioCodec}</div>
        <div class="text-text-tertiary">Audio bitrate</div><div class="text-text-primary">{s.audioBitrateKbps ? `${s.audioBitrateKbps} kbps` : '—'}</div>
        {isVideo && (
          <>
            <div class="text-text-tertiary">Video codec</div><div class="text-text-primary">{s.videoCodec}</div>
            <div class="text-text-tertiary">Resolution</div>
            <div class="text-text-primary">{s.videoWidth ? `${s.videoWidth}×${s.videoHeight}` : '—'}</div>
            <div class="text-text-tertiary">FPS</div>
            <div class="text-text-primary">{s.videoFPS ? s.videoFPS.toFixed(1) : '—'}</div>
            <div class="text-text-tertiary">GOP</div>
            <div class="text-text-primary">{s.videoGOP ? `${s.videoGOP.toFixed(1)} s` : '—'}</div>
            <div class="text-text-tertiary">Video bitrate</div>
            <div class="text-text-primary">{s.videoKbps ? `${s.videoKbps} kbps` : '—'}</div>
          </>
        )}
        <div class="col-span-2 mt-1 mb-0.5 border-t border-border" />
        <div class="text-text-tertiary">Buffer</div><div class="text-text-primary">{p.bufferedSec.toFixed(1)} s</div>
        {isVideo && (
          <>
            <div class="text-text-tertiary">Frames</div>
            <div class="text-text-primary">{p.totalFrames.toLocaleString()}</div>
            <div class="text-text-tertiary">Dropped</div>
            <div class={droppedPct > 1 ? 'text-danger' : 'text-text-primary'}>
              {p.droppedFrames.toLocaleString()}{p.totalFrames ? ` (${droppedPct.toFixed(2)}%)` : ''}
            </div>
            {p.latencySec > 0 && (
              <>
                <div class="text-text-tertiary">Latency</div>
                <div class="text-text-primary">{p.latencySec.toFixed(1)} s</div>
              </>
            )}
          </>
        )}
      </div>
    </div>
  )
}

// hlsRef holds the live hls.js instance so handlePause and unmount can tear
// it down. Import is dynamic (see attachHLS) so audio-only pages don't
// download the hls.js bundle at all.
type HlsLike = {
  loadSource(url: string): void
  attachMedia(el: HTMLMediaElement): void
  destroy(): void
}

// resolveHLSUrl returns master.m3u8 when the mount publishes a variant
// ladder, otherwise playlist.m3u8. We probe via HEAD so a 404 on the
// master endpoint is invisible to the user.
async function resolveHLSUrl(mountPath: string): Promise<string> {
  try {
    const res = await fetch(`${mountPath}/master.m3u8`, { method: 'HEAD' })
    if (res.ok) return `${mountPath}/master.m3u8`
  } catch {
    // Network errors fall through to playlist.m3u8.
  }
  return `${mountPath}/playlist.m3u8`
}

// attachWHEP negotiates a WebRTC viewer session via the server's WHEP
// endpoint and binds the resulting remote stream to the <video>
// element. Returns a cleanup function for unmount, or null if the
// negotiation failed (caller should fall back to HLS).
async function attachWHEP(
  mountPath: string,
  el: HTMLMediaElement,
): Promise<(() => void) | null> {
  if (typeof RTCPeerConnection === 'undefined') return null
  const pc = new RTCPeerConnection({
    iceServers: [{ urls: 'stun:stun.l.google.com:19302' }],
  })
  const remote = new MediaStream()
  pc.addTransceiver('video', { direction: 'recvonly' })
  pc.addTransceiver('audio', { direction: 'recvonly' })
  pc.ontrack = (ev) => {
    for (const track of ev.streams[0].getTracks()) remote.addTrack(track)
    el.srcObject = remote
  }
  try {
    const offer = await pc.createOffer()
    await pc.setLocalDescription(offer)
    // Wait for ICE gathering so we can send a complete SDP (server
    // doesn't trickle); short timeout in case a network prevents
    // reaching a STUN server.
    await new Promise<void>((resolve) => {
      if (pc.iceGatheringState === 'complete') return resolve()
      const t = setTimeout(() => resolve(), 2000)
      pc.addEventListener('icegatheringstatechange', () => {
        if (pc.iceGatheringState === 'complete') {
          clearTimeout(t)
          resolve()
        }
      })
    })
    const res = await fetch(`${mountPath}/whep`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/sdp' },
      body: pc.localDescription?.sdp ?? offer.sdp ?? '',
    })
    if (!res.ok) {
      pc.close()
      return null
    }
    const answer = await res.text()
    await pc.setRemoteDescription({ type: 'answer', sdp: answer })
    return () => pc.close()
  } catch {
    pc.close()
    return null
  }
}

async function attachHLS(url: string, el: HTMLMediaElement): Promise<HlsLike | null> {
  // Safari / iOS / tvOS / Android Chrome handle HLS natively — feed the
  // playlist URL straight into the element and skip the 120 KB bundle.
  if (el.canPlayType('application/vnd.apple.mpegurl')) {
    el.src = url
    return null
  }
  const { default: Hls } = await import('hls.js')
  if (!Hls.isSupported()) {
    // Nothing we can do — neither native HLS nor MSE support.
    el.src = url
    return null
  }
  // lowLatencyMode requires LL-HLS tags (#EXT-X-PART / #EXT-X-PART-INF)
  // which we don't emit yet; turning it on with plain short segments
  // makes hls.js chase the live edge too aggressively and the picture
  // judders. liveSyncDuration keeps the player a comfortable few
  // segments behind so we have headroom for jitter; maxBufferLength
  // caps the forward buffer so the DVR window doesn't fill memory.
  const hls = new Hls({
    enableWorker: true,
    liveSyncDuration: 6,
    liveMaxLatencyDuration: 30,
    maxBufferLength: 30,
    backBufferLength: 60,
  })
  hls.loadSource(url)
  hls.attachMedia(el)
  return hls as unknown as HlsLike
}

export function Player() {
  const audioRef = useRef<HTMLAudioElement>(null)
  const analyserRef = useRef<AnalyserNode | null>(null)
  const hlsRef = useRef<HlsLike | null>(null)
  const whepCleanup = useRef<(() => void) | null>(null)
  const albumArt = useAlbumArt(artist.value, title.value)

  const getFreqData = useCallback(() => {
    if (!analyserRef.current) return null
    return getFrequencyData(analyserRef.current)
  }, [])

  useEffect(() => {
    const sse = createSSE('/events')

    sse.on('metadata', (evt) => {
      if (evt.mount === data.mount) {
        title.value = evt.title
        artist.value = evt.artist
      }
    })

    sse.on('stream', (evt) => {
      if (evt.mount !== data.mount) return
      // Audience: prefer viewers (HLS / WHEP) when this is a video
      // mount; otherwise fall through to the raw listener count.
      const audience =
        data.hasVideo && typeof evt.viewers === 'number'
          ? evt.viewers
          : evt.listeners
      listeners.value = audience || 0
      // Bitrate is a string in some payloads ("128") and a number in
      // others; normalise to integer kbps for the UI.
      const audioBitrate =
        typeof evt.bitrate === 'number'
          ? evt.bitrate
          : parseInt(String(evt.bitrate), 10) || 0
      streamStats.value = {
        ...streamStats.value,
        audioBitrateKbps: audioBitrate,
        videoCodec: data.hasVideo ? 'H264' : '—',
        videoWidth: evt.video_width || 0,
        videoHeight: evt.video_height || 0,
        videoFPS: evt.video_fps || 0,
        videoGOP: evt.video_gop || 0,
        videoKbps: evt.video_kbps || 0,
      }
    })

    return () => sse.close()
  }, [])

  useEffect(() => {
    const el = audioRef.current
    if (!el) return
    el.volume = volume.value / 100
  }, [volume.value])



// pickAudioSource swaps to an MP3 sibling mount on browsers that can't
// decode Ogg/Opus (notably Safari and iOS). Tries `-mp3-256`, `-mp3-128`,
// then falls back to the original. When the browser can play Ogg we
// return the original immediately. Auto MP3 siblings are spawned by the
// server when AutoTranscodeMP3Bitrates is configured.
async function pickAudioSource(mountPath: string): Promise<string> {
  const probe = document.createElement('audio')
  const canOgg = probe.canPlayType('audio/ogg; codecs=opus') !== ''
              || probe.canPlayType('audio/ogg; codecs=vorbis') !== ''
              || probe.canPlayType('audio/ogg') !== ''
  if (canOgg) return mountPath
  for (const candidate of [`${mountPath}-mp3-256`, `${mountPath}-mp3-128`, `${mountPath}-128`]) {
    try {
      // Icecast ignores Range and would happily stream forever; we
      // abort as soon as we've read the response headers so the
      // probe doesn't leak a TCP connection per candidate.
      const ctrl = new AbortController()
      const r = await fetch(candidate, { method: 'GET', headers: { Range: 'bytes=0-0' }, signal: ctrl.signal })
      const ct = (r.headers.get('content-type') || '').toLowerCase()
      try { await r.body?.cancel() } catch {}
      ctrl.abort()
      if (r.ok && (ct.startsWith('audio/mpeg') || ct === 'audio/mp3')) return candidate
    } catch {}
  }
  return mountPath
}

  // Track whether we've already attached the HLS / WHEP source to the
  // video element. For video mounts we attach on mount (no user gesture
  // required — the video element accepts a src whenever) so the OS
  // native play button works on mobile too; the custom overlay button
  // simply calls .play(). For audio mounts we still defer src
  // assignment to handlePlay because we want to create the AudioContext
  // and resume it inside the user-gesture call frame for the visualiser.
  const srcAttachedRef = useRef(false)

  const attachVideoSource = useCallback(async () => {
    const el = audioRef.current
    if (!el || srcAttachedRef.current) return
    srcAttachedRef.current = true
    const mountPath = data.mount!.startsWith('/') ? data.mount! : `/${data.mount}`
    const wantsWebRTC = new URLSearchParams(window.location.search).get('webrtc') === '1'
    if (wantsWebRTC) {
      const whep = await attachWHEP(mountPath, el)
      if (whep) {
        whepCleanup.current = whep
        return
      }
    }
    const hlsUrl = await resolveHLSUrl(mountPath)
    hlsRef.current = await attachHLS(hlsUrl, el)
    // iOS Safari with preload="none" sometimes ignores a freshly-set
    // src and leaves the video element in an idle "no segment loaded"
    // state forever — the play() call later then fires onPlay because
    // the element transitions out of HAVE_NOTHING, but nothing is
    // actually buffered (Buffer: 0.0s, Frames: 0 in the stats panel).
    // An explicit .load() kicks the network stack into fetching the
    // playlist immediately, which makes the playback start when the
    // user taps play.
    try { el.load() } catch { /* some browsers throw if no src */ }
  }, [])

  // Attach the HLS / WHEP source on mount for video mounts. Without
  // this, mobile browsers that render the OS-level video controls
  // (Android Chrome, iOS Safari) end up with a play button that does
  // nothing because no src is set — and the custom overlay button can
  // be hard to tap when the native UI takes over the touch layer.
  // Attaching on mount means both buttons just need to call .play().
  useEffect(() => {
    if (!data.hasVideo) return
    void attachVideoSource()
  }, [attachVideoSource])

  const handlePlay = useCallback(async () => {
    const el = audioRef.current
    if (!el) return
    const mountPath = data.mount!.startsWith('/') ? data.mount! : `/${data.mount}`
    if (data.hasVideo) {
      // Source was attached on mount; just kick off playback. No
      // AudioContext routing for video mounts — the visualiser uses
      // the album-art column instead, and routing a video element's
      // audio through createMediaElementSource on iOS Safari makes
      // the device output silent until the page is reloaded.
      await attachVideoSource()
    } else {
      // Audio-only path: AudioContext must be created + resumed in the
      // user-gesture call frame, before .play(), otherwise iOS leaves
      // it suspended and playback is silent until the user clicks
      // again. Order: connect -> resume -> set src -> play.
      if (!analyserRef.current) {
        analyserRef.current = connectAudio(el)
      }
      await resumeAudio()
      el.src = await pickAudioSource(mountPath)
    }
    try {
      await el.play()
    } catch (err) {
      // Autoplay / gesture failure is expected on first click in some
      // environments; leave the user to press again. Propagate anything
      // unexpected to the console for debugging.
      // eslint-disable-next-line no-console
      console.warn('play() failed:', err)
    }
    playing.value = true
  }, [attachVideoSource])

  const handlePause = useCallback(() => {
    const el = audioRef.current
    if (!el) return
    el.pause()
    // Previously we cleared src to ''; some browsers warn on that. Leave
    // the src in place so resume is instant and no console noise fires.
    playing.value = false
  }, [])

  // Tear down the hls.js instance and any WebRTC peer connection on
  // unmount so we don't leak MediaSource workers or keep an ICE
  // session alive for a closed page.
  useEffect(() => {
    return () => {
      if (hlsRef.current) {
        hlsRef.current.destroy()
        hlsRef.current = null
      }
      if (whepCleanup.current) {
        whepCleanup.current()
        whepCleanup.current = null
      }
    }
  }, [])

  // After video has been playing for a few seconds, snapshot a frame and
  // upload it as the stream's poster. The server caches the latest upload
  // per mount and serves it at /{mount}/poster.jpg so landing / explore
  // cards can render a real thumbnail instead of a static placeholder.
  // We only try once per session and only for video streams.
  useEffect(() => {
    if (!data.hasVideo || !playing.value) return
    const el = audioRef.current as HTMLVideoElement | null
    if (!el) return
    const mountPath = data.mount!.startsWith('/') ? data.mount! : `/${data.mount}`
    let done = false
    const t = setTimeout(() => {
      if (done) return
      const vw = el.videoWidth
      const vh = el.videoHeight
      if (!vw || !vh) return
      const scale = Math.min(1, 640 / vw)
      const canvas = document.createElement('canvas')
      canvas.width = Math.round(vw * scale)
      canvas.height = Math.round(vh * scale)
      const ctx = canvas.getContext('2d')
      if (!ctx) return
      try {
        ctx.drawImage(el, 0, 0, canvas.width, canvas.height)
      } catch {
        // drawImage throws if the MediaSource hasn't produced a clean
        // frame yet (crossOrigin / tainted canvas). Nothing to do.
        return
      }
      canvas.toBlob(
        (blob) => {
          if (!blob) return
          fetch(`${mountPath}/poster.jpg`, { method: 'POST', body: blob }).catch(() => {})
          done = true
        },
        'image/jpeg',
        0.82,
      )
    }, 5000)
    return () => clearTimeout(t)
  }, [playing.value])

  // Sample client-side playback health every second while playing.
  // Buffer-ahead seconds, total/dropped frames (HTMLVideoElement only),
  // and HLS live-edge latency. Pauses when not playing so we don't
  // keep ticking after the user hits stop.
  useEffect(() => {
    if (!playing.value) return
    const id = setInterval(() => {
      const el = audioRef.current as HTMLMediaElement & {
        getVideoPlaybackQuality?: () => {
          droppedVideoFrames: number
          totalVideoFrames: number
        }
      } | null
      if (!el) return
      let bufferedSec = 0
      try {
        const ranges = el.buffered
        if (ranges && ranges.length > 0) {
          bufferedSec = ranges.end(ranges.length - 1) - el.currentTime
        }
      } catch { /* ignore */ }
      let dropped = 0
      let total = 0
      if (typeof el.getVideoPlaybackQuality === 'function') {
        try {
          const q = el.getVideoPlaybackQuality()
          dropped = q.droppedVideoFrames
          total = q.totalVideoFrames
        } catch { /* ignore */ }
      }
      let latency = 0
      const hls = hlsRef.current as unknown as { latency?: number } | null
      if (hls && typeof hls.latency === 'number') {
        latency = hls.latency
      }
      const transport = whepCleanup.current ? 'WebRTC' : (data.hasVideo ? 'HLS' : 'Icecast')
      playbackStats.value = {
        bufferedSec: Math.max(0, bufferedSec),
        droppedFrames: dropped,
        totalFrames: total,
        latencySec: latency,
        transport,
      }
    }, 1000)
    return () => clearInterval(id)
  }, [playing.value])

  // Drive the "playing for …" readout from a 1 s interval that only runs
  // while playing=true. We tick seconds locally rather than reading
  // el.currentTime because live Icecast audio streams don't expose a
  // sensible currentTime (and HLS currentTime resets on segment changes).
  useEffect(() => {
    if (!playing.value) {
      elapsed.value = 0
      return
    }
    const start = Date.now()
    const base = elapsed.value
    const id = setInterval(() => {
      elapsed.value = base + Math.floor((Date.now() - start) / 1000)
    }, 1000)
    return () => clearInterval(id)
  }, [playing.value])

  const handleModeChange = useCallback((m: 'http' | 'webrtc') => {
    mode.value = m
  }, [])

  const handleVolumeChange = useCallback((v: number) => {
    volume.value = v
  }, [])

  // Video-first layout: the stream's video element is the hero. The audio
  // vinyl visualizer + mode toggle are hidden because the video already
  // carries the audio track. Keeps just the essentials: title / artist,
  // play+pause overlay, volume, and the standard bottom-strip metadata.
  if (data.hasVideo) {
    return (
      <div class="min-h-screen bg-surface-base relative overflow-hidden flex flex-col">
        {/* Mini nav top-left */}
        <div class="fixed top-0 left-0 z-20 flex items-center gap-3 px-5 py-4">
          <a href="/" class="font-heading text-sm font-bold text-text-primary tracking-tight">
            Ti
          </a>
          <div class="flex items-center gap-1.5">
            <span
              class="w-1.5 h-1.5 rounded-full bg-live"
              style={{ animation: 'pulse-glow 2s ease-in-out infinite' }}
            />
            <span class="font-mono text-[9px] tracking-widest text-live uppercase">LIVE</span>
          </div>
        </div>

        {/* Video surface, 16:9 locked, centred */}
        <main class="flex-1 flex items-center justify-center px-4 pt-16 pb-24">
          <div class="relative w-full max-w-[min(92vw,calc(82vh*16/9))] aspect-video">
            <video
              ref={audioRef as any}
              crossOrigin="anonymous"
              preload="metadata"
              playsInline
              controls
              onPlay={() => { playing.value = true; playbackError.value = '' }}
              onPause={() => { playing.value = false }}
              onStalled={() => {
                playbackError.value = 'STALLED (video element waiting for data)'
              }}
              onSuspend={() => {
                // suspend fires when the browser stops fetching for
                // any reason — including "I have enough data" so we
                // don't surface this unconditionally. Only flag it
                // when nothing has been buffered yet, which is the
                // iOS Safari "silently refused the playlist" symptom.
                const el = audioRef.current
                if (el && el.readyState === 0 && el.networkState === 3 /* NETWORK_NO_SOURCE */) {
                  playbackError.value = 'NO_SOURCE (browser rejected the playlist)'
                }
              }}
              onError={(e) => {
                const err = (e.currentTarget as HTMLVideoElement).error
                const codes: Record<number, string> = {
                  1: 'MEDIA_ERR_ABORTED (user-initiated abort)',
                  2: 'MEDIA_ERR_NETWORK (segment fetch failed)',
                  3: 'MEDIA_ERR_DECODE (codec or bitstream rejected by decoder)',
                  4: 'MEDIA_ERR_SRC_NOT_SUPPORTED (browser cannot play this format)',
                }
                const code = err?.code ?? 0
                const label = codes[code] || `code=${code}`
                const msg = err?.message ? `${label}: ${err.message}` : label
                playbackError.value = msg
                // eslint-disable-next-line no-console
                console.warn('video element error', err, msg)
              }}
              class="absolute inset-0 w-full h-full rounded-xl bg-black shadow-2xl"
            />
            {!playing.value && (
              <button
                onClick={handlePlay}
                class="absolute inset-0 flex items-center justify-center group"
                aria-label="Play"
              >
                <span class="w-20 h-20 rounded-full bg-accent/90 flex items-center justify-center shadow-[0_0_32px_rgba(255,102,0,0.45)] group-hover:scale-105 transition-transform">
                  <svg class="w-10 h-10 text-surface-base ml-1" viewBox="0 0 24 24" fill="currentColor">
                    <path d="M8 5v14l11-7z" />
                  </svg>
                </span>
              </button>
            )}
            {playbackError.value && (
              <div class="absolute left-2 right-2 bottom-2 px-3 py-2 rounded-md bg-red-900/80 border border-red-500/60 text-red-100 font-mono text-[11px] leading-tight pointer-events-none">
                {playbackError.value}
              </div>
            )}
          </div>
        </main>

        {/* Title + volume tray */}
        <div class="fixed bottom-0 inset-x-0 z-20 border-t border-border bg-surface-base/90 backdrop-blur">
          <div class="mx-auto max-w-7xl px-4 py-3 flex flex-wrap items-center gap-x-6 gap-y-2">
            <div class="min-w-0 flex-1">
              <div class="text-sm font-bold text-text-primary truncate">{title}</div>
              <div class="font-mono text-[11px] text-text-tertiary truncate">{artist}</div>
            </div>
            <div class="flex items-center gap-4">
              <span class="font-mono text-[9px] tracking-widest text-text-tertiary/60 uppercase hidden sm:inline">
                {data.mount}
              </span>
              <span class="font-mono text-[9px] tracking-widest text-text-tertiary/60 uppercase">
                {listeners} {audienceLabel(listeners.value, true)}
              </span>
              {playing.value && (
                <span
                  class="font-mono text-[9px] tracking-widest text-accent uppercase tabular-nums"
                  title="Time since you pressed play"
                >
                  ● {formatElapsed(elapsed.value)}
                </span>
              )}
              <button
                onClick={() => (showStats.value = !showStats.value)}
                class={`font-mono text-[9px] tracking-widest uppercase px-2 py-1 rounded border ${showStats.value ? 'border-accent text-accent' : 'border-border text-text-tertiary hover:border-border-hover'}`}
                title="Toggle stream stats"
              >
                STATS
              </button>
              <VolumeKnob value={volume.value} onChange={handleVolumeChange} />
            </div>
          </div>
          {showStats.value && <StatsPanel isVideo={true} />}
        </div>
      </div>
    )
  }

  // Audio-only layout — vinyl visualizer design.
  //
  // pt-16 / pb-24 reserve the space taken by the fixed mini-nav and the
  // fixed bottom strip (both z-20). Without them the centred column runs
  // underneath the bottom strip on short viewports and the strip, being
  // above it, swallowed every click on the volume slider — the control
  // was visible but dead (#53). The video layout above already reserved
  // this space; this one didn't.
  //
  // overflow-x-hidden rather than overflow-hidden: clipping both axes
  // meant that once the column was taller than the viewport, the parts
  // that overflowed could not be scrolled to at all.
  return (
    <div class="min-h-screen bg-surface-base relative overflow-x-hidden flex flex-col items-center justify-center px-4 pt-16 pb-24">
      {/* Dot grid texture */}
      <div
        class="fixed inset-0 pointer-events-none z-0"
        style={{
          backgroundImage: 'radial-gradient(rgba(255,255,255,0.03) 1px, transparent 1px)',
          backgroundSize: '20px 20px',
        }}
      />

      {/* Ambient orange glow behind visualizer */}
      <div
        class="fixed pointer-events-none z-0"
        style={{
          width: '600px',
          height: '600px',
          top: '50%',
          left: '50%',
          transform: 'translate(-50%, -60%)',
          background: 'radial-gradient(ellipse, rgba(255,102,0,0.08) 0%, transparent 70%)',
        }}
      />

      <audio ref={audioRef} crossOrigin="anonymous" preload="none" />

      {/* Mini nav top-left */}
      <div class="fixed top-0 left-0 z-20 flex items-center gap-3 px-5 py-4">
        <a href="/" class="font-heading text-sm font-bold text-text-primary tracking-tight">
          Ti
        </a>
        <div class="flex items-center gap-1.5">
          <span
            class="w-1.5 h-1.5 rounded-full bg-live"
            style={{ animation: 'pulse-glow 2s ease-in-out infinite' }}
          />
          <span class="font-mono text-[9px] tracking-widest text-live uppercase">LIVE</span>
        </div>
      </div>

      {/* Main content */}
      <main class="relative z-10 flex flex-col items-center gap-8">
        {/* Visualizer */}
        <Visualizer size={260} getFreqData={getFreqData} albumArt={albumArt} />

        {/* Track info */}
        <div class="flex flex-col items-center gap-1.5 max-w-xs text-center">
          <h1 class="text-[22px] font-bold text-text-primary leading-tight break-words w-full">
            {title}
          </h1>
          <p class="font-mono text-xs text-text-tertiary break-words w-full">
            {artist}
          </p>
        </div>

        {/* Play / Pause — radio only needs this */}
        <button
          onClick={playing.value ? handlePause : handlePlay}
          class="w-14 h-14 rounded-full bg-accent flex items-center justify-center shadow-[0_0_20px_rgba(255,102,0,0.3)] hover:shadow-[0_0_28px_rgba(255,102,0,0.45)] transition-shadow"
          aria-label={playing.value ? 'Pause' : 'Play'}
        >
          {playing.value ? (
            <svg class="w-6 h-6 text-surface-base" viewBox="0 0 24 24" fill="currentColor">
              <path d="M6 19h4V5H6v14zm8-14v14h4V5h-4z" />
            </svg>
          ) : (
            <svg class="w-6 h-6 text-surface-base ml-0.5" viewBox="0 0 24 24" fill="currentColor">
              <path d="M8 5v14l11-7z" />
            </svg>
          )}
        </button>

        {/* Mode toggle */}
        {data.hasWebRTC && (
          <ModeToggle mode={mode.value} onChange={handleModeChange} />
        )}

        {/* Volume */}
        <VolumeKnob value={volume.value} onChange={handleVolumeChange} />
      </main>

      {/* Bottom strip */}
      <div class="fixed bottom-0 inset-x-0 z-20 border-t border-border">
        <div class="mx-auto max-w-7xl px-4 py-3 flex items-center justify-center gap-8">
          <span class="font-mono text-[9px] tracking-widest text-text-tertiary/50 uppercase">
            {data.mount}
          </span>
          <span class="font-mono text-[9px] tracking-widest text-text-tertiary/50 uppercase">
            {data.bitrate}kbps {data.format}
          </span>
          <span class="font-mono text-[9px] tracking-widest text-text-tertiary/50 uppercase">
            {listeners} {audienceLabel(listeners.value, false)}
          </span>
          {playing.value && (
            <span
              class="font-mono text-[9px] tracking-widest text-accent uppercase tabular-nums"
              title="Time since you pressed play"
            >
              ● {formatElapsed(elapsed.value)}
            </span>
          )}
          <button
            onClick={() => (showStats.value = !showStats.value)}
            class={`font-mono text-[9px] tracking-widest uppercase px-2 py-0.5 rounded border ${showStats.value ? 'border-accent text-accent' : 'border-border text-text-tertiary hover:border-border-hover'}`}
            title="Toggle stream stats"
          >
            STATS
          </button>
        </div>
      </div>
      {showStats.value && <StatsPanel isVideo={false} />}
    </div>
  )
}
