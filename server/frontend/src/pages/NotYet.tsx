import { useLocation } from 'wouter'
import { Hammer } from 'lucide-react'
import { ALL_NAV } from '@/app/nav'
import { isActive } from '@/app/Sidebar'
import { EmptyState, PageHeader } from '@/ui/display'

export default function NotYet() {
  const [path] = useLocation()
  const here = ALL_NAV.find((n) => isActive(n.href, path))
  return (
    <>
      <PageHeader title={here?.label ?? 'Not found'} sub={here?.hint} />
      <EmptyState icon={<Hammer className="size-5" />} title="This page is not ported yet" body="The shell, navigation and design system are final. This screen is next in the build order." />
    </>
  )
}
