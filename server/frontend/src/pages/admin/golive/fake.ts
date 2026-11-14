/**
 * Simulated capture for the Go Live page, enabled only by `?fake=1` in a dev
 * build (`import.meta.env.DEV`). Headless Chrome has no microphone, so this
 * generates a stereo speech-like tone/noise signal and a moving canvas as a
 * camera. The generated audio lives in its own AudioContext and is never
 * connected to a destination, so nothing is ever audible. In this mode the
 * engine also skips the real peer connection (see engine.ts `simulated`).
 */

export interface FakeOptions { hot: boolean }

export interface FakeSource {
  audio: MediaStream
  video?: MediaStream
  stop: () => void
}

export function fakeEnabled(): FakeOptions | null {
  if (!import.meta.env.DEV) return null
  const v = new URLSearchParams(window.location.search).get('fake')
  if (v === null) return null
  return { hot: v === 'hot' }
}

/** Fake device list returned in place of enumerateDevices(). */
export function fakeDevices(): MediaDeviceInfo[] {
  const mk = (kind: MediaDeviceKind, deviceId: string, label: string) => ({ kind, deviceId, label, groupId: deviceId, toJSON: () => ({}) }) as MediaDeviceInfo
  return [
    mk('audioinput', 'fake-mic', 'Generated tone (simulated microphone)'),
    mk('audioinput', 'fake-iface', 'Generated noise (simulated 2-channel interface)'),
    mk('videoinput', 'fake-cam', 'Generated pattern (simulated camera)'),
  ]
}

function pinkNoise(ctx: AudioContext): AudioBufferSourceNode {
  const len = ctx.sampleRate * 4
  const buf = ctx.createBuffer(2, len, ctx.sampleRate)
  for (let c = 0; c < 2; c++) {
    const d = buf.getChannelData(c)
    let b0 = 0, b1 = 0, b2 = 0
    for (let i = 0; i < len; i++) {
      const w = Math.random() * 2 - 1
      b0 = 0.99765 * b0 + w * 0.099046
      b1 = 0.963 * b1 + w * 0.2965164
      b2 = 0.57 * b2 + w * 1.0526913
      d[i] = (b0 + b1 + b2 + w * 0.1848) * 0.18
    }
  }
  const src = ctx.createBufferSource()
  src.buffer = buf
  src.loop = true
  return src
}

export function createFakeSource(opts: FakeOptions, wantVideo: boolean, fileLike = false): FakeSource {
  const ctx = new AudioContext({ sampleRate: 48000 })
  const dest = ctx.createMediaStreamDestination()
  dest.channelCount = 2
  const merger = ctx.createChannelMerger(2)
  merger.connect(dest)

  // Left: 196 + 392 Hz, right: 247 + 494 Hz, each with a slow syllable-like
  // envelope, plus pink noise, plus a ~8 s swell so the meters move.
  const envelope = (rate: number, depth: number) => {
    const g = ctx.createGain()
    g.gain.value = 1 - depth
    const lfo = ctx.createOscillator()
    lfo.frequency.value = rate
    const lg = ctx.createGain()
    lg.gain.value = depth
    lfo.connect(lg).connect(g.gain)
    lfo.start()
    return g
  }
  const tone = (f: number, amp: number, to: AudioNode) => {
    const o = ctx.createOscillator()
    o.frequency.value = f
    const g = ctx.createGain()
    g.gain.value = amp
    o.connect(g).connect(to)
    o.start()
  }
  const lOut = ctx.createGain(), rOut = ctx.createGain()
  // Normal mode peaks around -9 dBFS; hot mode runs into the clip lamp.
  lOut.gain.value = opts.hot ? 1.9 : 0.34
  rOut.gain.value = opts.hot ? 1.7 : 0.30
  const swell = ctx.createOscillator()
  swell.frequency.value = 0.13
  const swellDepth = ctx.createGain()
  swellDepth.gain.value = opts.hot ? 0.25 : 0.1
  swell.connect(swellDepth)
  swellDepth.connect(lOut.gain)
  swellDepth.connect(rOut.gain)
  swell.start()
  const lEnv = envelope(3.5, 0.6), rEnv = envelope(2.7, 0.5)
  tone(196, 0.5, lEnv); tone(392, 0.22, lEnv); tone(247, 0.55, rEnv); tone(494, 0.18, rEnv)
  lEnv.connect(lOut); rEnv.connect(rOut)
  const noise = pinkNoise(ctx)
  const nGain = ctx.createGain()
  nGain.gain.value = 0.25
  noise.connect(nGain)
  nGain.connect(lOut); nGain.connect(rOut)
  noise.start()
  lOut.connect(merger, 0, 0)
  rOut.connect(merger, 0, 1)
  void ctx.resume()

  let video: MediaStream | undefined
  let timer: ReturnType<typeof setInterval> | undefined
  if (wantVideo) {
    const cv = document.createElement('canvas')
    cv.width = 640; cv.height = 360
    const g = cv.getContext('2d')!
    const t0 = performance.now()
    const draw = () => {
      const t = (performance.now() - t0) / 1000
      g.fillStyle = '#10151c'; g.fillRect(0, 0, 640, 360)
      for (let i = 0; i < 8; i++) {
        g.fillStyle = `hsl(${(i * 40 + t * 30) % 360} 45% 45%)`
        g.fillRect(i * 80, 0, 80, 220)
      }
      g.fillStyle = '#f2f5f8'
      g.beginPath(); g.arc(320 + Math.sin(t * 1.6) * 220, 290, 26, 0, Math.PI * 2); g.fill()
      g.font = '20px monospace'; g.fillText(fileLike ? 'simulated video file' : 'simulated camera', 16, 344)
    }
    draw()
    timer = setInterval(draw, 66)
    video = cv.captureStream(15)
  }
  return {
    audio: dest.stream,
    video,
    stop() {
      if (timer) clearInterval(timer)
      video?.getTracks().forEach((t) => t.stop())
      dest.stream.getTracks().forEach((t) => t.stop())
      void ctx.close()
    },
  }
}
