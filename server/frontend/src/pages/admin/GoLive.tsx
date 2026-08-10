import { useEffect, useRef, useCallback } from 'preact/hooks'
import { signal } from '@preact/signals'
import type { AdminData } from '@/types'

// ── State signals ──────────────────────────────────────────────
const broadcasting = signal(false)
const status = signal<'ready' | 'connecting' | 'live'>('ready')
const selectedMount = signal('/live')
const selectedDeviceId = signal('')
const audioDevices = signal<MediaDeviceInfo[]>([])
const audioPermission = signal<'prompt' | 'granted' | 'denied'>('prompt')
const latency = signal(0)
const durationSec = signal(0)
const connectionFormat = signal('')
const errorMsg = signal('')

// Headroom colour ladder, applied imperatively alongside the text so the
// meters never trigger a render.
function writeHeadroom(el: HTMLElement | null, db: number) {
  if (!el) return
  if (db === -Infinity || !isFinite(db)) {
    el.textContent = '-- dB'
    el.style.color = 'var(--color-text-tertiary)'
    return
  }
  el.textContent = `${db.toFixed(1)} dB`
  el.style.color = db > 6 ? '#22c55e' : db > 3 ? '#eab308' : '#ef4444'
}

function getMounts(): string[] {
  const data = window.__TINYICE__ as AdminData | undefined
  return data?.mounts ?? ['/live']
}

function getCSRFToken(): string {
  const data = window.__TINYICE__ as AdminData | undefined
  return data?.csrfToken ?? ''
}

function formatDuration(sec: number): string {
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  const s = sec % 60
  if (h > 0) return `${h}:${m.toString().padStart(2, '0')}:${s.toString().padStart(2, '0')}`
  return `${m}:${s.toString().padStart(2, '0')}`
}

