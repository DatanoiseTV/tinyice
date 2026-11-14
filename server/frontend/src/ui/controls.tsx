import { forwardRef, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode, type SelectHTMLAttributes, type TextareaHTMLAttributes } from 'react'
import * as RSwitch from '@radix-ui/react-switch'
import * as RTooltip from '@radix-ui/react-tooltip'
import * as RDialog from '@radix-ui/react-dialog'
import * as RSelect from '@radix-ui/react-select'
import { Check, ChevronDown, Loader2, X } from 'lucide-react'
import { cn } from '@/lib/cn'

/* ---------- Button ---------- */
type Variant = 'primary' | 'secondary' | 'ghost' | 'danger'
type Size = 'sm' | 'md' | 'icon'
const variants: Record<Variant, string> = {
  primary: 'bg-accent text-accent-fg hover:brightness-110 border-transparent',
  secondary: 'bg-raised text-ink border-line-strong hover:bg-hover',
  ghost: 'bg-transparent text-muted border-transparent hover:bg-hover hover:text-ink',
  danger: 'bg-transparent text-danger border-line-strong hover:bg-danger/10 hover:border-danger/50',
}
const sizes: Record<Size, string> = { sm: 'h-7 px-2.5 text-xs gap-1.5', md: 'h-8 px-3 text-[13px] gap-2', icon: 'h-8 w-8 justify-center' }

export const Button = forwardRef<HTMLButtonElement, ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant; size?: Size; loading?: boolean }>(
  ({ variant = 'secondary', size = 'md', loading, className, children, disabled, ...p }, ref) => (
    <button
      ref={ref}
      disabled={disabled || loading}
      className={cn(
        'inline-flex items-center rounded-md border font-medium transition-colors select-none whitespace-nowrap',
        'disabled:opacity-45 disabled:pointer-events-none',
        variants[variant], sizes[size], className,
      )}
      {...p}
    >
      {loading && <Loader2 className="size-3.5 animate-spin" />}
      {children}
    </button>
  ),
)

/* ---------- Text inputs ---------- */
const field = 'w-full rounded-md border border-line-strong bg-bg px-2.5 text-[13px] text-ink placeholder:text-faint transition-colors hover:border-faint focus:border-accent focus:outline-none disabled:opacity-50'
export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement> & { mono?: boolean }>(
  ({ className, mono, ...p }, ref) => <input ref={ref} className={cn(field, 'h-8', mono && 'font-mono text-xs', className)} {...p} />,
)
export const Textarea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement> & { mono?: boolean }>(
  ({ className, mono, ...p }, ref) => <textarea ref={ref} className={cn(field, 'py-2 min-h-20 resize-y', mono && 'font-mono text-xs', className)} {...p} />,
)
export const NativeSelect = forwardRef<HTMLSelectElement, SelectHTMLAttributes<HTMLSelectElement>>(
  ({ className, ...p }, ref) => <select ref={ref} className={cn(field, 'h-8 pr-7', className)} {...p} />,
)

export function Field({ label, hint, error, children, className }: { label: string; hint?: ReactNode; error?: string | null; children: ReactNode; className?: string }) {
  return (
    <label className={cn('block space-y-1.5', className)}>
      <span className="text-xs font-medium text-muted">{label}</span>
      {children}
      {error ? <span className="block text-xs text-danger">{error}</span> : hint ? <span className="block text-xs text-faint">{hint}</span> : null}
    </label>
  )
}

/* ---------- Switch ---------- */
export function Switch({ checked, onCheckedChange, disabled, label }: { checked: boolean; onCheckedChange: (v: boolean) => void; disabled?: boolean; label: string }) {
  return (
    <RSwitch.Root
      checked={checked}
      onCheckedChange={onCheckedChange}
      disabled={disabled}
      aria-label={label}
      className="relative h-[18px] w-8 shrink-0 rounded-full border border-line-strong bg-hover transition-colors data-[state=checked]:border-accent data-[state=checked]:bg-accent disabled:opacity-50"
    >
      <RSwitch.Thumb className="block size-3 translate-x-[2px] rounded-full bg-muted transition-transform data-[state=checked]:translate-x-[16px] data-[state=checked]:bg-accent-fg" />
    </RSwitch.Root>
  )
}

