import { Activity, Radio, Disc3, Mic, Cable, Shuffle, Music2, Users, UserCheck, ShieldCheck, KeyRound, Webhook, Settings, LayoutDashboard, type LucideIcon } from 'lucide-react'

export interface NavItem { id: string; label: string; href: string; icon: LucideIcon; hint: string }
export interface NavGroup { label: string; items: NavItem[] }

/** Single source of truth for sidebar, breadcrumbs and the command palette. */
export const NAV: NavGroup[] = [
  { label: 'Overview', items: [
    { id: 'dashboard', label: 'Dashboard', href: '/admin', icon: LayoutDashboard, hint: 'Live system overview' },
  ] },
  { label: 'Broadcast', items: [
    { id: 'streams', label: 'Streams', href: '/admin/streams', icon: Radio, hint: 'Mounts, sources and listeners' },
    { id: 'autodj', label: 'AutoDJ', href: '/admin/autodj', icon: Disc3, hint: 'Playlists and automated playout' },
    { id: 'golive', label: 'Go Live', href: '/admin/golive', icon: Mic, hint: 'Broadcast from the browser' },
    { id: 'studio', label: 'Studio', href: '/admin/studio', icon: Music2, hint: 'Library and queue' },
  ] },
  { label: 'Network', items: [
    { id: 'relays', label: 'Relays', href: '/admin/relays', icon: Cable, hint: 'Pull streams from other servers' },
    { id: 'transcoders', label: 'Transcoders', href: '/admin/transcoders', icon: Shuffle, hint: 'Re-encode a mount' },
  ] },
  { label: 'Access', items: [
    { id: 'users', label: 'Users', href: '/admin/users', icon: Users, hint: 'Accounts and mount access' },
    { id: 'pending', label: 'Pending', href: '/admin/pending', icon: UserCheck, hint: 'Sign-ups awaiting approval' },
    { id: 'security', label: 'Security', href: '/admin/security', icon: ShieldCheck, hint: 'Bans, allow list and audit log' },
    { id: 'tokens', label: 'API keys', href: '/admin/tokens', icon: KeyRound, hint: 'Tokens for automation' },
    { id: 'webhooks', label: 'Webhooks', href: '/admin/webhooks', icon: Webhook, hint: 'Event notifications' },
  ] },
  { label: 'System', items: [
    { id: 'settings', label: 'Settings', href: '/admin/settings', icon: Settings, hint: 'Server, branding and limits' },
  ] },
]
export const ALL_NAV = NAV.flatMap((g) => g.items)
export { Activity }
