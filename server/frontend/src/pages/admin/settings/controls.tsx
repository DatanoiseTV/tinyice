import { useRef, useState } from 'react'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { Button, Input, Segmented, Textarea } from '@/ui/controls'
import { Badge } from '@/ui/display'
import { Legend } from '@/ui/console'
import { useConfirm } from '@/ui/kit'
import { Icon } from '@/ui/icons'
import { toast } from '@/lib/toast'
import { cn } from '@/lib/cn'
import { redirectToLogin } from '@/lib/api'
import { contrastRows, parseHex } from './contrast'
import type { Branding } from './types'

/* ---------- Listener limit ---------- */
const PRESETS = ['25', '50', '100', '250', '500', '1000', '0']
const nf = new Intl.NumberFormat('en')

/** Outbound bandwidth for n listeners on a 128 kbps stream: 16 KB/s each. */
export function bandwidthLine(n: number): string {
  if (n === 0) return 'No cap. Each listener on a 128 kbps stream uses about 16 KB/s, so outbound bandwidth grows with the audience.'
  const kb = n * 16
  const mb = kb / 1000
  return `${nf.format(n)} listeners at 128 kbps is about ${mb >= 10 ? Math.round(mb) : mb.toFixed(1)} MB/s (${nf.format(Math.round(n * 0.128 * 10) / 10)} Mbit/s) of outbound bandwidth per stream.`
}

export function ListenerLimit({ value, onChange, label }: { value: string; onChange: (v: string) => void; label: string }) {
  const n = /^\d+$/.test(value.trim()) ? Number(value) : NaN
  return (
    <div className="space-y-3">
      <div role="radiogroup" aria-label={`${label} presets`} className="flex flex-wrap gap-1 rounded-md border border-line bg-surface p-0.5">
        {PRESETS.map((p) => {
          const on = value.trim() === p
          return (
            <button key={p} type="button" role="radio" aria-checked={on} onClick={() => onChange(p)}
              className={cn('h-6 rounded-[4px] px-2.5 text-xs font-medium transition-colors', on ? 'bg-hover text-ink' : 'text-faint hover:text-muted')}>
              {p === '0' ? 'Unlimited' : nf.format(Number(p))}
            </button>
          )
        })}
      </div>
      <div className="flex items-center gap-2">
        <span className="text-xs text-muted">Custom</span>
        <div className="w-32"><Input inputMode="numeric" mono className="num" aria-label={label} value={value} onChange={(e) => onChange(e.target.value)} placeholder="e.g. 300" /></div>
        <span className="text-xs text-faint">0 = unlimited</span>
      </div>
      <p className="text-xs text-muted" aria-live="polite">{Number.isFinite(n) && n <= 1_000_000 ? bandwidthLine(n) : 'Enter a whole number from 0 to 1,000,000.'}</p>
    </div>
  )
}

/* ---------- Logo ---------- */
const LOGO_EXT = /\.(png|jpe?g|gif|webp)$/i

export function LogoControl({ draft, saved, setPath, logoVer, onUploaded }: {
  draft: Branding; saved: Branding; setPath: (p: string) => void; logoVer: number; onUploaded: (path: string) => void
}) {
  const fileRef = useRef<HTMLInputElement>(null)
  const [uploading, setUploading] = useState(false)
  const confirm = useConfirm()
  const logoSrc = draft.logo_path ? `/branding/logo?v=${logoVer}` : null
  async function upload(file: File) {
    if (!LOGO_EXT.test(file.name)) return toast.error('Logo must be a PNG, JPEG, GIF or WebP image. SVG is refused by the server.')
    if (file.size > 2 << 20) return toast.error('Logo must be 2 MB or smaller')
    setUploading(true)
    try {
      const form = new FormData()
      form.append('logo', file)
      const res = await fetch('/api/branding/logo', { method: 'POST', body: form, credentials: 'same-origin' })
      if (res.status === 401) { redirectToLogin(); return }
      const data = await res.json().catch(() => ({} as { error?: string; path?: string }))
      if (!res.ok) throw new Error(data.error || res.statusText)
      onUploaded(data.path || 'branding/logo')
      toast.success('Logo uploaded')
    } catch (e) { toast.error((e as Error).message) }
    finally { setUploading(false); if (fileRef.current) fileRef.current.value = '' }
  }
  return (
    <div className="flex items-center gap-4">
      <div className="flex size-16 shrink-0 items-center justify-center overflow-hidden rounded-lg border border-line-strong bg-raised">
        {logoSrc ? <img src={logoSrc} alt="Current logo" className="size-full object-cover" /> : <span className="font-mono text-sm font-semibold text-faint">{(draft.page_title || 'Ti').slice(0, 2).toUpperCase()}</span>}
      </div>
      <div className="space-y-2">
        <input ref={fileRef} type="file" accept="image/png,image/jpeg,image/gif,image/webp" className="sr-only" aria-label="Choose logo file" tabIndex={-1} onChange={(e) => e.target.files?.[0] && upload(e.target.files[0])} />
        <div className="flex gap-2">
          <Button size="sm" loading={uploading} onClick={() => fileRef.current?.click()}><Icon name="upload" size={14} />{draft.logo_path ? 'Replace' : 'Upload'}</Button>
          {draft.logo_path && <Button size="sm" variant="danger" onClick={() => confirm.ask({ title: 'Remove the logo?', body: 'The initials are used instead. Takes effect when you save.', confirm: 'Remove', run: () => setPath('') })}>Remove</Button>}
        </div>
        {!draft.logo_path && !!saved.logo_path && <Badge tone="warn">Removal pending save</Badge>}
        <p className="text-xs text-faint">PNG, JPEG, GIF or WebP, up to 2 MB. Uploads apply at once.</p>
      </div>
      {confirm.dialog}
    </div>
  )
}

