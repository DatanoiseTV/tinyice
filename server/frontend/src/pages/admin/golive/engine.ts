/**
 * Browser broadcast engine for the Go Live page.
 *
 * Lifecycle: idle -> arming -> armed (input open, meters live, nothing sent)
 * -> connecting -> live <-> reconnecting. Every audio source, whatever its
 * origin, is routed through one WebAudio graph and the *processed* audio track
 * is what gets published, so the input gain and limiter shown to the operator
 * are the ones the listeners hear:
 *
 *   sources -> input(stereo) -> gain -> [meter taps] -> limiter|bypass -> post -> MediaStreamDestination
 *                                                                          \-> monitor (only while enabled)
 *
 * The engine is a module singleton: a broadcast survives navigating to other
 * admin pages. A page that unmounts while only armed (nothing on air) closes
 * the input again so a microphone is never left open in the background.
 */
import { createFakeSource, fakeDevices, fakeEnabled, type FakeSource } from './fake'

export type SourceKind = 'mic' | 'system' | 'camera' | 'screen' | 'file'
export type Phase = 'idle' | 'arming' | 'armed' | 'connecting' | 'live' | 'reconnecting'
export type Auth = { kind: 'session'; csrf: string } | { kind: 'password'; password: string }
export type PermState = 'prompt' | 'granted' | 'denied' | 'unknown'

export interface CaptureOpts {
  source: SourceKind
  audioDeviceId: string
  videoDeviceId: string
  videoHeight: 480 | 720 | 1080
  /** Browser echo cancellation, noise suppression and AGC. Off: they fight the gain stage and mangle music. */
  voiceProcessing: boolean
  screenMic: boolean
  screenSystemAudio: boolean
  file: File | null
}

export interface LinkStats {
  bytesSent: number
  bitrateKbps: number | null
  rttMs: number | null
  lossPct: number | null
  jitterMs: number | null
  fps: number | null
  width: number
  height: number
  limitation: string
  bitrateHistory: number[]
  rttHistory: number[]
}

export interface Snapshot {
  phase: Phase
  error: string | null
  notice: string | null
  /** Changes whenever the preview video stream is replaced. */
  previewId: number
  hasVideo: boolean
  simulated: boolean
  sampleRate: number
  videoInfo: string
  mount: string | null
  startedAt: number | null
  elapsed: number
  attempt: number
  maxAttempts: number
  retryIn: number | null
  unstable: boolean
  stats: LinkStats
  gainDb: number
  limiter: boolean
  monitor: boolean
  monitorLevel: number
}

export interface Levels { peakL: number; peakR: number; rmsL: number; rmsR: number; clipped: boolean; reductionDb: number }

export const GAIN_MIN_DB = -30
export const GAIN_MAX_DB = 20
const MAX_ATTEMPTS = 5
const RETRY_DELAYS = [2, 4, 8, 15, 30]
const HANDSHAKE_TIMEOUT_MS = 15000
const CONNECT_TIMEOUT_MS = 20000
const DISCONNECT_GRACE_MS = 6000
const HISTORY = 60

const emptyStats = (): LinkStats => ({ bytesSent: 0, bitrateKbps: null, rttMs: null, lossPct: null, jitterMs: null, fps: null, width: 0, height: 0, limitation: '', bitrateHistory: [], rttHistory: [] })
const dbToLin = (db: number) => Math.pow(10, db / 20)

/** Raised for server refusals that retrying cannot fix. */
class FatalPublishError extends Error {}

export function describeMediaError(e: unknown, what: string): string {
  const name = (e as DOMException)?.name ?? ''
  const msg = (e as Error)?.message ?? String(e)
  switch (name) {
    case 'NotAllowedError':
    case 'SecurityError':
      return what === 'screen'
        ? 'Screen sharing was cancelled or blocked. Start it again and pick a window, tab or screen in the browser dialog.'
        : `Access to the ${what} is blocked. Allow it from the lock icon in the address bar, then open the input again.`
    case 'NotFoundError':
    case 'DevicesNotFoundError':
      return `No ${what} was found. Connect one and try again.`
    case 'NotReadableError':
    case 'TrackStartError':
      return `The ${what} could not be opened. Another application may be using it exclusively.`
    case 'OverconstrainedError':
      return `The selected ${what} is no longer available. Pick another device from the list.`
    case 'AbortError':
      return `The ${what} request was interrupted. Try again.`
    default:
      return msg || `Could not open the ${what}.`
  }
}

