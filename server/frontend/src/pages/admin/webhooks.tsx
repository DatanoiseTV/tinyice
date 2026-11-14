import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, page } from '@/lib/api'
import { Button, Switch } from '@/ui/controls'
import { Badge, EmptyState, PageHeader, Skeleton } from '@/ui/display'
import { Panel } from '@/ui/console'
import { Menu, useAction, useConfirm } from '@/ui/kit'
import { Icon } from '@/ui/icons'
import { cn } from '@/lib/cn'
import WebhookEditor from './webhooks/WebhookEditor'
import TestDialog, { type TestRun } from './webhooks/TestDialog'
import { maskUrl, type PreviewEnv } from './webhooks/template'
import { toBody, type Webhook, type WebhookMeta } from './webhooks/types'

function Skeletons() {
  return <div className="space-y-px">{[0, 1, 2].map((i) => <div key={i} className="flex items-center gap-4 px-4 py-4"><Skeleton className="h-8 w-8" /><div className="flex-1 space-y-2"><Skeleton className="h-4 w-48" /><Skeleton className="h-3 w-72" /></div></div>)}</div>
}

export default function WebhooksPage() {
  const isSuper = page().user?.role === 'superadmin'
  const [hooks, setHooks] = useState<Webhook[] | null>(null)
  const [meta, setMeta] = useState<WebhookMeta | null>(null)
  const [env, setEnv] = useState<PreviewEnv>({ hostname: location.hostname, baseUrl: '', version: page().version ?? '' })
  const [error, setError] = useState('')
  const [editing, setEditing] = useState<Webhook | 'new' | null>(null)
  const [testing, setTesting] = useState<Webhook | null>(null)
  const [runs, setRuns] = useState<Record<string, TestRun[]>>({})
  const { run, isBusy } = useAction()
  const confirm = useConfirm()

  const load = useCallback(async () => {
    try { setHooks((await api.get<Webhook[] | null>('/api/webhooks')) ?? []); setError('') }
    catch (e) { setError((e as Error).message); setHooks((h) => h ?? []) }
  }, [])

  useEffect(() => {
    if (!isSuper) return
    load()
    api.get<WebhookMeta>('/api/webhooks/meta').then(setMeta).catch((e) => setError((e as Error).message))
    api.get<{ hostname: string; base_url: string }>('/api/settings')
      .then((s) => setEnv((p) => ({ ...p, hostname: s.hostname || p.hostname, baseUrl: s.base_url || '' }))).catch(() => {})
  }, [isSuper, load])

  const stats = useMemo(() => ({ on: (hooks ?? []).filter((h) => h.enabled).length, total: hooks?.length ?? 0 }), [hooks])

  if (!isSuper) {
    return (<><PageHeader title="Webhooks" /><Panel legend="Access"><EmptyState icon={<Icon name="lock" size={18} />} title="Superadmin only" body="Webhooks carry destination URLs and credentials, so only superadmin accounts can view or change them." /></Panel></>)
  }

  const toggle = (h: Webhook, enabled: boolean) => run(`tg-${h.id}`, async () => {
    await api.put(`/api/webhooks?id=${encodeURIComponent(h.id)}`, { ...toBody(h), enabled })
    await load()
  })
  const duplicate = (h: Webhook) => run(`dup-${h.id}`, async () => {
    await api.post('/api/webhooks', { ...toBody(h), name: `${h.name || maskUrl(h.url)} (copy)`, enabled: false })
    await load()
  }, 'Duplicated, disabled until you enable it')
  const remove = (h: Webhook) => confirm.ask({
    title: `Delete "${h.name || maskUrl(h.url)}"?`, body: 'The receiver stops getting events immediately. This cannot be undone.',
    run: () => run(`del-${h.id}`, async () => { await api.del(`/api/webhooks?id=${encodeURIComponent(h.id)}`); await load() }, 'Webhook deleted'),
  })

  if (editing && meta) {
    return (
      <>
        <PageHeader title={editing === 'new' ? 'New webhook' : 'Edit webhook'} sub="Template, headers and a rendered preview of what the receiver gets."
          actions={<Button variant="ghost" onClick={() => setEditing(null)}><Icon name="prev" size={14} />All webhooks</Button>} />
        <WebhookEditor key={editing === 'new' ? 'new' : editing.id} initial={editing === 'new' ? null : editing} meta={meta} env={env}
          onClose={() => setEditing(null)} onSaved={() => { setEditing(null); load() }} />
      </>
    )
  }

  return (
    <>
      <PageHeader title="Webhooks" sub="Outbound HTTP notifications for stream, source and security events. Best effort, no retries."
        actions={<Button variant="primary" disabled={!meta} onClick={() => setEditing('new')}><Icon name="plus" size={14} />New webhook</Button>} />
      {error && (
        <div role="alert" className="mb-3 flex items-center justify-between gap-3 rounded-md border border-danger/40 px-3 py-2 text-xs text-danger">
          <span>{error}</span><Button size="sm" variant="ghost" onClick={load}>Retry</Button>
        </div>
      )}
      <Panel legend="Subscriptions" flush actions={hooks && <span className="num font-mono text-[11px] text-faint">{stats.on} of {stats.total} enabled</span>}>
        {hooks === null ? <Skeletons /> : hooks.length === 0 ? (
          <EmptyState icon={<Icon name="webhook" size={18} />} title="No webhooks yet"
            body="Push now-playing updates to Discord, Slack, ntfy or any URL, and get told when a source connects, drops or an IP is locked out."
            action={<Button variant="primary" disabled={!meta} onClick={() => setEditing('new')}><Icon name="plus" size={14} />Create the first one</Button>} />
        ) : (
          <ul className="divide-y divide-line">
            {hooks.map((h) => (
              <li key={h.id} className={cn('flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3', !h.enabled && 'opacity-70')}>
                <div className="min-w-0 flex-1 basis-64">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="max-w-full truncate text-[13px] font-medium text-ink">{h.name || maskUrl(h.url)}</span>
                    <Badge>{h.method || 'POST'}</Badge>
                    {!h.enabled && <Badge tone="warn">Disabled</Badge>}
                    {h.body_template ? <Badge tone="info">Template</Badge> : null}
                  </div>
                  {h.name && <div className="mt-0.5 truncate font-mono text-xs text-faint" title="Credentials in the URL are hidden">{maskUrl(h.url)}</div>}
                  <div className="mt-1.5 flex flex-wrap gap-1">{h.events.map((e) => <Badge key={e} tone="accent">{e}</Badge>)}</div>
                </div>
                <div className="flex items-center gap-1.5">
                  {runs[h.id]?.[0] && <span className="mr-1 text-[11px] text-faint" title="Last test send this session">Tested {runs[h.id][0].at.toLocaleTimeString()}</span>}
                  <Button size="sm" onClick={() => setTesting(h)} disabled={!meta}><Icon name="bolt" size={14} />Test</Button>
                  <Switch label={`${h.enabled ? 'Disable' : 'Enable'} ${h.name || 'webhook'}`} checked={h.enabled} disabled={isBusy(`tg-${h.id}`)} onCheckedChange={(v) => toggle(h, v)} />
                  <Button size="icon" variant="ghost" aria-label={`Edit ${h.name || 'webhook'}`} disabled={!meta} onClick={() => setEditing(h)}><Icon name="edit" size={14} /></Button>
                  <Menu label={`More actions for ${h.name || 'webhook'}`} actions={[{ label: 'Duplicate', icon: 'copy', onSelect: () => duplicate(h) }, 'sep', { label: 'Delete', icon: 'trash', danger: true, onSelect: () => remove(h) }]} />
                </div>
              </li>
            ))}
          </ul>
        )}
      </Panel>
      {meta && (
        <TestDialog hook={testing} meta={meta} env={env} runs={testing ? runs[testing.id] ?? [] : []}
          onRun={(r) => testing && setRuns((p) => ({ ...p, [testing.id]: [r, ...(p[testing.id] ?? [])].slice(0, 5) }))} onClose={() => setTesting(null)} />
      )}
      {confirm.dialog}
    </>
  )
}
