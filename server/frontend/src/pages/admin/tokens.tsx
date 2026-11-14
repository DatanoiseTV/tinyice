import { useMemo, useState } from 'react'
import { api } from '@/lib/api'
import { Button, Segmented } from '@/ui/controls'
import { Badge, EmptyState, PageHeader, Skeleton, Table, Td, Th } from '@/ui/display'
import { Legend, Panel, Readout } from '@/ui/console'
import { Icon } from '@/ui/icons'
import { SearchInput, useAction } from '@/ui/kit'
import { useTypedConfirm } from '@/widgets/TypedConfirm'
import { CreateDialog } from './tokens/CreateDialog'
import { SecretDialog, type Secret } from './tokens/SecretDialog'
import { absTime, ago, isNever, useLoad, useMe, useRole } from './users/lib'

interface Token { id: string; name: string; username: string; created_at: string; last_used_at: string; last_used_ip: string; expires_at: string; prefix: string }
type Scope = 'mine' | 'all'
const SOON = 14 * 86400e3

function expiry(t: Token, now: number): { state: 'never' | 'ok' | 'soon' | 'expired'; text: string } {
  if (isNever(t.expires_at)) return { state: 'never', text: 'No expiry' }
  const ms = new Date(t.expires_at).getTime() - now
  if (ms <= 0) return { state: 'expired', text: `Expired ${ago(t.expires_at)}` }
  return { state: ms < SOON ? 'soon' : 'ok', text: ago(t.expires_at) }
}