export function capabilities() {
  const md = typeof navigator !== 'undefined' ? navigator.mediaDevices : undefined
  const caps = typeof RTCRtpSender !== 'undefined' && RTCRtpSender.getCapabilities ? RTCRtpSender.getCapabilities('video') : null
  return {
    secure: typeof window !== 'undefined' && window.isSecureContext,
    capture: !!md?.getUserMedia,
    display: !!md?.getDisplayMedia,
    webrtc: typeof RTCPeerConnection !== 'undefined',
    h264: !!caps?.codecs.some((c) => c.mimeType.toLowerCase() === 'video/h264'),
  }
}

export async function listDevices(): Promise<{ audio: MediaDeviceInfo[]; video: MediaDeviceInfo[] }> {
  if (fakeEnabled()) { const d = fakeDevices(); return { audio: d.filter((x) => x.kind === 'audioinput'), video: d.filter((x) => x.kind === 'videoinput') } }
  if (!navigator.mediaDevices?.enumerateDevices) return { audio: [], video: [] }
  const all = await navigator.mediaDevices.enumerateDevices()
  return { audio: all.filter((d) => d.kind === 'audioinput' && d.deviceId), video: all.filter((d) => d.kind === 'videoinput' && d.deviceId) }
}

/** Permission state where the browser exposes it (Firefox and Safari do not for devices). */
export async function queryPermission(name: 'microphone' | 'camera', onChange: (s: PermState) => void): Promise<() => void> {
  if (fakeEnabled()) { onChange('granted'); return () => {} }
  try {
    const st = await navigator.permissions.query({ name: name as PermissionName })
    const read = () => onChange(st.state === 'granted' ? 'granted' : st.state === 'denied' ? 'denied' : 'prompt')
    read()
    st.addEventListener('change', read)
    return () => st.removeEventListener('change', read)
  } catch {
    onChange('unknown')
    return () => {}
  }
}

function basicHeader(password: string): string {
  const bytes = new TextEncoder().encode(`source:${password}`)
  let bin = ''
  bytes.forEach((b) => { bin += String.fromCharCode(b) })
  return `Basic ${btoa(bin)}`
}

class Broadcaster {
  private snap: Snapshot = {
    phase: 'idle', error: null, notice: null, previewId: 0, hasVideo: false, simulated: false, sampleRate: 0, videoInfo: '',
    mount: null, startedAt: null, elapsed: 0, attempt: 0, maxAttempts: MAX_ATTEMPTS, retryIn: null, unstable: false, stats: emptyStats(),
    gainDb: 0, limiter: true, monitor: false, monitorLevel: 0.4,
  }
  private subs = new Set<() => void>()

  // capture
  private ctx: AudioContext | null = null
  private capturedTracks: MediaStreamTrack[] = []
  private fake: FakeSource | null = null
  private fileEl: HTMLVideoElement | null = null
  private fileUrl = ''
  private out: MediaStream | null = null
  private previewStream: MediaStream | null = null
  private gain: GainNode | null = null
  private bypass: GainNode | null = null
  private limiterGate: GainNode | null = null
  private compressor: DynamicsCompressorNode | null = null
  private monitorGain: GainNode | null = null
  private anL: AnalyserNode | null = null
  private anR: AnalyserNode | null = null
  private bufL = new Float32Array(1024)
  private bufR = new Float32Array(1024)
  private clipLatched = false
  private lastSignalAt = 0

  // publish
  private pc: RTCPeerConnection | null = null
  private gen = 0
  private target: { mount: string; auth: Auth } | null = null
  private abort: AbortController | null = null
  private timers: { handshake?: ReturnType<typeof setTimeout>; connect?: ReturnType<typeof setTimeout>; grace?: ReturnType<typeof setTimeout>; retry?: ReturnType<typeof setTimeout>; tick?: ReturnType<typeof setInterval> } = {}
  private bytesBase = 0
  private lastBytes = 0
  private lastTickAt = 0
  private simBytes = 0
  private origTitle = ''
  private dropAt = 0
  private dropped = false

  // ---- store ----
  subscribe = (fn: () => void) => { this.subs.add(fn); return () => { this.subs.delete(fn) } }
  getSnapshot = () => this.snap
  private set(patch: Partial<Snapshot>) { this.snap = { ...this.snap, ...patch }; this.subs.forEach((f) => f()) }

