/**
 * Web Audio graph for the audio-only player: element -> analyser -> gain ->
 * destination. The analyser feeds the spectrum; the gain node is the volume
 * control on platforms where HTMLMediaElement.volume is read-only (iOS).
 *
 * createMediaElementSource may be called once per element, so the graph is
 * kept in module state. It must be created inside the user-gesture call
 * frame, otherwise iOS leaves the context suspended and playback is silent.
 */
interface Graph { ctx: AudioContext; analyser: AnalyserNode; gain: GainNode; el: HTMLMediaElement }
let graph: Graph | null = null

export function connectAudio(el: HTMLMediaElement): Graph | null {
  if (graph?.el === el) return graph
  if (graph) return null // a different element already owns the context
  try {
    const Ctx = window.AudioContext ?? (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext
    const ctx = new Ctx()
    const source = ctx.createMediaElementSource(el)
    const analyser = ctx.createAnalyser()
    analyser.fftSize = 256
    analyser.smoothingTimeConstant = 0.78
    const gain = ctx.createGain()
    source.connect(analyser)
    analyser.connect(gain)
    gain.connect(ctx.destination)
    graph = { ctx, analyser, gain, el }
    return graph
  } catch {
    return null
  }
}

export function getGraph(): Graph | null {
  return graph
}

export async function resumeAudio(): Promise<void> {
  try { await graph?.ctx.resume() } catch { /* resume is best effort */ }
}

/** True when assigning HTMLMediaElement.volume actually sticks (false on iOS). */
let volumeWritable: boolean | null = null
export function elementVolumeWritable(): boolean {
  if (volumeWritable === null) {
    const a = document.createElement('audio')
    a.volume = 0.5
    volumeWritable = a.volume === 0.5
  }
  return volumeWritable
}
