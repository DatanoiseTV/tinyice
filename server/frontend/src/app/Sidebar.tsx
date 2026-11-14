import { Link, useLocation } from 'wouter'
import { LogOut, PanelLeftClose, PanelLeftOpen } from 'lucide-react'
import { NAV } from './nav'
import { page } from '@/lib/api'
import { Tip } from '@/ui/controls'
import { cn } from '@/lib/cn'

export function isActive(href: string, path: string) {
  return href === '/admin' ? path === '/admin' || path === '/admin/' : path.startsWith(href)
}

export function Sidebar({ collapsed, onToggle, mobileOpen, onNavigate }: { collapsed: boolean; onToggle: () => void; mobileOpen: boolean; onNavigate: () => void }) {
  const [path] = useLocation()
  const { branding, pageTitle, user, version } = page()
  const name = branding.logoUrl ? null : (pageTitle || 'TinyIce')
  const rail = collapsed && !mobileOpen

  return (
    <aside
      className={cn(
        'fixed inset-y-0 left-0 z-40 flex flex-col border-r border-line bg-surface transition-[width,transform] duration-200',
        rail ? 'w-14' : 'w-56',
        'max-md:w-64 max-md:-translate-x-full', mobileOpen && 'max-md:translate-x-0',
      )}
    >
      <div className={cn('flex h-12 shrink-0 items-center border-b border-line', rail ? 'justify-center' : 'gap-2.5 px-4')}>
        {branding.logoUrl ? (
          <img src={branding.logoUrl} alt="" className="size-6 rounded object-cover" />
        ) : (
          <span className="flex size-6 items-center justify-center rounded bg-accent font-mono text-[11px] font-bold text-accent-fg">{name!.slice(0, 2)}</span>
        )}
        {!rail && <span className="truncate text-[13px] font-semibold text-ink">{pageTitle || 'TinyIce'}</span>}
      </div>

      <nav className="flex-1 overflow-y-auto px-2 py-3" aria-label="Main">
        {NAV.map((g, gi) => (
          <div key={g.label} className={cn(gi > 0 && 'mt-4')}>
            {!rail ? <div className="mb-1 px-2 text-[11px] font-medium text-faint">{g.label}</div> : gi > 0 && <div className="mx-2 mb-2 h-px bg-line" />}
            <ul className="space-y-0.5">
              {g.items.map((it) => {
                const active = isActive(it.href, path)
                const link = (
                  <Link
                    href={it.href}
                    onClick={onNavigate}
                    aria-current={active ? 'page' : undefined}
                    className={cn(
                      'group relative flex h-8 items-center rounded-md text-[13px] transition-colors',
                      rail ? 'justify-center' : 'gap-2.5 px-2',
                      active ? 'bg-hover text-ink' : 'text-muted hover:bg-hover hover:text-ink',
                    )}
                  >
                    {active && <span className="absolute -left-2 top-1.5 h-5 w-[3px] rounded-r bg-accent" />}
                    <it.icon className={cn('size-4 shrink-0', active ? 'text-accent' : 'text-faint group-hover:text-muted')} strokeWidth={1.75} />
                    {!rail && <span className="truncate">{it.label}</span>}
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
          <span className="flex size-7 shrink-0 items-center justify-center rounded-full border border-line-strong bg-raised text-xs font-semibold uppercase text-muted" aria-hidden>{user.username.slice(0, 1)}</span>
          {!rail && (
            <div className="min-w-0 flex-1 leading-tight">
              <div className="truncate text-[13px] text-ink">{user.username}</div>
              <div className="text-[11px] text-faint">{user.role}</div>
            </div>
          )}
          <Tip content="Sign out" side={rail ? 'right' : 'top'}>
            <a href="/logout" className="flex size-7 items-center justify-center rounded text-faint hover:bg-hover hover:text-danger" aria-label="Sign out"><LogOut className="size-4" /></a>
          </Tip>
        </div>
        <div className={cn('mt-1 flex items-center', rail ? 'justify-center' : 'justify-between px-1.5')}>
          {!rail && <span className="font-mono text-[10px] text-faint">v{version}</span>}
          <button onClick={onToggle} className="hidden size-6 items-center justify-center rounded text-faint hover:bg-hover hover:text-ink md:flex" aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}>
            {collapsed ? <PanelLeftOpen className="size-4" /> : <PanelLeftClose className="size-4" />}
          </button>
        </div>
      </div>
    </aside>
  )
}
