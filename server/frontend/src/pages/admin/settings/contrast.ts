/** WCAG 2.x contrast helpers for the accent picker. */
import { accentForeground } from '@/lib/theme'

export function parseHex(hex: string): [number, number, number] | null {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex.trim())
  if (!m) return null
  const n = parseInt(m[1], 16)
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255]
}

const lin = (c: number) => { c /= 255; return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4 }
export function luminance(hex: string): number {
  const c = parseHex(hex)
  return c ? 0.2126 * lin(c[0]) + 0.7152 * lin(c[1]) + 0.0722 * lin(c[2]) : 0
}
export function ratio(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}

export { accentForeground }

/**
 * The shell's two themes, copied from src/styles/globals.css so the picker can
 * judge the accent against both at once (the stylesheet only exposes the active
 * theme). Keep in sync when the tokens change.
 */
export const THEME_TOKENS = {
  dark: { '--bg': '#0b0c0e', '--surface': '#101216', '--raised': '#15181c', '--hover': '#1a1e23', '--border': '#22262c', '--border-strong': '#2e343c', '--text': '#e9ebee', '--muted': '#a0a7b1', '--faint': '#6c737e' },
  light: { '--bg': '#f5f6f8', '--surface': '#ffffff', '--raised': '#fafbfc', '--hover': '#f0f2f5', '--border': '#e2e5ea', '--border-strong': '#cfd4db', '--text': '#14171a', '--muted': '#545c67', '--faint': '#7d8591' },
} as const

export type Grade = 'AA' | 'UI' | 'fail'
/** AA text needs 4.5, large text and UI components 3. */
export const grade = (r: number): Grade => (r >= 4.5 ? 'AA' : r >= 3 ? 'UI' : 'fail')

export interface ContrastRow { key: string; label: string; use: string; ratio: number; grade: Grade; need: 'text' | 'ui'; pass: boolean }

export function contrastRows(accent: string): ContrastRow[] {
  const fg = accentForeground(accent)
  const mk = (key: string, label: string, use: string, r: number, need: 'text' | 'ui'): ContrastRow => ({ key, label, use, ratio: r, grade: grade(r), need, pass: need === 'text' ? r >= 4.5 : r >= 3 })
  return [
    mk('btn', 'Button label', 'Text on the accent fill', ratio(fg, accent), 'text'),
    mk('dark', 'Accent on dark', 'Links, active tab, focus ring', ratio(accent, THEME_TOKENS.dark['--surface']), 'ui'),
    mk('light', 'Accent on light', 'Links, active tab, focus ring', ratio(accent, THEME_TOKENS.light['--surface']), 'ui'),
  ]
}

/** Set the accent and its foreground on the live document (the same variables initTheme sets). */
export function applyAccent(hex: string) {
  if (!parseHex(hex)) return
  const r = document.documentElement.style
  r.setProperty('--accent', hex)
  r.setProperty('--accent-fg', accentForeground(hex))
}
