import { Link, useLocation } from 'wouter'
import { NAV } from './nav'
import { page } from '@/lib/api'
import { Tip } from '@/ui/controls'
import { Icon, Logo } from '@/ui/icons'
import { Legend } from '@/ui/console'
import { shortVersion } from '@/lib/format'
import { useBroadcast } from '@/pages/admin/golive/store'
import { cn } from '@/lib/cn'

export function isActive(href: string, path: string) {
  return href === '/admin' ? path === '/admin' || path === '/admin/' : path.startsWith(href)
}

export function Sidebar({ collapsed, onToggle, mobileOpen, onNavigate }: { collapsed: boolean; onToggle: () => void; mobileOpen: boolean; onNavigate: () => void }) {
  const [path] = useLocation()
  const { branding, pageTitle, user, version } = page()
  const rail = collapsed && !mobileOpen
  // The browser broadcast outlives the Go Live page, so the shell must show it is still on air.
  const onAir = useBroadcast().phase === 'live'

  return (
    <aside
      className={cn(
        'fixed inset-y-0 left-0 z-40 flex flex-col border-r border-line bg-surface transition-[width,transform] duration-200',
        rail ? 'w-14' : 'w-56', 'max-md:w-64 max-md:-translate-x-full', mobileOpen && 'max-md:translate-x-0',
      )}
    >
      <div className={cn('flex h-12 shrink-0 items-center border-b border-line', rail ? 'justify-center' : 'gap-2.5 px-4')}>
        {branding.logoUrl ? <img src={branding.logoUrl} alt="" className="size-6 rounded object-cover" /> : <Logo size={24} />}
        {!rail && <span className="truncate text-[13px] font-semibold tracking-tight text-ink">{pageTitle || 'TinyIce'}</span>}
      </div>

      <nav className="flex-1 overflow-y-auto px-2 py-3" aria-label="Main">
        {NAV.map((g, gi) => (
          <div key={g.label} className={cn(gi > 0 && 'mt-4')}>
            {!rail ? <Legend className="mb-1 block px-2">{g.label}</Legend> : gi > 0 && <div className="mx-2 mb-2 h-px bg-line" />}
            <ul className="space-y-0.5">
              {g.items.map((it) => {
                const active = isActive(it.href, path)
                const link = (
                  <Link
                    href={it.href} onClick={onNavigate} aria-current={active ? 'page' : undefined}
                    className={cn('group relative flex h-8 items-center rounded-md text-[13px] transition-colors', rail ? 'justify-center' : 'gap-2.5 px-2', active ? 'bg-hover text-ink' : 'text-muted hover:bg-hover hover:text-ink')}
                  >
                    {active && <span className="absolute -left-2 top-1.5 h-5 w-[3px] rounded-r bg-accent" />}
                    <Icon name={it.icon} lit={active} className={cn('shrink-0', active ? 'text-ink' : 'text-faint group-hover:text-muted')} />
                    {!rail && <span className="truncate">{it.label}</span>}
                    {it.id === 'golive' && onAir && <span className={cn('size-2 rounded-full bg-danger tally', rail ? 'absolute right-1.5 top-1.5' : 'ml-auto')} role="status" aria-label="On air" />}
                  </Link>
                )
                return <li key={it.id}>{rail ? <Tip content={it.label}>{link}</Tip> : link}</li>
              })}
            </ul>
          </div>
        ))}
      </nav>

      <div className="shrink-0 border-t border-line p-2">
        <div className={cn('flex items-center', rail ? 'flex-col gap-1' : 'gap-2 px-1.5 py-1')}>
          <span className="flex size-7 shrink-0 items-center justify-center rounded-md border border-line-strong bg-raised font-mono text-xs font-semibold uppercase text-muted" aria-hidden>{user.username.slice(0, 1)}</span>
          {!rail && (
            <div className="min-w-0 flex-1 leading-tight">
              <div className="truncate text-[13px] text-ink">{user.username}</div>
              <Legend>{user.role}</Legend>
            </div>
          )}
          <Tip content="Sign out" side={rail ? 'right' : 'top'}>
            <a href="/logout" className="flex size-7 items-center justify-center rounded text-faint hover:bg-hover hover:text-danger" aria-label="Sign out"><Icon name="logout" /></a>
          </Tip>
        </div>
        <div className={cn('mt-1 flex items-center', rail ? 'justify-center' : 'justify-between px-1.5')}>
          {!rail && <span title={version}><Legend>{shortVersion(version)}</Legend></span>}
          <button onClick={onToggle} className="hidden size-6 items-center justify-center rounded text-faint hover:bg-hover hover:text-ink md:flex" aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}>
            <Icon name="sidebar" size={15} />
          </button>
        </div>
      </div>
    </aside>
  )
}