  get preview(): MediaStream | null { return this.previewStream }
  get busy() { return this.snap.phase !== 'idle' && this.snap.phase !== 'armed' }
  get onAir() { return this.snap.phase === 'connecting' || this.snap.phase === 'live' || this.snap.phase === 'reconnecting' }
  get lastSignal() { return this.lastSignalAt }
  get clipped() { return this.clipLatched }
  clearError() { if (this.snap.error || this.snap.notice) this.set({ error: null, notice: null }) }

  // ---- capture ----
  async arm(o: CaptureOpts): Promise<boolean> {
    if (this.snap.phase !== 'idle') await this.release()
    this.set({ phase: 'arming', error: null, notice: null, monitor: false })
    const fake = fakeEnabled()
    let ctx: AudioContext
    try {
      try { ctx = new AudioContext({ sampleRate: 48000, latencyHint: 'interactive' }) } catch { ctx = new AudioContext() }
    } catch (e) {
      this.set({ phase: 'idle', error: `WebAudio is not available in this browser: ${(e as Error).message}` })
      return false
    }
    this.ctx = ctx
    const audioStreams: MediaStream[] = []
    const videoTracks: MediaStreamTrack[] = []
    let notice: string | null = null
    try {
      await ctx.resume().catch(() => {})
      if (ctx.state !== 'running') notice = 'The browser is holding the audio engine suspended, so meters will stay at zero. Click anywhere on the page, then open the input again.'
      const audioC: MediaTrackConstraints = {
        deviceId: o.audioDeviceId ? { exact: o.audioDeviceId } : undefined,
        echoCancellation: o.voiceProcessing, noiseSuppression: o.voiceProcessing, autoGainControl: o.voiceProcessing,
        channelCount: { ideal: 2 },
      }
      const videoC: MediaTrackConstraints = {
        deviceId: o.videoDeviceId ? { exact: o.videoDeviceId } : undefined,
        height: { ideal: o.videoHeight }, width: { ideal: Math.round((o.videoHeight * 16) / 9) }, frameRate: { ideal: 30 },
      }
      let fileSource: MediaElementAudioSourceNode | null = null

      if (fake) {
        this.fake = createFakeSource(fake, o.source === 'camera' || o.source === 'screen' || o.source === 'file', o.source === 'file')
        audioStreams.push(this.fake.audio)
        this.fake.video?.getVideoTracks().forEach((t) => videoTracks.push(t))
      } else if (!navigator.mediaDevices) {
        throw new DOMException('Browser capture needs a secure context.', 'SecurityError')
      } else if (o.source === 'mic') {
        const s = await navigator.mediaDevices.getUserMedia({ audio: audioC })
        this.capturedTracks.push(...s.getTracks()); audioStreams.push(s)
      } else if (o.source === 'system') {
        const s = await navigator.mediaDevices.getDisplayMedia({ video: true, audio: { echoCancellation: false, noiseSuppression: false, autoGainControl: false } as MediaTrackConstraints })
        s.getVideoTracks().forEach((t) => t.stop())
        if (s.getAudioTracks().length === 0) {
          s.getTracks().forEach((t) => t.stop())
          throw new Error('No audio was shared. In the browser dialog choose a tab or screen and tick "Share audio". System audio capture is only offered by Chrome and Edge, and on macOS only for tabs.')
        }
        s.getVideoTracks().forEach((t) => s.removeTrack(t))
        this.capturedTracks.push(...s.getTracks()); audioStreams.push(s)
      } else if (o.source === 'camera') {
        let s: MediaStream
        try {
          s = await navigator.mediaDevices.getUserMedia({ audio: audioC, video: videoC })
        } catch (e) {
          if ((e as DOMException).name !== 'NotFoundError') throw e
          // A camera without a microphone: send the picture rather than nothing.
          s = await navigator.mediaDevices.getUserMedia({ video: videoC })
          notice = 'No microphone was found, so the camera is going out without sound.'
        }
        this.capturedTracks.push(...s.getTracks())
        if (s.getAudioTracks().length) audioStreams.push(new MediaStream(s.getAudioTracks()))
        s.getVideoTracks().forEach((t) => videoTracks.push(t))
      } else if (o.source === 'screen') {
        const d = await navigator.mediaDevices.getDisplayMedia({ video: { frameRate: 30 }, audio: o.screenSystemAudio ? ({ echoCancellation: false, noiseSuppression: false, autoGainControl: false } as MediaTrackConstraints) : false })
        this.capturedTracks.push(...d.getTracks())
        d.getVideoTracks().forEach((t) => videoTracks.push(t))
        if (d.getAudioTracks().length) audioStreams.push(new MediaStream(d.getAudioTracks()))
        else if (o.screenSystemAudio) notice = 'The screen was shared without system audio. Only the microphone (if enabled) is sent.'
        if (o.screenMic) {
          try {
            const m = await navigator.mediaDevices.getUserMedia({ audio: audioC })
            this.capturedTracks.push(...m.getTracks()); audioStreams.push(m)
          } catch (e) {
            notice = `Microphone unavailable for the screen share: ${describeMediaError(e, 'microphone')}`
          }
        }
      } else if (o.source === 'file') {
        if (!o.file) throw new Error('Choose a video file first.')
        const el = document.createElement('video')
        el.playsInline = true; el.loop = true
        this.fileUrl = URL.createObjectURL(o.file)
        el.src = this.fileUrl
        this.fileEl = el
        // Audio goes through WebAudio (never to the speakers); the picture comes from captureStream.
        fileSource = ctx.createMediaElementSource(el)
        await el.play()
        const cap = (el as HTMLVideoElement & { captureStream?: () => MediaStream; mozCaptureStream?: () => MediaStream })
        const grab = cap.captureStream ?? cap.mozCaptureStream
        if (!grab) throw new Error('This browser cannot capture a video element.')
        const cs = grab.call(cap)
        cs.getVideoTracks().forEach((t) => videoTracks.push(t))
        if (cs.getVideoTracks().length === 0) notice = 'The file has no video track; only its audio is sent.'
      }

      // ---- graph ----
      const input = ctx.createGain()
      input.channelCount = 2; input.channelCountMode = 'explicit'; input.channelInterpretation = 'speakers'
      for (const s of audioStreams) ctx.createMediaStreamSource(s).connect(input)
      fileSource?.connect(input)
      const gain = ctx.createGain()
      gain.gain.value = dbToLin(this.snap.gainDb)
      input.connect(gain)
      const split = ctx.createChannelSplitter(2)
      gain.connect(split)
      const mkAn = () => { const a = ctx.createAnalyser(); a.fftSize = 1024; a.smoothingTimeConstant = 0; return a }
      const anL = mkAn(), anR = mkAn()
      split.connect(anL, 0); split.connect(anR, 1)
      // Soft limiter: ratio 20 is steep, not a brickwall; the CLIP lamp still reads the pre-limiter signal.
      // Threshold stays >= -3 dB because Chrome adds automatic makeup gain below the threshold.
      const comp = ctx.createDynamicsCompressor()
      comp.threshold.value = -2; comp.knee.value = 0; comp.ratio.value = 20; comp.attack.value = 0.003; comp.release.value = 0.12
      const limiterGate = ctx.createGain(); limiterGate.gain.value = this.snap.limiter ? 1 : 0
      const bypass = ctx.createGain(); bypass.gain.value = this.snap.limiter ? 0 : 1
      const post = ctx.createGain()
      gain.connect(comp); comp.connect(limiterGate); limiterGate.connect(post)
      gain.connect(bypass); bypass.connect(post)
      const dest = ctx.createMediaStreamDestination()
      dest.channelCount = 2; dest.channelCountMode = 'explicit'
      post.connect(dest)
      const monitorGain = ctx.createGain()
      monitorGain.gain.value = this.snap.monitorLevel * this.snap.monitorLevel
      post.connect(monitorGain) // not connected to ctx.destination until the operator enables monitoring

      Object.assign(this, { gain, bypass, limiterGate, compressor: comp, monitorGain, anL, anR })
      this.out = new MediaStream([...dest.stream.getAudioTracks(), ...videoTracks])
      this.previewStream = videoTracks.length ? new MediaStream(videoTracks) : null

      const lost = () => this.onTrackLost()
      for (const t of this.capturedTracks) t.addEventListener('ended', lost)
      const vs = videoTracks[0]?.getSettings()
      this.lastSignalAt = 0; this.clipLatched = false
      this.set({
        phase: 'armed', notice, hasVideo: videoTracks.length > 0, simulated: !!fake, sampleRate: ctx.sampleRate,
        videoInfo: vs?.width ? `${vs.width}x${vs.height}${vs.frameRate ? `@${Math.round(vs.frameRate)}` : ''}` : (fake && videoTracks.length ? '640x360@15' : ''),
        previewId: this.snap.previewId + 1,
      })
      return true
    } catch (e) {
      const what = o.source === 'camera' ? 'camera' : o.source === 'screen' || o.source === 'system' ? 'screen' : o.source === 'file' ? 'file' : 'microphone'
      await this.teardownCapture()
      this.set({ phase: 'idle', error: e instanceof DOMException || (e as Error)?.name ? describeMediaError(e, what) : String(e), hasVideo: false })
      return false
    }
  }

