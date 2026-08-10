import type { FileInfo } from '@/types'

interface FileItemProps {
  file: FileInfo
  onAdd: () => void
  active: boolean
}

export function FileItem({ file, onAdd, active }: FileItemProps) {
  return (
    <div
      class={`group flex items-center gap-2.5 px-3 py-2 rounded-lg transition-colors cursor-pointer ${
        active
          ? 'bg-accent-subtle border border-border-accent'
          : 'hover:bg-surface-hover border border-transparent'
      }`}
    >
      {/* Icon */}
      <div class="w-5 h-5 flex-shrink-0 flex items-center justify-center">
        {file.is_dir ? (
          <svg class="w-4 h-4 text-accent" viewBox="0 0 24 24" fill="currentColor">
            <path d="M10 4H4c-1.1 0-2 .9-2 2v12c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V8c0-1.1-.9-2-2-2h-8l-2-2z" />
          </svg>
        ) : (
          <svg class="w-4 h-4 text-text-tertiary" viewBox="0 0 24 24" fill="currentColor">
            <path d="M12 3v10.55c-.59-.34-1.27-.55-2-.55-2.21 0-4 1.79-4 4s1.79 4 4 4 4-1.79 4-4V7h4V3h-6z" />
          </svg>
        )}
      </div>

      {/* Name + metadata */}
      <div class="flex-1 min-w-0">
        <div class="text-sm text-text-primary truncate">
          {file.name}{file.is_dir && '/'}
        </div>
        {!file.is_dir && file.title && file.title !== file.name && (
          <div class="font-mono text-[9px] text-text-tertiary truncate">{file.title}</div>
        )}
      </div>

      {/* Add button */}
      {!file.is_dir && (
        <button
          onClick={(e) => { e.stopPropagation(); onAdd() }}
          class={`w-6 h-6 rounded flex items-center justify-center transition-all ${
            active
              ? 'text-accent opacity-100'
              : 'text-text-tertiary opacity-0 group-hover:opacity-100 hover:text-accent'
          }`}
          aria-label={`Add ${file.name}`}
          title="Add to playlist"
        >
          <svg class="w-4 h-4" viewBox="0 0 24 24" fill="currentColor">
            <path d="M19 13h-6v6h-2v-6H5v-2h6V5h2v6h6v2z" />
          </svg>
        </button>
      )}
    </div>
  )
}
