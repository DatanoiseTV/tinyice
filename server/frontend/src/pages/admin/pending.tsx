import { useMemo, useState } from 'react'
import { api } from '@/lib/api'
import { Button } from '@/ui/controls'
import { Badge, EmptyState, PageHeader, Skeleton, Table, Td, Th } from '@/ui/display'
import { Panel } from '@/ui/console'
import { Icon } from '@/ui/icons'
import { useAction, useConfirm } from '@/ui/kit'
import { ApproveDialog, type PendingRow } from './pending/ApproveDialog'
import { absTime, ago, useLoad, useRole } from './users/lib'

/** Provider ids come from the server config; show them as written, but make the common ones readable. */
const PROVIDER: Record<string, string> = { google: 'Google', github: 'GitHub', microsoft: 'Microsoft', oidc: 'OIDC' }

export default function Pending() {
  const role = useRole()
  const isSuper = role === 'superadmin'
  // Both endpoints answer 401 to anything but a superadmin, and a 401 sends the browser to /login: do not ask.
  const pending = useLoad(() => api.get<PendingRow[]>('/api/pending-users'), { enabled: isSuper, every: 30_000 })
  const users = useLoad(() => api.get<{ username: string }[]>('/api/users'), { enabled: isSuper })
  const act = useAction()
  const confirm = useConfirm()
  const [target, setTarget] = useState<PendingRow | null>(null)
  const rows = useMemo(() => [...(pending.data ?? [])].sort((a, b) => a.requested_at.localeCompare(b.requested_at)), [pending.data])

  const approve = async (p: PendingRow, username: string, roleName: string) => {
    const r = await act.run('approve', () => api.post('/api/pending-users/approve', { id: p.id, username, role: roleName }), `Approved ${p.email} as ${username}`)
    if (r === undefined) return false
    await Promise.all([pending.reload(), users.reload()])
    return true
  }
  const deny = (p: PendingRow) => confirm.ask({
    title: `Deny ${p.name || p.email}?`,
    body: 'The request leaves this list and the person is not notified. They can ask again after 24 hours.',
    confirm: 'Deny request',
    run: async () => {
      const r = await act.run('deny', () => api.post('/api/pending-users/deny', { id: p.id }), 'Request denied')
      if (r !== undefined) await pending.reload()
      return r !== undefined
    },
  })

  return (
    <>
      <PageHeader title="Pending" sub="People who signed in through single sign-on and are waiting for an account."
        actions={isSuper && <Button onClick={() => void pending.reload()} aria-label="Refresh requests"><Icon name="refresh" size={14} />Refresh</Button>} />
      <Panel legend="Requests" title={isSuper && pending.data ? `${rows.length} waiting` : undefined} lamp={rows.length ? 'warn' : 'off'} flush>
        {!isSuper ? <EmptyState icon={<Icon name="lock" size={20} />} title="Approving sign-ups is limited to superadmins" body={`You are signed in as ${role || 'an unknown role'}.`} />
          : pending.loading ? <div className="space-y-2 p-4">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-11 w-full" />)}</div>
          : pending.error ? <EmptyState icon={<Icon name="alert" size={20} />} title="Could not load requests" body={pending.error} action={<Button onClick={() => void pending.reload()}>Retry</Button>} />
          : rows.length === 0 ? <EmptyState icon={<Icon name="pending" size={20} />} title="No requests waiting" body="When someone signs in with a provider that has no matching account, they appear here for approval." />
          : (
            <Table>
              <thead><tr><Th>Person</Th><Th>Provider</Th><Th>Requested</Th><Th align="right" className="relative"><span className="sr-only">Actions</span></Th></tr></thead>
              <tbody>
                {rows.map((p) => (
                  <tr key={p.id} className="transition-colors hover:bg-hover/60">
                    <Td>
                      <div className="min-w-0"><div className="truncate text-[13px] text-ink">{p.name || p.email.split('@')[0]}</div><div className="truncate font-mono text-[11px] text-muted">{p.email}</div></div>
                    </Td>
                    <Td><Badge>{PROVIDER[p.provider] ?? p.provider}</Badge></Td>
                    <Td><span className="num font-mono text-xs text-muted" title={absTime(p.requested_at)}>{ago(p.requested_at)}</span></Td>
                    <Td align="right">
                      <div className="flex justify-end gap-1.5">
                        <Button size="sm" variant="primary" onClick={() => setTarget(p)} aria-label={`Approve ${p.email}`}>Approve</Button>
                        <Button size="sm" variant="danger" onClick={() => deny(p)} aria-label={`Deny ${p.email}`}>Deny</Button>
                      </div>
                    </Td>
                  </tr>
                ))}
              </tbody>
            </Table>
          )}
      </Panel>
      <ApproveDialog target={target} onClose={() => setTarget(null)} taken={(users.data ?? []).map((u) => u.username)} onApprove={approve} />
      {confirm.dialog}
    </>
  )
}
