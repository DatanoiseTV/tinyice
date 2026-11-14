import type { CSSProperties } from 'react'
import { Badge } from '@/ui/display'
import { Legend } from '@/ui/console'
import { accentForeground, parseHex, THEME_TOKENS } from './contrast'
import type { Branding } from './types'

function Mini({ theme, b, logoSrc }: { theme: 'dark' | 'light'; b: Branding; logoSrc: string | null }) {
  // Scoped token override: the same variables the shell uses, so every class below is a real kit class.
  const accent = parseHex(b.accent_color) ? b.accent_color : '#808080'
  const style = { ...THEME_TOKENS[theme], '--accent': accent, '--accent-fg': accentForeground(accent), '--live': theme === 'dark' ? '#2fbf71' : '#14a15a' } as unknown as CSSProperties
  const name = b.page_title || 'TinyIce'
  return (
    <div style={style} className="overflow-hidden rounded-lg border border-line-strong bg-bg text-ink" aria-label={`${theme} theme preview`} role="img">
      <div className="flex items-center gap-2.5 border-b border-line bg-surface px-3 py-2">
        <span className="flex size-6 items-center justify-center overflow-hidden rounded-md bg-accent text-[10px] font-semibold text-accent-fg">
          {logoSrc ? <img src={logoSrc} alt="" className="size-full object-cover" /> : name.slice(0, 2).toUpperCase()}
        </span>
        <span className="truncate text-xs font-semibold">{name}</span>
        <span className="ml-auto font-mono text-[9px] uppercase tracking-[0.09em] text-faint">{theme}</span>
      </div>
      <div className="space-y-3 p-3">
        <div className="flex gap-4 border-b border-line text-xs">
          <span className="relative pb-2 text-ink after:absolute after:inset-x-0 after:-bottom-px after:h-0.5 after:rounded-full after:bg-accent">Streams</span>
          <span className="pb-2 text-muted">Users</span>
        </div>
        <p className="text-xs text-muted">{b.page_subtitle || 'Live streaming server'}. <span className="text-accent underline underline-offset-2">Listen now</span></p>
        <div className="flex flex-wrap items-center gap-2">
          <span className="inline-flex h-7 items-center rounded-md bg-accent px-2.5 text-xs font-medium text-accent-fg">Save</span>
          <span className="inline-flex h-7 items-center rounded-md border border-line-strong bg-raised px-2.5 text-xs">Cancel</span>
          <Badge tone="accent">now_playing</Badge>
          <span className="relative h-[18px] w-8 rounded-full border border-accent bg-accent"><span className="absolute left-[16px] top-[2px] size-3 rounded-full bg-accent-fg" /></span>
        </div>
      </div>
    </div>
  )
}

export default function Preview({ b, logoSrc }: { b: Branding; logoSrc: string | null }) {
  return (
    <div className="space-y-2">
      <Legend>Theme preview</Legend>
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-1">
        <Mini theme="dark" b={b} logoSrc={logoSrc} />
        <Mini theme="light" b={b} logoSrc={logoSrc} />
      </div>
    </div>
  )
}
