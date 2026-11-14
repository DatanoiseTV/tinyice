import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { Badge } from '@/ui/display'
import { Legend, Panel } from '@/ui/console'
import { Input, Segmented } from '@/ui/controls'
import { Icon } from '@/ui/icons'
import { CodeBlock, CopyButton, SearchInput } from '@/ui/kit'
import { cn } from '@/lib/cn'
import { PublicShell } from '../shared/Chrome'
import { ACCESS_LABEL, ENDPOINTS, GROUPS, type Endpoint, type Method } from './endpoints'
import { authSnippets, createMountSnippet, curlFor, embedSnippet, listenSnippets, parseBase, sourceSnippets, sseSnippets, webrtcSourceSnippet, type Env, type Tabbed } from './snippets'

const EnvCtx = createContext<Env>(parseBase(location.origin, '/live'))
const useEnv = () => useContext(EnvCtx)

const SECTIONS = [
  { group: 'Start', items: [['server', 'Your server'], ['flow', 'How it fits together']] },
  { group: 'Publish', items: [['source', 'Icecast source'], ['webrtc-source', 'Browser source (WebRTC)']] },
  { group: 'Listen', items: [['listen', 'HTTP, HLS and WebRTC'], ['embed', 'Embed widget'], ['metadata', 'Now playing and status']] },
  { group: 'API', items: [['auth', 'Authentication'], ['reference', 'Endpoint reference'], ['openapi', 'OpenAPI and Swagger']] },
] as const

const LABELS: Record<string, string> = Object.fromEntries(SECTIONS.flatMap((s) => s.items.map((i) => [i[0], i[1]] as [string, string])))

function Tabs({ tabs }: { tabs: Tabbed }) {
  const [id, setId] = useState(tabs[0].id)
  const cur = tabs.find((t) => t.id === id) ?? tabs[0]
  return (
    <div className="space-y-2">
      {tabs.length > 1 && <div className="overflow-x-auto"><Segmented label="Example" value={cur.id} onChange={setId} options={tabs.map((t) => ({ value: t.id, label: t.label }))} /></div>}
      <CodeBlock label={cur.lang}>{cur.code}</CodeBlock>
    </div>
  )
}

function Section({ id, title, children }: { id: string; title: string; children: ReactNode }) {
  return (
    <section id={id} className="scroll-mt-20 border-b border-line py-8 last:border-b-0">
      <h2 className="mb-3 text-[17px] font-semibold tracking-tight text-ink"><a href={`#${id}`} className="hover:text-accent">{title}</a></h2>
      <div className="space-y-4 text-[13.5px] leading-relaxed text-muted [&_code]:rounded [&_code]:border [&_code]:border-line [&_code]:bg-raised [&_code]:px-1 [&_code]:font-mono [&_code]:text-xs [&_code]:text-ink">{children}</div>
    </section>
  )
}

const METHOD_TONE: Record<Method, string> = {
  GET: 'text-info border-info/30 bg-info/10', POST: 'text-live border-live/30 bg-live/10', PUT: 'text-warn border-warn/30 bg-warn/10', DELETE: 'text-danger border-danger/30 bg-danger/10',
}
const MethodTag = ({ m }: { m: Method }) => <span className={cn('inline-flex h-5 w-14 shrink-0 items-center justify-center rounded border font-mono text-[10px] font-semibold', METHOD_TONE[m])}>{m}</span>

function EndpointRow({ ep, open, onToggle }: { ep: Endpoint; open: boolean; onToggle: () => void }) {
  const env = useEnv()
  return (
    <li className="border-b border-line last:border-b-0">
      <button type="button" onClick={onToggle} aria-expanded={open} className="flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors hover:bg-hover/60">
        <MethodTag m={ep.method} />
        <code className="shrink-0 font-mono text-xs text-ink">{ep.path}</code>
        <span className="hidden min-w-0 flex-1 truncate text-xs text-muted md:block">{ep.summary}</span>
        <Icon name={open ? 'chevron-up' : 'chevron-down'} size={14} className="ml-auto shrink-0 text-faint" />
      </button>
      {open && (
        <div className="space-y-3 border-t border-line bg-bg/50 px-4 py-4 text-[13px]">
          <p className="text-ink">{ep.summary}</p>
          <div className="flex flex-wrap items-center gap-2"><Legend>Access</Legend><Badge tone={ep.access === 'public' ? 'live' : 'neutral'}>{ACCESS_LABEL[ep.access]}</Badge></div>
          {ep.query && <div><Legend>Query</Legend><ul className="mt-1 space-y-0.5 text-xs text-muted">{ep.query.map((q) => <li key={q}><code className="font-mono text-ink">{q.split(':')[0]}</code>{q.slice(q.indexOf(':'))}</li>)}</ul></div>}
          {ep.note && <p className="text-xs text-muted">{ep.note}</p>}
          {ep.access === 'auth' || ep.method !== 'GET' || ep.body ? <CodeBlock label="curl">{curlFor(env, ep)}</CodeBlock> : <CodeBlock label="curl">{`curl "${env.base}${ep.path.replace('{mount}', env.mount.replace(/^\//, ''))}"`}</CodeBlock>}
          {ep.response && <CodeBlock label="response">{ep.response}</CodeBlock>}
        </div>
      )}
    </li>
  )
}

