import { Badge } from '@/ui/display'
import { Icon } from '@/ui/icons'
import { cn } from '@/lib/cn'

export const ROLES = [
  { value: 'superadmin', label: 'Superadmin', help: 'Full control: accounts, sign-ups, bans, the audit log and every setting.' },
  { value: 'admin', label: 'Admin', help: 'Runs the broadcast side. Cannot manage accounts, sign-ups, bans or read the audit log.' },
  { value: 'dj', label: 'DJ', help: 'Stream-only account. Sources audio to the mounts it owns and has no admin rights.' },
] as const

export const roleLabel = (r: string) => ROLES.find((x) => x.value === r)?.label ?? r

/** Superadmin is the privileged tone; unknown role strings (the server accepts any) get a warning tone. */
export function RoleBadge({ role }: { role: string }) {
  const known = ROLES.some((r) => r.value === role)
  return <Badge tone={role === 'superadmin' ? 'accent' : known ? 'neutral' : 'warn'}>{known ? roleLabel(role) : role || 'none'}</Badge>
}

type Level = 'yes' | 'own' | 'no'
/**
 * Derived from the Go handlers, not from intent. Each row names the route
 * whose check it reflects so the table can be audited against the source.
 */
const MATRIX: { cap: string; route: string; superadmin: Level; admin: Level; dj: Level }[] = [
  { cap: 'Manage user accounts', route: '/api/users', superadmin: 'yes', admin: 'no', dj: 'no' },
  { cap: 'Approve or deny sign-ups', route: '/api/pending-users', superadmin: 'yes', admin: 'no', dj: 'no' },
  { cap: 'Edit bans and allow list', route: '/api/security/bans, whitelist', superadmin: 'yes', admin: 'no', dj: 'no' },
  { cap: 'Read the audit log', route: '/api/security/audit', superadmin: 'yes', admin: 'no', dj: 'no' },
  { cap: 'See login and scan lockouts', route: '/admin/security-stats', superadmin: 'yes', admin: 'yes', dj: 'yes' },
  { cap: 'Clear a lockout', route: '/admin/clear-*-lockout', superadmin: 'yes', admin: 'yes', dj: 'no' },
  { cap: 'API keys', route: '/api/tokens', superadmin: 'yes', admin: 'own', dj: 'own' },
]

function Cell({ level }: { level: Level }) {
  if (level === 'yes') return <span className="inline-flex items-center gap-1 text-ink"><Icon name="check" size={14} className="text-accent" /><span className="sr-only">Allowed</span></span>
  if (level === 'own') return <Badge>Own only</Badge>
  return <span className="inline-flex items-center gap-1 text-faint"><Icon name="minus" size={14} /><span className="sr-only">Not allowed</span></span>
}

export function PermissionMatrix({ current }: { current: string }) {
  return (
    <div className="relative overflow-x-auto">
      <table className="w-full min-w-[520px] border-collapse text-[13px]">
        <caption className="sr-only">What each role can do, taken from the server's route checks</caption>
        <thead>
          <tr>
            <th scope="col" className="h-9 border-b border-line px-4 text-left text-xs font-medium text-faint">Capability</th>
            {ROLES.map((r) => (
              <th key={r.value} scope="col" className={cn('h-9 border-b border-line px-4 text-center text-xs font-medium', r.value === current ? 'text-accent' : 'text-faint')}>
                {r.label}{r.value === current && <span className="ml-1 font-mono text-[10px] uppercase tracking-wider">you</span>}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {MATRIX.map((m) => (
            <tr key={m.cap} className="hover:bg-hover/50">
              <th scope="row" className="h-11 border-b border-line px-4 text-left font-normal text-ink">
                {m.cap}<span className="mt-0.5 block font-mono text-[10px] text-faint">{m.route}</span>
              </th>
              {ROLES.map((r) => <td key={r.value} className={cn('h-11 border-b border-line px-4 text-center', r.value === current && 'bg-accent/5')}><Cell level={m[r.value]} /></td>)}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