  private onTrackLost() {
    if (this.snap.phase === 'idle') return
    void this.release('The input was disconnected or sharing was stopped from the browser.')
  }

  private async teardownCapture() {
    this.capturedTracks.forEach((t) => t.stop())
    this.capturedTracks = []
    this.fake?.stop(); this.fake = null
    if (this.fileEl) { this.fileEl.pause(); this.fileEl.removeAttribute('src'); this.fileEl.load(); this.fileEl = null }
    if (this.fileUrl) { URL.revokeObjectURL(this.fileUrl); this.fileUrl = '' }
    this.out?.getTracks().forEach((t) => t.stop())
    this.out = null; this.previewStream = null
    const ctx = this.ctx
    this.ctx = null
    Object.assign(this, { gain: null, bypass: null, limiterGate: null, compressor: null, monitorGain: null, anL: null, anR: null })
    if (ctx) await ctx.close().catch(() => {})
  }

  /** Close the input completely (and stop any broadcast). */
  async release(error?: string) {
    this.stopPublishing()
    await this.teardownCapture()
    this.set({ phase: 'idle', error: error ?? null, notice: null, hasVideo: false, monitor: false, previewId: this.snap.previewId + 1, videoInfo: '' })
  }

  // ---- audio controls ----
  setGainDb(db: number) {
    const v = Math.max(GAIN_MIN_DB, Math.min(GAIN_MAX_DB, db))
    if (this.gain && this.ctx) this.gain.gain.setTargetAtTime(dbToLin(v), this.ctx.currentTime, 0.02)
    this.set({ gainDb: v })
  }
  setLimiter(on: boolean) {
    if (this.limiterGate && this.bypass && this.ctx) {
      const t = this.ctx.currentTime
      this.limiterGate.gain.setTargetAtTime(on ? 1 : 0, t, 0.01)
      this.bypass.gain.setTargetAtTime(on ? 0 : 1, t, 0.01)
    }
    this.set({ limiter: on })
  }
  setMonitor(on: boolean) {
    if (!this.monitorGain || !this.ctx) return
    this.monitorGain.disconnect()
    if (on) this.monitorGain.connect(this.ctx.destination)
    this.set({ monitor: on })
  }
  setMonitorLevel(v: number) {
    if (this.monitorGain && this.ctx) this.monitorGain.gain.setTargetAtTime(v * v, this.ctx.currentTime, 0.02)
    this.set({ monitorLevel: v })
  }
  resetClip() { this.clipLatched = false }

