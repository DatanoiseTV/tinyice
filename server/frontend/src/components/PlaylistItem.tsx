import { EqBars } from '@/components/EqBars'
import type { PlaylistItem as PlaylistItemType } from '@/types'

interface PlaylistItemProps {
  item: PlaylistItemType
  index: number
  playing: boolean
  onRemove: () => void
  onPlayNext: () => void
}

export function PlaylistItem({ item, index, playing, onRemove, onPlayNext }: PlaylistItemProps) {
  return (
    <div
      class={`group flex items-center gap-3 px-3 py-2 rounded-lg transition-colors ${
        playing
          ? 'bg-accent-subtle text-accent'
          : 'hover:bg-surface-hover text-text-secondary'
      }`}
    >
      {/* Index or EqBars */}
      <div class="w-6 flex-shrink-0 flex items-center justify-center">
        {playing ? (
          <EqBars bars={3} />
        ) : (
          <span class="font-mono text-[11px] text-text-tertiary">{index + 1}</span>
        )}
      </div>

      {/* Track info. The API exposes title + path only — there is no
          per-item artist or duration, so don't render empty rows for
          them. */}
      <div class="flex-1 min-w-0" title={item.path}>
        <div class={`text-sm truncate ${playing ? 'text-accent font-medium' : 'text-text-primary'}`}>
          {item.title || item.path}
        </div>
      </div>

      {/* Actions (visible on hover for non-playing) */}
      <div class={`flex items-center gap-1 ${playing ? 'opacity-100' : 'opacity-0 group-hover:opacity-100'} transition-opacity`}>
        <button
          onClick={onPlayNext}
          class="w-7 h-7 rounded flex items-center justify-center text-text-tertiary hover:text-text-primary transition-colors"
          aria-label="Play next"
          title="Play next"
        >
          <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="currentColor">
            <path d="M6 18l8.5-6L6 6v12zm2-8.14L11.03 12 8 14.14V9.86zM16 6h2v12h-2z" />
          </svg>
        </button>
        <button
          onClick={onRemove}
          class="w-7 h-7 rounded flex items-center justify-center text-text-tertiary hover:text-danger transition-colors"
          aria-label="Remove"
          title="Remove"
        >
          <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="currentColor">
            <path d="M19 6.41L17.59 5 12 10.59 6.41 5 5 6.41 10.59 12 5 17.59 6.41 19 12 13.41 17.59 19 19 17.59 13.41 12z" />
          </svg>
        </button>
      </div>

      {/* Drag handle */}
      <div class={`flex-shrink-0 cursor-grab ${playing ? 'opacity-50' : 'opacity-0 group-hover:opacity-50'} hover:!opacity-100 transition-opacity`}>
        <svg class="w-4 h-4 text-text-tertiary" viewBox="0 0 24 24" fill="currentColor">
          <path d="M3 15h18v-2H3v2zm0 4h18v-2H3v2zm0-8h18V9H3v2zm0-6v2h18V5H3z" />
        </svg>
      </div>
    </div>
  )
}
