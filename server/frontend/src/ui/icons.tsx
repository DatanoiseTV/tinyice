import type { SVGProps } from 'react'

/**
 * TinyIce icon set. Drawn for this product on a 20px grid: 1.5px stroke,
 * round caps and joins, optical weight matched to Inter at 13px. Glyphs that
 * stand for a live signal carry a small "tally" dot, which `lit` tints with
 * the brand accent. Nothing here comes from a third-party icon pack.
 */
interface Glyph { d: string; dot?: [number, number]; fill?: boolean }

const G: Record<string, Glyph> = {
  // navigation
  dashboard: { d: 'M3 3h6v8H3z M11 3h6v4.5h-6z M11 9.5h6V17h-6z M3 13h6v4H3z' },
  streams: { d: 'M6.3 6.3a5.3 5.3 0 0 0 0 7.4 M13.7 6.3a5.3 5.3 0 0 1 0 7.4 M3.6 3.6a9.1 9.1 0 0 0 0 12.8 M16.4 3.6a9.1 9.1 0 0 1 0 12.8', dot: [10, 10] },
  autodj: { d: 'M10 2.5a7.5 7.5 0 1 0 0 15 7.5 7.5 0 0 0 0-15z M4.9 10A5.1 5.1 0 0 1 10 4.9', dot: [10, 10] },
  golive: { d: 'M10 2.5A2.5 2.5 0 0 0 7.5 5v5a2.5 2.5 0 0 0 5 0V5A2.5 2.5 0 0 0 10 2.5z M4.5 9.5a5.5 5.5 0 0 0 11 0 M10 15v2.5 M7 17.5h6' },
  studio: { d: 'M5 3v14 M10 3v14 M15 3v14 M3.2 6.5h3.6v3H3.2z M8.2 11.5h3.6v3H8.2z M13.2 5h3.6v3h-3.6z' },
  relays: { d: 'M4.5 8a2 2 0 1 0 0 4 2 2 0 0 0 0-4z M15.5 8a2 2 0 1 0 0 4 2 2 0 0 0 0-4z M6.5 10h7 M11.5 7.8l2 2.2-2 2.2' },
  transcoders: { d: 'M3 7h12 M12 4l3 3-3 3 M17 13H5 M8 10l-3 3 3 3' },
  users: { d: 'M7.5 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6z M2.5 17c0-3 2.2-5 5-5s5 2 5 5 M13.5 4.2a3 3 0 0 1 0 5.6 M15 12.3c1.6.6 2.5 2.3 2.5 4.7' },
  pending: { d: 'M7 9a3 3 0 1 0 0-6 3 3 0 0 0 0 6z M2.5 17c0-3 2-5 4.5-5 .9 0 1.7.2 2.4.6 M14.5 17.5a3 3 0 1 0 0-6 3 3 0 0 0 0 6z M14.5 13.4v2l1.2.7' },
  security: { d: 'M10 2.5l6 2.2v4.6c0 4-2.6 6.7-6 8.2-3.4-1.5-6-4.2-6-8.2V4.7z M7.3 10l2 2 3.4-3.8' },
  key: { d: 'M6.5 10.5a3 3 0 1 0 0 6 3 3 0 0 0 0-6z M8.7 11.3l7.8-7.8 M13.5 6.5l2 2 M15 5l1.5 1.5' },
  webhook: { d: 'M10 7a2 2 0 1 0 0-4 2 2 0 0 0 0 4z M5 17a2 2 0 1 0 0-4 2 2 0 0 0 0 4z M15 17a2 2 0 1 0 0-4 2 2 0 0 0 0 4z M9.1 6.8L5.9 13.2 M10.9 6.8l3.2 6.4 M7 15h6' },
  settings: { d: 'M3 6h7 M16 6h1 M3 14h1 M10 14h7 M13 3.8a2.2 2.2 0 1 0 0 4.4 2.2 2.2 0 0 0 0-4.4z M7 11.8a2.2 2.2 0 1 0 0 4.4 2.2 2.2 0 0 0 0-4.4z' },
  logout: { d: 'M8 3.5H5A1.5 1.5 0 0 0 3.5 5v10A1.5 1.5 0 0 0 5 16.5h3 M12.5 6.5L16 10l-3.5 3.5 M16 10H8' },
  // chrome
  search: { d: 'M9 14.5a5.5 5.5 0 1 0 0-11 5.5 5.5 0 0 0 0 11z M13 13l4 4' },
  sun: { d: 'M10 6.5a3.5 3.5 0 1 0 0 7 3.5 3.5 0 0 0 0-7z M10 2v2 M10 16v2 M2 10h2 M16 10h2 M4.3 4.3l1.4 1.4 M14.3 14.3l1.4 1.4 M4.3 15.7l1.4-1.4 M14.3 5.7l1.4-1.4' },
  moon: { d: 'M16 11.5A6.5 6.5 0 0 1 8.5 4a6.5 6.5 0 1 0 7.5 7.5z' },
  sidebar: { d: 'M3.5 4.5h13v11h-13z M8 4.5v11' },
  menu: { d: 'M3.5 6h13 M3.5 10h13 M3.5 14h13' },
  check: { d: 'M4.5 10.5l3.5 3.5 7.5-8' },
  x: { d: 'M5 5l10 10 M15 5L5 15' },
  plus: { d: 'M10 4v12 M4 10h12' },
  minus: { d: 'M4 10h12' },
  'chevron-down': { d: 'M5 8l5 5 5-5' },
  'chevron-up': { d: 'M5 12l5-5 5 5' },
  'chevron-right': { d: 'M8 5l5 5-5 5' },
  'chevron-left': { d: 'M12 5l-5 5 5 5' },
  'arrow-up': { d: 'M10 16V4 M5.5 8.5L10 4l4.5 4.5' },
  'arrow-down': { d: 'M10 4v12 M5.5 11.5L10 16l4.5-4.5' },
  more: { d: 'M4.5 10h.01 M10 10h.01 M15.5 10h.01' },
  grip: { d: 'M7.5 5h.01 M12.5 5h.01 M7.5 10h.01 M12.5 10h.01 M7.5 15h.01 M12.5 15h.01' },
  filter: { d: 'M3 5.5h14 M6 10h8 M8.5 14.5h3' },
  list: { d: 'M7 5h10 M7 10h10 M7 15h10 M3.5 5h.01 M3.5 10h.01 M3.5 15h.01' },
  // actions
  trash: { d: 'M4 6h12 M8 6V4h4v2 M5.5 6l.7 10h7.6l.7-10 M8.5 9v4 M11.5 9v4' },
  copy: { d: 'M7 7h9v9H7z M13 7V4.5a.5.5 0 0 0-.5-.5h-8a.5.5 0 0 0-.5.5v8a.5.5 0 0 0 .5.5H7' },
  edit: { d: 'M12.5 4l3.5 3.5L7.5 16H4v-3.5z' },
  refresh: { d: 'M16 10a6 6 0 1 1-1.8-4.3 M16 3.5v3h-3' },
  external: { d: 'M8 4.5H5.5a1 1 0 0 0-1 1v9a1 1 0 0 0 1 1h9a1 1 0 0 0 1-1V12 M11 3.5h5.5V9 M16.5 3.5L9 11' },
  upload: { d: 'M10 13V3.5 M6 7l4-4 4 4 M4 13v2.5a1 1 0 0 0 1 1h10a1 1 0 0 0 1-1V13' },
  download: { d: 'M10 3.5V13 M6 9.5l4 4 4-4 M4 13v2.5a1 1 0 0 0 1 1h10a1 1 0 0 0 1-1V13' },
  link: { d: 'M8.5 11.5a3 3 0 0 0 4.2 0l2.6-2.6a3 3 0 0 0-4.2-4.2l-.8.8 M11.5 8.5a3 3 0 0 0-4.2 0l-2.6 2.6a3 3 0 0 0 4.2 4.2l.8-.8' },
  ban: { d: 'M10 3a7 7 0 1 0 0 14 7 7 0 0 0 0-14z M5.1 5.1l9.8 9.8' },
  eye: { d: 'M2 10s3-5.5 8-5.5S18 10 18 10s-3 5.5-8 5.5S2 10 2 10z M10 12.2a2.2 2.2 0 1 0 0-4.4 2.2 2.2 0 0 0 0 4.4z' },
  lock: { d: 'M5 9h10v7.5H5z M7 9V6.5a3 3 0 0 1 6 0V9' },
  // transport
  play: { d: 'M6.5 4.5v11l9-5.5z', fill: true },
  pause: { d: 'M5.5 4.5h3v11h-3z M11.5 4.5h3v11h-3z', fill: true },
  stop: { d: 'M5 5h10v10H5z', fill: true },
  next: { d: 'M5 4.5v11l8-5.5z M15 4.5v11', fill: true },
  prev: { d: 'M15 4.5v11L7 10z M5 4.5v11', fill: true },
  shuffle: { d: 'M3 6h2.5c3 0 4 8 7 8H17 M3 14h2.5c1.2 0 2-1.3 2.8-2.8 M10.3 8.3C11 7 11.7 6 12.5 6H17 M15 4l2 2-2 2 M15 12l2 2-2 2' },
  loop: { d: 'M5 8V7a2 2 0 0 1 2-2h8 M13 3l2 2-2 2 M15 12v1a2 2 0 0 1-2 2H5 M7 17l-2-2 2-2' },
  volume: { d: 'M4 8v4h3l4 3.5v-11L7 8z M14 7.5a3.6 3.6 0 0 1 0 5 M16.2 5.3a6.6 6.6 0 0 1 0 9.4' },
  mute: { d: 'M4 8v4h3l4 3.5v-11L7 8z M14 8l4 4 M18 8l-4 4' },
  // data / status
  headphones: { d: 'M4 12v-1.5a6 6 0 0 1 12 0V12 M4 12h2.2v4H5a1 1 0 0 1-1-1z M16 12h-2.2v4H15a1 1 0 0 0 1-1z' },
  clock: { d: 'M10 3a7 7 0 1 0 0 14 7 7 0 0 0 0-14z M10 6.5V10l2.5 1.5' },
  alert: { d: 'M10 3.2l7.2 12.8H2.8z M10 8v3.5 M10 14h.01' },
  info: { d: 'M10 3a7 7 0 1 0 0 14 7 7 0 0 0 0-14z M10 9v5 M10 6.3h.01' },
  globe: { d: 'M10 3a7 7 0 1 0 0 14 7 7 0 0 0 0-14z M3 10h14 M10 3c2 2 3 4.5 3 7s-1 5-3 7c-2-2-3-4.5-3-7s1-5 3-7z' },
  pin: { d: 'M10 17.5s5-4.5 5-8.5a5 5 0 0 0-10 0c0 4 5 8.5 5 8.5z M10 10.6a1.8 1.8 0 1 0 0-3.6 1.8 1.8 0 0 0 0 3.6z' },
  folder: { d: 'M3 6.5V15a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8a1 1 0 0 0-1-1h-6L8.5 5H4a1 1 0 0 0-1 1z' },
  music: { d: 'M8 14.5V5l8-1.5V13 M8 14.5a2 2 0 1 1-4 0 2 2 0 0 1 4 0z M16 13a2 2 0 1 1-4 0 2 2 0 0 1 4 0z' },
  doc: { d: 'M5 3.5h7l3 3v10H5z M12 3.5v3h3 M7.5 10h5 M7.5 13h5' },
  code: { d: 'M7 6l-4 4 4 4 M13 6l4 4-4 4' },
  mail: { d: 'M3 5.5h14v9H3z M3 6l7 5 7-5' },
  bolt: { d: 'M11 2.5L4.5 11H10l-1 6.5L15.5 9H10z' },
  cpu: { d: 'M6 6h8v8H6z M8.5 8.5h3v3h-3z M8 3v3 M12 3v3 M8 14v3 M12 14v3 M3 8h3 M3 12h3 M14 8h3 M14 12h3' },
  wave: { d: 'M2.5 10h1 M6 6.5v7 M9.5 3.5v13 M13 7v6 M16.5 8.5v3' },
  video: { d: 'M3 6h9.5v8H3z M12.5 9l4.5-2.5v7L12.5 11' },
  record: { d: 'M10 6a4 4 0 1 0 0 8 4 4 0 0 0 0-8z', fill: true },
}