/* ---------- Segmented control ---------- */
export function Segmented<T extends string>({ value, onChange, options, label }: { value: T; onChange: (v: T) => void; options: { value: T; label: string }[]; label: string }) {
  return (
    <div role="radiogroup" aria-label={label} className="inline-flex rounded-md border border-line bg-surface p-0.5">
      {options.map((o) => (
        <button
          key={o.value}
          role="radio"
          aria-checked={value === o.value}
          onClick={() => onChange(o.value)}
          className={cn(
            'h-6 rounded-[4px] px-2.5 text-xs font-medium transition-colors',
            value === o.value ? 'bg-hover text-ink' : 'text-faint hover:text-muted',
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  )
}

/* ---------- Tooltip ---------- */
export const TooltipProvider = RTooltip.Provider
export function Tip({ content, children, side = 'right' }: { content: ReactNode; children: ReactNode; side?: 'top' | 'right' | 'bottom' | 'left' }) {
  return (
    <RTooltip.Root delayDuration={250}>
      <RTooltip.Trigger asChild>{children}</RTooltip.Trigger>
      <RTooltip.Portal>
        <RTooltip.Content side={side} sideOffset={8} className="z-50 rounded-md border border-line-strong bg-raised px-2 py-1 text-xs text-ink animate-[pop-in_.12s_ease-out]">
          {content}
        </RTooltip.Content>
      </RTooltip.Portal>
    </RTooltip.Root>
  )
}

/* ---------- Dialog ---------- */
export function Dialog({ open, onOpenChange, title, description, children, footer, width = 'max-w-md' }: {
  open: boolean; onOpenChange: (o: boolean) => void; title: string; description?: string; children?: ReactNode; footer?: ReactNode; width?: string
}) {
  return (
    <RDialog.Root open={open} onOpenChange={onOpenChange}>
      <RDialog.Portal>
        <RDialog.Overlay className="fixed inset-0 z-50 bg-black/60 animate-[overlay-in_.15s_ease-out]" />
        <RDialog.Content className={cn('fixed left-1/2 top-[14vh] z-50 w-[calc(100%-2rem)] -translate-x-1/2 rounded-lg border border-line-strong bg-surface animate-[pop-in_.14s_ease-out]', width)}>
          <div className="flex items-start justify-between gap-4 border-b border-line px-5 py-4">
            <div>
              <RDialog.Title className="text-sm font-semibold text-ink">{title}</RDialog.Title>
              {description && <RDialog.Description className="mt-1 text-xs text-muted">{description}</RDialog.Description>}
            </div>
            <RDialog.Close className="rounded p-1 text-faint hover:bg-hover hover:text-ink" aria-label="Close"><X className="size-4" /></RDialog.Close>
          </div>
          {children && <div className="px-5 py-4">{children}</div>}
          {footer && <div className="flex justify-end gap-2 border-t border-line px-5 py-3">{footer}</div>}
        </RDialog.Content>
      </RDialog.Portal>
    </RDialog.Root>
  )
}

/* ---------- Styled Select (Radix) ---------- */
export function Select({ value, onValueChange, options, placeholder, className }: {
  value: string; onValueChange: (v: string) => void; options: { value: string; label: string }[]; placeholder?: string; className?: string
}) {
  return (
    <RSelect.Root value={value} onValueChange={onValueChange}>
      <RSelect.Trigger className={cn(field, 'inline-flex h-8 items-center justify-between gap-2', className)}>
        <RSelect.Value placeholder={placeholder} />
        <RSelect.Icon><ChevronDown className="size-3.5 text-faint" /></RSelect.Icon>
      </RSelect.Trigger>
      <RSelect.Portal>
        <RSelect.Content position="popper" sideOffset={4} className="z-50 min-w-[var(--radix-select-trigger-width)] rounded-md border border-line-strong bg-raised p-1 animate-[pop-in_.12s_ease-out]">
          <RSelect.Viewport>
            {options.map((o) => (
              <RSelect.Item key={o.value} value={o.value} className="flex h-7 cursor-default select-none items-center justify-between gap-3 rounded px-2 text-[13px] text-ink outline-none data-[highlighted]:bg-hover">
                <RSelect.ItemText>{o.label}</RSelect.ItemText>
                <RSelect.ItemIndicator><Check className="size-3.5 text-accent" /></RSelect.ItemIndicator>
              </RSelect.Item>
            ))}
          </RSelect.Viewport>
        </RSelect.Content>
      </RSelect.Portal>
    </RSelect.Root>
  )
}
