import { useEffect, useState } from 'react'
import { Button, Dialog, Field, Input, Switch } from '@/ui/controls'
import { CopyButton } from '@/ui/kit'
import { Icon } from '@/ui/icons'
import { createMount, updateMount } from './actions'
import { ConnectGuide } from './ConnectGuide'
import type { Mount } from './model'

const MOUNT_RE = /^\/[A-Za-z0-9._~\-/]+$/

export function validateMount(raw: string, existing: string[]): string | null {
  const v = raw.trim()
  if (!v) return 'Enter a mount path.'
  const m = v.startsWith('/') ? v : `/${v}`
  if (!MOUNT_RE.test(m)) return 'Use letters, digits and . _ ~ - / only, no spaces.'
  if (m.endsWith('/') || m.includes('//')) return 'The path cannot end with a slash or contain an empty segment.'
  if (m.endsWith('/video')) return '"/video" is reserved for the picture of a parent mount.'
  if (m.length > 100) return 'Keep the path under 100 characters.'
  if (existing.includes(m)) return `${m} already exists.`
  return null
}

function randomPassword(): string {
  const a = new Uint8Array(15)
  crypto.getRandomValues(a)
  return Array.from(a, (b) => 'abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789'[b % 57]).join('')
}

const MAX_BURST = 1048576

/**
 * Create a mount, or edit one. Creation ends on a step that shows the encoder
 * settings together with the password: it is typed or generated here and never
 * readable again, so this is the only moment to copy it.
 */
export function MountDialog({ open, onOpenChange, edit, existing, onDone }: {
  open: boolean; onOpenChange: (o: boolean) => void; edit: Mount | null; existing: string[]; onDone: () => void
}) {
  const [mount, setMount] = useState('')
  const [password, setPassword] = useState('')
  const [burst, setBurst] = useState('65536')
  const [enabled, setEnabled] = useState(true)
  const [visible, setVisible] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [touched, setTouched] = useState(false)
  const [created, setCreated] = useState<{ mount: string; password: string } | null>(null)

  useEffect(() => {
    if (!open) return
    setMount(''); setPassword(''); setBurst('65536'); setError(null); setTouched(false); setCreated(null)
    setEnabled(edit?.enabled ?? true); setVisible(edit?.visible ?? false)
  }, [open, edit])

  const mountErr = edit ? null : validateMount(mount, existing)
  const burstN = Number(burst)
  const burstErr = !edit && (!Number.isInteger(burstN) || burstN < 0 || burstN > MAX_BURST) ? `Whole number from 0 to ${MAX_BURST.toLocaleString()}.` : null
  const pwErr = !edit && !password ? 'A source password is required.' : null

  const submit = async () => {
    setTouched(true); setError(null)
    if (mountErr || burstErr || pwErr) return
    setBusy(true)
    try {
      if (edit) {
        await updateMount(edit.mount, { enabled, visible, ...(password ? { password } : {}) })
        onDone(); onOpenChange(false)
      } else {
        const m = mount.trim().startsWith('/') ? mount.trim() : `/${mount.trim()}`
        await createMount(m, password, burstN)
        onDone()
        setCreated({ mount: m, password })
      }
    } catch (e) { setError((e as Error).message || 'Save failed') }
    finally { setBusy(false) }
  }

  if (created) {
    return (
      <Dialog open={open} onOpenChange={onOpenChange} title={`${created.mount} created`} description="Point an encoder at it. The password is shown now and cannot be read back later." width="max-w-xl"
        footer={<Button variant="primary" onClick={() => onOpenChange(false)}>Done</Button>}>
        <div className="mb-4 flex items-center justify-between gap-3 rounded-md border border-line bg-bg px-3 py-2">
          <div className="min-w-0"><div className="text-xs text-muted">Source password</div><div className="truncate font-mono text-xs text-ink">{created.password}</div></div>
          <CopyButton text={created.password} label="Copy password" />
        </div>
        <ConnectGuide mount={created.mount} password={created.password} />
      </Dialog>
    )
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !busy && onOpenChange(o)} title={edit ? `Edit ${edit.mount}` : 'Add mount'}
      description={edit ? 'The path cannot change. Leave the password empty to keep the current one.' : 'A mount is the address listeners and encoders use, for example /live.'}
      footer={<>
        <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>Cancel</Button>
        <Button variant="primary" loading={busy} onClick={submit}>{edit ? 'Save' : 'Create mount'}</Button>
      </>}>
      <form className="space-y-4" onSubmit={(e) => { e.preventDefault(); void submit() }}>
        {!edit && (
          <Field label="Mount path" error={touched ? mountErr : null} hint="Listeners open this path, for example /live or /shows/morning.">
            <Input mono value={mount} onChange={(e) => setMount(e.target.value)} placeholder="/live" autoFocus autoComplete="off" spellCheck={false} aria-invalid={!!(touched && mountErr)} />
          </Field>
        )}
        <Field label={edit ? 'New source password' : 'Source password'} error={touched ? pwErr : null}
          hint={edit ? 'Encoders must be updated when this changes. Empty keeps the current password.' : 'What encoders send to connect. Stored hashed, so it cannot be shown again.'}>
          <div className="flex gap-2">
            <Input mono type="text" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="off" spellCheck={false} placeholder={edit ? 'unchanged' : ''} />
            <Button type="button" size="md" onClick={() => setPassword(randomPassword())} aria-label="Generate a password" title="Generate a password"><Icon name="refresh" size={14} />Generate</Button>
          </div>
        </Field>
        {!edit && (
          <Field label="Burst size (bytes)" error={touched ? burstErr : null} hint="Audio sent to a listener on connect so playback starts at once. 65536 is about 4 s at 128 kbps. 0 uses the server default.">
            <Input mono inputMode="numeric" value={burst} onChange={(e) => setBurst(e.target.value)} aria-invalid={!!(touched && burstErr)} />
          </Field>
        )}
        {edit && (
          <div className="divide-y divide-line rounded-md border border-line">
            <div className="flex items-center justify-between gap-4 px-3 py-2.5">
              <div><div className="text-[13px] text-ink">Accept sources</div><div className="text-xs text-muted">Off disconnects the current source and refuses new ones.</div></div>
              <Switch label="Accept sources" checked={enabled} onCheckedChange={setEnabled} />
            </div>
            <div className="flex items-center justify-between gap-4 px-3 py-2.5">
              <div><div className="text-[13px] text-ink">Public listing</div><div className="text-xs text-muted">Show this mount on the landing page and the directory.</div></div>
              <Switch label="Public listing" checked={visible} onCheckedChange={setVisible} />
            </div>
          </div>
        )}
        {error && <p role="alert" className="text-xs text-danger">{error}</p>}
        <button type="submit" className="hidden" aria-hidden tabIndex={-1} />
      </form>
    </Dialog>
  )
}
