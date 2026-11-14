import { useState } from 'react'
import { Button, Dialog } from '@/ui/controls'
import { Legend } from '@/ui/console'
import { Icon } from '@/ui/icons'
import { Checkbox, CodeBlock } from '@/ui/kit'
import { toast } from '@/lib/toast'

export interface Secret { name: string; token: string }

/**
 * The one place a raw key is ever rendered. It lives only in this component's
 * props, is never persisted or logged, and the dialog refuses to close until
 * the operator has copied it or ticked that they stored it.
 */
export function SecretDialog({ secret, onDone }: { secret: Secret | null; onDone: () => void }) {
  const [copied, setCopied] = useState(false)
  const [ack, setAck] = useState(false)
  const safe = copied || ack
  const finish = () => { setCopied(false); setAck(false); onDone() }
  const copy = async () => {
    if (!secret) return
    try { await navigator.clipboard.writeText(secret.token); setCopied(true) } catch { toast.error('Clipboard unavailable. Select the key and copy it by hand.') }
  }
  return (
    <Dialog open={!!secret} width="max-w-lg" title="Key created" description={secret ? `Name: ${secret.name}` : undefined}
      onOpenChange={(o) => { if (o) return; if (safe) finish(); else toast.info('Copy the key first, or confirm you have stored it.') }}
      footer={<Button variant="primary" disabled={!safe} onClick={finish}>Done</Button>}>
      {secret && (
        <div className="space-y-4">
          <div role="alert" className="flex gap-2.5 rounded-md border border-warn/40 bg-warn/10 p-3 text-xs text-ink">
            <Icon name="alert" size={16} className="mt-px shrink-0 text-warn" />
            <p><strong className="font-semibold">You will not see this key again.</strong> Only a hash is stored on the server, so a lost key cannot be recovered. Create a new one and revoke this one instead.</p>
          </div>
          <div>
            <Legend className="mb-1.5 block">Secret</Legend>
            <div className="flex items-stretch gap-2">
              <code className="min-w-0 flex-1 select-all break-all rounded-md border border-line-strong bg-bg px-3 py-2 font-mono text-xs text-ink" data-testid="secret">{secret.token}</code>
              <Button onClick={copy}><Icon name={copied ? 'check' : 'copy'} size={14} />{copied ? 'Copied' : 'Copy'}</Button>
            </div>
          </div>
          <CodeBlock label="Use it">{`curl -H "Authorization: Bearer ${secret.token}" \\\n  ${window.location.origin}/api/streams`}</CodeBlock>
          <div className="flex items-center gap-2.5 text-[13px] text-muted">
            <Checkbox checked={ack} onChange={setAck} label="I have stored this key somewhere safe" />
            <span>I have stored this key somewhere safe</span>
          </div>
        </div>
      )}
    </Dialog>
  )
}
