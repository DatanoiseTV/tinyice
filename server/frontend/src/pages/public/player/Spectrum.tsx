import { useEffect, useRef } from 'react'

const BARS = 56

/**
 * Flat spectrum monitor. Reads the analyser each frame (or decays to a quiet
 * baseline when there is none), paints in the live theme colours, and pauses
 * while the tab is hidden. Peak caps fall slower than the bars, like a
 * console's peak-hold.
 */
export function Spectrum({ analyser, active, className }: { analyser: () => AnalyserNode | null; active: boolean; className?: string }) {
  const ref = useRef<HTMLCanvasElement>(null)
  const activeRef = useRef(active)
  activeRef.current = active

  useEffect(() => {
    const canvas = ref.current
    if (!canvas) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return
    const level = new Float32Array(BARS)
    const peak = new Float32Array(BARS)
    let bins: Uint8Array<ArrayBuffer> | null = null
    let raf = 0
    let w = 0, h = 0
    let accent = '#ff6a00', dim = '#2e343c'

    const colours = () => {
      const cs = getComputedStyle(document.documentElement)
      accent = cs.getPropertyValue('--accent').trim() || accent
      dim = cs.getPropertyValue('--border-strong').trim() || dim
    }
    const resize = () => {
      const r = canvas.getBoundingClientRect()
      const dpr = window.devicePixelRatio || 1
      w = r.width; h = r.height
      canvas.width = Math.max(1, Math.round(w * dpr))
      canvas.height = Math.max(1, Math.round(h * dpr))
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    }
    const ro = new ResizeObserver(resize)
    ro.observe(canvas)
    const mo = new MutationObserver(colours)
    mo.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme', 'style'] })
    colours(); resize()

    const reduced = matchMedia('(prefers-reduced-motion: reduce)').matches

    const frame = () => {
      raf = requestAnimationFrame(frame)
      if (document.hidden || w === 0) return
      const node = activeRef.current ? analyser() : null
      if (node) {
        if (!bins || bins.length !== node.frequencyBinCount) bins = new Uint8Array(node.frequencyBinCount)
        node.getByteFrequencyData(bins)
        // Log-ish spread: low bins get a few bars each, the top shares many.
        const usable = Math.floor(bins.length * 0.8)
        for (let i = 0; i < BARS; i++) {
          const a = Math.floor(Math.pow(i / BARS, 1.6) * usable)
          const b = Math.max(a + 1, Math.floor(Math.pow((i + 1) / BARS, 1.6) * usable))
          let m = 0
          for (let k = a; k < b; k++) if (bins[k] > m) m = bins[k]
          const v = m / 255
          level[i] = v > level[i] ? v : level[i] * 0.86 + v * 0.14
        }
      } else {
        for (let i = 0; i < BARS; i++) level[i] *= 0.88
      }
      ctx.clearRect(0, 0, w, h)
      const gap = 2
      const bw = Math.max(1, (w - gap * (BARS - 1)) / BARS)
      for (let i = 0; i < BARS; i++) {
        const x = i * (bw + gap)
        const lh = Math.max(2, level[i] * (h - 2))
        peak[i] = lh > peak[i] ? lh : Math.max(2, peak[i] - (reduced ? 6 : 0.7))
        ctx.fillStyle = dim
        ctx.fillRect(x, h - 2, bw, 2)
        if (lh > 2.5) { ctx.fillStyle = accent; ctx.fillRect(x, h - lh, bw, lh) }
        if (peak[i] > 4) { ctx.fillStyle = accent; ctx.globalAlpha = 0.55; ctx.fillRect(x, h - peak[i] - 2, bw, 1.5); ctx.globalAlpha = 1 }
      }
    }
    raf = requestAnimationFrame(frame)
    return () => { cancelAnimationFrame(raf); ro.disconnect(); mo.disconnect() }
  }, [analyser])

  return <canvas ref={ref} className={className} role="img" aria-label="Audio spectrum" />
}
