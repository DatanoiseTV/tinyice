import type { ReactNode } from 'react'
import { Button } from '@/ui/controls'
import { Badge } from '@/ui/display'
import { cn } from '@/lib/cn'

export type SectionId = 'station' | 'look' | 'listeners' | 'security' | 'network' | 'signin'

/** One setting, described declaratively so the same definition drives the section view and the search results. */
export interface RowDef {
  id: string
  section: SectionId
  label: string
  /** One plain sentence on what it does. */
  summary: ReactNode
  /** Text only: also searched. */
  summaryText: string
  useWhen?: string
  keywords?: string
  restart?: boolean
  recommended?: string
  defaultText?: string
  isDefault?: boolean
  onReset?: () => void
  dirty?: boolean
  saved?: string
  error?: string
  stack?: boolean
  control: ReactNode
}

export const matches = (r: RowDef, q: string) => `${r.label} ${r.summaryText} ${r.useWhen ?? ''} ${r.keywords ?? ''}`.toLowerCase().includes(q.toLowerCase())

function Hl({ text, q }: { text: string; q: string }) {
  const i = q ? text.toLowerCase().indexOf(q.toLowerCase()) : -1
  if (i < 0) return <>{text}</>
  return <>{text.slice(0, i)}<mark className="rounded bg-accent/25 px-0.5 text-ink">{text.slice(i, i + q.length)}</mark>{text.slice(i + q.length)}</>
}

export default function Row({ r, q }: { r: RowDef; q: string }) {
  return (
    <div id={`set-${r.id}`} className={cn('flex gap-x-6 gap-y-3 border-b border-line px-4 py-4 last:border-b-0', r.stack ? 'flex-col' : 'items-start justify-between max-sm:flex-col')}>
      <div className="min-w-0 max-w-xl">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="text-[13px] font-medium text-ink"><Hl text={r.label} q={q} /></span>
          {r.recommended && <Badge tone="accent">Recommended: {r.recommended}</Badge>}
          {r.restart && <Badge tone="warn">Restart needed</Badge>}
          {r.dirty && <Badge tone="warn">Changed</Badge>}
        </div>
        <p className="mt-1 text-xs leading-relaxed text-muted">{r.summary}</p>
        {r.useWhen && <p className="mt-1 text-xs leading-relaxed text-faint">Use this when {r.useWhen}</p>}
        {(r.defaultText !== undefined || r.dirty) && (
          <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-faint">
            {r.defaultText !== undefined && <span>Default: <span className="text-muted">{r.defaultText}</span></span>}
            {r.dirty && r.saved !== undefined && <span>Saved: <span className="text-muted">{r.saved}</span></span>}
            {r.onReset && !r.isDefault && <Button size="sm" variant="ghost" className="!h-6 !px-1.5" onClick={r.onReset}>Reset to default</Button>}
          </div>
        )}
      </div>
      <div className={cn('min-w-0 space-y-1.5', r.stack ? 'w-full' : 'sm:w-72 sm:shrink-0 max-sm:w-full')}>
        {r.control}
        {r.error && <p role="alert" className="text-xs text-danger">{r.error}</p>}
      </div>
    </div>
  )
}
