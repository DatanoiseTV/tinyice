import { lazy, Suspense, useEffect, useMemo, useState, type ComponentType } from 'react'
import { Route, Switch, useLocation } from 'wouter'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Sidebar, isActive } from './Sidebar'
import { CommandPalette } from './CommandPalette'
import { Toasts } from './Toasts'
import { ALL_NAV } from './nav'
import { LiveProvider, useLive } from '@/lib/live'
import { currentTheme, setTheme } from '@/lib/theme'
import { TooltipProvider } from '@/ui/controls'
import { Kbd, Skeleton, EmptyState, PageHeader } from '@/ui/display'
import { Tally } from '@/ui/console'
import { Icon } from '@/ui/icons'
import { cn } from '@/lib/cn'

const qc = new QueryClient({ defaultOptions: { queries: { staleTime: 10_000, retry: 1, refetchOnWindowFocus: false } } })

/**
 * Admin pages are discovered by file name: src/pages/admin/<nav id>.tsx with a
 * default export. Adding a page never touches this file (see app/nav.ts for ids).
 */
const modules = import.meta.glob<{ default: ComponentType }>('/src/pages/admin/*.tsx')

function Missing({ id }: { id: string }) {
  const item = ALL_NAV.find((n) => n.id === id)
  return (
    <>
      <PageHeader title={item?.label ?? 'Not found'} sub={item?.hint} />
      <EmptyState icon={<Icon name="settings" size={20} />} title={item ? 'Not built yet' : 'Page not found'} body={item ? 'This screen has no implementation in this build.' : 'There is no admin page at this address.'} />
    </>
  )
}

function Topbar({ onMenu, onSearch }: { onMenu: () => void; onSearch: () => void }) {
  const [path] = useLocation()
  const { connected, stats } = useLive()
  const [theme, setT] = useState(currentTheme())
  const here = ALL_NAV.find((n) => isActive(n.href, path))
  const toggle = () => { const t = theme === 'dark' ? 'light' : 'dark'; setTheme(t); setT(t) }

  return (
    <header className="sticky top-0 z-30 flex h-12 items-center gap-3 border-b border-line bg-bg/85 px-4 backdrop-blur">
      <button onClick={onMenu} className="flex size-8 items-center justify-center rounded-md text-muted hover:bg-hover md:hidden" aria-label="Open menu"><Icon name="menu" /></button>
      <div className="flex items-center gap-2 text-[13px]">
        <span className="text-faint">Admin</span><span className="text-line-strong">/</span><span className="font-medium text-ink">{here?.label ?? 'Not found'}</span>
      </div>
      <button onClick={onSearch} className="ml-auto flex h-8 w-64 items-center gap-2 rounded-md border border-line bg-surface px-2.5 text-xs text-faint transition-colors hover:border-line-strong max-lg:w-auto" aria-label="Search">
        <Icon name="search" size={14} /><span className="max-lg:hidden">Search or jump to...</span><span className="ml-auto flex gap-1 max-lg:hidden"><Kbd>⌘</Kbd><Kbd>K</Kbd></span>
      </button>
      <div className="flex items-center gap-2 rounded-md border border-line bg-surface px-2.5 py-1.5 text-xs" title={connected ? 'Event stream connected' : 'Connecting to event stream'}>
        <Tally on={connected} />
        <span className="num whitespace-nowrap text-muted">{connected ? stats?.listeners ?? 0 : '...'}<span className="max-sm:hidden">{connected ? ' listening' : ' connecting'}</span></span>
      </div>
      <button onClick={toggle} className="flex size-8 items-center justify-center rounded-md text-muted hover:bg-hover hover:text-ink" aria-label="Toggle theme">
        <Icon name={theme === 'dark' ? 'sun' : 'moon'} />
      </button>
    </header>
  )
}

function Routes() {
  const routes = useMemo(() => ALL_NAV.map((n) => {
    const loader = modules[`/src/pages/admin/${n.id}.tsx`]
    return { n, Comp: loader ? lazy(loader) : null }
  }), [])
  return (
    <Suspense fallback={<Skeleton className="h-64 w-full" />}>
      <Switch>
        {routes.map(({ n, Comp }) => (
          <Route key={n.id} path={n.href} nest={false}>{Comp ? <Comp /> : <Missing id={n.id} />}</Route>
        ))}
        <Route><Missing id="" /></Route>
      </Switch>
    </Suspense>
  )
}

function Shell() {
  const [collapsed, setCollapsed] = useState(() => { try { return localStorage.getItem('tinyice-rail') === '1' } catch { return false } })
  const [mobile, setMobile] = useState(false)
  const [palette, setPalette] = useState(false)
  useEffect(() => { try { localStorage.setItem('tinyice-rail', collapsed ? '1' : '0') } catch { /* ignore */ } }, [collapsed])
  useEffect(() => {
    const h = (e: KeyboardEvent) => { if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setPalette((o) => !o) } }
    addEventListener('keydown', h)
    return () => removeEventListener('keydown', h)
  }, [])

  return (
    <div className="min-h-full">
      <Sidebar collapsed={collapsed} onToggle={() => setCollapsed((c) => !c)} mobileOpen={mobile} onNavigate={() => setMobile(false)} />
      {mobile && <div className="fixed inset-0 z-30 bg-black/50 md:hidden" onClick={() => setMobile(false)} />}
      <div className={cn('min-w-0 transition-[padding] duration-200', collapsed ? 'md:pl-14' : 'md:pl-56')}>
        <Topbar onMenu={() => setMobile(true)} onSearch={() => setPalette(true)} />
        <main className="mx-auto max-w-[1440px] px-5 py-6 max-sm:px-4"><Routes /></main>
      </div>
      <CommandPalette open={palette} onOpenChange={setPalette} />
      <Toasts />
    </div>
  )
}

export function AdminApp() {
  return (
    <QueryClientProvider client={qc}>
      <TooltipProvider><LiveProvider><Shell /></LiveProvider></TooltipProvider>
    </QueryClientProvider>
  )
}
