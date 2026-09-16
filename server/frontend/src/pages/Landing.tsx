import { useEffect } from 'preact/hooks'
import { signal } from '@preact/signals'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { Nav } from '@/components/Nav'
import { StreamCard } from '@/components/StreamCard'
import { STREAM_TTL_MS } from '@/lib/liveStreams'
import { createSSE } from '@/lib/sse'
import type { LandingData, StreamInfo } from '@/types'

const data = (window.__TINYICE__ ?? {}) as Partial<LandingData>
const streams = signal<StreamInfo[]>(data.streams ?? [])
const lastSeen = new Map<string, number>()

// Configure marked for safe rendering
marked.setOptions({ breaks: true, gfm: true })

// Any admin can edit the landing markdown via Settings → Branding. Without
// sanitisation, marked happily renders <script> / <img onerror=…> straight
// into every visitor's DOM — admin-to-visitor XSS. Run every render through
// DOMPurify before handing it to dangerouslySetInnerHTML.
function renderSafeMarkdown(md: string): string {
  const raw = marked.parse(md) as string
  return DOMPurify.sanitize(raw, { USE_PROFILES: { html: true } })
}

export function Landing() {
  useEffect(() => {
    const sse = createSSE('/events')


    sse.on('stream', (evt) => {
      lastSeen.set(evt.mount, Date.now())
      streams.value = streams.value.map((s) =>
        s.mount === evt.mount
          ? {
              ...s,
              title: evt.title,
              artist: evt.artist,
              listeners: evt.listeners,
              // The event says whether a source is connected; this used
              // to hardcode true, so a mount that existed without a
              // source showed as on air.
              live: evt.live ?? true,
            }
          : s
      )
    })
    // A mount stops sending `stream` events when its source goes away;
    // without this it stayed marked live on the page indefinitely.
    const sweep = setInterval(() => {
      const now = Date.now()
      streams.value = streams.value.map((s) => {
        const seen = lastSeen.get(s.mount)
        if (seen === undefined) return s
        return now - seen < STREAM_TTL_MS ? s : { ...s, live: false, listeners: 0 }
      })
    }, 2000)


    return () => {
      clearInterval(sweep)
      sse.close()
    }
  }, [])

  const liveStreams = streams.value.filter((s) => s.live)
  const hasLive = liveStreams.length > 0
  const hasStreams = streams.value.length > 0

  const customTitle = data.pageTitle && data.pageTitle !== 'TinyIce' ? data.pageTitle : ''
  const customSubtitle = data.pageSubtitle && data.pageSubtitle !== 'Live Streaming Server powered by Go' ? data.pageSubtitle : ''
  const landingMarkdown = data.branding?.landingMarkdown || ''
  const accentColor = data.branding?.accentColor
  const isCustomized = !!(customTitle || customSubtitle || landingMarkdown)

  return (
    <div
      class="min-h-screen bg-surface-base relative overflow-hidden"
      style={accentColor ? { '--color-accent': accentColor } as any : undefined}
    >
      {/* Dot grid texture */}
      <div
        class="fixed inset-0 pointer-events-none z-0"
        style={{
          backgroundImage: 'radial-gradient(rgba(255,255,255,0.03) 1px, transparent 1px)',
          backgroundSize: '20px 20px',
        }}
      />

      {/* Ambient glow */}
      <div
        class="fixed top-0 right-0 w-[800px] h-[800px] pointer-events-none z-0"
        style={{
          background: 'radial-gradient(ellipse at 80% 20%, rgba(255,102,0,0.06) 0%, transparent 70%)',
        }}
      />

      {/* Nav */}
      <Nav branding={data.branding ?? { logoUrl: null, accentColor: '#ff6600' }} pageTitle={data.pageTitle} />

      {/* Hero */}
      <main class="relative z-10 pt-14">
        <div class="mx-auto px-8 lg:px-16 xl:px-24 py-20 lg:py-32">
          <div class="flex flex-col lg:flex-row gap-12 lg:gap-20 items-start">
            {/* Left column */}
            <div class="flex flex-col gap-8 min-w-0 flex-1">
              {/* Subtitle / category */}
              <span class="font-mono text-xs tracking-widest text-accent">
                {customSubtitle || '— AUDIO STREAMING SERVER'}
              </span>

              {/* Title */}
              <h1 class="font-heading text-5xl font-bold tracking-tight leading-tight">
                {isCustomized ? (
                  <span class="bg-gradient-to-r from-white to-accent bg-clip-text text-transparent">
                    {customTitle || data.pageTitle || 'TinyIce'}
                  </span>
                ) : (
                  <>
                    One binary.
                    <br />
                    <span class="bg-gradient-to-r from-white to-accent bg-clip-text text-transparent">
                      Pure audio.
                    </span>
                  </>
                )}
              </h1>

              {/* Description / markdown content */}
              {landingMarkdown ? (
                <div
                  class="max-w-lg markdown-content"
                  dangerouslySetInnerHTML={{ __html: renderSafeMarkdown(landingMarkdown) }}
                />
              ) : (
                <p class="text-text-tertiary text-base leading-relaxed max-w-md">
                  A lightweight, high-performance audio streaming server written in pure Go.
                  Deploy anywhere with a single binary — no dependencies, no containers, no complexity.
                </p>
              )}

              {/* CTA buttons — only show defaults when not customized */}
              {!isCustomized && (
                <div class="flex items-center gap-4">
                  <a
                    href="/admin"
                    class="font-mono text-xs tracking-widest font-bold px-6 py-3 rounded bg-accent text-surface-base hover:bg-accent/90 transition-colors"
                    style={{ boxShadow: '0 0 20px rgba(255,102,0,0.25)' }}
                  >
                    GET STARTED
                  </a>
                  <a
                    href="/developers"
                    class="font-mono text-xs tracking-widest font-bold px-6 py-3 rounded border border-border hover:border-border-hover text-text-secondary hover:text-text-primary transition-colors"
                  >
                    VIEW DOCS
                  </a>
                </div>
              )}

              {/* Stats strip — only show when not customized */}
              {!isCustomized && (
                <div class="border-t border-border pt-6 mt-4 flex items-center gap-10">
                  <div class="flex flex-col gap-1">
                    <span class="font-mono text-2xl font-bold text-text-primary">100K+</span>
                    <span class="font-mono text-[10px] tracking-wider text-text-tertiary uppercase">
                      Concurrent
                    </span>
                  </div>
                  <div class="flex flex-col gap-1">
                    <span class="font-mono text-2xl font-bold text-text-primary">&lt;1ms</span>
                    <span class="font-mono text-[10px] tracking-wider text-text-tertiary uppercase">
                      Latency
                    </span>
                  </div>
                  <div class="flex flex-col gap-1">
                    <span class="font-mono text-2xl font-bold text-text-primary">~8MB</span>
                    <span class="font-mono text-[10px] tracking-wider text-text-tertiary uppercase">
                      Binary
                    </span>
                  </div>
                </div>
              )}
            </div>

            {/* Right column — only shown when streams exist */}
            {hasStreams && (
              <div class="flex flex-col gap-4 w-full lg:w-[360px] lg:shrink-0">
                <div class="flex items-center gap-2 mb-2">
                  {hasLive && (
                    <span
                      class="w-2 h-2 rounded-full bg-live"
                      style={{ animation: 'pulse-glow 2s ease-in-out infinite' }}
                    />
                  )}
                  <span class="font-mono text-xs tracking-widest text-text-secondary">
                    LIVE STREAMS
                  </span>
                </div>

                <div class="flex flex-col gap-3">
                  {streams.value.map((stream) => (
                    <StreamCard
                      key={stream.mount}
                      stream={stream}
                      onPlay={() => {
                        window.location.href = `/player/${stream.mount}`
                      }}
                    />
                  ))}
                </div>
              </div>
            )}
          </div>
        </div>
      </main>

      {/* Footer */}
      <footer class="relative z-10 border-t border-border">
        <div class="px-8 lg:px-16 xl:px-24 py-6 flex items-center justify-between">
          <span class="font-mono text-[9px] tracking-widest text-text-tertiary/50 uppercase">
            {customTitle || 'TINYICE'} {!isCustomized && '// PURE GO'}
          </span>
          <span class="font-mono text-[9px] tracking-widest text-text-tertiary/50">
            {data.version || 'dev'}
          </span>
        </div>
      </footer>
    </div>
  )
}
