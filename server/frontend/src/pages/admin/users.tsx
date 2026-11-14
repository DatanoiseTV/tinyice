import { useMemo, useState } from 'react'
import { api } from '@/lib/api'
import { Button, Segmented, Tip } from '@/ui/controls'
import { Badge, EmptyState, PageHeader, Skeleton, Table, Td, Th } from '@/ui/display'
import { Panel } from '@/ui/console'
import { Icon } from '@/ui/icons'
import { Menu, SearchInput, useAction } from '@/ui/kit'
import { useTypedConfirm } from '@/widgets/TypedConfirm'
import { PermissionMatrix, RoleBadge, ROLES } from './users/roles'
import { UserDialog, type UserForm, type UserRow } from './users/UserDialog'
import { useLoad, useMe, useRole } from './users/lib'

type RoleFilter = 'all' | 'superadmin' | 'admin' | 'dj'

function MountChips({ mounts }: { mounts: string[] }) {
  if (mounts.length === 0) return <span className="text-xs text-faint">None</span>
  const shown = [...mounts].sort().slice(0, 3)
  return (
    <div className="flex flex-wrap items-center gap-1">
      {shown.map((m) => <Badge key={m} className="font-mono">{m}</Badge>)}
      {mounts.length > 3 && <Tip side="top" content={[...mounts].sort().slice(3).join(', ')}><span tabIndex={0} className="cursor-default font-mono text-[11px] text-faint">+{mounts.length - 3}</span></Tip>}
    </div>
  )
}