  /** Sample peak and RMS of the signal after the gain stage (before the limiter). */
  readLevels(): Levels | null {
    const { anL, anR } = this
    if (!anL || !anR) return null
    if (this.bufL.length !== anL.fftSize) { this.bufL = new Float32Array(anL.fftSize); this.bufR = new Float32Array(anR.fftSize) }
    anL.getFloatTimeDomainData(this.bufL as Float32Array<ArrayBuffer>)
    anR.getFloatTimeDomainData(this.bufR as Float32Array<ArrayBuffer>)
    const m = (b: Float32Array) => {
      let p = 0, s = 0
      for (let i = 0; i < b.length; i++) { const a = Math.abs(b[i]); if (a > p) p = a; s += b[i] * b[i] }
      return [p, Math.sqrt(s / b.length)] as const
    }
    const [peakL, rmsL] = m(this.bufL), [peakR, rmsR] = m(this.bufR)
    const peak = Math.max(peakL, peakR)
    if (peak > 0.003) this.lastSignalAt = Date.now() // about -50 dBFS
    if (peak >= 0.99) this.clipLatched = true
    return { peakL, peakR, rmsL, rmsR, clipped: this.clipLatched, reductionDb: this.compressor?.reduction ?? 0 }
  }

  // ---- publishing ----
  async goLive(mount: string, auth: Auth): Promise<void> {
    if (this.snap.phase !== 'armed' || !this.out) return
    this.target = { mount, auth }
    this.abort = new AbortController()
    this.bytesBase = 0; this.lastBytes = 0; this.simBytes = 0; this.dropped = false
    this.dropAt = Number(new URLSearchParams(window.location.search).get('drop') || 0)
    this.set({ phase: 'connecting', error: null, mount, attempt: 0, retryIn: null, unstable: false, startedAt: null, elapsed: 0, stats: emptyStats() })
    this.startTick()
    window.addEventListener('beforeunload', this.guard)
    try {
      await this.publish()
    } catch (e) {
      if ((e as Error).name !== 'AbortError') this.failPublishing((e as Error).message || 'Broadcast handshake failed.')
    }
  }