export type IconName = keyof typeof G | (string & {})

/** Add page-specific glyphs from the page's own module (20px grid, same stroke rules). Never edit G for one-off icons. */
export function registerIcons(extra: Record<string, Glyph>) { Object.assign(G, extra) }

export function Icon({ name, size = 16, lit, solid, className, ...p }: { name: IconName; size?: number; lit?: boolean; solid?: boolean } & Omit<SVGProps<SVGSVGElement>, 'name'>) {
  const g = G[name as string]
  if (!g) return null
  const filled = g.fill && solid !== false
  return (
    <svg
      viewBox="0 0 20 20" width={size} height={size} fill="none" stroke="currentColor" strokeWidth={1.5}
      strokeLinecap="round" strokeLinejoin="round" aria-hidden className={className} {...p}
    >
      <path d={g.d} fill={filled ? 'currentColor' : 'none'} />
      {g.dot && <circle cx={g.dot[0]} cy={g.dot[1]} r="1.25" fill={lit ? 'var(--accent)' : 'currentColor'} stroke="none" />}
    </svg>
  )
}

/** TinyIce mark: a soft-cornered cube cut by a three-bar signal. Scales from 16px to splash size. */
export function Logo({ size = 24, className }: { size?: number; className?: string }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} className={className} aria-hidden>
      <rect width="24" height="24" rx="6" fill="var(--accent)" />
      <path d="M7 14v-3 M10 16V8 M13 18V6 M16 15v-6" stroke="var(--accent-fg)" strokeWidth="2" strokeLinecap="round" fill="none" />
    </svg>
  )
}
