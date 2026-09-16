import { signal } from '@preact/signals'
import { useEffect } from 'preact/hooks'
import { createSSE } from '../../lib/sse'
import { LiveStreamTracker } from '../../lib/liveStreams'
import { StatCard } from '../../components/StatCard'
import { ListenerHistoryChart } from '../../components/ListenerHistoryChart'
import { TrafficTotalsCard } from '../../components/TrafficTotalsCard'
import { GeoMapCard } from '../../components/GeoMapCard'
import type { StatsEvent, StreamEvent } from '../../types'

// Reactive state
const stats = signal<StatsEvent>({
  listeners: 0,
  streams: 0,
  bandwidth: 0,
  bandwidth_in: 0,
  bandwidth_out: 0,
  uptime: 0,
  goroutines: 0,
  memory: 0,
  gc: 0,
})

const streams = signal<StreamEvent[]>([])
const tracker = new LiveStreamTracker<StreamEvent>()
const connected = signal(false)
const timeRange = signal<'1H' | '24H' | '7D' | '30D' | '90D' | '1Y' | 'ALL'>('1H')

function formatUptime(seconds: number): string {
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`
  if (seconds < 86400) {
    const h = Math.floor(seconds / 3600)
    const m = Math.floor((seconds % 3600) / 60)
    return `${h}h ${m}m`
  }
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  return `${d}d ${h}h`
}

// formatLabel — pretty-print a content-type into a short tag for the
// streams table. "audio/mpeg" → "MP3", "audio/ogg" → "OGG", etc.
function formatLabel(ct: string): string {
  const lower = (ct || '').toLowerCase()
  if (lower.includes('mpeg') || lower.includes('mp3')) return 'MP3'
  if (lower.includes('opus')) return 'OPUS'
  if (lower.includes('ogg')) return 'OGG'
  if (lower.includes('aac')) return 'AAC'
  if (lower.includes('flac')) return 'FLAC'
  if (lower.includes('vorbis')) return 'VORBIS'
  // Fallback: drop the "audio/" prefix and uppercase.
  if (lower.startsWith('audio/')) return lower.slice(6).toUpperCase()
  return lower.toUpperCase()
}

function formatBandwidth(bytesPerSec: number): string {
  if (bytesPerSec < 1024) return `${bytesPerSec} B/s`
  if (bytesPerSec < 1048576) return `${(bytesPerSec / 1024).toFixed(1)} KB/s`
  return `${(bytesPerSec / 1048576).toFixed(1)} MB/s`
}

export function Dashboard() {
  useEffect(() => {
    const sse = createSSE('/admin/events')

    const offStats = sse.on('stats', (data: StatsEvent) => {
      stats.value = data
      connected.value = true
    })

    const offStream = sse.on('stream', (data: StreamEvent) => {
      streams.value = tracker.upsert(streams.value, data)
    })
    // Mounts that stop sending events have gone away; without this sweep
    // a disconnected source stayed on the dashboard until a reload.
    const sweep = setInterval(() => {
      streams.value = tracker.prune(streams.value)
    }, 2000)

    return () => {
      clearInterval(sweep)
      offStats()
      offStream()
      sse.close()
    }
  }, [])

  const totalStreams = streams.value.length
  const activeStreams = streams.value.filter((s) => s.listeners > 0).length

  return (
    <div class="p-7 max-w-[1400px]">
      {/* Header */}
      <div class="flex items-center justify-between mb-6">
        <div>
          <div class="font-mono text-[10px] tracking-[2px] text-text-tertiary mb-1">
            DASHBOARD
          </div>
          <h1 class="text-xl font-bold text-text-primary">System Overview</h1>
        </div>
        <div class="flex items-center gap-2">
          <span
            class="w-2 h-2 rounded-full"
            style={{
              backgroundColor: connected.value ? 'var(--color-live)' : 'var(--color-danger)',
              animation: connected.value ? 'pulse-glow 2s ease-in-out infinite' : 'none',
            }}
          />
          <span class="font-mono text-[10px] tracking-widest text-text-tertiary uppercase">
            {connected.value ? 'ALL SYSTEMS OK' : 'CONNECTING...'}
          </span>
        </div>
      </div>

      {/* Stats row */}
      <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-4 mb-6">
        <StatCard
          label="Listeners"
          value={stats.value.listeners}
          subtitle="connected now"
          gauge={Math.min(100, stats.value.listeners)}
        />
        <StatCard
          label="Streams"
          value={`${activeStreams} / ${totalStreams}`}
          subtitle="active / total"
        />
        <StatCard
          label="Inbound"
          value={formatBandwidth(stats.value.bandwidth_in || 0)}
          subtitle="from sources"
        />
        <StatCard
          label="Outbound"
          value={formatBandwidth(stats.value.bandwidth_out || stats.value.bandwidth || 0)}
          subtitle="to listeners"
        />
        <StatCard
          label="Uptime"
          value={formatUptime(stats.value.uptime)}
          subtitle="since last restart"
        />
      </div>

      {/* Listener history — real data from listener_histories table */}
      <div class="mb-6">
        <div class="flex justify-end gap-1 mb-2">
          {(['1H', '24H', '7D', '30D', '90D', '1Y', 'ALL'] as const).map((range) => (
            <button
              key={range}
              onClick={() => (timeRange.value = range)}
              class={`
                px-2 py-1 rounded font-mono text-[10px] tracking-wider transition-colors
                ${
                  timeRange.value === range
                    ? 'bg-accent/15 text-accent'
                    : 'text-text-tertiary hover:text-text-secondary hover:bg-surface-hover'
                }
              `}
            >
              {range}
            </button>
          ))}
        </div>
        <ListenerHistoryChart range={timeRange.value} />
      </div>

      {/* Cumulative bytes-in / bytes-out across day/week/month/all. */}
      <div class="mb-6">
        <TrafficTotalsCard />
      </div>

      {/* Live listener map (CartoDB Dark Matter tiles, DB-IP geolocation). */}
      <div class="mb-6">
        <GeoMapCard />
      </div>

      {/* Streams table */}
      <div class="rounded-lg border border-border bg-surface-raised overflow-hidden">
        <div class="px-4 py-3 border-b border-border">
          <span class="font-mono text-[10px] tracking-widest uppercase text-text-tertiary">
            Active Streams
          </span>
        </div>
        {streams.value.length === 0 ? (
          <div class="px-4 py-8 text-center text-text-tertiary text-sm">
            No streams connected
          </div>
        ) : (
          <table class="w-full">
            <thead>
              <tr class="text-left text-text-tertiary font-mono text-[10px] tracking-wider uppercase border-b border-border">
                <th class="px-4 py-2 font-normal">Status</th>
                <th class="px-4 py-2 font-normal">Mount</th>
                <th class="px-4 py-2 font-normal">Format</th>
                <th class="px-4 py-2 font-normal">Listeners</th>
                <th class="px-4 py-2 font-normal">Health</th>
              </tr>
            </thead>
            <tbody>
              {streams.value.map((stream) => (
                <tr
                  key={stream.mount}
                  class="border-b border-border last:border-b-0 hover:bg-surface-hover transition-colors"
                >
                  {/* Status dot */}
                  <td class="px-4 py-3">
                    <span
                      class="w-2 h-2 rounded-full inline-block"
                      style={{
                        backgroundColor:
                          stream.listeners > 0
                            ? 'var(--color-live)'
                            : 'var(--color-text-tertiary)',
                      }}
                    />
                  </td>
                  {/* Mount */}
                  <td class="px-4 py-3">
                    <span class="font-mono font-bold text-sm text-text-primary">
                      {stream.mount}
                    </span>
                    {stream.title && (
                      <div class="text-xs text-text-secondary mt-0.5">
                        {stream.artist ? `${stream.artist} - ${stream.title}` : stream.title}
                      </div>
                    )}
                  </td>
                  {/* Format — for transcoded outputs we show
                      "<source format/bitrate> → <output format/bitrate>"
                      so the operator can see at a glance what's being
                      converted. Source format is the upstream mount's
                      content-type (e.g. audio/ogg) and its advertised
                      bitrate. */}
                  <td class="px-4 py-3">
                    {stream.is_transcoded && stream.source_type ? (
                      <div class="flex items-center gap-1.5">
                        <span class="font-mono text-xs text-text-tertiary uppercase">
                          {formatLabel(stream.source_type)}
                        </span>
                        {stream.source_bitrate && (
                          <span class="text-text-tertiary text-[10px]">
                            {stream.source_bitrate}k
                          </span>
                        )}
                        <span class="text-text-tertiary text-[10px]">→</span>
                        <span class="font-mono text-xs text-text-secondary uppercase">
                          {stream.format}
                        </span>
                        {Number(stream.bitrate) > 0 && (
                          <span class="text-text-tertiary text-[10px]">
                            {stream.bitrate}k
                          </span>
                        )}
                      </div>
                    ) : (
                      <>
                        <span class="font-mono text-xs text-text-secondary uppercase">
                          {stream.format}
                        </span>
                        {Number(stream.bitrate) > 0 && (
                          <span class="text-text-tertiary text-xs ml-1">
                            {stream.bitrate}k
                          </span>
                        )}
                      </>
                    )}
                  </td>
                  {/* Listeners */}
                  <td class="px-4 py-3">
                    <span class="font-mono text-sm text-text-primary">
                      {stream.listeners}
                    </span>
                  </td>
                  {/* Health bar */}
                  <td class="px-4 py-3">
                    <div class="flex items-center gap-2">
                      <div class="h-1 w-16 rounded-full bg-surface-overlay overflow-hidden">
                        <div
                          class="h-full rounded-full transition-all duration-500"
                          style={{
                            width: `${Math.min(100, Math.max(0, stream.health))}%`,
                            backgroundColor:
                              stream.health >= 80
                                ? 'var(--color-live)'
                                : stream.health >= 50
                                  ? 'var(--color-accent)'
                                  : 'var(--color-danger)',
                          }}
                        />
                      </div>
                      <span class="font-mono text-[10px] text-text-tertiary">
                        {stream.health}%
                      </span>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {/* System info footer */}
      <div class="flex gap-6 mt-4 text-text-tertiary font-mono text-[10px] tracking-wider">
        <span>MEM {(stats.value.memory / 1048576).toFixed(1)} MB</span>
        <span>GR {stats.value.goroutines}</span>
        <span>GC {stats.value.gc}</span>
      </div>
    </div>
  )
}