export default function Tokens() {
  const role = useRole()
  const me = useMe()
  const isSuper = role === 'superadmin'
  const { data, error, loading, reload } = useLoad(() => api.get<Token[]>('/api/tokens'), { every: 60_000 })
  const act = useAction()
  const typed = useTypedConfirm()
  const [creating, setCreating] = useState(false)
  const [secret, setSecret] = useState<Secret | null>(null)
  const [scope, setScope] = useState<Scope>('all')
  const [q, setQ] = useState('')

  const now = Date.now()
  const all = data ?? []
  // Non-superadmins only ever receive their own keys; superadmins receive everyone's.
  const visible = useMemo(() => all.filter((t) => (!isSuper || scope === 'all' || t.username === me) && t.name.toLowerCase().includes(q.trim().toLowerCase())), [all, isSuper, scope, me, q])
  const stat = useMemo(() => {
    const ex = all.map((t) => expiry(t, now).state)
    return { active: ex.filter((s) => s !== 'expired').length, soon: ex.filter((s) => s === 'soon').length, expired: ex.filter((s) => s === 'expired').length, unused: all.filter((t) => isNever(t.last_used_at) && expiry(t, now).state !== 'expired').length }
  }, [all, now])

  const create = async (name: string, expiresAt?: string) => {
    const r = await act.run('create', () => api.post<{ id: string; token: string; name: string }>('/api/tokens', { name, ...(expiresAt ? { expires_at: expiresAt } : {}) }))
    if (!r) return false
    setSecret({ name: r.name, token: r.token })
    await reload()
    return true
  }
  const revoke = (t: Token) => typed.ask({
    title: `Revoke ${t.name}?`,
    body: `Anything using this key stops working at once${t.username !== me ? `, and it belongs to ${t.username}` : ''}. This cannot be undone.`,
    phrase: t.name, confirm: 'Revoke key',
    run: async () => {
      const r = await act.run('revoke', () => api.del(`/api/tokens?id=${encodeURIComponent(t.id)}`), `Revoked ${t.name}`)
      if (r !== undefined) await reload()
      return r !== undefined
    },
  })

  return (
    <>
      <PageHeader title="API keys" sub="Bearer tokens for scripts and integrations. A key acts as the account that created it."
        actions={<Button variant="primary" onClick={() => setCreating(true)}><Icon name="plus" size={14} />Create key</Button>} />

      <div className="mb-3 grid grid-cols-2 gap-3 lg:grid-cols-4">
        {([['Active', stat.active, undefined], ['Expiring in 14 days', stat.soon, stat.soon ? 'warn' : undefined], ['Expired', stat.expired, undefined], ['Never used', stat.unused, undefined]] as const).map(([label, n, tone]) => (
          <div key={label} className="min-w-0 rounded-lg border border-line bg-surface p-4"><Legend className="mb-3 block">{label}</Legend><Readout value={loading ? '-' : String(n)} tone={tone} /></div>
        ))}
      </div>

      <Panel legend="Keys" title={data ? `${visible.length} shown` : undefined} flush>
        <div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2.5">
          <SearchInput value={q} onChange={setQ} placeholder="Filter by name" className="w-full sm:w-56" />
          {isSuper && <Segmented<Scope> label="Whose keys" value={scope} onChange={setScope} options={[{ value: 'mine', label: 'Mine' }, { value: 'all', label: 'Everyone' }]} />}
          {!isSuper && <span className="text-xs text-faint">Showing only keys created by {me || 'you'}.</span>}
        </div>
        {loading ? <div className="space-y-2 p-4">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-10 w-full" />)}</div>
          : error ? <EmptyState icon={<Icon name="alert" size={20} />} title="Could not load keys" body={error} action={<Button onClick={() => void reload()}>Retry</Button>} />
          : all.length === 0 ? <EmptyState icon={<Icon name="key" size={20} />} title="No API keys yet" body="Create a key to let a script or another service call the API without a browser session." action={<Button variant="primary" onClick={() => setCreating(true)}>Create key</Button>} />
          : visible.length === 0 ? <EmptyState icon={<Icon name="search" size={20} />} title="No key matches" body={isSuper && scope === 'mine' ? 'Try the Everyone view or change the filter.' : 'Change the filter.'} action={<Button onClick={() => { setQ(''); setScope('all') }}>Clear filters</Button>} />
          : (
            <Table>
              <thead><tr><Th>Name</Th><Th className="max-sm:hidden">Key</Th><Th className="max-sm:hidden">Created</Th><Th className="max-sm:hidden">Last used</Th><Th>Expires</Th><Th align="right" className="relative"><span className="sr-only">Actions</span></Th></tr></thead>
              <tbody>
                {visible.map((t) => {
                  const ex = expiry(t, now)
                  return (
                    <tr key={t.id} className="transition-colors hover:bg-hover/60">
                      <Td>
                        <div className="flex items-center gap-2"><span className="text-[13px] text-ink">{t.name}</span>{t.username !== me && <Badge>{t.username}</Badge>}</div>
                        <div className="mt-1 font-mono text-[10px] text-faint sm:hidden">{t.prefix} · used {ago(t.last_used_at)}</div>
                      </Td>
                      <Td mono className="text-muted max-sm:hidden">{t.prefix}</Td>
                      <Td className="max-sm:hidden"><span className="num font-mono text-xs text-muted" title={absTime(t.created_at)}>{ago(t.created_at)}</span></Td>
                      <Td className="max-sm:hidden">
                        {isNever(t.last_used_at) ? <span className="text-xs text-faint">Never used</span>
                          : <span className="num font-mono text-xs text-muted" title={absTime(t.last_used_at)}>{ago(t.last_used_at)}{t.last_used_ip && <span className="ml-1.5 text-faint">{t.last_used_ip}</span>}</span>}
                      </Td>
                      <Td>
                        {ex.state === 'expired' ? <Badge tone="danger" className="whitespace-nowrap">{ex.text}</Badge>
                          : ex.state === 'soon' ? <Badge tone="warn" className="whitespace-nowrap" >Expires {ex.text}</Badge>
                          : ex.state === 'never' ? <span className="text-xs text-faint">{ex.text}</span>
                          : <span className="num font-mono text-xs text-muted" title={absTime(t.expires_at)}>{ex.text}</span>}
                      </Td>
                      <Td align="right"><Button size="sm" variant="danger" onClick={() => revoke(t)} aria-label={`Revoke ${t.name}`}><Icon name="trash" size={14} /><span className="max-sm:hidden">Revoke</span></Button></Td>
                    </tr>
                  )
                })}
              </tbody>
            </Table>
          )}
      </Panel>

      <CreateDialog open={creating} onOpenChange={setCreating} role={role} onCreate={create} />
      <SecretDialog secret={secret} onDone={() => setSecret(null)} />
      {typed.dialog}
    </>
  )
}
