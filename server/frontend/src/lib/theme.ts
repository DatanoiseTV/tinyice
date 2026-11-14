import { page } from './api'

export type Theme = 'dark' | 'light'
const KEY = 'tinyice-theme'

export function currentTheme(): Theme {
  try {
    const t = localStorage.getItem(KEY)
    if (t === 'light' || t === 'dark') return t
  } catch { /* storage may be unavailable */ }
  return matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
}

export function setTheme(t: Theme) {
  document.documentElement.dataset.theme = t
  try { localStorage.setItem(KEY, t) } catch { /* ignore */ }
}

/** Contrast-pick the text colour that goes on top of the brand accent. */
export function accentForeground(hex: string): string {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex)
  if (!m) return '#0b0c0e'
  const n = parseInt(m[1], 16)
  const lin = (c: number) => { c /= 255; return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4 }
  const L = 0.2126 * lin((n >> 16) & 255) + 0.7152 * lin((n >> 8) & 255) + 0.0722 * lin(n & 255)
  // Pick whichever of near-black and white has the higher WCAG contrast against
  // the accent. They are equal at L = 0.179; a higher threshold (the old 0.4)
  // put white text at 2.9:1 on the default orange where black gives about 7:1.
  const onDark = (L + 0.05) / (0.0045 + 0.05)   // vs #0b0c0e (L of about 0.0045)
  const onLight = 1.05 / (L + 0.05)
  return onDark >= onLight ? '#0b0c0e' : '#ffffff'
}

/** Apply stored theme + the operator's branding accent. Call before first paint. */
export function initTheme() {
  setTheme(currentTheme())
  const accent = page().branding?.accentColor
  if (accent && /^#[0-9a-f]{6}$/i.test(accent)) {
    const r = document.documentElement.style
    r.setProperty('--accent', accent)
    r.setProperty('--accent-fg', accentForeground(accent))
  }
}
