import { useState, type ReactNode } from 'react'
import { Icon, Logo } from '@/ui/icons'
import { Button } from '@/ui/controls'
import { cn } from '@/lib/cn'
import { currentTheme, setTheme, type Theme } from '@/lib/theme'
import { Toasts } from '@/app/Toasts'
import { pageMeta } from './stations'
import './icons'
import { shortVersion } from '@/lib/format'

export function ThemeToggle() {
  const [t, setT] = useState<Theme>(currentTheme)
  const next: Theme = t === 'dark' ? 'light' : 'dark'
  return (
    <Button variant="ghost" size="icon" aria-label={`Switch to ${next} theme`} onClick={() => { setTheme(next); setT(next) }}>
      <Icon name={t === 'dark' ? 'sun' : 'moon'} size={16} />
    </Button>
  )
}

export function Brand({ size = 28 }: { size?: number }) {
  const m = pageMeta()
  return m.logoUrl
    ? <img src={m.logoUrl} alt="" width={size} height={size} className="rounded-md object-cover" style={{ width: size, height: size }} />
    : <Logo size={size} />
}

const links = [
  { href: '/explore', label: 'Explore', id: 'explore' },
  { href: '/developers', label: 'Developers', id: 'developers' },
]

/** Header + footer shared by the public pages. `wide` lifts the content width cap (docs). */
export function PublicShell({ active, children, wide, bare }: { active?: string; children: ReactNode; wide?: boolean; bare?: boolean }) {
  const m = pageMeta()
  return (
    <div className="flex min-h-full flex-col bg-bg text-ink">
      <header className="sticky top-0 z-30 border-b border-line bg-bg/90 backdrop-blur supports-[backdrop-filter]:bg-bg/80">
        <div className={cn('mx-auto flex h-14 items-center gap-3 px-5', wide ? 'max-w-[1280px]' : 'max-w-[1080px]')}>
          <a href="/" className="flex min-w-0 items-center gap-2.5 rounded-md" aria-label={`${m.title} home`}>
            <Brand />
            <span className="truncate text-sm font-semibold tracking-tight">{m.title}</span>
          </a>
          <nav aria-label="Primary" className="ml-auto flex items-center gap-0.5">
            {links.map((l) => (
              <a key={l.id} href={l.href} aria-current={active === l.id ? 'page' : undefined}
                className={cn('flex h-8 items-center rounded-md px-2.5 text-[13px] transition-colors hover:bg-hover hover:text-ink', active === l.id ? 'bg-hover text-ink' : 'text-muted')}>
                {l.label}
              </a>
            ))}
            <a href="/admin" className="hidden h-8 items-center rounded-md px-2.5 text-[13px] text-accent hover:bg-hover sm:flex">Admin</a>
            <ThemeToggle />
          </nav>
        </div>
      </header>
      <div className="flex-1">{children}</div>
      {!bare && (
        <footer className="border-t border-line">
          <div className={cn('mx-auto flex flex-wrap items-center gap-x-4 gap-y-1 px-5 py-4 font-mono text-[11px] text-faint', wide ? 'max-w-[1280px]' : 'max-w-[1080px]')}>
            <span>{m.title}</span>
            <a href="/developers" className="hover:text-ink">API</a>
            <a href="/admin" className="hover:text-ink sm:hidden">Admin</a>
            <span className="ml-auto">TinyIce {shortVersion(m.version)}</span>
          </div>
        </footer>
      )}
      <Toasts />
    </div>
  )
}
