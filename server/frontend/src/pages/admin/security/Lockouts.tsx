import { adminPost } from '@/lib/api'
import { Button } from '@/ui/controls'
import { Badge, EmptyState, Skeleton, Table, Td, Th } from '@/ui/display'
import { Legend, Panel } from '@/ui/console'
import { Icon } from '@/ui/icons'
import { useAction, useConfirm } from '@/ui/kit'

export interface IpStat { ip: string; count: number; locked: boolean; expires_in: string }
export interface SecurityStats { auth_fails: IpStat[]; scanners: IpStat[] }

function StatTable({ rows, kind, canClear, canBan, banned, onClear, onBan }: {
  rows: IpStat[]; kind: 'auth' | 'scan'; canClear: boolean; canBan: boolean; banned: string[]
  onClear: (s: IpStat) => void; onBan: (s: IpStat) => void
}) {
  const sorted = [...rows].sort((a, b) => Number(b.locked) - Number(a.locked) || b.count - a.count)
  return (
    <Table>
      <thead><tr><Th>Address</Th><Th align="right" className="max-sm:hidden">{kind === 'auth' ? 'Failures' : 'Probes'}</Th><Th className="max-sm:hidden">State</Th><Th align="right" className="relative"><span className="sr-only">Actions</span></Th></tr></thead>
      <tbody>
        {sorted.map((s) => {
          const state = s.locked ? <Badge tone="danger" className="whitespace-nowrap">Locked, {s.expires_in} left</Badge> : <Badge>Counting</Badge>
          return (
          <tr key={s.ip} className="transition-colors hover:bg-hover/60">
            <Td mono className="max-sm:h-auto max-sm:py-2"><div>{s.ip}</div><div className="mt-1 flex flex-col items-start gap-1 sm:hidden">{state}<span className="text-[11px] text-faint">{s.count} {kind === 'auth' ? 'failures' : 'probes'}</span></div></Td>
            <Td align="right" mono className="max-sm:hidden">{s.count}</Td>
            <Td className="max-sm:hidden">{state}</Td>
            <Td align="right">
              <div className="flex justify-end gap-1.5">
                {canBan && <Button size="sm" variant="ghost" disabled={banned.includes(s.ip)} onClick={() => onBan(s)} aria-label={`Ban ${s.ip}`}><Icon name="ban" size={14} /><span className="max-sm:hidden">{banned.includes(s.ip) ? 'Banned' : 'Ban'}</span></Button>}
                {canClear && <Button size="sm" onClick={() => onClear(s)} aria-label={`Clear ${s.locked ? 'lockout' : 'counter'} for ${s.ip}`}>{s.locked ? 'Unlock' : 'Reset'}</Button>}
              </div>
            </Td>
          </tr>
          )
        })}
      </tbody>
    </Table>
  )
}

export function Lockouts({ stats, loading, error, reload, canClear, canBan, banned, onBan }: {
  stats: SecurityStats | null; loading: boolean; error: string | null; reload: () => Promise<void>
  canClear: boolean; canBan: boolean; banned: string[]; onBan: (ip: string) => void
}) {
  const act = useAction()
  const confirm = useConfirm()
  const clear = (kind: 'auth' | 'scan') => (s: IpStat) => confirm.ask({
    title: s.locked ? `Unlock ${s.ip}?` : `Reset the counter for ${s.ip}?`,
    body: s.locked
      ? kind === 'auth' ? 'This address gets a fresh set of five login attempts straight away. Do it only if you know it is legitimate.' : 'The address is allowed to reach the server again straight away.'
      : 'The recorded count for this address goes back to zero.',
    confirm: s.locked ? 'Unlock' : 'Reset',
    run: async () => {
      const r = await act.run('clear', () => adminPost(kind === 'auth' ? '/admin/clear-auth-lockout' : '/admin/clear-scan-lockout', { ip: s.ip }), `Cleared ${s.ip}`)
      if (r !== undefined) await reload()
      return r !== undefined
    },
  })
  const sections = [
    { kind: 'auth' as const, legend: 'Login failures', help: 'Five failed sign-ins lock an address for 15 minutes. Rows below that count are still being watched.', rows: stats?.auth_fails ?? [] },
    { kind: 'scan' as const, legend: 'Scanners', help: 'Addresses requesting paths that only automated probes ask for.', rows: stats?.scanners ?? [] },
  ]
  return (
    <div className="grid gap-3 xl:grid-cols-2 [&>*]:min-w-0">
      {sections.map((sec) => {
        const locked = sec.rows.filter((r) => r.locked).length
        return (
          <Panel key={sec.kind} legend={sec.legend} title={stats ? `${locked} locked of ${sec.rows.length}` : undefined} lamp={locked ? 'danger' : 'off'} flush>
            <div className="border-b border-line px-4 py-3 text-xs text-muted">{sec.help}</div>
            {loading ? <div className="space-y-2 p-4">{[0, 1].map((i) => <Skeleton key={i} className="h-9 w-full" />)}</div>
              : error ? <EmptyState icon={<Icon name="alert" size={20} />} title="Could not load lockouts" body={error} action={<Button onClick={() => void reload()}>Retry</Button>} />
              : sec.rows.length === 0 ? <EmptyState icon={<Icon name="lock" size={20} />} title="Nothing recorded" body="Counters live in memory and reset when the server restarts." />
              : <StatTable rows={sec.rows} kind={sec.kind} canClear={canClear} canBan={canBan} banned={banned} onClear={clear(sec.kind)} onBan={(s) => onBan(s.ip)} />}
            {!canClear && !loading && !error && <p className="border-t border-line px-4 py-2.5"><Legend>Read only</Legend> <span className="text-xs text-muted">Clearing needs the admin or superadmin role.</span></p>}
          </Panel>
        )
      })}
      {confirm.dialog}
    </div>
  )
}