function Reference() {
  const [q, setQ] = useState('')
  const [open, setOpen] = useState<string | null>(null)
  const n = q.trim().toLowerCase()
  const shown = useMemo(() => ENDPOINTS.filter((e) => !n || `${e.method} ${e.path} ${e.summary} ${e.group}`.toLowerCase().includes(n)), [n])
  return (
    <div className="space-y-3">
      <SearchInput value={q} onChange={setQ} placeholder="Filter endpoints, for example autodj or DELETE" />
      {shown.length === 0 && <p className="rounded-lg border border-line bg-surface p-6 text-center text-xs text-muted">No endpoint matches. The full list is in the OpenAPI spec below.</p>}
      {GROUPS.map((g) => {
        const list = shown.filter((e) => e.group === g)
        if (!list.length) return null
        return (
          <Panel key={g} legend={g} title={`${list.length} endpoint${list.length === 1 ? '' : 's'}`} flush>
            <ul>{list.map((e) => { const k = `${e.method} ${e.path}`; return <EndpointRow key={k} ep={e} open={open === k} onToggle={() => setOpen(open === k ? null : k)} /> })}</ul>
          </Panel>
        )
      })}
    </div>
  )
}

function ServerPanel({ base, mount, setBase, setMount }: { base: string; mount: string; setBase: (v: string) => void; setMount: (v: string) => void }) {
  return (
    <Panel legend="Your server" title="Examples below use these values">
      <div className="grid gap-3 sm:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
        <label className="block space-y-1.5"><span className="text-xs font-medium text-muted">Server URL</span><Input mono value={base} onChange={(e) => setBase(e.target.value)} spellCheck={false} autoCapitalize="none" inputMode="url" /></label>
        <label className="block space-y-1.5"><span className="text-xs font-medium text-muted">Mount</span><Input mono value={mount} onChange={(e) => setMount(e.target.value)} spellCheck={false} autoCapitalize="none" /></label>
      </div>
    </Panel>
  )
}

function Docs() {
  const env = useEnv()
  return (
    <>
      <Section id="flow" title="How it fits together">
        <p>A source publishes audio to a mount. TinyIce keeps one ring buffer per mount and serves it to any number of listeners over plain HTTP, HLS or WebRTC. Mounts, passwords and users are managed in the admin UI or through the REST API.</p>
        <ol className="grid gap-2 sm:grid-cols-3">
          {[['1', 'Create a mount', 'Admin, Streams, or POST /api/streams. Each mount has its own source password.'], ['2', 'Publish', 'Point an encoder at the mount with the Icecast protocol, or publish from a browser over WebRTC.'], ['3', 'Listen', `Open ${env.base}${env.mount} in any player, or use the built-in player at /player${env.mount}.`]].map(([k, t, d]) => (
            <li key={k} className="rounded-lg border border-line bg-surface p-3"><Legend>Step {k}</Legend><div className="mt-1 text-[13px] font-medium text-ink">{t}</div><p className="mt-1 text-xs">{d}</p></li>
          ))}
        </ol>
        <CodeBlock label="create a mount">{createMountSnippet(env)}</CodeBlock>
      </Section>

      <Section id="source" title="Icecast source">
        <p>Any Icecast-compatible encoder works unchanged. The password is the mount's source password (or the server default); the username is not checked. Repeated wrong passwords lock the client IP out temporarily. Only one source can hold a mount at a time.</p>
        <Tabs tabs={sourceSnippets(env)} />
      </Section>

      <Section id="webrtc-source" title="Browser source (WebRTC)">
        <p>Publishes Opus audio, optionally with H.264 video, from a browser. Authenticate with the source password (Basic auth or <code>password</code> query parameter), or call it from a logged-in admin session.</p>
        <CodeBlock label="js">{webrtcSourceSnippet(env)}</CodeBlock>
        <p>Opus mounts cannot be served over HLS; listeners use WebRTC or the raw HTTP stream.</p>
      </Section>

      <Section id="listen" title="HTTP, HLS and WebRTC">
        <p>The raw stream is served at the mount path in the content type it was published in. HLS is available for MP3 and AAC mounts; WHEP carries Opus and video without transcoding.</p>
        <Tabs tabs={listenSnippets(env)} />
      </Section>

      <Section id="embed" title="Embed widget">
        <p>One iframe puts a minimal player on any page. It follows the operator's accent colour and the visitor's theme.</p>
        <CodeBlock label="html">{embedSnippet(env)}</CodeBlock>
      </Section>

      <Section id="metadata" title="Now playing and status">
        <p><code>/events</code> is public and needs no credentials. It sends the visible mounts twice a second; CORS allows any origin.</p>
        <Tabs tabs={sseSnippets(env)} />
      </Section>

      <Section id="auth" title="Authentication">
        <p>Read-only public endpoints need nothing. Management endpoints under <code>/api</code> accept a session cookie or an API token. JSON requests need no CSRF token. Tokens carry the permissions of the user who created them, may expire, and record last use.</p>
        <Tabs tabs={authSnippets(env)} />
        <p>Errors are JSON: <code>{'{ "error": "message" }'}</code> with status 400, 401 (not signed in), 403 (not allowed) or 404.</p>
      </Section>

      <Section id="reference" title="Endpoint reference">
        <p>The endpoints most integrations use. Select a row for parameters and a ready-to-run curl command.</p>
        <Reference />
      </Section>

      <Section id="openapi" title="OpenAPI and Swagger">
        <p>The complete schema, including every response type, is served by this server.</p>
        <div className="grid gap-2 sm:grid-cols-2">
          <a href="/api/docs" target="_blank" rel="noopener" className="flex items-center gap-3 rounded-lg border border-line bg-surface p-4 transition-colors hover:border-accent"><Icon name="book" size={18} className="text-accent" /><div className="min-w-0 flex-1"><div className="text-[13px] font-medium text-ink">Swagger UI</div><div className="text-xs">Try requests in the browser</div></div><Icon name="external" size={14} className="text-faint" /></a>
          <a href="/api/openapi.yaml" target="_blank" rel="noopener" className="flex items-center gap-3 rounded-lg border border-line bg-surface p-4 transition-colors hover:border-accent"><Icon name="doc" size={18} className="text-accent" /><div className="min-w-0 flex-1"><div className="text-[13px] font-medium text-ink">openapi.yaml</div><div className="text-xs">OpenAPI 3.0 specification</div></div><CopyButton text={`${env.base}/api/openapi.yaml`} label="Copy spec URL" /></a>
        </div>
      </Section>
    </>
  )
}

