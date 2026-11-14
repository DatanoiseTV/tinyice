import type { IconName } from '@/ui/icons'

export interface NavItem { id: string; label: string; href: string; icon: IconName; hint: string }
export interface NavGroup { label: string; items: NavItem[] }

/** Single source of truth for sidebar, breadcrumbs and the command palette. */
export const NAV: NavGroup[] = [
  { label: 'Overview', items: [
    { id: 'dashboard', label: 'Dashboard', href: '/admin', icon: 'dashboard', hint: 'Live system overview' },
  ] },
  { label: 'Broadcast', items: [
    { id: 'streams', label: 'Streams', href: '/admin/streams', icon: 'streams', hint: 'Mounts, sources and listeners' },
    { id: 'autodj', label: 'AutoDJ', href: '/admin/autodj', icon: 'autodj', hint: 'Playlists and automated playout' },
    { id: 'golive', label: 'Go Live', href: '/admin/golive', icon: 'golive', hint: 'Broadcast from the browser' },
    { id: 'studio', label: 'Studio', href: '/admin/studio', icon: 'studio', hint: 'Library and queue' },
  ] },
  { label: 'Network', items: [
    { id: 'relays', label: 'Relays', href: '/admin/relays', icon: 'relays', hint: 'Pull streams from other servers' },
    { id: 'transcoders', label: 'Transcoders', href: '/admin/transcoders', icon: 'transcoders', hint: 'Re-encode a mount' },
  ] },
  { label: 'Access', items: [
    { id: 'users', label: 'Users', href: '/admin/users', icon: 'users', hint: 'Accounts and mount access' },
    { id: 'pending', label: 'Pending', href: '/admin/pending', icon: 'pending', hint: 'Sign-ups awaiting approval' },
    { id: 'security', label: 'Security', href: '/admin/security', icon: 'security', hint: 'Bans, allow list and audit log' },
    { id: 'tokens', label: 'API keys', href: '/admin/tokens', icon: 'key', hint: 'Tokens for automation' },
    { id: 'webhooks', label: 'Webhooks', href: '/admin/webhooks', icon: 'webhook', hint: 'Event notifications' },
  ] },
  { label: 'System', items: [
    { id: 'settings', label: 'Settings', href: '/admin/settings', icon: 'settings', hint: 'Server, branding and limits' },
  ] },
]
export const ALL_NAV = NAV.flatMap((g) => g.items)
