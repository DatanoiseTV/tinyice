import { useEffect, useState } from 'react'
import { Badge } from '@/ui/display'
import { Button, Field, Input, Segmented } from '@/ui/controls'
import { Icon } from '@/ui/icons'
import { Legend, Panel } from '@/ui/console'
import { cn } from '@/lib/cn'
import { broadcaster, type Snapshot } from './engine'
import type { Prefs } from './store'

export interface MountInfo { mount: string; enabled: boolean }

export function mountError(m: string): string | null {
  if (!m.trim()) return 'Enter a mount path.'
  if (!m.startsWith('/')) return 'A mount starts with a slash, for example /live.'
  if (m.length < 2) return 'Add a name after the slash.'
  if (/[\s?#]/.test(m)) return 'A mount cannot contain spaces, ? or #.'
  if (m.length > 128) return 'Keep the mount under 128 characters.'
  return null
}

export function DestinationPanel({ prefs, set, password, setPassword, mounts, liveElsewhere, csrf, showErrors, locked }: {
  prefs: Prefs; set: (p: Partial<Prefs>) => void; password: string; setPassword: (v: string) => void
  mounts: MountInfo[]; liveElsewhere: boolean; csrf: string; showErrors: boolean; locked: boolean
}) {
  const err = mountError(prefs.mount)
  const known = mounts.find((m) => m.mount === prefs.mount)
  const passErr = prefs.authMode === 'password' && !password ? 'Enter the mount source password.' : null
  const sessionErr = prefs.authMode === 'session' && !csrf ? 'This page has no session token. Reload the admin page or use the source password.' : null
  return (
    <Panel legend="Destination" title={prefs.mount || undefined}>
      <div className="space-y-4">
        <Field label="Mount" error={showErrors ? err : null} hint={!err && !known && prefs.mount ? 'Not on the server yet. It is created when you go live.' : undefined}>
          <Input
            mono value={prefs.mount} list="golive-mounts" placeholder="/live" spellCheck={false} autoCapitalize="off" autoComplete="off"
            disabled={locked} onChange={(e) => set({ mount: e.target.value.trim() })} aria-invalid={showErrors && !!err}
          />
        </Field>
        <datalist id="golive-mounts">{mounts.map((m) => <option key={m.mount} value={m.mount} />)}</datalist>
        {known && !known.enabled && <p role="alert" className="flex items-start gap-2 text-xs text-danger"><Icon name="ban" size={14} className="mt-0.5 shrink-0" />This mount is disabled. The server refuses sources on it until it is enabled in Streams.</p>}
        {liveElsewhere && <p role="alert" className="flex items-start gap-2 text-xs text-warn"><Icon name="alert" size={14} className="mt-0.5 shrink-0" />This mount is live from another source. Going live may be refused or replace it.</p>}

        <div className="space-y-2">
          <Legend>Credentials</Legend>
          <div>
            <Segmented
              label="Authentication" value={prefs.authMode} onChange={(v) => !locked && set({ authMode: v })}
              options={[{ value: 'session', label: 'This admin session' }, { value: 'password', label: 'Source password' }]}
            />
          </div>
          {prefs.authMode === 'session' ? (
            <p className={cn('text-xs', showErrors && sessionErr ? 'text-danger' : 'text-faint')}>{sessionErr ?? 'Uses your signed-in account. It must have access to the mount.'}</p>
          ) : (
            <Field label="Source password" error={showErrors ? passErr : null} hint="Sent once as HTTP Basic auth over this connection. Not stored.">
              <Input type="password" mono value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" disabled={locked} aria-invalid={showErrors && !!passErr} />
            </Field>
          )}
        </div>
        <p className="text-xs text-faint">Publishes Opus 48 kHz stereo over WebRTC, plus H.264 when a video source is open. Opus mounts play through WebRTC in the player; HLS is not served for them.</p>
      </div>
    </Panel>
  )
}

export type CheckState = 'pass' | 'warn' | 'fail' | 'wait'
export interface Check { label: string; state: CheckState; detail?: string }

export function Preflight({ snap, mountOk, authOk, mountFree, simulated }: { snap: Snapshot; mountOk: boolean; authOk: boolean; mountFree: boolean; simulated: boolean }) {
  const [, tick] = useState(0)
  useEffect(() => { const t = setInterval(() => tick((n) => n + 1), 500); return () => clearInterval(t) }, [])
  const open = snap.phase !== 'idle' && snap.phase !== 'arming'
  const signal = open && Date.now() - broadcaster.lastSignal < 3000
  const checks: Check[] = [
    { label: 'Input open', state: open ? 'pass' : 'wait', detail: open ? undefined : 'Open the input to meter it' },
    { label: 'Signal present', state: !open ? 'wait' : signal ? 'pass' : 'warn', detail: open && !signal ? 'Nothing above -50 dBFS in 3 s' : undefined },
    { label: 'Not clipping', state: !open ? 'wait' : broadcaster.clipped ? 'fail' : 'pass', detail: broadcaster.clipped ? 'Lower the input gain' : undefined },
    { label: 'Mount valid', state: mountOk ? 'pass' : 'fail' },
    { label: 'Credentials set', state: authOk ? 'pass' : 'fail' },
    { label: 'Mount free', state: mountFree ? 'pass' : 'warn', detail: mountFree ? undefined : 'Live from another source' },
    { label: 'Monitor off', state: snap.monitor ? 'warn' : 'pass', detail: snap.monitor ? 'Feedback risk without headphones' : undefined },
  ]
  const word = { pass: 'ok', wait: 'pending', warn: 'attention', fail: 'failed' }
  return (
    <ul className="space-y-1.5" aria-label="Pre-flight checks">
      {checks.map((c) => (
        <li key={c.label} className="flex items-center gap-2.5 text-[13px]">
          <Icon name={c.state === 'pass' ? 'check' : c.state === 'wait' ? 'minus' : 'alert'} size={14} className={c.state === 'pass' ? 'text-muted' : c.state === 'wait' ? 'text-faint' : c.state === 'warn' ? 'text-warn' : 'text-danger'} />
          <span className={c.state === 'wait' ? 'text-faint' : 'text-ink'}>{c.label}</span>
          {c.detail && <span className={cn('truncate text-xs', c.state === 'fail' ? 'text-danger' : c.state === 'warn' ? 'text-warn' : 'text-faint')}>{c.detail}</span>}
          <span className="sr-only">{word[c.state]}</span>
        </li>
      ))}
      {simulated && <li><Badge tone="warn">Simulated: nothing is sent</Badge></li>}
    </ul>
  )
}

export function GoLiveButton({ snap, onGo, onStop, disabled }: { snap: Snapshot; onGo: () => void; onStop: () => void; disabled?: boolean }) {
  const on = snap.phase === 'live' || snap.phase === 'reconnecting'
  const connecting = snap.phase === 'connecting'
  // One fixed-size button for every state: only label and variant change, so it never reflows or flickers.
  return (
    <Button
      variant={on ? 'danger' : connecting ? 'secondary' : 'primary'} className="h-11 w-full justify-center text-sm font-semibold"
      loading={snap.phase === 'arming'} disabled={disabled && !on && !connecting}
      onClick={on || connecting ? onStop : onGo}
    >
      <Icon name={on || connecting ? 'stop' : 'golive'} size={16} />
      {on ? 'Stop broadcast' : connecting ? 'Cancel' : 'Go live'}
    </Button>
  )
}