export function GoLive() {
  const pcRef = useRef<RTCPeerConnection | null>(null)
  const streamRef = useRef<MediaStream | null>(null)
  const analyserRef = useRef<AnalyserNode | null>(null)
  const splitterRef = useRef<ChannelSplitterNode | null>(null)
  const analyserLRef = useRef<AnalyserNode | null>(null)
  const analyserRRef = useRef<AnalyserNode | null>(null)
  const audioCtxRef = useRef<AudioContext | null>(null)
  const rafRef = useRef<number>(0)
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const barsRef = useRef<HTMLDivElement | null>(null)
  const levelLRef = useRef<HTMLDivElement | null>(null)
  const levelRRef = useRef<HTMLDivElement | null>(null)
  const peakLRef = useRef<HTMLDivElement | null>(null)
  const peakRRef = useRef<HTMLDivElement | null>(null)
  const headroomLRef = useRef<HTMLSpanElement | null>(null)
  const headroomRRef = useRef<HTMLSpanElement | null>(null)
  const peakLVal = useRef(0)
  const peakRVal = useRef(0)
  const peakLDecay = useRef(0)
  const peakRDecay = useRef(0)

  async function enumerateAudioDevices() {
    const devices = await navigator.mediaDevices.enumerateDevices()
    audioDevices.value = devices.filter((d) => d.kind === 'audioinput')
    if (audioDevices.value.length > 0 && !selectedDeviceId.value) {
      selectedDeviceId.value = audioDevices.value[0].deviceId
    }
  }

  async function requestAudioPermission() {
    try {
      // getUserMedia triggers the browser permission prompt and unlocks device labels
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
      // Stop the temporary stream immediately — we just needed the permission
      stream.getTracks().forEach((t) => t.stop())
      audioPermission.value = 'granted'
      await enumerateAudioDevices()
    } catch {
      audioPermission.value = 'denied'
    }
  }

  useEffect(() => {
    selectedMount.value = getMounts()[0] || '/live'

    // Check if permission is already granted (e.g. from a previous visit)
    if (navigator.permissions) {
      navigator.permissions.query({ name: 'microphone' as PermissionName }).then((result) => {
        if (result.state === 'granted') {
          audioPermission.value = 'granted'
          enumerateAudioDevices()
        } else {
          audioPermission.value = result.state === 'denied' ? 'denied' : 'prompt'
        }
      }).catch(() => {
        // permissions.query not supported for microphone in some browsers — try enumerate
        enumerateAudioDevices().then(() => {
          // If we got labels, permission was already granted
          if (audioDevices.value.some((d) => d.label)) {
            audioPermission.value = 'granted'
          }
        })
      })
    } else {
      enumerateAudioDevices()
    }

    return () => {
      stopBroadcast()
    }
  }, [])

  const stopBroadcast = useCallback(() => {
    if (pcRef.current) {
      pcRef.current.close()
      pcRef.current = null
    }
    if (streamRef.current) {
      streamRef.current.getTracks().forEach((t) => t.stop())
      streamRef.current = null
    }
    if (audioCtxRef.current) {
      audioCtxRef.current.close()
      audioCtxRef.current = null
    }
    if (rafRef.current) {
      cancelAnimationFrame(rafRef.current)
      rafRef.current = 0
    }
    if (timerRef.current) {
      clearInterval(timerRef.current)
      timerRef.current = null
    }
    analyserRef.current = null
    splitterRef.current = null
    analyserLRef.current = null
    analyserRRef.current = null
    broadcasting.value = false
    status.value = 'ready'
    durationSec.value = 0
    latency.value = 0
    // The meters are DOM-driven; reset them the same way.
    peakLVal.current = 0
    peakRVal.current = 0
    if (levelLRef.current) levelLRef.current.style.width = '0%'
    if (levelRRef.current) levelRRef.current.style.width = '0%'
    if (peakLRef.current) peakLRef.current.style.left = '0%'
    if (peakRRef.current) peakRRef.current.style.left = '0%'
    writeHeadroom(headroomLRef.current, -Infinity)
    writeHeadroom(headroomRRef.current, -Infinity)
  }, [])

  const startBroadcast = useCallback(async () => {
    try {
      errorMsg.value = ''
      status.value = 'connecting'

      // Capture mic
      const constraints: MediaStreamConstraints = {
        audio: selectedDeviceId.value
          ? { deviceId: { exact: selectedDeviceId.value } }
          : true,
      }
      const stream = await navigator.mediaDevices.getUserMedia(constraints)
      streamRef.current = stream

      // Set up Web Audio for analysis
      const audioCtx = new AudioContext()
      audioCtxRef.current = audioCtx
      const source = audioCtx.createMediaStreamSource(stream)

      // Main analyser for spectrum
      const analyser = audioCtx.createAnalyser()
      analyser.fftSize = 64
      analyser.smoothingTimeConstant = 0.8
      source.connect(analyser)
      analyserRef.current = analyser

      // Channel splitter for L/R levels
      const splitter = audioCtx.createChannelSplitter(2)
      source.connect(splitter)
      splitterRef.current = splitter

      const analyserL = audioCtx.createAnalyser()
      analyserL.fftSize = 256
      analyserL.smoothingTimeConstant = 0.8
      splitter.connect(analyserL, 0)
      analyserLRef.current = analyserL

      const analyserR = audioCtx.createAnalyser()
      analyserR.fftSize = 256
      analyserR.smoothingTimeConstant = 0.8
      // If mono, channel 1 may not exist; connect channel 0 as fallback
      try {
        splitter.connect(analyserR, 1)
      } catch {
        splitter.connect(analyserR, 0)
      }
      analyserRRef.current = analyserR

      // WebRTC peer connection
      const pc = new RTCPeerConnection({
        iceServers: [{ urls: 'stun:stun.l.google.com:19302' }],
      })
      pcRef.current = pc

      pc.onconnectionstatechange = () => {
        if (pc.connectionState === 'connected') {
          status.value = 'live'
          broadcasting.value = true
          connectionFormat.value = `WebRTC / Opus / ${audioCtx.sampleRate}Hz`
        } else if (
          pc.connectionState === 'disconnected' ||
          pc.connectionState === 'failed' ||
          pc.connectionState === 'closed'
        ) {
          const failed = pc.connectionState === 'failed'
          stopBroadcast()
          if (failed) {
            errorMsg.value =
              'WebRTC connection failed. Check that UDP traffic to the server is not blocked by a firewall.'
          }
        }
      }

      stream.getTracks().forEach((track) => {
        pc.addTrack(track, stream)
      })

      const offer = await pc.createOffer()
      await pc.setLocalDescription(offer)

      const mount = selectedMount.value
      // The session cookie authenticates us as an admin with access to
      // this mount; X-CSRF-Token is what stops a third-party page from
      // starting a broadcast with that cookie. Without both, the server
      // falls back to demanding the mount's source password.
      const res = await fetch(
        `/webrtc/source-offer?mount=${encodeURIComponent(mount)}`,
        {
          method: 'POST',
          body: JSON.stringify(pc.localDescription),
          headers: {
            'Content-Type': 'application/json',
            'X-CSRF-Token': getCSRFToken(),
          },
          credentials: 'same-origin',
        }
      )

      if (!res.ok) {
        const errText = (await res.text()).trim()
        if (res.status === 401 || res.status === 403) {
          throw new Error(
            `Not authorised to broadcast to ${mount}. Check that your account has access to this mount, then reload the page (your session may have expired).`
          )
        }
        throw new Error(errText || `Broadcast handshake failed (HTTP ${res.status})`)
      }

      const answer = await res.json()
      await pc.setRemoteDescription(answer)

      // Start visualization loop
      const freqData = new Uint8Array(analyser.frequencyBinCount)
      const timeLData = new Uint8Array(analyserL.fftSize)
      const timeRData = new Uint8Array(analyserR.fftSize)

      const tick = () => {
        // Spectrum bars
        analyser.getByteFrequencyData(freqData)
        if (barsRef.current) {
          const bars = barsRef.current.children
          for (let i = 0; i < bars.length; i++) {
            const val = i < freqData.length ? freqData[i] / 255 : 0
            ;(bars[i] as HTMLElement).style.transform = `scaleY(${Math.max(0.05, val)})`
          }
        }

        // Level meters
        analyserL.getByteTimeDomainData(timeLData)
        analyserR.getByteTimeDomainData(timeRData)

        let sumL = 0
        let sumR = 0
        for (let i = 0; i < timeLData.length; i++) {
          const sL = (timeLData[i] - 128) / 128
          const sR = (timeRData[i] - 128) / 128
          sumL += sL * sL
          sumR += sR * sR
        }
        const rmsL = Math.sqrt(sumL / timeLData.length)
        const rmsR = Math.sqrt(sumR / timeRData.length)
        // Everything below is written straight to the DOM through refs
        // rather than to signals. Signals read during render would
        // re-render the whole page 60 times a second while live, which
        // on a phone is enough work per frame to show up as jank in the
        // animated LIVE badge and broadcast button.
        const lvlL = Math.min(1, rmsL * 3)
        const lvlR = Math.min(1, rmsR * 3)

        // Headroom (dB before clipping)
        const hrL = rmsL > 0 ? 20 * Math.log10(1 / rmsL) : -Infinity
        const hrR = rmsR > 0 ? 20 * Math.log10(1 / rmsR) : -Infinity

        // Peak hold with slow decay
        if (lvlL >= peakLVal.current) {
          peakLVal.current = lvlL
          peakLDecay.current = 0
        } else {
          peakLDecay.current++
          if (peakLDecay.current > 30) {
            peakLVal.current = Math.max(0, peakLVal.current - 0.005)
          }
        }
        if (lvlR >= peakRVal.current) {
          peakRVal.current = lvlR
          peakRDecay.current = 0
        } else {
          peakRDecay.current++
          if (peakRDecay.current > 30) {
            peakRVal.current = Math.max(0, peakRVal.current - 0.005)
          }
        }

        if (levelLRef.current) levelLRef.current.style.width = `${lvlL * 100}%`
        if (levelRRef.current) levelRRef.current.style.width = `${lvlR * 100}%`
        if (peakLRef.current) peakLRef.current.style.left = `${peakLVal.current * 100}%`
        if (peakRRef.current) peakRRef.current.style.left = `${peakRVal.current * 100}%`
        writeHeadroom(headroomLRef.current, hrL)
        writeHeadroom(headroomRRef.current, hrR)

        rafRef.current = requestAnimationFrame(tick)
      }
      rafRef.current = requestAnimationFrame(tick)

      // Duration timer
      durationSec.value = 0
      timerRef.current = setInterval(() => {
        durationSec.value++
        // Rough latency from stats
        if (pcRef.current) {
          pcRef.current.getStats().then((stats) => {
            stats.forEach((report) => {
              if (report.type === 'candidate-pair' && report.currentRoundTripTime) {
                latency.value = Math.round(report.currentRoundTripTime * 1000)
              }
            })
          })
        }
      }, 1000)
    } catch (err) {
      // Surface the failure. Previously this was console-only, so a
      // rejected handshake looked identical to "the button does
      // nothing" — the button snapped back to GO LIVE with no clue why.
      console.error('GoLive error:', err)
      const msg = err instanceof Error ? err.message : String(err)
      stopBroadcast()
      errorMsg.value = msg
    }
  }, [stopBroadcast])

  const handleToggle = useCallback(() => {
    if (broadcasting.value || status.value === 'connecting') {
      stopBroadcast()
    } else {
      startBroadcast()
    }
  }, [startBroadcast, stopBroadcast])

  const mounts = getMounts()
  const isLive = status.value === 'live'
  const isConnecting = status.value === 'connecting'
  const BAR_COUNT = 32

  return (
    <div class="p-7 max-w-2xl mx-auto">
      {/* Header */}
      <div class="mb-6">
        <div class="font-mono text-[10px] tracking-[2px] text-text-tertiary mb-1">GO LIVE</div>
        <h1 class="text-xl font-heading font-bold text-text-primary">Browser Broadcast</h1>
      </div>

      {/* Status badge */}
      <div class="flex items-center gap-3 mb-6">
        <span
          class={`inline-flex items-center gap-2 px-4 py-2 rounded-full font-mono text-sm font-bold tracking-wider uppercase ${
            isLive
              ? 'bg-danger/20 text-danger'
              : isConnecting
                ? 'bg-accent/20 text-accent'
                : 'bg-surface-overlay text-text-tertiary'
          }`}
          style={isLive ? { animation: 'pulse-live-glow 2s ease-in-out infinite', '--color-live': 'var(--color-danger)' } : undefined}
        >
          <span
            class={`w-2.5 h-2.5 rounded-full ${
              isLive ? 'bg-danger' : isConnecting ? 'bg-accent' : 'bg-text-tertiary'
            }`}
          />
          {isLive ? 'LIVE' : isConnecting ? 'CONNECTING' : 'READY'}
        </span>
      </div>

      {/* Error banner */}
      {errorMsg.value && (
        <div
          role="alert"
          class="mb-6 flex items-start gap-3 rounded-lg border border-danger/30 bg-danger/10 px-4 py-3 text-sm text-danger"
        >
          <svg class="w-4 h-4 mt-0.5 flex-shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="12" cy="12" r="10" /><line x1="12" y1="8" x2="12" y2="12" /><line x1="12" y1="16" x2="12.01" y2="16" />
          </svg>
          <span class="flex-1">{errorMsg.value}</span>
          <button
            onClick={() => { errorMsg.value = '' }}
            class="font-mono text-[10px] tracking-widest uppercase opacity-70 hover:opacity-100"
            aria-label="Dismiss error"
          >
            Dismiss
          </button>
        </div>
      )}

      {/* Mount selector */}
      <div class="space-y-4 mb-6">
        <div>
          <label class="block font-mono text-[9px] tracking-widest text-text-tertiary uppercase mb-2">
            MOUNT POINT
          </label>
          <select
            value={selectedMount.value}
            onChange={(e) => { selectedMount.value = (e.target as HTMLSelectElement).value }}
            disabled={broadcasting.value || isConnecting}
            class="w-full h-10 px-3 rounded-lg bg-surface-overlay border border-border text-sm text-text-primary focus:border-accent outline-none transition-colors disabled:opacity-50"
          >
            {mounts.map((m) => (
              <option key={m} value={m}>{m}</option>
            ))}
          </select>
        </div>

        {/* Input device selector */}
        <div>
          <label class="block font-mono text-[9px] tracking-widest text-text-tertiary uppercase mb-2">
            INPUT DEVICE
          </label>
          {audioPermission.value === 'granted' ? (
            <select
              value={selectedDeviceId.value}
              onChange={(e) => { selectedDeviceId.value = (e.target as HTMLSelectElement).value }}
              disabled={broadcasting.value || isConnecting}
              class="w-full h-10 px-3 rounded-lg bg-surface-overlay border border-border text-sm text-text-primary focus:border-accent outline-none transition-colors disabled:opacity-50"
            >
              {audioDevices.value.map((d, i) => (
                <option key={d.deviceId} value={d.deviceId}>
                  {d.label || `Microphone (Device ${i + 1})`}
                </option>
              ))}
              {audioDevices.value.length === 0 && (
                <option value="">No audio devices found</option>
              )}
            </select>
          ) : audioPermission.value === 'denied' ? (
            <div class="w-full h-10 px-3 rounded-lg bg-danger/10 border border-danger/30 text-sm text-danger flex items-center gap-2">
              <svg class="w-4 h-4 flex-shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <circle cx="12" cy="12" r="10" /><line x1="15" y1="9" x2="9" y2="15" /><line x1="9" y1="9" x2="15" y2="15" />
              </svg>
              Microphone access denied. Check browser permissions.
            </div>
          ) : (
            <button
              onClick={requestAudioPermission}
              class="w-full h-10 px-3 rounded-lg bg-accent/10 border border-accent/30 text-sm text-accent font-mono tracking-wider hover:bg-accent/20 transition-colors flex items-center justify-center gap-2"
            >
              <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M12 2a3 3 0 0 0-3 3v7a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3Z" />
                <path d="M19 10v2a7 7 0 0 1-14 0v-2" />
                <line x1="12" y1="19" x2="12" y2="22" />
              </svg>
              ALLOW MICROPHONE ACCESS
            </button>
          )}
        </div>
      </div>

      {/* Spectrum analyzer */}
      <div class="mb-6">
        <label class="block font-mono text-[9px] tracking-widest text-text-tertiary uppercase mb-2">
          SPECTRUM
        </label>
        <div class="bg-surface-raised rounded-lg border border-border p-4">
          <div ref={barsRef} class="flex items-end gap-[2px] h-24">
            {Array.from({ length: BAR_COUNT }, (_, i) => (
              <div
                key={i}
                class={`flex-1 rounded-sm origin-bottom ${isLive ? 'bg-accent' : 'bg-surface-overlay'}`}
                style={{
                  height: '100%',
                  transform: isLive ? undefined : 'scaleY(0.05)',
                  transition: isLive ? undefined : 'transform 0.3s ease',
                }}
              />
            ))}
          </div>
        </div>
      </div>

      {/* Level meters */}
      <div class="mb-6">
        <label class="block font-mono text-[9px] tracking-widest text-text-tertiary uppercase mb-2">
          LEVELS
        </label>
        <div class="space-y-2">
          <div class="flex items-center gap-3">
            <span class="font-mono text-[10px] text-text-tertiary w-3">L</span>
            <div class="flex-1 h-3 bg-surface-overlay rounded-full overflow-hidden relative">
              <div
                ref={levelLRef}
                class="h-full bg-accent rounded-full transition-[width] duration-75"
                style={{ width: '0%' }}
              />
              <div
                ref={peakLRef}
                class="absolute top-0 h-full w-[2px] bg-text-primary"
                style={{ left: '0%', transition: 'left 0.05s linear' }}
              />
            </div>
            <span
              ref={headroomLRef}
              class="font-mono text-[10px] w-20 text-right"
              style={{ color: 'var(--color-text-tertiary)' }}
            >
              -- dB
            </span>
          </div>
          <div class="flex items-center gap-3">
            <span class="font-mono text-[10px] text-text-tertiary w-3">R</span>
            <div class="flex-1 h-3 bg-surface-overlay rounded-full overflow-hidden relative">
              <div
                ref={levelRRef}
                class="h-full bg-accent rounded-full transition-[width] duration-75"
                style={{ width: '0%' }}
              />
              <div
                ref={peakRRef}
                class="absolute top-0 h-full w-[2px] bg-text-primary"
                style={{ left: '0%', transition: 'left 0.05s linear' }}
              />
            </div>
            <span
              ref={headroomRRef}
              class="font-mono text-[10px] w-20 text-right"
              style={{ color: 'var(--color-text-tertiary)' }}
            >
              -- dB
            </span>
          </div>
        </div>
      </div>

      {/* GO LIVE button */}
      <button
        onClick={handleToggle}
        disabled={isConnecting}
        class={`w-full h-14 rounded-xl font-heading font-bold text-lg tracking-wider uppercase transition-all duration-300 disabled:opacity-50 ${
          isLive
            ? 'bg-danger text-white hover:bg-danger/90'
            : 'bg-accent text-white hover:bg-accent/90'
        }`}
        style={isLive ? { animation: 'pulse-live-glow 2s ease-in-out infinite', '--color-live': 'var(--color-danger)' } : undefined}
      >
        {isLive ? 'STOP BROADCAST' : isConnecting ? 'CONNECTING...' : 'GO LIVE'}
      </button>

      {/* Connection info */}
      {isLive && (
        <div class="mt-6 p-4 rounded-lg bg-surface-raised border border-border">
          <label class="block font-mono text-[9px] tracking-widest text-text-tertiary uppercase mb-3">
            CONNECTION INFO
          </label>
          <div class="grid grid-cols-3 gap-4">
            <div>
              <div class="font-mono text-[10px] text-text-tertiary mb-1">Latency</div>
              <div class="font-mono text-sm text-text-primary">{latency.value}ms</div>
            </div>
            <div>
              <div class="font-mono text-[10px] text-text-tertiary mb-1">Duration</div>
              <div class="font-mono text-sm text-text-primary">{formatDuration(durationSec.value)}</div>
            </div>
            <div>
              <div class="font-mono text-[10px] text-text-tertiary mb-1">Format</div>
              <div class="font-mono text-sm text-text-primary truncate">{connectionFormat.value}</div>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
