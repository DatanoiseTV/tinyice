import { useEffect, useRef } from 'preact/hooks'
import { signal } from '@preact/signals'
import { EqBars } from '@/components/EqBars'
import { createSSE } from '@/lib/sse'
import type { PlayerData } from '@/types'

const data = (window.__TINYICE__ ?? {}) as Partial<PlayerData>
const title = signal(data.title || '')
const artist = signal(data.artist || '')
const playing = signal(false)

export function Embed() {
  const audioRef = useRef<HTMLAudioElement>(null)

  useEffect(() => {
    const sse = createSSE('/events')


    sse.on('stream', (evt) => {
      if (evt.mount === data.mount) {
        title.value = evt.title
        artist.value = evt.artist
      }
    })

    return () => sse.close()
  }, [])

  function togglePlay() {
    const audio = audioRef.current
    if (!audio || !data.mount) return
    if (playing.value) {
      audio.pause()
      audio.src = ''
      playing.value = false
    } else {
      audio.src = data.mount.startsWith('/') ? data.mount : `/${data.mount}`
      audio.play()
      playing.value = true
    }
  }

  return (
    <div class="h-[80px] bg-surface-base flex flex-col overflow-hidden">
      <audio ref={audioRef} preload="none" />

      <div class="flex-1 flex items-center gap-3 px-3">
        {/* Play button */}
        <button
          onClick={togglePlay}
          class="h-8 w-8 flex-shrink-0 rounded-full bg-accent flex items-center justify-center hover:bg-accent/90 transition-colors"
          aria-label={playing.value ? 'Pause' : 'Play'}
        >
          {playing.value ? (
            <svg class="w-3.5 h-3.5 text-surface-base" viewBox="0 0 24 24" fill="currentColor">
              <rect x="6" y="4" width="4" height="16" />
              <rect x="14" y="4" width="4" height="16" />
            </svg>
          ) : (
            <svg class="w-3.5 h-3.5 text-surface-base ml-0.5" viewBox="0 0 24 24" fill="currentColor">
              <path d="M8 5v14l11-7z" />
            </svg>
          )}
        </button>

        {/* Track info */}
        <div class="flex-1 min-w-0">
          <p class="text-sm text-text-primary truncate">
            {title.value || 'No track info'}
          </p>
          <p class="text-xs text-text-secondary truncate">
            {artist.value || '\u00A0'}
          </p>
        </div>

        {/* EQ visualizer */}
        {playing.value && <EqBars bars={4} />}
      </div>

      {/* Bottom visualizer bar */}
      {playing.value && (
        <div class="h-[2px] bg-gradient-to-r from-accent via-accent/60 to-transparent" />
      )}
    </div>
  )
}