  private guard = (e: BeforeUnloadEvent) => { e.preventDefault() }

  private startTick() {
    this.clearTimer('tick')
    this.lastTickAt = Date.now()
    this.timers.tick = setInterval(() => void this.tick(), 1000)
  }

  private clearTimer(k: keyof Broadcaster['timers']) {
    const t = this.timers[k]
    if (t === undefined) return
    if (k === 'tick') clearInterval(t as ReturnType<typeof setInterval>); else clearTimeout(t as ReturnType<typeof setTimeout>)
    delete this.timers[k]
  }

  private closePc() {
    if (this.pc) {
      this.bytesBase += this.lastBytes; this.lastBytes = 0
      const pc = this.pc
      this.pc = null
      pc.onconnectionstatechange = null
      pc.close()
    }
  }

  private async publish(): Promise<void> {
    const t = this.target, out = this.out
    if (!t || !out) return
    const gen = ++this.gen
    this.closePc()
    this.clearTimer('connect'); this.clearTimer('grace')
    const ctrl = this.abort ?? new AbortController()
    this.abort = ctrl
    const headers: Record<string, string> = { 'Content-Type': 'application/json' }
    // A source password wins server-side; otherwise the session cookie plus CSRF token authorise the publish.
    if (t.auth.kind === 'password') headers.Authorization = basicHeader(t.auth.password)
    else headers['X-CSRF-Token'] = t.auth.csrf
    const url = `/webrtc/source-offer?mount=${encodeURIComponent(t.mount)}`

    let body: string
    if (this.snap.simulated) {
      body = JSON.stringify({ type: 'offer', sdp: 'simulated' })
    } else {
      const caps = capabilities()
      if (!caps.webrtc) throw new FatalPublishError('This browser has no WebRTC support.')
      const pc = new RTCPeerConnection({ iceServers: [{ urls: 'stun:stun.l.google.com:19302' }] })
      this.pc = pc
      pc.onconnectionstatechange = () => this.onPcState(pc, gen)
      out.getTracks().forEach((tr) => pc.addTrack(tr, out))
      if (out.getVideoTracks().length) {
        // The server ingests Opus and H.264 only. Offering H.264 alone makes an unsupported browser fail
        // here, with a reason, rather than negotiate a codec the server cannot republish.
        const h264 = RTCRtpSender.getCapabilities('video')?.codecs.filter((c) => c.mimeType.toLowerCase() === 'video/h264') ?? []
        if (!h264.length) throw new FatalPublishError('This browser cannot encode H.264, which the server requires for video. Use Chrome, Edge or Safari, or broadcast audio only.')
        for (const tr of pc.getTransceivers()) if (tr.sender.track?.kind === 'video') tr.setCodecPreferences(h264)
      }
      const offer = await pc.createOffer()
      await pc.setLocalDescription(offer)
      body = JSON.stringify(pc.localDescription)
    }

    // The operator's abort and the handshake timeout are told apart: the first is silent, the second is an error.
    const req = new AbortController()
    const onAbort = () => req.abort()
    ctrl.signal.addEventListener('abort', onAbort)
    let timedOut = false
    this.clearTimer('handshake')
    this.timers.handshake = setTimeout(() => { timedOut = true; req.abort() }, HANDSHAKE_TIMEOUT_MS)
    let res: Response
    try {
      res = await fetch(url, { method: 'POST', body, headers, credentials: 'same-origin', signal: req.signal })
    } catch (e) {
      if (timedOut) throw new Error('The server did not answer the handshake within 15 seconds.')
      throw e
    } finally {
      this.clearTimer('handshake')
      ctrl.signal.removeEventListener('abort', onAbort)
    }
    if (gen !== this.gen) return
    if (!res.ok) {
      const txt = (await res.text().catch(() => '')).trim()
      let msg = txt
      try { msg = (JSON.parse(txt) as { error?: string }).error || txt } catch { /* plain text body */ }
      if (res.status === 401 && t.auth.kind === 'password') throw new FatalPublishError(`The source password for ${t.mount} was rejected. After repeated failures the server also rate-limits this address.`)
      if (res.status === 401 || res.status === 403) throw new FatalPublishError(`Not authorised to broadcast to ${t.mount}. Check that your account has access to this mount and that it is not disabled, then reload the page (your session may have expired).`)
      const err = new Error(msg || `Broadcast handshake failed (HTTP ${res.status}).`)
      if (res.status >= 400 && res.status < 500) throw new FatalPublishError(err.message)
      throw err
    }
    const answer = await res.json()
    if (gen !== this.gen) return
    this.timers.connect = setTimeout(() => this.onConnectTimeout(gen), CONNECT_TIMEOUT_MS)
    if (this.snap.simulated) {
      setTimeout(() => { if (gen === this.gen && this.target) this.onConnected(gen) }, 700)
    } else {
      await this.pc!.setRemoteDescription(answer)
    }
  }

