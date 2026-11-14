import { useCallback, useRef, useState, type ReactNode } from 'react'
import * as RTabs from '@radix-ui/react-tabs'
import * as RMenu from '@radix-ui/react-dropdown-menu'
import { Button, Dialog } from './controls'
import { Icon, type IconName } from './icons'
import { Legend } from './console'
import { toast } from '@/lib/toast'
import { cn } from '@/lib/cn'

/* ---------- Tabs ---------- */
export function Tabs({ value, onValueChange, tabs, children, className }: {
  value: string; onValueChange: (v: string) => void; tabs: { value: string; label: string; icon?: IconName; count?: number }[]; children?: ReactNode; className?: string
}) {
  return (
    <RTabs.Root value={value} onValueChange={onValueChange} className={className}>
      <RTabs.List className="mb-4 flex gap-1 overflow-x-auto border-b border-line" aria-label="Sections">
        {tabs.map((t) => (
          <RTabs.Trigger key={t.value} value={t.value} className="relative flex h-9 shrink-0 items-center gap-2 px-3 text-[13px] text-muted transition-colors hover:text-ink data-[state=active]:text-ink data-[state=active]:after:absolute data-[state=active]:after:inset-x-2 data-[state=active]:after:-bottom-px data-[state=active]:after:h-0.5 data-[state=active]:after:rounded-full data-[state=active]:after:bg-accent">
            {t.icon && <Icon name={t.icon} size={14} />}{t.label}
            {t.count !== undefined && <span className="num rounded bg-hover px-1.5 font-mono text-[10px] text-faint">{t.count}</span>}
          </RTabs.Trigger>
        ))}
      </RTabs.List>
      {children}
    </RTabs.Root>
  )
}
export const TabPanel = RTabs.Content

/* ---------- Row action menu ---------- */
export interface MenuAction { label: string; icon?: IconName; onSelect: () => void; danger?: boolean; disabled?: boolean }
export function Menu({ actions, label = 'Actions' }: { actions: (MenuAction | 'sep')[]; label?: string }) {
  return (
    <RMenu.Root>
      <RMenu.Trigger asChild>
        <button className="flex size-7 items-center justify-center rounded-md text-faint transition-colors hover:bg-hover hover:text-ink data-[state=open]:bg-hover" aria-label={label}><Icon name="more" /></button>
      </RMenu.Trigger>
      <RMenu.Portal>
        <RMenu.Content align="end" sideOffset={4} className="z-50 min-w-40 rounded-md border border-line-strong bg-raised p-1 animate-[pop-in_.12s_ease-out]">
          {actions.map((a, i) => a === 'sep' ? <RMenu.Separator key={i} className="my-1 h-px bg-line" /> : (
            <RMenu.Item key={a.label} disabled={a.disabled} onSelect={a.onSelect}
              className={cn('flex h-7 cursor-default select-none items-center gap-2 rounded px-2 text-[13px] outline-none data-[disabled]:opacity-40 data-[highlighted]:bg-hover', a.danger ? 'text-danger' : 'text-ink')}>
              {a.icon && <Icon name={a.icon} size={14} className={a.danger ? '' : 'text-faint'} />}{a.label}
            </RMenu.Item>
          ))}
        </RMenu.Content>
      </RMenu.Portal>
    </RMenu.Root>
  )
}

/* ---------- Checkbox ---------- */
export function Checkbox({ checked, onChange, label, disabled }: { checked: boolean; onChange: (v: boolean) => void; label: string; disabled?: boolean }) {
  return (
    <button
      type="button" role="checkbox" aria-checked={checked} aria-label={label} disabled={disabled} onClick={() => onChange(!checked)}
      className={cn('flex size-4 shrink-0 items-center justify-center rounded-[4px] border transition-colors disabled:opacity-40', checked ? 'border-accent bg-accent text-accent-fg' : 'border-line-strong bg-bg hover:border-faint')}
    >{checked && <Icon name="check" size={12} strokeWidth={2.25} />}</button>
  )
}

