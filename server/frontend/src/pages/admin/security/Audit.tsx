import { useMemo, useState } from 'react'
import { api } from '@/lib/api'
import { Button, NativeSelect, Segmented } from '@/ui/controls'
import { Badge, EmptyState, Skeleton } from '@/ui/display'
import { Legend, Panel } from '@/ui/console'
import { Icon } from '@/ui/icons'
import { SearchInput } from '@/ui/kit'
import { absTime, useLoad } from '../users/lib'

export interface AuditEntry { id: number; timestamp: string; username: string; action: string; resource_type: string; resource_id: string; detail: string; ip: string }
interface AuditPage { entries: AuditEntry[] | null; total: number; page: number; limit: number }

/** Keys are the category ids the server filters by (relay/history.go AuditCategoryRules). */
const CATEGORIES = [
  ['', 'All categories'], ['auth', 'Sign-in and keys'], ['streams', 'Streams'], ['autodj', 'AutoDJ'], ['relays', 'Relays'],
  ['transcoders', 'Transcoders'], ['webhooks', 'Webhooks'], ['users', 'Users'], ['security', 'Security'], ['settings', 'Settings'],
] as const

type Range = 'all' | '1h' | '24h' | '7d'
const RANGE_MS: Record<Range, number> = { all: Infinity, '1h': 3600e3, '24h': 86400e3, '7d': 7 * 86400e3 }
const LIMIT = 100

/** Warn-worthy outcomes get a tone; everything else stays neutral so tones keep meaning. */
function toneOf(action: string): 'danger' | 'info' | 'warn' | 'neutral' {
  if (/(deleted|denied|revoked|failed|banned|unwhitelisted|disabled|kicked)/.test(action) && !/unbanned/.test(action)) return 'danger'
  if (/(created|approved|registered|whitelisted)/.test(action)) return 'info'
  if (/(updated|unbanned)/.test(action)) return 'warn'
  return 'neutral'
}
const words = (action: string) => {
  const t = action.replace(/_/g, ' ').replace(/^ip /, 'IP ').replace(/^autodj /, 'AutoDJ ')
  return t[0].toUpperCase() + t.slice(1)
}

function dayLabel(d: Date, now: Date) {
  const day = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime()
  const diff = Math.round((day(now) - day(d)) / 86400e3)
  if (diff === 0) return 'Today'
  if (diff === 1) return 'Yesterday'
  return d.toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric', year: d.getFullYear() === now.getFullYear() ? undefined : 'numeric' })
}

