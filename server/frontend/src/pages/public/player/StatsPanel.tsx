import { Legend, Panel } from '@/ui/console'
import { cn } from '@/lib/cn'
import type { PlaybackStats } from './usePlayback'

/** What the source is producing right now, from the public /events feed. */
export interface SourceStats {
  audioCodec: string
  audioKbps: number
  videoCodec: string
  videoWidth: number
  videoHeight: number
  videoFPS: number
  videoGOP: number
  videoKbps: number
}

function Row({ k, v, bad }: { k: string; v: string; bad?: boolean }) {
  return (
    <div className="flex items-baseline justify-between gap-4 border-b border-line py-1.5 last:border-b-0">
      <Legend>{k}</Legend>
      <span className={cn('num font-mono text-xs', bad ? 'text-danger' : 'text-ink')}>{v}</span>
    </div>
  )
}

export function StatsPanel({ source, playback, transport, isVideo, className }: {
  source: SourceStats; playback: PlaybackStats; transport: string; isVideo: boolean; className?: string
}) {
  const droppedPct = playback.totalFrames > 0 ? (playback.droppedFrames / playback.totalFrames) * 100 : 0
  return (
    <Panel legend="Stream stats" className={className}>
      <div role="group" aria-label="Stream statistics">
        <Row k="Transport" v={transport} />
        <Row k="Audio codec" v={source.audioCodec} />
        <Row k="Audio bitrate" v={source.audioKbps ? `${source.audioKbps} kbps` : '-'} />
        {isVideo && (
          <>
            <Row k="Video codec" v={source.videoCodec} />
            <Row k="Resolution" v={source.videoWidth ? `${source.videoWidth}x${source.videoHeight}` : '-'} />
            <Row k="Frame rate" v={source.videoFPS ? `${source.videoFPS.toFixed(1)} fps` : '-'} />
            <Row k="GOP" v={source.videoGOP ? `${source.videoGOP.toFixed(1)} s` : '-'} />
            <Row k="Video bitrate" v={source.videoKbps ? `${source.videoKbps} kbps` : '-'} />
          </>
        )}
        <Row k="Buffer" v={`${playback.bufferedSec.toFixed(1)} s`} />
        {isVideo && (
          <>
            <Row k="Frames" v={playback.totalFrames.toLocaleString()} />
            <Row k="Dropped" v={`${playback.droppedFrames.toLocaleString()}${playback.totalFrames ? ` (${droppedPct.toFixed(2)}%)` : ''}`} bad={droppedPct > 1} />
            {playback.latencySec > 0 && <Row k="Latency" v={`${playback.latencySec.toFixed(1)} s`} />}
          </>
        )}
      </div>
    </Panel>
  )
}
