import { dismiss, useToasts } from '@/lib/toast'
import { Icon } from '@/ui/icons'
import { cn } from '@/lib/cn'

const icon = { success: 'check', error: 'alert', info: 'info' } as const
const tone = { success: 'text-live', error: 'text-danger', info: 'text-info' }

export function Toasts() {
  const items = useToasts()
  return (
    <div className="pointer-events-none fixed bottom-4 right-4 z-[60] flex w-80 flex-col gap-2 max-sm:inset-x-4 max-sm:w-auto" role="status" aria-live="polite">
      {items.map((t) => (
        <div key={t.id} className="pointer-events-auto flex items-start gap-2.5 rounded-lg border border-line-strong bg-raised px-3 py-2.5 animate-[fade-in_.15s_ease-out]">
          <Icon name={icon[t.kind]} className={cn('mt-px shrink-0', tone[t.kind])} />
          <p className="flex-1 text-[13px] text-ink">{t.message}</p>
          <button onClick={() => dismiss(t.id)} className="text-faint hover:text-ink" aria-label="Dismiss"><Icon name="x" size={14} /></button>
        </div>
      ))}
    </div>
  )
}