/* ---------- Confirm (destructive actions) ---------- */
export function useConfirm() {
  const [state, setState] = useState<{ title: string; body?: string; confirm: string; run: () => Promise<unknown> | void } | null>(null)
  const [busy, setBusy] = useState(false)
  const ask = useCallback((o: { title: string; body?: string; confirm?: string; run: () => Promise<unknown> | void }) => setState({ confirm: 'Delete', ...o }), [])
  const dialog = (
    <Dialog open={!!state} onOpenChange={(o) => !o && !busy && setState(null)} title={state?.title ?? ''} description={state?.body}
      footer={<>
        <Button variant="ghost" onClick={() => setState(null)} disabled={busy}>Cancel</Button>
        <Button variant="danger" loading={busy} onClick={async () => { setBusy(true); try { await state!.run(); setState(null) } finally { setBusy(false) } }}>{state?.confirm}</Button>
      </>} />
  )
  return { ask, dialog }
}

/* ---------- Copy ---------- */
export function CopyButton({ text, label = 'Copy' }: { text: string; label?: string }) {
  const [done, setDone] = useState(false)
  const t = useRef<ReturnType<typeof setTimeout>>(undefined)
  return (
    <button
      type="button" aria-label={label} title={label}
      onClick={async () => { try { await navigator.clipboard.writeText(text); setDone(true); clearTimeout(t.current); t.current = setTimeout(() => setDone(false), 1400) } catch { toast.error('Clipboard unavailable') } }}
      className="flex size-7 items-center justify-center rounded-md text-faint transition-colors hover:bg-hover hover:text-ink"
    ><Icon name={done ? 'check' : 'copy'} size={14} className={done ? 'text-live' : ''} /></button>
  )
}

export function CodeBlock({ children, label }: { children: string; label?: string }) {
  return (
    <div className="overflow-hidden rounded-md border border-line bg-bg">
      <div className="flex h-8 items-center justify-between border-b border-line px-3"><Legend>{label ?? 'Code'}</Legend><CopyButton text={children} /></div>
      <pre className="overflow-x-auto p-3 font-mono text-xs leading-relaxed text-ink">{children}</pre>
    </div>
  )
}

/* ---------- Settings row: label + help on the left, control on the right ---------- */
export function SettingRow({ label, help, children, stack }: { label: string; help?: ReactNode; children: ReactNode; stack?: boolean }) {
  return (
    <div className={cn('flex gap-6 border-b border-line px-4 py-3.5 last:border-b-0', stack ? 'flex-col' : 'items-center justify-between max-sm:flex-col max-sm:items-stretch max-sm:gap-2')}>
      <div className="min-w-0 max-w-md"><div className="text-[13px] font-medium text-ink">{label}</div>{help && <div className="mt-0.5 text-xs text-muted">{help}</div>}</div>
      <div className={cn('shrink-0', !stack && 'sm:w-72')}>{children}</div>
    </div>
  )
}

/* ---------- Search ---------- */
export function SearchInput({ value, onChange, placeholder = 'Search', className }: { value: string; onChange: (v: string) => void; placeholder?: string; className?: string }) {
  return (
    <div className={cn('relative', className)}>
      <Icon name="search" size={14} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-faint" />
      <input value={value} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} aria-label={placeholder}
        className="h-8 w-full rounded-md border border-line-strong bg-bg pl-8 pr-2.5 text-[13px] text-ink placeholder:text-faint transition-colors hover:border-faint focus:border-accent focus:outline-none" />
    </div>
  )
}

/* ---------- Async actions ---------- */
/** Run a mutation with a busy flag and a toast on success/failure. Errors never escape. */
export function useAction() {
  const [busy, setBusy] = useState<string | null>(null)
  const run = useCallback(async <T,>(key: string, fn: () => Promise<T>, ok?: string): Promise<T | undefined> => {
    setBusy(key)
    try { const r = await fn(); if (ok) toast.success(ok); return r }
    catch (e) { toast.error((e as Error).message || 'Request failed'); return undefined }
    finally { setBusy((b) => (b === key ? null : b)) }
  }, [])
  return { run, busy, isBusy: (k: string) => busy === k }
}