export function Audit({ enabled }: { enabled: boolean }) {
  const [category, setCategory] = useState('')
  const [pageNo, setPageNo] = useState(1)
  const [actor, setActor] = useState('')
  const [text, setText] = useState('')
  const [range, setRange] = useState<Range>('all')
  const { data, error, loading, reload } = useLoad(
    () => api.get<AuditPage>(`/api/security/audit?page=${pageNo}&limit=${LIMIT}${category ? `&category=${category}` : ''}`),
    { enabled, key: `${pageNo}|${category}` },
  )
  const entries = data?.entries ?? []
  const total = data?.total ?? 0
  const filtered = useMemo(() => {
    const now = Date.now(), a = actor.trim().toLowerCase(), t = text.trim().toLowerCase()
    return entries.filter((e) =>
      (range === 'all' || now - new Date(e.timestamp).getTime() <= RANGE_MS[range]) &&
      (!a || e.username.toLowerCase().includes(a)) &&
      (!t || [e.action, e.resource_type, e.resource_id, e.detail, e.ip].some((v) => v.toLowerCase().includes(t))))
  }, [entries, actor, text, range])
  const groups = useMemo(() => {
    const now = new Date()
    const out: { label: string; rows: AuditEntry[] }[] = []
    for (const e of filtered) {
      const label = dayLabel(new Date(e.timestamp), now)
      const g = out[out.length - 1]
      if (g && g.label === label) g.rows.push(e); else out.push({ label, rows: [e] })
    }
    return out
  }, [filtered])
  const pages = Math.max(1, Math.ceil(total / LIMIT))
  const clientFiltered = !!(actor.trim() || text.trim() || range !== 'all')

  return (
    <Panel legend="Audit log" title={data ? `${total} entr${total === 1 ? 'y' : 'ies'}${category ? ' in category' : ''}` : undefined} flush
      actions={<Button size="sm" variant="ghost" onClick={() => void reload()} aria-label="Refresh audit log"><Icon name="refresh" size={14} /></Button>}>
      <div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2.5">
        <div className="w-full sm:w-48">
          <NativeSelect aria-label="Category" value={category} onChange={(e) => { setCategory(e.target.value); setPageNo(1) }}>
            {CATEGORIES.map(([v, l]) => <option key={v} value={v}>{l}</option>)}
          </NativeSelect>
        </div>
        <SearchInput value={actor} onChange={setActor} placeholder="Actor" className="w-36" />
        <SearchInput value={text} onChange={setText} placeholder="Search action, target, IP" className="w-full sm:w-56" />
        <Segmented<Range> label="Time window" value={range} onChange={setRange} options={[{ value: 'all', label: 'Any time' }, { value: '1h', label: '1 h' }, { value: '24h', label: '24 h' }, { value: '7d', label: '7 d' }]} />
      </div>
      {clientFiltered && data && <p className="border-b border-line px-4 py-2 text-xs text-muted">Actor, search and time filters apply to the {entries.length} entries on this page ({filtered.length} match). Category filters the whole log.</p>}
      {loading ? <div className="space-y-2 p-4">{[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-9 w-full" />)}</div>
        : error ? <EmptyState icon={<Icon name="alert" size={20} />} title="Could not load the audit log" body={error} action={<Button onClick={() => void reload()}>Retry</Button>} />
        : total === 0 && !category ? <EmptyState icon={<Icon name="doc" size={20} />} title="No audit entries" body="The log fills as operators sign in and change things. If it stays empty, audit logging may be switched off in Settings." />
        : groups.length === 0 ? <EmptyState icon={<Icon name="filter" size={20} />} title="No entry matches" body="Widen the time window or clear the filters." action={<Button onClick={() => { setCategory(''); setActor(''); setText(''); setRange('all'); setPageNo(1) }}>Clear filters</Button>} />
        : (
          <div>
            {groups.map((g) => (
              <section key={g.label} aria-label={g.label}>
                <div className="sticky top-0 z-10 flex h-8 items-center border-b border-line bg-surface px-4"><Legend>{g.label}</Legend></div>
                <ol>
                  {g.rows.map((e) => (
                    <li key={e.id} className="grid grid-cols-[4.5rem_1fr] gap-x-3 gap-y-1 border-b border-line px-4 py-2.5 hover:bg-hover/60 sm:grid-cols-[4.5rem_minmax(0,1fr)_auto]">
                      <time dateTime={e.timestamp} title={absTime(e.timestamp)} className="num pt-0.5 font-mono text-xs text-muted">{new Date(e.timestamp).toLocaleTimeString([], { hour12: false })}</time>
                      <div className="min-w-0">
                        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                          <Badge tone={toneOf(e.action)}>{words(e.action)}</Badge>
                          <span className="font-mono text-xs text-ink">{e.username || 'unknown'}</span>
                          {e.resource_id && <span className="min-w-0 truncate font-mono text-xs text-muted">{e.resource_type} {e.resource_id}</span>}
                        </div>
                        {e.detail && <p className="mt-1 break-words text-xs text-faint">{e.detail}</p>}
                      </div>
                      <span className="col-start-2 font-mono text-[11px] text-faint sm:col-start-3 sm:text-right">{e.ip}</span>
                    </li>
                  ))}
                </ol>
              </section>
            ))}
            <div className="flex items-center justify-between gap-3 px-4 py-3">
              <span className="num font-mono text-xs text-faint">Page {data?.page ?? pageNo} of {pages}</span>
              <div className="flex gap-2">
                <Button size="sm" disabled={pageNo <= 1} onClick={() => setPageNo((p) => p - 1)}>Newer</Button>
                <Button size="sm" disabled={pageNo >= pages} onClick={() => setPageNo((p) => p + 1)}>Older</Button>
              </div>
            </div>
          </div>
        )}
    </Panel>
  )
}