  private onConnectTimeout(gen: number) {
    if (gen !== this.gen) return
    this.handleLoss('The connection did not complete. UDP traffic to the server may be blocked by a firewall.')
  }

  private onPcState(pc: RTCPeerConnection, gen: number) {
    if (gen !== this.gen || pc !== this.pc) return
    const st = pc.connectionState
    if (st === 'connected') this.onConnected(gen)
    else if (st === 'disconnected') {
      // Often transient (Wi-Fi roam): give ICE a few seconds to recover before tearing anything down.
      this.set({ unstable: true })
      this.clearTimer('grace')
      this.timers.grace = setTimeout(() => { if (gen === this.gen) this.handleLoss('The connection to the server was lost.') }, DISCONNECT_GRACE_MS)
    } else if (st === 'failed') {
      this.handleLoss('WebRTC connection failed. Check that UDP traffic to the server is not blocked by a firewall.')
    }
  }

  private onConnected(gen: number) {
    if (gen !== this.gen) return
    this.clearTimer('connect'); this.clearTimer('grace')
    const now = Date.now()
    this.set({ phase: 'live', unstable: false, attempt: 0, retryIn: null, notice: null, startedAt: this.snap.startedAt ?? now })
    this.updateTitle()
  }

  /** Connection dropped while publishing. Never connected: fail. Was live: retry with backoff. */
  private handleLoss(reason: string) {
    if (this.snap.phase === 'connecting') { this.failPublishing(reason); return }
    if (this.snap.phase !== 'live' && this.snap.phase !== 'reconnecting') return
    const attempt = this.snap.attempt + 1
    if (attempt > MAX_ATTEMPTS) {
      this.failPublishing(`${reason} It could not be restored after ${MAX_ATTEMPTS} attempts; the broadcast has stopped.`)
      return
    }
    this.gen++ // ignore events from the connection being replaced
    this.closePc()
    this.clearTimer('connect'); this.clearTimer('grace'); this.clearTimer('retry')
    const delay = RETRY_DELAYS[attempt - 1] ?? 30
    this.set({ phase: 'reconnecting', attempt, retryIn: delay, unstable: true, notice: reason })
    this.updateTitle()
    this.timers.retry = setTimeout(() => {
      this.set({ retryIn: null })
      this.publish().catch((e: Error) => {
        if (e.name === 'AbortError') return
        if (e instanceof FatalPublishError) this.failPublishing(e.message)
        else this.handleLoss(e.message || 'Reconnect failed.')
      })
    }, delay * 1000)
  }

  private async tick() {
    const phase = this.snap.phase
    if (phase !== 'live' && phase !== 'reconnecting' && phase !== 'connecting') return
    const now = Date.now()
    const dt = Math.max(0.2, (now - this.lastTickAt) / 1000)
    this.lastTickAt = now
    const patch: Partial<Snapshot> = {}
    if (this.snap.startedAt) patch.elapsed = Math.floor((now - this.snap.startedAt) / 1000)
    if (phase === 'reconnecting' && this.snap.retryIn !== null) patch.retryIn = Math.max(0, this.snap.retryIn - 1)

    let stats = this.snap.stats
    if (this.snap.simulated) {
      if (phase === 'live') {
        const kbps = (this.snap.hasVideo ? 1150 : 64) * (0.94 + Math.random() * 0.12)
        this.simBytes += (kbps * 1000 / 8) * dt
        const rtt = 18 + Math.random() * 7
        stats = this.nextStats(stats, this.simBytes, kbps, rtt, 0, 2 + Math.random(), this.snap.hasVideo ? 15 : null, this.snap.hasVideo ? 640 : 0, this.snap.hasVideo ? 360 : 0, '')
        if (this.dropAt && !this.dropped && (patch.elapsed ?? 0) >= this.dropAt) { this.dropped = true; this.set({ stats }); this.handleLoss('The connection to the server was lost (simulated drop).'); return }
      }
    } else if (this.pc) {
      try { stats = await this.collect(this.pc, stats, dt) } catch { /* pc closed while polling */ }
    }
    this.set({ ...patch, stats })
    if (this.snap.phase === 'live' || this.snap.phase === 'reconnecting') this.updateTitle()
  }

