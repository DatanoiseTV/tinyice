import { useMemo, useRef, useState } from 'react'
import { api } from '@/lib/api'
import { toast } from '@/lib/toast'
import { Button, Field, Input, Select, Switch, Textarea } from '@/ui/controls'
import { Badge } from '@/ui/display'
import { Checkbox, CodeBlock, useAction } from '@/ui/kit'
import { Legend, Panel } from '@/ui/console'
import { Icon } from '@/ui/icons'
import { cn } from '@/lib/cn'
import { METHODS, type Preset, type Webhook, type WebhookBody, type WebhookMeta } from './types'
import { isPrivateHost, isSensitiveHeader, parseTemplate, previewRequest, TemplateError, type PreviewEnv } from './template'

interface HeaderRow { rid: number; key: string; value: string; reveal: boolean }
let ridSeq = 1
const rowsFrom = (h?: Record<string, string>): HeaderRow[] => Object.entries(h ?? {}).map(([key, value]) => ({ rid: ridSeq++, key, value, reveal: false }))

const TOKEN = /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/

interface Draft { name: string; url: string; method: string; contentType: string; events: string[]; headers: HeaderRow[]; template: string; enabled: boolean }

function draftFrom(w: Webhook | null): Draft {
  return {
    name: w?.name ?? '', url: w?.url ?? '', method: w?.method || 'POST', contentType: w?.content_type || 'application/json',
    events: w ? [...w.events] : ['now_playing'], headers: rowsFrom(w?.headers), template: w?.body_template ?? '', enabled: w?.enabled ?? true,
  }
}

function headerObject(rows: HeaderRow[]): Record<string, string> {
  const o: Record<string, string> = {}
  for (const r of rows) if (r.key.trim()) o[r.key.trim()] = r.value
  return o
}

function toWire(d: Draft): WebhookBody {
  return { name: d.name.trim(), url: d.url.trim(), method: d.method, events: d.events, headers: headerObject(d.headers), body_template: d.template, content_type: d.contentType.trim(), enabled: d.enabled }
}

function validate(d: Draft): { url?: string; events?: string; headers?: string; template?: string; contentType?: string } {
  const e: ReturnType<typeof validate> = {}
  const url = d.url.trim()
  if (!url) e.url = 'URL is required'
  else {
    try {
      const u = new URL(url)
      if (u.protocol !== 'http:' && u.protocol !== 'https:') e.url = 'Use an http:// or https:// URL'
      else if (isPrivateHost(u.hostname)) e.url = 'The server refuses loopback, private and link-local addresses'
    } catch { e.url = 'Not a valid URL' }
  }
  if (d.events.length === 0) e.events = 'Pick at least one event'
  const seen = new Set<string>()
  for (const r of d.headers) {
    const k = r.key.trim()
    if (!k) { if (r.value) e.headers = 'A header value needs a name'; continue }
    if (!TOKEN.test(k)) { e.headers = `"${k}" is not a valid header name`; break }
    if (seen.has(k.toLowerCase())) { e.headers = `Header "${k}" appears twice`; break }
    seen.add(k.toLowerCase())
  }
  if (!d.contentType.trim()) e.contentType = 'Content-Type is required (default application/json)'
  if (d.template.trim()) {
    try { parseTemplate(d.template) } catch (x) { if (x instanceof TemplateError) e.template = x.message }
  }
  return e
}

