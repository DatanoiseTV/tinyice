import { useEffect } from 'preact/hooks'
import { signal } from '@preact/signals'
import { Nav } from '@/components/Nav'
import { StreamCard } from '@/components/StreamCard'
import { STREAM_TTL_MS } from '@/lib/liveStreams'
import { createSSE } from '@/lib/sse'
import type { LandingData, StreamInfo } from '@/types'

const data = (window.__TINYICE__ ?? {}) as Partial<LandingData>
const streams = signal<StreamInfo[]>(data.streams ?? [])
const lastSeen = new Map<string, number>()
const search = signal('')

export function Explore() {
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

  const q = search.value.toLowerCase()
  const filtered = q
    ? streams.value.filter(
        (s) =>
          s.mount.toLowerCase().includes(q) ||
          (s.title && s.title.toLowerCase().includes(q)) ||
          (s.artist && s.artist.toLowerCase().includes(q))
      )
    : streams.value

  return (
    <div class="min-h-screen bg-surface-base">
      <Nav branding={data.branding ?? { logoUrl: null, accentColor: '#ff6600' }} pageTitle={data.pageTitle} />

      <main class="relative z-10 pt-14">
        <div class="mx-auto max-w-7xl px-4 py-10">
          {/* Search */}
          <div class="relative mb-8">
            <svg
              class="absolute left-4 top-1/2 -translate-y-1/2 w-4 h-4 text-text-tertiary"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
            >
              <circle cx="11" cy="11" r="8" />
              <line x1="21" y1="21" x2="16.65" y2="16.65" />
            </svg>
            <input
              type="text"
              placeholder="Search streams..."
              value={search.value}
              onInput={(e) => {
                search.value = (e.target as HTMLInputElement).value
              }}
              class="w-full bg-[rgba(255,255,255,0.03)] border border-border rounded-lg pl-11 pr-4 py-3 text-text-primary font-mono text-sm placeholder:text-text-tertiary focus:outline-none focus:border-accent/40 transition-colors"
            />
          </div>

          {/* Grid */}
          {filtered.length > 0 ? (
            <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
              {filtered.map((stream) => (
                <StreamCard
                  key={stream.mount}
                  stream={stream}
                  onPlay={() => {
                    window.location.href = `/player/${stream.mount}`
                  }}
                />
              ))}
            </div>
          ) : (
            <div class="rounded-lg border border-border bg-surface-raised p-8 text-center">
              <p class="font-mono text-xs text-text-tertiary tracking-wider">
                {q ? 'NO MATCHING STREAMS' : 'NO ACTIVE STREAMS'}
              </p>
            </div>
          )}
        </div>
      </main>

      {/* Footer */}
      <footer class="relative z-10 border-t border-border">
        <div class="mx-auto max-w-7xl px-4 py-6 flex items-center justify-between">
          <span class="font-mono text-[9px] tracking-widest text-text-tertiary/50 uppercase">
            TINYICE // PURE GO
          </span>
          <span class="font-mono text-[9px] tracking-widest text-text-tertiary/50">
            {data.version || 'dev'}
          </span>
        </div>
      </footer>
    </div>
  )
}
