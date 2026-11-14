import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Button, Input } from '@/ui/controls'
import { Badge, EmptyState, Skeleton } from '@/ui/display'
import { Icon } from '@/ui/icons'
import { SearchInput } from '@/ui/kit'
import { parseIp, kindOf } from './ip'

/** One editable address list (bans or allow list). All policy lives in the callbacks. */
export function IpList({ id, items, loading, error, canEdit, addLabel, emptyTitle, emptyBody, onAdd, onRemove, onRetry, lockedNote }: {
  id: string; items: string[]; loading: boolean; error: string | null; canEdit: boolean; addLabel: string
  emptyTitle: string; emptyBody: ReactNode; lockedNote?: string
  onAdd: (value: string, parsed: Extract<ReturnType<typeof parseIp>, { ok: true }>) => Promise<boolean>
  onRemove: (ip: string) => void; onRetry: () => void
}) {
  const [text, setText] = useState('')
  const [err, setErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [q, setQ] = useState('')
  const sorted = useMemo(() => [...items].sort((a, b) => a.localeCompare(b, undefined, { numeric: true })), [items])
  const shown = q.trim() ? sorted.filter((i) => i.toLowerCase().includes(q.trim().toLowerCase())) : sorted
  // A confirmed add happens in a dialog after submit returns, so clear the field when the list grows.
  const len = useRef(items.length)
  useEffect(() => { if (items.length > len.current) setText(''); len.current = items.length }, [items.length])
  const preview = text.trim() ? parseIp(text) : null

  const submit = async () => {
    const p = parseIp(text)
    if (!p.ok) { setErr(p.error); return }
    if (items.includes(p.value)) { setErr('Already in the list'); return }
    setErr(null); setBusy(true)
    try { if (await onAdd(p.value, p)) setText('') } finally { setBusy(false) }
  }

  return (
    <div>
      {canEdit && (
        <form className="border-b border-line p-4" onSubmit={(e) => { e.preventDefault(); void submit() }} noValidate>
          <div className="flex items-start gap-2">
            <div className="min-w-0 flex-1">
              <Input id={`${id}-input`} mono aria-label={addLabel} aria-invalid={!!err} aria-describedby={`${id}-hint`} placeholder="203.0.113.7 or 198.51.100.0/24" value={text}
                onChange={(e) => { setText(e.target.value); setErr(null) }} autoComplete="off" spellCheck={false} />
              <p id={`${id}-hint`} className={`mt-1.5 min-h-4 text-xs ${err ? 'text-danger' : 'text-faint'}`} aria-live="polite">
                {err ?? (preview ? (preview.ok ? `${preview.kind}${preview.value !== text.trim() ? `, stored as ${preview.value}` : ''}` : preview.error) : 'IPv4, IPv6, or a CIDR range.')}
              </p>
            </div>
            <Button type="submit" variant="primary" loading={busy}>Add</Button>
          </div>
        </form>
      )}
      {lockedNote && !canEdit && <p className="border-b border-line px-4 py-2.5 text-xs text-muted">{lockedNote}</p>}
      {sorted.length > 8 && <div className="border-b border-line px-4 py-2.5"><SearchInput value={q} onChange={setQ} placeholder="Filter list" className="w-full sm:w-64" /></div>}
      {loading ? <div className="space-y-2 p-4">{[0, 1].map((i) => <Skeleton key={i} className="h-8 w-full" />)}</div>
        : error ? <EmptyState icon={<Icon name="alert" size={20} />} title="Could not load" body={error} action={<Button onClick={onRetry}>Retry</Button>} />
        : sorted.length === 0 ? <EmptyState icon={<Icon name="list" size={20} />} title={emptyTitle} body={emptyBody} />
        : shown.length === 0 ? <p className="px-4 py-6 text-center text-xs text-muted">No entry matches the filter.</p>
        : (
          <ul className="divide-y divide-line">
            {shown.map((ip) => (
              <li key={ip} className="flex min-h-11 items-center gap-3 px-4 py-1.5 transition-colors hover:bg-hover/60">
                <span className="min-w-0 flex-1 truncate font-mono text-xs text-ink">{ip}</span>
                {kindOf(ip) === 'Invalid' ? <Badge tone="warn">Malformed, never matches</Badge> : <Badge>{kindOf(ip)}</Badge>}
                {canEdit && <Button size="sm" variant="ghost" onClick={() => onRemove(ip)} aria-label={`Remove ${ip}`}><Icon name="trash" size={14} /></Button>}
              </li>
            ))}
          </ul>
        )}
    </div>
  )
}