function Nav({ active }: { active: string }) {
  return (
    <nav aria-label="Documentation" className="space-y-5">
      {SECTIONS.map((s) => (
        <div key={s.group}>
          <Legend className="mb-1.5 block px-2">{s.group}</Legend>
          <ul>{s.items.map(([id, label]) => (
            <li key={id}><a href={`#${id}`} aria-current={active === id ? 'location' : undefined} className={cn('flex h-8 items-center rounded-md border-l-2 px-2.5 text-[13px] transition-colors', active === id ? 'border-accent bg-hover text-ink' : 'border-transparent text-muted hover:text-ink')}>{label}</a></li>
          ))}</ul>
        </div>
      ))}
    </nav>
  )
}

function useActive(ids: string[]) {
  const [active, setActive] = useState(ids[0])
  useEffect(() => {
    const io = new IntersectionObserver((es) => { for (const e of es) if (e.isIntersecting) setActive(e.target.id) }, { rootMargin: '-72px 0px -65% 0px' })
    ids.forEach((id) => { const el = document.getElementById(id); if (el) io.observe(el) })
    return () => io.disconnect()
  }, [ids])
  return active
}

export function App() {
  const [rawBase, setBase] = useState(location.origin)
  const [mount, setMount] = useState('/live')
  const env = useMemo(() => parseBase(rawBase, mount), [rawBase, mount])
  const ids = useMemo(() => SECTIONS.flatMap((s) => s.items.map((i) => i[0] as string)), [])
  const active = useActive(ids)
  return (
    <PublicShell active="developers" wide>
      <EnvCtx.Provider value={env}>
        <div className="mx-auto grid max-w-[1280px] gap-x-10 px-5 lg:grid-cols-[208px_minmax(0,1fr)]">
          <aside className="hidden lg:block"><div className="sticky top-20 max-h-[calc(100vh-6rem)] overflow-y-auto py-8"><Nav active={active} /></div></aside>
          <main className="min-w-0 max-w-3xl pb-16 pt-8">
            <h1 className="text-xl font-semibold tracking-tight text-ink">Developers</h1>
            <p className="mb-5 mt-1 text-[13px] text-muted">Publish, play and manage this server from code.</p>
            <div className="mb-4 flex gap-1.5 overflow-x-auto pb-1 lg:hidden" aria-label="Sections">
              {ids.map((id) => <a key={id} href={`#${id}`} className={cn('shrink-0 rounded-md border px-2.5 py-1 text-xs', active === id ? 'border-accent text-ink' : 'border-line text-muted')}>{LABELS[id]}</a>)}
            </div>
            <section id="server" className="scroll-mt-20"><ServerPanel base={rawBase} mount={mount} setBase={setBase} setMount={setMount} /></section>
            <Docs />
          </main>
        </div>
      </EnvCtx.Provider>
    </PublicShell>
  )
}