export default function Users() {
  const role = useRole()
  const me = useMe()
  const isSuper = role === 'superadmin'
  const { data, error, loading, reload } = useLoad(() => api.get<UserRow[]>('/api/users'), { enabled: isSuper })
  const act = useAction()
  const typed = useTypedConfirm()
  const [dialog, setDialog] = useState<{ open: boolean; editing: UserRow | null }>({ open: false, editing: null })
  const [q, setQ] = useState('')
  const [filter, setFilter] = useState<RoleFilter>('all')

  const users = useMemo(() => [...(data ?? [])].sort((a, b) => a.username.localeCompare(b.username)), [data])
  const counts = useMemo(() => ({ all: users.length, superadmin: users.filter((u) => u.role === 'superadmin').length, admin: users.filter((u) => u.role === 'admin').length, dj: users.filter((u) => u.role === 'dj').length }), [users])
  const rows = users.filter((u) => (filter === 'all' || u.role === filter) && u.username.toLowerCase().includes(q.trim().toLowerCase()))
  const supers = counts.superadmin

  const save = async (f: UserForm): Promise<boolean> => {
    const editing = dialog.editing
    const run = async () => {
      const ok = await act.run('save', () => editing
        ? api.put('/api/users', { username: editing.username, role: f.role, ...(f.password ? { password: f.password } : {}) })
        : api.post('/api/users', { username: f.username, password: f.password, role: f.role }),
      editing ? `Saved ${editing.username}` : `Created ${f.username}`)
      if (ok === undefined) return false
      await reload()
      return true
    }
    // Granting superadmin is the one change that cannot be undone by the user it affects.
    if (f.role === 'superadmin' && editing?.role !== 'superadmin') {
      setDialog({ open: false, editing: null })
      typed.ask({
        title: `Grant superadmin to ${f.username}?`,
        body: 'Superadmins can create and delete accounts, approve sign-ups, change bans and read the audit log.',
        phrase: f.username, confirm: 'Grant superadmin', run,
      })
      return true
    }
    return run()
  }

  const remove = (u: UserRow) => typed.ask({
    title: `Remove ${u.username}?`,
    body: 'The account is deleted and every active session of this user ends immediately. API keys they created stop working only when revoked on the API keys page.',
    phrase: u.username, confirm: 'Remove user',
    run: async () => {
      const r = await act.run('del', () => api.del(`/api/users?username=${encodeURIComponent(u.username)}`), `Removed ${u.username}`)
      if (r === undefined) return false
      await reload()
      return true
    },
  })

  const header = (
    <PageHeader title="Users" sub="Accounts that can sign in, what each role may do, and which mounts each account owns."
      actions={isSuper && <Button variant="primary" onClick={() => setDialog({ open: true, editing: null })}><Icon name="plus" size={14} />Add user</Button>} />
  )

  return (
    <>
      {header}
      <div className="grid gap-3 xl:grid-cols-3 [&>*]:min-w-0">
        <Panel legend="Accounts" title={isSuper && data ? `${users.length} total` : undefined} flush className="xl:col-span-2"
          actions={isSuper && <Button size="sm" variant="ghost" onClick={() => void reload()} aria-label="Refresh users"><Icon name="refresh" size={14} /></Button>}>
          {!isSuper ? (
            <EmptyState icon={<Icon name="lock" size={20} />} title="Account management is limited to superadmins" body={`You are signed in as ${role || 'an unknown role'}. Ask a superadmin to add or change accounts.`} />
          ) : (
            <>
              <div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2.5">
                <SearchInput value={q} onChange={setQ} placeholder="Filter by username" className="w-full sm:w-56" />
                <Segmented<RoleFilter> label="Filter by role" value={filter} onChange={setFilter}
                  options={[{ value: 'all', label: `All ${counts.all}` }, ...ROLES.map((r) => ({ value: r.value as RoleFilter, label: `${r.label} ${counts[r.value]}` }))]} />
              </div>
              {loading ? <div className="space-y-2 p-4">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-9 w-full" />)}</div>
                : error ? <EmptyState icon={<Icon name="alert" size={20} />} title="Could not load users" body={error} action={<Button onClick={() => void reload()}>Retry</Button>} />
                : users.length === 0 ? <EmptyState icon={<Icon name="users" size={20} />} title="No accounts" body="Add the first account to let someone sign in." action={<Button variant="primary" onClick={() => setDialog({ open: true, editing: null })}>Add user</Button>} />
                : rows.length === 0 ? <EmptyState icon={<Icon name="search" size={20} />} title="No account matches" body="Change the filter or the search text." action={<Button onClick={() => { setQ(''); setFilter('all') }}>Clear filters</Button>} />
                : (
                  <Table>
                    <thead><tr><Th>Account</Th><Th>Role</Th><Th className="max-sm:hidden">Mounts</Th><Th align="right" className="relative"><span className="sr-only">Actions</span></Th></tr></thead>
                    <tbody>
                      {rows.map((u) => {
                        const self = u.username === me
                        const lastSuper = u.role === 'superadmin' && supers <= 1
                        return (
                          <tr key={u.username} className="transition-colors hover:bg-hover/60">
                            <Td>
                              <div className="flex items-center gap-2.5">
                                <span className="flex size-7 shrink-0 items-center justify-center rounded-md border border-line bg-raised font-mono text-[11px] uppercase text-muted" aria-hidden>{u.username.slice(0, 2)}</span>
                                <span className="font-mono text-xs text-ink">{u.username}</span>
                                {self && <Badge tone="info">You</Badge>}
                              </div>
                              {(u.mounts?.length ?? 0) > 0 && <div className="mt-1 pl-[38px] font-mono text-[10px] text-faint sm:hidden">{u.mounts.length} mount{u.mounts.length === 1 ? '' : 's'}</div>}
                            </Td>
                            <Td><RoleBadge role={u.role} /></Td>
                            <Td className="max-sm:hidden"><MountChips mounts={u.mounts ?? []} /></Td>
                            <Td align="right">
                              <Menu label={`Actions for ${u.username}`} actions={[
                                { label: 'Edit', icon: 'edit', onSelect: () => setDialog({ open: true, editing: u }) },
                                'sep',
                                { label: self ? 'Cannot remove yourself' : lastSuper ? 'Last superadmin' : 'Remove', icon: 'trash', danger: true, disabled: self || lastSuper, onSelect: () => remove(u) },
                              ]} />
                            </Td>
                          </tr>
                        )
                      })}
                    </tbody>
                  </Table>
                )}
            </>
          )}
        </Panel>

        <div className="space-y-3">
          <Panel legend="Roles">
            <dl className="space-y-3">
              {ROLES.map((r) => (
                <div key={r.value}>
                  <dt className="flex items-center gap-2"><RoleBadge role={r.value} />{isSuper && data && <span className="num font-mono text-[11px] text-faint">{counts[r.value]} account{counts[r.value] === 1 ? '' : 's'}</span>}</dt>
                  <dd className="mt-1 text-xs text-muted">{r.help}</dd>
                </div>
              ))}
            </dl>
          </Panel>
          <Panel legend="Sign-in methods">
            <p className="text-xs text-muted">Accounts created here use a password. Passkeys are registered by each user from their own session, and single sign-in accounts arrive through <span className="text-ink">Pending</span>. The server does not list either per account, so they are not shown here.</p>
          </Panel>
        </div>
      </div>

      <Panel legend="Permissions" title="What each role can do" flush className="mt-3">
        <PermissionMatrix current={role} />
      </Panel>

      <UserDialog open={dialog.open} editing={dialog.editing} me={me} existing={users.map((u) => u.username)} soleSuperadmin={supers <= 1}
        onOpenChange={(o) => setDialog((d) => ({ ...d, open: o }))} onSubmit={save} />
      {typed.dialog}
    </>
  )
}
