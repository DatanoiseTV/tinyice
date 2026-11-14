import { useEffect, useState } from 'react'
import { Button, Dialog, Field, Input, NativeSelect } from '@/ui/controls'
import { ROLES } from '../users/roles'

export interface PendingRow { id: string; email: string; name: string; provider: string; requested_at: string }

/** Suggest a username from the mailbox part, restricted to the characters the Users page accepts. */
const suggest = (email: string) => email.split('@')[0].toLowerCase().replace(/[^a-z0-9._-]/g, '').slice(0, 64)
const NAME_RE = /^[A-Za-z0-9._@-]{1,64}$/

export function ApproveDialog({ target, onClose, taken, onApprove }: {
  target: PendingRow | null; onClose: () => void; taken: string[]
  onApprove: (p: PendingRow, username: string, role: string) => Promise<boolean>
}) {
  const [username, setUsername] = useState('')
  const [role, setRole] = useState('dj')
  const [ack, setAck] = useState('')
  const [busy, setBusy] = useState(false)
  const [touched, setTouched] = useState(false)
  useEffect(() => { if (target) { setUsername(suggest(target.email)); setRole('dj'); setAck(''); setTouched(false) } }, [target])

  const name = username.trim()
  const nameError = !NAME_RE.test(name) ? 'Use 1-64 letters, digits and . _ @ -' : taken.includes(name) ? 'That username already exists' : null
  const needsAck = role === 'superadmin'
  const ackOk = !needsAck || ack.trim() === name
  const submit = async () => {
    setTouched(true)
    if (!target || nameError || !ackOk) return
    setBusy(true)
    try { if (await onApprove(target, name, role)) onClose() } finally { setBusy(false) }
  }
  return (
    <Dialog open={!!target} onOpenChange={(o) => !o && !busy && onClose()} title="Approve sign-up"
      description={target ? `Creates an account for ${target.email}. The person signs in with ${target.provider}; no password is set.` : undefined}
      footer={<><Button variant="ghost" onClick={onClose} disabled={busy}>Cancel</Button><Button variant="primary" loading={busy} disabled={needsAck && !ackOk} onClick={submit}>Approve</Button></>}>
      <form className="space-y-4" noValidate onSubmit={(e) => { e.preventDefault(); void submit() }}>
        <Field label="Username" error={touched ? nameError : null}><Input mono autoFocus value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" /></Field>
        <div className="space-y-1.5">
          <span id="approve-role" className="text-xs font-medium text-muted">Role</span>
          <NativeSelect aria-labelledby="approve-role" value={role} onChange={(e) => setRole(e.target.value)}>
            {ROLES.slice().reverse().map((r) => <option key={r.value} value={r.value}>{r.label}</option>)}
          </NativeSelect>
          <p className="text-xs text-faint">{ROLES.find((r) => r.value === role)?.help}</p>
        </div>
        {needsAck && (
          <Field label={`Type ${name || 'the username'} to grant superadmin`} hint="Superadmins can manage accounts, bans and the audit log.">
            <Input mono value={ack} onChange={(e) => setAck(e.target.value)} autoComplete="off" spellCheck={false} />
          </Field>
        )}
      </form>
    </Dialog>
  )
}