export default function WebhookEditor({ initial, meta, env, onClose, onSaved }: {
  initial: Webhook | null; meta: WebhookMeta; env: PreviewEnv; onClose: () => void; onSaved: () => void
}) {
  const [d, setD] = useState<Draft>(() => draftFrom(initial))
  const base = useRef(JSON.stringify(toWire(draftFrom(initial))))
  const [touched, setTouched] = useState(false)
  const [serverError, setServerError] = useState('')
  const [previewEvent, setPreviewEvent] = useState(initial?.events[0] ?? 'now_playing')
  const taRef = useRef<HTMLTextAreaElement>(null)
  const { run, isBusy } = useAction()

  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => setD((p) => ({ ...p, [k]: v }))
  const errs = useMemo(() => validate(d), [d])
  const dirty = JSON.stringify(toWire(d)) !== base.current
  const hasErrors = Object.keys(errs).length > 0
  const show = (k: keyof typeof errs) => (touched ? errs[k] : undefined)

  const evNames = d.events.length ? d.events : meta.events.map((e) => e.name)
  const activeEvent = meta.events.find((e) => e.name === (evNames.includes(previewEvent) ? previewEvent : evNames[0])) ?? meta.events[0]
  const wire = toWire(d)
  const preview = useMemo(
    () => previewRequest({ id: initial?.id, url: wire.url, method: wire.method, content_type: wire.content_type, headers: wire.headers, body_template: wire.body_template }, activeEvent, env),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [d, activeEvent, env, initial?.id],
  )

  function applyPreset(p: Preset) {
    setD((cur) => ({
      ...cur,
      // The URL hint is a template with <placeholders>; never overwrite a real URL the operator typed.
      url: cur.url.trim() ? cur.url : p.url_hint,
      method: p.method || 'POST', contentType: p.content_type || 'application/json', template: p.body, headers: rowsFrom(p.headers),
      events: !initial && p.events.length ? [...p.events] : cur.events,
    }))
    if (p.events[0]) setPreviewEvent(p.events[0])
    toast.info(`Loaded preset: ${p.name}`)
  }

  function insert(ph: string) {
    const token = `{{.${ph}}}`
    const ta = taRef.current
    const s = ta?.selectionStart ?? d.template.length, e = ta?.selectionEnd ?? d.template.length
    set('template', d.template.slice(0, s) + token + d.template.slice(e))
    requestAnimationFrame(() => { ta?.focus(); ta?.setSelectionRange(s + token.length, s + token.length) })
  }

  async function save() {
    setTouched(true)
    setServerError('')
    if (hasErrors) return
    const body = toWire(d)
    try {
      await (initial ? api.put(`/api/webhooks?id=${encodeURIComponent(initial.id)}`, body) : api.post('/api/webhooks', body))
      toast.success(initial ? 'Webhook updated' : 'Webhook created')
      onSaved()
    } catch (e) { setServerError((e as Error).message || 'Save failed') }
  }

  const selectedPreset = '__none__'
  return (
    <div className="space-y-3">
      <div className="grid grid-cols-[minmax(0,1fr)] gap-3 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.15fr)]">
        <div className="space-y-3">
          <Panel legend="Destination">
            <div className="space-y-3.5">
              {meta.presets.length > 0 && (
                <Field label="Load preset" hint="Fills method, headers and body. Your URL is kept if you already typed one.">
                  <Select value={selectedPreset} onValueChange={(v) => { const p = meta.presets.find((x) => x.id === v); if (p) applyPreset(p) }}
                    options={[{ value: selectedPreset, label: 'Choose a template' }, ...meta.presets.map((p) => ({ value: p.id, label: p.name }))]} />
                </Field>
              )}
              <Field label="Name (optional)"><Input value={d.name} onChange={(e) => set('name', e.target.value)} placeholder="Discord, #now-playing" maxLength={80} /></Field>
              <Field label="URL" error={show('url')} hint="Tokens in the path are hidden in the list. Private and loopback hosts are refused.">
                <Input mono type="url" value={d.url} onChange={(e) => set('url', e.target.value)} placeholder="https://example.com/webhook" aria-invalid={!!show('url')} />
              </Field>
              <div className="grid grid-cols-[7rem_1fr] gap-3">
                <Field label="Method">
                  <Select value={d.method} onValueChange={(v) => set('method', v)} options={METHODS.map((m) => ({ value: m, label: m }))} />
                </Field>
                <Field label="Content-Type" error={show('contentType')}>
                  <Input mono value={d.contentType} onChange={(e) => set('contentType', e.target.value)} placeholder="application/json" />
                </Field>
              </div>
              <div className="flex items-center justify-between rounded-md border border-line px-3 py-2">
                <div><div className="text-[13px] font-medium text-ink">Enabled</div><div className="text-xs text-muted">Disabled webhooks keep their settings but receive nothing. Test sends still work.</div></div>
                <Switch label="Enabled" checked={d.enabled} onCheckedChange={(v) => set('enabled', v)} />
              </div>
            </div>
          </Panel>

          <Panel legend="Events" actions={<Badge tone={d.events.length ? 'accent' : 'neutral'}>{d.events.length} selected</Badge>}>
            <ul className="space-y-1">
              {meta.events.map((ev) => {
                const on = d.events.includes(ev.name)
                return (
                  <li key={ev.name}>
                    <label className="flex cursor-pointer items-start gap-3 rounded-md px-2 py-2 hover:bg-hover/60">
                      <span className="mt-0.5"><Checkbox label={ev.name} checked={on} onChange={(v) => { set('events', v ? [...d.events, ev.name] : d.events.filter((x) => x !== ev.name)); if (v) setPreviewEvent(ev.name) }} /></span>
                      <span className="min-w-0"><span className="block font-mono text-xs text-ink">{ev.name}</span><span className="block text-xs text-muted">{ev.description}</span></span>
                    </label>
                  </li>
                )
              })}
            </ul>
            {show('events') && <p className="mt-2 text-xs text-danger">{show('events')}</p>}
          </Panel>

          <Panel legend="Headers" actions={<Button size="sm" variant="ghost" onClick={() => set('headers', [...d.headers, { rid: ridSeq++, key: '', value: '', reveal: false }])}><Icon name="plus" size={14} />Add</Button>}>
            {d.headers.length === 0 ? (
              <p className="text-xs text-muted">No custom headers. Content-Type, User-Agent, X-TinyIce-Event and X-TinyIce-Webhook-ID are always sent.</p>
            ) : (
              <ul className="space-y-2">
                {d.headers.map((r) => {
                  const secret = isSensitiveHeader(r.key) && !r.reveal
                  return (
                    <li key={r.rid} className="flex gap-2">
                      <Input mono aria-label="Header name" value={r.key} onChange={(e) => set('headers', d.headers.map((x) => (x.rid === r.rid ? { ...x, key: e.target.value } : x)))} placeholder="Authorization" className="w-2/5" />
                      <div className="relative min-w-0 flex-1">
                        <Input mono aria-label={`Value of ${r.key || 'header'}`} type={secret ? 'password' : 'text'} autoComplete="off" value={r.value}
                          onChange={(e) => set('headers', d.headers.map((x) => (x.rid === r.rid ? { ...x, value: e.target.value } : x)))} placeholder="Bearer ..." className={isSensitiveHeader(r.key) ? 'pr-8' : ''} />
                        {isSensitiveHeader(r.key) && (
                          <button type="button" aria-label={r.reveal ? 'Hide value' : 'Show value'} aria-pressed={r.reveal} onClick={() => set('headers', d.headers.map((x) => (x.rid === r.rid ? { ...x, reveal: !x.reveal } : x)))}
                            className={cn('absolute right-1.5 top-1/2 flex size-6 -translate-y-1/2 items-center justify-center rounded text-faint hover:text-ink', r.reveal && 'text-accent')}><Icon name="eye" size={14} /></button>
                        )}
                      </div>
                      <Button size="icon" variant="ghost" aria-label="Remove header" onClick={() => set('headers', d.headers.filter((x) => x.rid !== r.rid))}><Icon name="x" size={14} /></Button>
                    </li>
                  )
                })}
              </ul>
            )}
            {show('headers') && <p className="mt-2 text-xs text-danger">{show('headers')}</p>}
            <p className="mt-3 text-xs text-faint">Values may use {'{{.Placeholders}}'}. Values for names that look like credentials are masked here; the server stores and returns them as plain text.</p>
          </Panel>
        </div>

        <div className="space-y-3">
          <Panel legend="Body template">
            <Textarea ref={taRef} mono rows={11} value={d.template} onChange={(e) => set('template', e.target.value)} aria-label="Body template" aria-invalid={!!show('template')}
              placeholder={'Empty sends the default JSON envelope.\n\n{\n  "content": "Now playing: {{.Artist}} - {{.Title}}"\n}'} spellCheck={false} />
            {errs.template && <p className="mt-1.5 text-xs text-danger">Template does not parse: {errs.template}</p>}
            <div className="mt-3 space-y-2">
              <Legend>Insert placeholder</Legend>
              <div className="flex flex-wrap gap-1">
                {[...meta.global_placeholders, ...activeEvent.placeholders.filter((p) => !meta.global_placeholders.includes(p))].map((ph) => (
                  <button key={ph} type="button" onClick={() => insert(ph)} title={`Insert {{.${ph}}}`}
                    className={cn('h-6 rounded border px-1.5 font-mono text-[11px] transition-colors hover:border-accent hover:text-accent', meta.global_placeholders.includes(ph) ? 'border-line text-faint' : 'border-line-strong text-muted')}>{ph}</button>
                ))}
              </div>
              <p className="text-xs text-faint">Grey chips are always available; the others come from the previewed event.</p>
            </div>
            <details className="mt-3 text-xs text-muted">
              <summary className="cursor-pointer text-faint hover:text-ink">Template functions</summary>
              <ul className="mt-2 space-y-1.5">
                {meta.funcs.map((f) => <li key={f.name}><code className="font-mono text-ink">{f.name}</code> {f.description} <code className="font-mono text-faint">{f.example}</code></li>)}
              </ul>
            </details>
          </Panel>

          <Panel legend="Request preview" actions={
            <Select value={activeEvent.name} onValueChange={setPreviewEvent} className="!h-7 w-44 text-xs" options={evNames.map((n) => ({ value: n, label: n }))} />
          }>
            <p className="mb-3 text-xs text-muted">{activeEvent.description} Rendered from the sample payload; the live values differ.</p>
            <div className="mb-2 flex items-center gap-2"><Badge>{preview.method}</Badge><span className="min-w-0 break-all font-mono text-xs text-ink" data-testid="preview-url">{preview.url || '(no URL yet)'}</span></div>
            <div className="mb-3 rounded-md border border-line bg-bg p-2.5 font-mono text-[11px] leading-relaxed text-muted">
              {preview.headers.map(([k, v]) => <div key={k} className="break-all"><span className="text-faint">{k}:</span> {isSensitiveHeader(k) ? '••••••' : v}</div>)}
            </div>
            {preview.problem ? (
              <div role="status" className={cn('flex gap-2 rounded-md border p-3 text-xs', preview.problem.kind === 'error' ? 'border-danger/40 text-danger' : 'border-warn/40 text-warn')}>
                <Icon name="alert" size={14} className="mt-px shrink-0" /><span>{preview.problem.message}</span>
              </div>
            ) : preview.body ? <CodeBlock label="Body">{preview.body}</CodeBlock> : <p className="text-xs text-faint">No body is sent.</p>}
            {preview.notes.map((n) => <p key={n} className="mt-2 text-xs text-faint">{n}</p>)}
          </Panel>
        </div>
      </div>

      <div className="sticky bottom-0 z-10 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-line-strong bg-surface px-4 py-2.5" role="region" aria-label="Save">
        <div className="min-w-0 text-xs" aria-live="polite">
          {serverError ? <span className="text-danger">{serverError}</span>
            : touched && hasErrors ? <span className="text-danger">Fix the highlighted fields to save.</span>
            : dirty ? <span className="text-warn">Unsaved changes</span> : <span className="text-faint">No changes</span>}
        </div>
        <div className="flex gap-2">
          <Button variant="ghost" onClick={onClose}>{dirty ? 'Discard' : 'Close'}</Button>
          <Button variant="primary" loading={isBusy('save')} disabled={!dirty && !!initial} onClick={() => run('save', save)}>{initial ? 'Save changes' : 'Create webhook'}</Button>
        </div>
      </div>
    </div>
  )
}