  private nextStats(prev: LinkStats, bytes: number, kbps: number | null, rtt: number | null, loss: number | null, jitter: number | null, fps: number | null, width: number, height: number, limitation: string): LinkStats {
    return {
      bytesSent: this.bytesBase + bytes, bitrateKbps: kbps, rttMs: rtt, lossPct: loss, jitterMs: jitter, fps, width, height, limitation,
      bitrateHistory: kbps === null ? prev.bitrateHistory : [...prev.bitrateHistory, kbps].slice(-HISTORY),
      rttHistory: rtt === null ? prev.rttHistory : [...prev.rttHistory, rtt].slice(-HISTORY),
    }
  }

  private async collect(pc: RTCPeerConnection, prev: LinkStats, dt: number): Promise<LinkStats> {
    const rep = await pc.getStats()
    let bytes = 0, rtt: number | null = null, pairRtt: number | null = null, loss: number | null = null, jitter: number | null = null
    let fps: number | null = null, width = 0, height = 0, limitation = ''
    rep.forEach((r) => {
      if (r.type === 'outbound-rtp') {
        bytes += r.bytesSent ?? 0
        if (r.kind === 'video') { fps = r.framesPerSecond ?? null; width = r.frameWidth ?? 0; height = r.frameHeight ?? 0; limitation = r.qualityLimitationReason && r.qualityLimitationReason !== 'none' ? r.qualityLimitationReason : '' }
      } else if (r.type === 'remote-inbound-rtp') {
        if (r.roundTripTime != null) rtt = Math.max(rtt ?? 0, r.roundTripTime * 1000)
        if (r.fractionLost != null) loss = Math.max(loss ?? 0, r.fractionLost * 100)
        if (r.jitter != null) jitter = Math.max(jitter ?? 0, r.jitter * 1000)
      } else if (r.type === 'candidate-pair' && r.state === 'succeeded' && r.nominated && r.currentRoundTripTime != null) {
        pairRtt = r.currentRoundTripTime * 1000
      }
    })
    const kbps = this.lastBytes && bytes >= this.lastBytes ? ((bytes - this.lastBytes) * 8) / 1000 / dt : null
    this.lastBytes = bytes
    return this.nextStats(prev, bytes, kbps, rtt ?? pairRtt, loss, jitter, fps, width, height, limitation)
  }

  private updateTitle() {
    if (!this.origTitle) this.origTitle = document.title
    const s = this.snap
    const t = s.elapsed, mm = String(Math.floor(t / 60)).padStart(2, '0'), ss = String(t % 60).padStart(2, '0')
    document.title = `${s.phase === 'live' ? 'ON AIR' : 'RECONNECTING'} ${mm}:${ss} ${s.mount ?? ''} - TinyIce`
  }

  private stopPublishing() {
    this.gen++
    this.abort?.abort(); this.abort = null
    this.clearTimer('handshake'); this.clearTimer('connect'); this.clearTimer('grace'); this.clearTimer('retry'); this.clearTimer('tick')
    this.closePc()
    this.target = null
    window.removeEventListener('beforeunload', this.guard)
    if (this.origTitle) { document.title = this.origTitle; this.origTitle = '' }
  }

  private failPublishing(msg: string) {
    this.stopPublishing()
    if (this.snap.phase === 'idle') return
    this.set({ phase: 'armed', error: msg, notice: null, startedAt: null, elapsed: 0, unstable: false, retryIn: null, attempt: 0, mount: null })
  }

  /** Operator stop: ends the broadcast and leaves the input open. */
  stop() {
    if (!this.onAir) return
    this.stopPublishing()
    this.set({ phase: 'armed', error: null, notice: null, startedAt: null, elapsed: 0, unstable: false, retryIn: null, attempt: 0, mount: null })
  }
}

export const broadcaster = new Broadcaster()

if (import.meta.hot) import.meta.hot.dispose(() => { void broadcaster.release() })
