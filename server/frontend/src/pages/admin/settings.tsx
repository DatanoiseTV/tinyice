import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, page } from '@/lib/api'
import { toast } from '@/lib/toast'
import { cn } from '@/lib/cn'
import { Button, Select } from '@/ui/controls'
import { Badge, EmptyState, PageHeader, Skeleton } from '@/ui/display'
import { Panel } from '@/ui/console'
import { Icon, type IconName } from '@/ui/icons'
import { SearchInput } from '@/ui/kit'
import Row, { matches, type SectionId } from './settings/Row'
import Preview from './settings/Preview'
import { buildRows } from './settings/rows'
import { applyAccent, parseHex } from './settings/contrast'
import {
  BRANDING_SECTION, SERVER_SECTION, toServerDraft, validateBranding, validateServer,
  type Branding, type ServerDraft, type ServerSettings,
} from './settings/types'

interface Section { id: SectionId; label: string; icon: IconName; blurb: string; intro?: string }
const SECTIONS: Section[] = [
  { id: 'station', label: 'Station', icon: 'streams', blurb: 'Name, tagline and contact details' },
  { id: 'look', label: 'Look and feel', icon: 'eye', blurb: 'Logo, colour and landing page' },
  { id: 'listeners', label: 'Listeners', icon: 'users', blurb: 'Limits, delay and discoverability' },
  { id: 'security', label: 'Security and logging', icon: 'security', blurb: 'Who did what' },
  { id: 'network', label: 'Address and network', icon: 'globe', blurb: 'Host name, public address, HTTPS',
    intro: 'The first two settings can be changed here. The rest are read-only: they are set in tinyice.json, with command-line flags (--port, --https-port, --use-https, --auto-https, --domains) or with "tinyice set <option> <value>", and apply after a restart.' },
  { id: 'signin', label: 'Sign-in', icon: 'key', blurb: 'How people log in (read-only)',
    intro: 'Single sign-on providers and outgoing e-mail are set in tinyice.json (oidc_providers, smtp) and apply after a restart. There is no settings endpoint for them, so this page only reports what the login page offers.' },
]
const keys = <T extends object>(o: T) => Object.keys(o) as (keyof T)[]

