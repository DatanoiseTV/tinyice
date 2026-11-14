import { useState } from 'react'
import { api } from '@/lib/api'
import { Button, Dialog, Select } from '@/ui/controls'
import { Badge } from '@/ui/display'
import { CodeBlock, useAction } from '@/ui/kit'
import { Legend } from '@/ui/console'
import { Icon } from '@/ui/icons'
import { maskUrl, previewRequest, type PreviewEnv } from './template'
import { toBody, type TestResult, type Webhook, type WebhookMeta } from './types'

export interface TestRun { at: Date; event: string; result?: TestResult; error?: string }

/**
 * Test-fire. POST /api/webhooks/test only queues the delivery and answers
 * {status:'queued'}; the receiver's response is logged server-side and never
 * returned. The dialog shows exactly what is sent and reports the real
 * outcome we have. http_status / latency_ms / response_excerpt render only
 * if the server starts returning them.
 */
export default function TestDialog({ hook, meta, env, runs, onRun, onClose }: {
  hook: Webhook | null; meta: WebhookMeta; env: PreviewEnv; runs: TestRun[]; onRun: (r: TestRun) => void; onClose: () => void
}) {
  const [event, setEvent] = useState('')
  const { run, isBusy } = useAction()
  if (!hook) return <Dialog open={false} onOpenChange={onClose} title="" />
  const ev = event && hook.events.includes(event) ? event : hook.events[0]
  const info = meta.events.find((e) => e.name === ev) ?? meta.events[0]
  const b = toBody(hook)
  const pv = previewRequest({ id: hook.id, url: b.url, method: b.method, content_type: b.content_type, headers: b.headers, body_template: b.body_template }, info, env)
  const mine = runs.slice(0, 5)

  async function fire() {
    const at = new Date()
    try {
      const result = await api.post<TestResult>(`/api/webhooks/test?id=${encodeURIComponent(hook!.id)}&event=${encodeURIComponent(ev)}`)
      onRun({ at, event: ev, result })
    } catch (e) { onRun({ at, event: ev, error: (e as Error).message }) }
  }
  const last = mine[0]

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()} title="Send a test" width="max-w-xl"
      description={`${hook.name || maskUrl(hook.url)}. Sends the sample payload for the chosen event, whether or not the webhook is enabled or subscribed to it.`}
      footer={<><Button variant="ghost" onClick={onClose}>Close</Button><Button variant="primary" loading={isBusy('fire')} onClick={() => run('fire', fire)}><Icon name="bolt" size={14} />Send test</Button></>}>
      <div className="space-y-4">
        <div className="space-y-1.5">
          <Legend>Event</Legend>
          <Select value={ev} onValueChange={setEvent} options={hook.events.map((e) => ({ value: e, label: e }))} />
        </div>
        <div>
          <div className="mb-1.5 flex items-center gap-2"><Legend>Request</Legend><Badge>{pv.method}</Badge></div>
          <p className="mb-2 break-all font-mono text-xs text-muted">{maskUrl(pv.url)}</p>
          {pv.body && <CodeBlock label="Body">{pv.body}</CodeBlock>}
          {pv.problem && <p className="text-xs text-warn">{pv.problem.message}</p>}
        </div>
        <div aria-live="polite">
          <Legend>Result</Legend>
          {!last ? <p className="mt-1.5 text-xs text-faint">Nothing sent yet in this session.</p> : (
            <ul className="mt-1.5 divide-y divide-line rounded-md border border-line">
              {mine.map((r, i) => (
                <li key={i} className="space-y-1 px-3 py-2 text-xs">
                  <div className="flex items-center gap-2">
                    <Badge tone={r.error || (r.result?.http_status && r.result.http_status >= 400) ? 'danger' : r.result?.http_status ? 'live' : 'info'}>
                      {r.error ? 'Rejected' : r.result?.http_status ? `HTTP ${r.result.http_status}` : 'Queued'}
                    </Badge>
                    <span className="font-mono text-faint">{r.event}</span>
                    {r.result?.latency_ms !== undefined && <span className="num font-mono text-muted">{r.result.latency_ms} ms</span>}
                    <span className="num ml-auto font-mono text-faint">{r.at.toLocaleTimeString()}</span>
                  </div>
                  {r.error && <p className="text-danger">{r.error}</p>}
                  {r.result?.error && <p className="text-danger">{r.result.error}</p>}
                  {r.result?.response_excerpt && <pre className="overflow-x-auto font-mono text-[11px] text-muted">{r.result.response_excerpt}</pre>}
                  {i === 0 && !r.error && r.result?.http_status === undefined && <p className="text-muted">Delivery runs in the background. The receiver's status code and response are written to the server log only.</p>}
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </Dialog>
  )
}
