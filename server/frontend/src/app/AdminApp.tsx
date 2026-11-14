import { lazy, Suspense, useEffect, useState } from 'react'
import { Route, Switch, useLocation } from 'wouter'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Menu, Moon, Search, Sun } from 'lucide-react'
import { Sidebar, isActive } from './Sidebar'
import { CommandPalette } from './CommandPalette'
import { Toasts } from './Toasts'
import { ALL_NAV } from './nav'
import { LiveProvider, useLive } from '@/lib/live'
import { currentTheme, setTheme } from '@/lib/theme'
import { TooltipProvider } from '@/ui/controls'
import { Kbd, Skeleton, Tally } from '@/ui/display'
import { Dashboard } from '@/pages/Dashboard'
import { cn } from '@/lib/cn'

const NotYet = lazy(() => import('@/pages/NotYet'))
const qc = new QueryClient({ defaultOptions: { queries: { staleTime: 10_000, retry: 1, refetchOnWindowFocus: false } } })

function Topbar({ onMenu, onSearch }: { onMenu: () => void; onSearch: () => void }) {
  const [path] = useLocation()
  const { connected, stats } = useLive()
  const [theme, setT] = useState(currentTheme())
  const here = ALL_NAV.find((n) => isActive(n.href, path))
  const toggle = () => { const t = theme === 'dark' ? 'light' : 'dark'; setTheme(t); setT(t) }

  return (
    <header className="sticky top-0 z-30 flex h-12 items-center gap-3 border-b border-line bg-bg/85 px-4 backdrop-blur">
      <button onClick={onMenu} className="flex size-8 items-center justify-center rounded-md text-muted hover:bg-hover md:hidden" aria-label="Open menu"><Menu className="size-4" /></button>
      <div className="flex items-center gap-2 text-[13px]">
        <span className="text-faint">Admin</span><span className="text-line-strong">/</span><span className="font-medium text-ink">{here?.label ?? 'Not found'}</span>
      </div>
      <button onClick={onSearch} className="ml-auto flex h-8 w-64 items-center gap-2 rounded-md border border-line bg-surface px-2.5 text-xs text-faint transition-colors hover:border-line-strong max-lg:w-auto">
        <Search className="size-3.5" /><span className="max-lg:hidden">Search or jump to...</span><span className="ml-auto flex gap-1 max-lg:hidden"><Kbd>⌘</Kbd><Kbd>K</Kbd></span>
      </button>
      <div className="flex items-center gap-2 rounded-md border border-line bg-surface px-2.5 py-1.5 text-xs" title={connected ? 'Event stream connected' : 'Connecting to event stream'}>
        <Tally on={connected} className={cn(!connected && 'bg-warn')} />
        <span className="num whitespace-nowrap text-muted">{connected ? `${stats?.listeners ?? 0}` : '...'}<span className="max-sm:hidden">{connected ? ' listening' : ' Connecting'}</span></span>
      </div>
      <button onClick={toggle} className="flex size-8 items-center justify-center rounded-md text-muted hover:bg-hover hover:text-ink" aria-label="Toggle theme">
        {theme === 'dark' ? <Sun className="size-4" /> : <Moon className="size-4" />}
      </button>
    </header>
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
        <main className="mx-auto max-w-[1440px] px-5 py-6 max-sm:px-4">
          <Suspense fallback={<Skeleton className="h-64 w-full" />}>
            <Switch>
              <Route path="/admin" component={Dashboard} />
              <Route path="/admin/:rest*" component={NotYet} />
            </Switch>
          </Suspense>
        </main>
      </div>
      <CommandPalette open={palette} onOpenChange={setPalette} />
      <Toasts />
    </div>
  )
}

export function AdminApp() {
  return (
    <QueryClientProvider client={qc}>
      <TooltipProvider>
        <LiveProvider><Shell /></LiveProvider>
      </TooltipProvider>
    </QueryClientProvider>
  )
}
