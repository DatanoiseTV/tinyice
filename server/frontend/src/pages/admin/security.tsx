import { useState } from 'react'
import { api } from '@/lib/api'
import { EmptyState, PageHeader } from '@/ui/display'
import { Legend, Readout } from '@/ui/console'
import { Icon } from '@/ui/icons'
import { Tabs, TabPanel, useAction, useConfirm } from '@/ui/kit'
import { Lockouts, type SecurityStats } from './security/Lockouts'
import { Rules } from './security/Rules'
import { Audit } from './security/Audit'
import { useLoad, useRole } from './users/lib'

function Kpi({ label, value, unit, tone }: { label: string; value: string; unit?: string; tone?: 'warn' | 'danger' }) {
  return (
    <div className="min-w-0 rounded-lg border border-line bg-surface p-4">
      <Legend className="mb-3 block">{label}</Legend>
      <Readout value={value} unit={unit} tone={tone} />
    </div>
  )
}

export default function Security() {
  const role = useRole()
  const isSuper = role === 'superadmin'
  const canClear = isSuper || role === 'admin'
  const [tab, setTab] = useState('lockouts')
  const act = useAction()
  const confirm = useConfirm()

  // Superadmin-only reads are skipped for other roles: the server answers 403, and noise helps nobody.
  const bans = useLoad(() => api.get<string[]>('/api/security/bans'), { enabled: isSuper })
  const allow = useLoad(() => api.get<string[]>('/api/security/whitelist'), { enabled: isSuper })
  const stats = useLoad(() => api.get<SecurityStats>('/admin/security-stats'), { every: 10_000 })
  const audit = useLoad(() => api.get<{ total: number }>('/api/security/audit?limit=1'), { enabled: isSuper })

  const locked = (stats.data?.auth_fails ?? []).filter((s) => s.locked).length + (stats.data?.scanners ?? []).filter((s) => s.locked).length
  const banList = bans.data ?? []

  const banFromLockout = (ip: string) => confirm.ask({
    title: `Ban ${ip}?`,
    body: 'The address is refused on every listener until the ban is removed on the Rules tab. A lockout expires by itself; a ban does not.',
    confirm: 'Ban address',
    run: async () => {
      const r = await act.run('ban', () => api.post('/api/security/bans', { ip }), `Banned ${ip}`)
      if (r !== undefined) await bans.reload()
      return r !== undefined
    },
  })

  return (
    <>
      <PageHeader title="Security" sub="Who is locked out, which addresses are banned or exempt, and a record of what operators changed." />
      <div className="mb-3 grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Kpi label="Locked out now" value={stats.data ? String(locked) : '-'} tone={locked ? 'danger' : undefined} />
        <Kpi label="Banned entries" value={isSuper && bans.data ? String(bans.data.length) : '-'} />
        <Kpi label="Allow-listed entries" value={isSuper && allow.data ? String(allow.data.length) : '-'} />
        <Kpi label="Audit entries" value={isSuper && audit.data ? String(audit.data.total) : '-'} />
      </div>
      <Tabs value={tab} onValueChange={setTab} tabs={[
        { value: 'lockouts', label: 'Lockouts', icon: 'lock', count: locked },
        { value: 'rules', label: 'Bans and allow list', icon: 'ban' },
        { value: 'audit', label: 'Audit log', icon: 'list' },
      ]}>
        <TabPanel value="lockouts" className="outline-none">
          <Lockouts stats={stats.data} loading={stats.loading} error={stats.error} reload={stats.reload} canClear={canClear} canBan={isSuper} banned={banList} onBan={banFromLockout} />
        </TabPanel>
        <TabPanel value="rules" className="outline-none">
          {isSuper ? <Rules bans={{ ...bans, reload: bans.reload }} allow={{ ...allow, reload: allow.reload }} canEdit />
            : <div className="rounded-lg border border-line bg-surface"><EmptyState icon={<Icon name="lock" size={20} />} title="Bans and the allow list are limited to superadmins" body={`You are signed in as ${role || 'an unknown role'}. You can still see and clear lockouts.`} /></div>}
        </TabPanel>
        <TabPanel value="audit" className="outline-none">
          {isSuper ? <Audit enabled />
            : <div className="rounded-lg border border-line bg-surface"><EmptyState icon={<Icon name="lock" size={20} />} title="The audit log is limited to superadmins" body={`You are signed in as ${role || 'an unknown role'}.`} /></div>}
        </TabPanel>
      </Tabs>
      {confirm.dialog}
    </>
  )
}
