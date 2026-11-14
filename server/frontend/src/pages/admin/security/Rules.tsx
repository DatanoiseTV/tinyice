import { api } from '@/lib/api'
import { toast } from '@/lib/toast'
import { Panel } from '@/ui/console'
import { useAction, useConfirm } from '@/ui/kit'
import { useTypedConfirm } from '@/widgets/TypedConfirm'
import { IpList } from './IpList'

interface ListState { data: string[] | null; loading: boolean; error: string | null; reload: () => Promise<void> }

export function Rules({ bans, allow, canEdit }: { bans: ListState; allow: ListState; canEdit: boolean }) {
  const act = useAction()
  const confirm = useConfirm()
  const typed = useTypedConfirm()
  const note = 'Only superadmins can read or change these lists.'

  const write = async (path: string, ip: string, ok: string) => {
    const r = await act.run(path, () => api.post(path, { ip }), ok)
    return r !== undefined
  }
  const remove = (path: string, list: ListState, ip: string, ok: string) => async () => {
    const r = await act.run(path, () => api.del(`${path}?ip=${encodeURIComponent(ip)}`), ok)
    if (r !== undefined) await list.reload()
    return r !== undefined
  }

  return (
    <div className="grid gap-3 xl:grid-cols-2 [&>*]:min-w-0">
      <Panel legend="Banned" title={bans.data ? `${bans.data.length} entr${bans.data.length === 1 ? 'y' : 'ies'}` : undefined} lamp={bans.data?.length ? 'danger' : 'off'} flush>
        <div className="border-b border-line px-4 py-3 text-xs text-muted">Connections from these addresses are refused on every listener. Ranges use CIDR notation. Addresses on the allow list are never banned.</div>
        <IpList id="ban" items={bans.data ?? []} loading={bans.loading} error={bans.error} canEdit={canEdit} lockedNote={note} addLabel="Address or range to ban"
          emptyTitle="Nobody is banned" emptyBody="Ban an address or a range here, or from the lockout tables when a scanner shows up." onRetry={() => void bans.reload()}
          onAdd={async (value, p) => {
            const go = async () => { const ok = await write('/api/security/bans', value, `Banned ${value}`); if (ok) await bans.reload(); return ok }
            if (p.whole) { toast.error('That range would ban every client, including you'); return false }
            if (p.broad) { typed.ask({ title: `Ban the whole range ${value}?`, body: 'This range is larger than a typical network and may include real listeners and your own address.', phrase: value, confirm: 'Ban range', run: go }); return false }
            return go()
          }}
          onRemove={(ip) => confirm.ask({ title: `Remove the ban on ${ip}?`, body: 'The address can connect again straight away.', confirm: 'Remove ban', run: remove('/api/security/bans', bans, ip, `Unbanned ${ip}`) })} />
      </Panel>

      <Panel legend="Allow list" title={allow.data ? `${allow.data.length} entr${allow.data.length === 1 ? 'y' : 'ies'}` : undefined} lamp={allow.data?.length ? 'warn' : 'off'} flush>
        <div className="border-b border-line px-4 py-3 text-xs text-muted">Addresses here are exempt from bans, login lockouts and scan detection. This list does not restrict who can connect: everyone else is still allowed.</div>
        <IpList id="allow" items={allow.data ?? []} loading={allow.loading} error={allow.error} canEdit={canEdit} lockedNote={note} addLabel="Address or range to exempt"
          emptyTitle="Nothing is exempt" emptyBody="Add an office network or a monitoring host that must never be locked out." onRetry={() => void allow.reload()}
          onAdd={async (value, p) => {
            const go = async () => { const ok = await write('/api/security/whitelist', value, `Added ${value} to the allow list`); if (ok) await allow.reload(); return ok }
            if (p.broad || p.whole) { typed.ask({ title: `Exempt the whole range ${value}?`, body: 'Bans, login lockouts and scan detection stop applying to every address in this range.', phrase: value, confirm: 'Add to allow list', run: go }); return false }
            return go()
          }}
          onRemove={(ip) => confirm.ask({ title: `Remove ${ip} from the allow list?`, body: 'Bans and lockouts apply to this address again.', confirm: 'Remove', run: remove('/api/security/whitelist', allow, ip, `Removed ${ip}`) })} />
      </Panel>
      {confirm.dialog}
      {typed.dialog}
    </div>
  )
}