/* ---------- Accent ---------- */
const SWATCHES = ['#ff6a00', '#e5484d', '#e93d82', '#8e4ec6', '#3e63dd', '#0091ff', '#12a594', '#30a46c', '#f5a524', '#6e56cf']
const WARN: Record<string, string> = {
  btn: 'Button labels are hard to read on this colour.',
  dark: 'On the dark theme the accent is too dim for links, tabs and focus rings.',
  light: 'On the light theme the accent is too pale for links, tabs and focus rings.',
}

export function AccentControl({ value, saved, onChange, invalid }: { value: string; saved: string; onChange: (v: string) => void; invalid: boolean }) {
  const ok = !!parseHex(value)
  const rows = ok ? contrastRows(value) : []
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-1.5" role="radiogroup" aria-label="Accent swatches">
        {SWATCHES.map((c) => (
          <button key={c} role="radio" aria-checked={value.toLowerCase() === c} aria-label={c} title={c} onClick={() => onChange(c)} style={{ backgroundColor: c }}
            className={cn('size-7 rounded-md border transition-transform hover:scale-105', value.toLowerCase() === c ? 'border-ink ring-2 ring-ink/30' : 'border-line-strong')} />
        ))}
      </div>
      <div className="flex items-center gap-2">
        <label className="relative size-8 shrink-0 cursor-pointer overflow-hidden rounded-md border border-line-strong" style={{ backgroundColor: ok ? value : 'transparent' }}>
          <input type="color" aria-label="Pick a colour" value={ok ? value.toLowerCase() : '#ff6a00'} onChange={(e) => onChange(e.target.value)} className="absolute inset-0 size-full cursor-pointer opacity-0" />
        </label>
        <div className="w-32"><Input mono value={value} onChange={(e) => onChange(e.target.value.trim())} placeholder="#ff6a00" aria-label="Accent hex value" aria-invalid={invalid} spellCheck={false} maxLength={7} /></div>
        {value !== saved && <Button size="sm" variant="ghost" onClick={() => onChange(saved)}>Undo</Button>}
      </div>
      {ok && (
        <div>
          <Legend>Contrast</Legend>
          <ul className="mt-1.5 divide-y divide-line rounded-md border border-line" aria-live="polite">
            {rows.map((r) => (
              <li key={r.key} className="flex items-center gap-3 px-3 py-2 text-xs">
                <div className="min-w-0 flex-1"><div className="text-ink">{r.label}</div><div className="text-faint">{r.use}</div></div>
                <span className="num font-mono text-ink">{r.ratio.toFixed(1)}:1</span>
                <Badge tone={r.pass ? 'neutral' : 'danger'}>{r.pass ? (r.need === 'text' ? 'Pass AA' : 'Pass 3:1') : `Below ${r.need === 'text' ? '4.5' : '3'}:1`}</Badge>
              </li>
            ))}
          </ul>
          {rows.some((r) => !r.pass) && (
            <div className="mt-2 space-y-1 text-xs text-warn" role="status">
              {rows.filter((r) => !r.pass).map((r) => <p key={r.key} className="flex gap-2"><Icon name="alert" size={14} className="mt-px shrink-0" />{WARN[r.key]}</p>)}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

/* ---------- Landing text ---------- */
export function renderMarkdown(md: string): string {
  return DOMPurify.sanitize(marked.parse(md, { breaks: true, gfm: true }) as string, { USE_PROFILES: { html: true } })
}
const PROSE = 'text-[13px] leading-relaxed text-ink [&_h1]:mb-2 [&_h1]:text-lg [&_h1]:font-semibold [&_h2]:mb-2 [&_h2]:mt-4 [&_h2]:text-base [&_h2]:font-semibold [&_h3]:mb-1.5 [&_h3]:mt-3 [&_h3]:font-semibold [&_p]:mb-3 [&_ul]:mb-3 [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:mb-3 [&_ol]:list-decimal [&_ol]:pl-5 [&_a]:text-accent [&_a]:underline [&_a]:underline-offset-2 [&_code]:rounded [&_code]:bg-hover [&_code]:px-1 [&_code]:font-mono [&_code]:text-xs [&_pre]:mb-3 [&_pre]:overflow-x-auto [&_pre]:rounded-md [&_pre]:bg-bg [&_pre]:p-3 [&_blockquote]:border-l-2 [&_blockquote]:border-line-strong [&_blockquote]:pl-3 [&_blockquote]:text-muted [&_img]:max-w-full [&_hr]:my-4 [&_hr]:border-line'
const PLACEHOLDER = 'Welcome to our station.\n\n## Schedule\n- **Mon-Fri** 8am-10pm: live shows\n- **Weekends**: AutoDJ\n\n[Listen now](/explore)'

export function LandingControl({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const [mode, setMode] = useState<'write' | 'preview'>('write')
  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <Segmented label="Landing text view" value={mode} onChange={setMode} options={[{ value: 'write', label: 'Write' }, { value: 'preview', label: 'Preview' }]} />
        <span className="num font-mono text-[11px] text-faint">{value.length.toLocaleString()} characters</span>
      </div>
      {mode === 'write'
        ? <Textarea mono rows={12} value={value} onChange={(e) => onChange(e.target.value)} aria-label="Landing page text" placeholder={PLACEHOLDER} spellCheck={false} className="min-h-60" />
        : <div className="min-h-60 rounded-md border border-line bg-bg p-4" aria-label="Rendered preview">
            {value.trim() ? <div className={PROSE} dangerouslySetInnerHTML={{ __html: renderMarkdown(value) }} /> : <p className="text-xs text-faint">Nothing to show. The landing page lists live streams without an intro.</p>}
          </div>}
    </div>
  )
}
