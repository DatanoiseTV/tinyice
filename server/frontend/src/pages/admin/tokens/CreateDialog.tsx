import { useEffect, useState } from 'react'
import { Button, Dialog, Field, Input, NativeSelect } from '@/ui/controls'
import { RoleBadge } from '../users/roles'

const PRESETS = [['30', '30 days'], ['90', '90 days'], ['365', '1 year'], ['custom', 'Pick a date'], ['never', 'Never expires']] as const

/** Expiry is sent as an RFC 3339 instant; a picked date means the end of that day, local time. */
function expiryOf(choice: string, date: string): string | undefined {
  if (choice === 'never') return undefined
  if (choice === 'custom') return new Date(`${date}T23:59:59`).toISOString()
  return new Date(Date.now() + Number(choice) * 86400e3).toISOString()
}

export function CreateDialog({ open, onOpenChange, role, onCreate }: {
  open: boolean; onOpenChange: (o: boolean) => void; role: string
  onCreate: (name: string, expiresAt?: string) => Promise<boolean>
}) {
  const [name, setName] = useState('')
  const [choice, setChoice] = useState('90')
  const [date, setDate] = useState('')
  const [busy, setBusy] = useState(false)
  const [touched, setTouched] = useState(false)
  useEffect(() => { if (open) { setName(''); setChoice('90'); setDate(''); setTouched(false) } }, [open])

  const today = new Date().toLocaleDateString('en-CA')
  const nameError = !name.trim() ? 'Give the key a name so you can recognise it later' : name.trim().length > 80 ? 'Use at most 80 characters' : null
  const dateError = choice === 'custom' && (!date ? 'Pick an expiry date' : date <= today ? 'The date must be in the future' : null)
  const submit = async () => {
    setTouched(true)
    if (nameError || dateError) return
    setBusy(true)
    try { if (await onCreate(name.trim(), expiryOf(choice, date))) onOpenChange(false) } finally { setBusy(false) }
  }
  return (
    <Dialog open={open} onOpenChange={(o) => !busy && onOpenChange(o)} title="Create API key"
      description="The key is shown once, right after you create it."
      footer={<><Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>Cancel</Button><Button variant="primary" loading={busy} onClick={submit}>Create key</Button></>}>
      <form className="space-y-4" noValidate onSubmit={(e) => { e.preventDefault(); void submit() }}>
        <Field label="Name" error={touched ? nameError : null}><Input autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="Now-playing bridge" maxLength={80} autoComplete="off" /></Field>
        <Field label="Expires" error={touched ? dateError || null : null}
          hint={choice === 'never' ? 'A key that never expires stays valid until you revoke it. Prefer an expiry for anything that leaves this server.' : undefined}>
          <NativeSelect value={choice} onChange={(e) => setChoice(e.target.value)}>{PRESETS.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</NativeSelect>
        </Field>
        {choice === 'custom' && <Field label="Expiry date"><Input type="date" min={today} value={date} onChange={(e) => setDate(e.target.value)} /></Field>}
        <div className="rounded-md border border-line bg-bg p-3 text-xs text-muted">
          <span className="mr-1.5 inline-flex align-middle"><RoleBadge role={role} /></span>
          A key acts with the role of the account that creates it. The server has no per-key scopes, so create it from an account that has only the rights the integration needs. Keys cannot create other keys.
        </div>
      </form>
    </Dialog>
  )
}
