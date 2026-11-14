import { Badge } from '@/ui/display'
import type { MountStatus } from './model'

export const lampOf = (s: MountStatus): 'live' | 'warn' | 'danger' | 'off' => (s === 'live' ? 'live' : s === 'stalled' ? 'warn' : 'off')

const LABEL: Record<MountStatus, string> = { live: 'On air', stalled: 'Stalling', offline: 'No source', disabled: 'Disabled' }
const TONE = { live: 'live', stalled: 'warn', offline: 'neutral', disabled: 'neutral' } as const

export function StatusBadge({ status }: { status: MountStatus }) {
  return <Badge tone={TONE[status]} dot>{LABEL[status]}</Badge>
}
export const statusLabel = (s: MountStatus) => LABEL[s]
