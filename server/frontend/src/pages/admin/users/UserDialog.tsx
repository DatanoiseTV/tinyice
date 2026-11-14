import { useEffect, useState } from 'react'
import { Button, Dialog, Field, Input, NativeSelect } from '@/ui/controls'
import { CopyButton } from '@/ui/kit'
import { Badge } from '@/ui/display'
import { ROLES } from './roles'

export interface UserRow { username: string; role: string; mounts: string[] }
export interface UserForm { username: string; password: string; role: string }

const NAME_RE = /^[A-Za-z0-9._@-]{1,64}$/

/** 20 characters from a 56-symbol alphabet without look-alikes, from the browser CSPRNG. */
function generatePassword(): string {
  const alphabet = 'abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789'
  const bytes = new Uint8Array(20)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => alphabet[b % alphabet.length]).join('')
}

export function UserDialog({ open, onOpenChange, editing, me, existing, soleSuperadmin, onSubmit }: {
  open: boolean; onOpenChange: (o: boolean) => void; editing: UserRow | null; me: string; existing: string[]; soleSuperadmin: boolean
  onSubmit: (f: UserForm) => Promise<boolean>
}) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState('admin')
  const [reveal, setReveal] = useState(false)
  const [busy, setBusy] = useState(false)
  const [touched, setTouched] = useState(false)

  useEffect(() => {
    if (!open) return
    setUsername(editing?.username ?? ''); setPassword(''); setRole(editing?.role ?? 'admin'); setReveal(false); setTouched(false)
  }, [open, editing])

  const isSelf = !!editing && editing.username === me
  // Changing your own role or demoting the only superadmin would lock the operator out of this page.
  const roleLocked = isSelf || (!!editing && editing.role === 'superadmin' && soleSuperadmin)
  const name = username.trim()
  const errors = {
    username: !editing && (!NAME_RE.test(name) ? 'Use 1-64 letters, digits and . _ @ -' : existing.includes(name) ? 'That username already exists' : null) || null,
    password: !editing ? (password.length < 8 ? 'Use at least 8 characters' : null) : password && password.length < 8 ? 'Use at least 8 characters, or leave blank to keep the current one' : null,
  }
  const invalid = !!(errors.username || errors.password)

  const submit = async () => {
    setTouched(true)
    if (invalid) return
    setBusy(true)
    try { if (await onSubmit({ username: name || editing!.username, password, role })) onOpenChange(false) } finally { setBusy(false) }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !busy && onOpenChange(o)} title={editing ? `Edit ${editing.username}` : 'Add user'}
      description={editing ? 'Change the role or set a new password.' : 'Creates a password account. The user can add a passkey after the first sign-in.'}
      footer={<><Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>Cancel</Button><Button variant="primary" loading={busy} onClick={submit}>{editing ? 'Save changes' : 'Create user'}</Button></>}>
      <form className="space-y-4" onSubmit={(e) => { e.preventDefault(); void submit() }} noValidate>
        <Field label="Username" error={touched ? errors.username : null}>
          <Input mono value={username} onChange={(e) => setUsername(e.target.value)} disabled={!!editing} autoComplete="off" autoFocus={!editing} />
        </Field>
        <Field label={editing ? 'New password' : 'Password'} hint={editing ? 'Leave blank to keep the current password.' : undefined} error={touched ? errors.password : null}>
          <div className="flex items-center gap-1.5">
            <Input type={reveal ? 'text' : 'password'} mono value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" />
            {password && reveal && <CopyButton text={password} label="Copy password" />}
            <Button type="button" size="sm" onClick={() => { setPassword(generatePassword()); setReveal(true) }}>Generate</Button>
          </div>
        </Field>
        <div className="space-y-1.5">
          <span className="text-xs font-medium text-muted" id="role-label">Role</span>
          <NativeSelect aria-labelledby="role-label" value={role} onChange={(e) => setRole(e.target.value)} disabled={roleLocked}>
            {ROLES.map((r) => <option key={r.value} value={r.value}>{r.label}</option>)}
            {!ROLES.some((r) => r.value === role) && <option value={role}>{role}</option>}
          </NativeSelect>
          <p className="text-xs text-faint">
            {ROLES.find((r) => r.value === role)?.help}
            {roleLocked && <> <Badge tone="warn" className="ml-1">Locked</Badge> {isSelf ? 'You cannot change your own role.' : 'This is the only superadmin.'}</>}
          </p>
        </div>
      </form>
    </Dialog>
  )
}
