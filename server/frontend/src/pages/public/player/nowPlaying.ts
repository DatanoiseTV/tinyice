import { useEffect, useState } from 'react'
import { createSSE } from '@/lib/sse'
import type { PlayerData } from '@/types'
import { codecFromMime, kbps } from './source'
import type { SourceStats } from './StatsPanel'
import { real } from '@/lib/format'

/**
 * Live metadata for one mount from the public /events feed. `title` on the
 * wire is the current song and `artist` is the stream name, the same pairing
 * the page is rendered with. The original UI listened for a `metadata` event
 * the server never emits, so the song froze at its page-load value.
 */
export function useNowPlaying(data: Partial<PlayerData>) {
  const hasVideo = !!data.hasVideo
  const [song, setSong] = useState(real(data.title))
  const [name, setName] = useState(real(data.artist))
  const [audience, setAudience] = useState(data.listeners ?? 0)
  const [sourceLive, setSourceLive] = useState<boolean | undefined>(undefined)
  const [source, setSource] = useState<SourceStats>({
    audioCodec: codecFromMime(data.format), audioKbps: kbps(data.bitrate), videoCodec: hasVideo ? 'H264' : '-',
    videoWidth: 0, videoHeight: 0, videoFPS: 0, videoGOP: 0, videoKbps: 0,
  })

  useEffect(() => {
    const sse = createSSE('/events')
    const off = sse.on('stream', (evt) => {
      if (evt.mount !== data.mount) return
      // Only overwrite with real values: between tracks the feed sends empty strings.
      if (real(evt.title)) setSong(real(evt.title))
      if (real(evt.artist)) setName(real(evt.artist))
      // Video mounts count viewers (HLS / WHEP); audio mounts count listeners.
      setAudience((hasVideo && typeof evt.viewers === 'number' ? evt.viewers : evt.listeners) || 0)
      if (typeof evt.live === 'boolean') setSourceLive(evt.live)
      setSource((s) => ({
        ...s,
        // The feed carries bitrate as a number or as "128".
        audioKbps: kbps(evt.bitrate) || s.audioKbps,
        videoWidth: evt.video_width || 0, videoHeight: evt.video_height || 0,
        videoFPS: evt.video_fps || 0, videoGOP: evt.video_gop || 0, videoKbps: evt.video_kbps || 0,
      }))
    })
    return () => { off(); sse.close() }
  }, [data.mount, hasVideo])

  return { song, name, audience, sourceLive, source }
}