export default function SettingsPage() {
  const isSuper = page().user?.role === 'superadmin'
  const [section, setSection] = useState<SectionId>('station')
  const [q, setQ] = useState('')
  const [server, setServer] = useState<ServerSettings | null>(null)
  const [srvBase, setSrvBase] = useState<ServerDraft | null>(null)
  const [srv, setSrv] = useState<ServerDraft | null>(null)
  const [brBase, setBrBase] = useState<Branding | null>(null)
  const [br, setBr] = useState<Branding | null>(null)
  const [error, setError] = useState('')
  const [showErrors, setShowErrors] = useState(false)
  const [saving, setSaving] = useState(false)
  const [justSaved, setJustSaved] = useState<Partial<Record<SectionId, boolean>>>({})
  const [logoVer, setLogoVer] = useState(() => Date.now())

  const fetchAll = useCallback(() => Promise.all([api.get<ServerSettings>('/api/settings'), api.get<Branding>('/api/branding')]), [])
  const load = useCallback(async () => {
    setError('')
    try {
      const [s, b] = await fetchAll()
      setServer(s); setSrvBase(toServerDraft(s)); setSrv(toServerDraft(s)); setBrBase(b); setBr(b)
    } catch (e) { setError((e as Error).message || 'Could not load settings') }
  }, [fetchAll])
  useEffect(() => { if (isSuper) load() }, [isSuper, load])

  // Live theme preview: the whole interface adopts the draft accent while editing, and goes back to the saved one on leave.
  const savedAccent = useRef('')
  savedAccent.current = brBase?.accent_color ?? ''
  useEffect(() => { if (br && parseHex(br.accent_color)) applyAccent(br.accent_color) }, [br?.accent_color]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => () => { if (parseHex(savedAccent.current)) applyAccent(savedAccent.current) }, [])

  const srvDirty = useMemo(() => (srv && srvBase ? keys(srv).filter((k) => srv[k] !== srvBase[k]) : []), [srv, srvBase])
  const brDirty = useMemo(() => (br && brBase ? keys(br).filter((k) => br[k] !== brBase[k]) : []), [br, brBase])
  const dirtyCount = srvDirty.length + brDirty.length
  const perSection = useMemo(() => {
    const c: Partial<Record<SectionId, number>> = {}
    for (const k of srvDirty) c[SERVER_SECTION[k]] = (c[SERVER_SECTION[k]] ?? 0) + 1
    for (const k of brDirty) c[BRANDING_SECTION[k]] = (c[BRANDING_SECTION[k]] ?? 0) + 1
    return c
  }, [srvDirty, brDirty])

  useEffect(() => {
    if (!dirtyCount) return
    const h = (e: BeforeUnloadEvent) => { e.preventDefault() }
    window.addEventListener('beforeunload', h)
    return () => window.removeEventListener('beforeunload', h)
  }, [dirtyCount])

  if (!isSuper) {
    return (<><PageHeader title="Settings" /><Panel legend="Access"><EmptyState icon={<Icon name="lock" size={18} />} title="Superadmin only" body="Server settings and branding affect every user, so only superadmin accounts can view or change them." /></Panel></>)
  }
  if (error) {
    return (<><PageHeader title="Settings" /><Panel legend="Load error"><EmptyState icon={<Icon name="alert" size={18} />} title="Settings did not load" body={error} action={<Button onClick={load}><Icon name="refresh" size={14} />Retry</Button>} /></Panel></>)
  }
  if (!server || !srv || !srvBase || !br || !brBase) {
    return (<><PageHeader title="Settings" sub="Station details, look, listeners and network." /><div className="grid gap-5 lg:grid-cols-[13rem_1fr]"><Skeleton className="h-64" /><Skeleton className="h-96" /></div></>)
  }

  const setS = <K extends keyof ServerDraft>(k: K, v: ServerDraft[K]) => setSrv((p) => (p ? { ...p, [k]: v } : p))
  const setB = <K extends keyof Branding>(k: K, v: Branding[K]) => setBr((p) => (p ? { ...p, [k]: v } : p))

  function discard() {
    setSrv(srvBase); setBr(brBase); setShowErrors(false)
    if (brBase && parseHex(brBase.accent_color)) applyAccent(brBase.accent_color)
  }

  async function save() {
    if (!srv || !srvBase || !br || !brBase) return
    const se = validateServer(srv), be = validateBranding(br)
    const bad = [...keys(se).map((k) => SERVER_SECTION[k]), ...keys(be).map((k) => BRANDING_SECTION[k])]
    if (bad.length) { setShowErrors(true); setQ(''); setSection(bad[0]); toast.error('Fix the highlighted fields first'); return }
    setSaving(true)
    const sBody: Record<string, string | number | boolean> = {}
    for (const k of srvDirty) sBody[k] = k === 'max_listeners' ? Number(srv[k]) : (srv[k] as string | boolean)
    const bBody: Record<string, string> = {}
    for (const k of brDirty) bBody[k] = br[k]
    const touched = new Set<SectionId>([...srvDirty.map((k) => SERVER_SECTION[k]), ...brDirty.map((k) => BRANDING_SECTION[k])])
    const sJob = srvDirty.length ? api.put('/api/settings', sBody) : null
    const bJob = brDirty.length ? api.put('/api/branding', bBody) : null
    const [sr, brr] = await Promise.allSettled([sJob, bJob])
    const sOk = !!sJob && sr.status === 'fulfilled', bOk = !!bJob && brr.status === 'fulfilled'
    const failures: string[] = []
    if (sJob && sr.status === 'rejected') failures.push(`Server: ${(sr.reason as Error).message}`)
    if (bJob && brr.status === 'rejected') failures.push(`Branding: ${(brr.reason as Error).message}`)
    // Re-read so the page shows what the server stored, not what was typed.
    try {
      const [s, b] = await fetchAll()
      if (sOk) { setServer(s); setSrvBase(toServerDraft(s)); setSrv(toServerDraft(s)) }
      if (bOk) { setBrBase(b); setBr(b) }
    } catch {
      if (sOk) setSrvBase(srv)
      if (bOk) setBrBase(br)
      toast.error('Saved, but the stored values could not be re-read')
    }
    setSaving(false)
    failures.forEach((f) => toast.error(f))
    if (!failures.length) {
      toast.success('Settings saved'); setShowErrors(false)
      setJustSaved(Object.fromEntries([...touched].map((t) => [t, true])))
      setTimeout(() => setJustSaved({}), 5000)
    }
  }

  const rows = buildRows({ srv, srvBase, setS, br, brBase, setB, server, showErrors, logoVer, onLogoUploaded: (path) => { setBrBase((p) => p && { ...p, logo_path: path }); setBr((p) => p && { ...p, logo_path: path }); setLogoVer(Date.now()) } })
  const query = q.trim()
  const hits = query ? rows.filter((r) => matches(r, query)) : []
  const active = SECTIONS.find((s) => s.id === section)!
  const logoSrc = br.logo_path ? `/branding/logo?v=${logoVer}` : null
  const dirtyNames = SECTIONS.filter((s) => perSection[s.id]).map((s) => s.label)

  const rowsOf = (id: SectionId) => rows.filter((r) => r.section === id).map((r) => <Row key={r.id} r={r} q={query} />)
  const saved = (id: SectionId) => justSaved[id] && <Badge tone="accent"><Icon name="check" size={12} />Saved</Badge>

  return (
    <>
      <PageHeader title="Settings" sub="Station details, look, listeners and network." />
      <div className="grid gap-5 lg:grid-cols-[13rem_minmax(0,1fr)]">
        <aside className="space-y-3 lg:sticky lg:top-4 lg:self-start">
          <SearchInput value={q} onChange={setQ} placeholder="Search settings" />
          <nav aria-label="Settings sections" className="hidden space-y-0.5 lg:block">
            {SECTIONS.map((s) => (
              <button key={s.id} onClick={() => { setSection(s.id); setQ('') }} aria-current={!query && section === s.id ? 'page' : undefined}
                className={cn('flex w-full items-start gap-2.5 rounded-md px-2.5 py-2 text-left transition-colors hover:bg-hover', !query && section === s.id ? 'bg-hover text-ink' : 'text-muted')}>
                <Icon name={s.icon} size={16} className="mt-0.5 shrink-0" />
                <span className="min-w-0 flex-1"><span className="block text-[13px] font-medium">{s.label}</span><span className="block text-[11px] leading-snug text-faint">{s.blurb}</span></span>
                {perSection[s.id] ? <span className="num mt-0.5 rounded bg-warn/15 px-1.5 font-mono text-[10px] text-warn" aria-label={`${perSection[s.id]} unsaved`}>{perSection[s.id]}</span> : null}
              </button>
            ))}
          </nav>
          <div className="lg:hidden" role="group" aria-label="Section">
            <Select value={section} onValueChange={(v) => { setSection(v as SectionId); setQ('') }}
              options={SECTIONS.map((s) => ({ value: s.id, label: perSection[s.id] ? `${s.label} (${perSection[s.id]} unsaved)` : s.label }))} />
          </div>
        </aside>

        <div className="min-w-0 space-y-3">
          {query ? (
            hits.length === 0 ? <Panel legend="Search"><EmptyState icon={<Icon name="search" size={18} />} title={`No setting matches "${query}"`} body="Try a shorter word, or pick a section on the left." action={<Button onClick={() => setQ('')}>Clear search</Button>} /></Panel>
            : SECTIONS.filter((s) => hits.some((h) => h.section === s.id)).map((s) => (
              <Panel key={s.id} legend={s.label} flush>{hits.filter((h) => h.section === s.id).map((r) => <Row key={r.id} r={r} q={query} />)}</Panel>
            ))
          ) : (
            <div className={cn(section === 'look' && 'grid gap-3 xl:grid-cols-[minmax(0,1fr)_21rem]')}>
              <Panel legend={active.label} flush actions={saved(active.id)}>
                {active.intro && <p className="border-b border-line px-4 py-3 text-xs leading-relaxed text-muted">{active.intro}</p>}
                {rowsOf(active.id)}
              </Panel>
              {section === 'look' && <div className="xl:sticky xl:top-4 xl:self-start"><Preview b={br} logoSrc={logoSrc} /></div>}
            </div>
          )}
        </div>
      </div>

      <div role="region" aria-label="Save settings" className="sticky bottom-0 z-10 mt-4 flex flex-wrap items-center justify-between gap-3 rounded-lg border border-line-strong bg-surface px-4 py-2.5">
        <div className="min-w-0 text-xs" aria-live="polite">
          {dirtyCount ? <span className="text-warn">{dirtyCount} unsaved {dirtyCount === 1 ? 'change' : 'changes'} in {dirtyNames.join(', ')}</span> : <span className="text-faint">All changes saved</span>}
        </div>
        <div className="flex gap-2">
          <Button variant="ghost" disabled={!dirtyCount || saving} onClick={discard}>Discard</Button>
          <Button variant="primary" loading={saving} disabled={!dirtyCount} onClick={save}>Save changes</Button>
        </div>
      </div>
    </>
  )
}
