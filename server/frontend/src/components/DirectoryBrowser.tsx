import { useEffect } from 'preact/hooks'
import { signal } from '@preact/signals'
import { api } from '@/lib/api'

// Mirrors browseResponse in server/handlers_media_browse.go. The server
// only ever returns directories at or below the configured media roots,
// so there is nothing here to validate a second time — but the paths are
// still server data, rendered as text, never as markup.
interface BrowseEntry {
  name: string
  path: string
  /** -1 when the server skipped the count (too many siblings, or slow disk). */
  track_count: number
  dir_count: number
}

interface BrowseResponse {
  roots: string[]
  path: string
  /** "" at a root: there is nowhere above it inside the sandbox. */
  parent: string
  track_count: number
  dir_count: number
  entries: BrowseEntry[]
  truncated: boolean
}

const listing = signal<BrowseResponse | null>(null)
const loading = signal(false)
const error = signal('')

async function load(path: string) {
  loading.value = true
  error.value = ''
  try {
    const q = path ? `?path=${encodeURIComponent(path)}` : ''
    listing.value = await api.get<BrowseResponse>(`/api/autodj/browse${q}`)
  } catch (e) {
    error.value = (e as Error).message || 'Could not read that directory'
  } finally {
    loading.value = false
  }
}

// A library's parent holds no tracks of its own, so reporting only the
// track count against it reads as "nothing here" for exactly the folder
// the operator is looking at. Fall back to the subfolder count.
function contentsLabel(tracks: number, dirs: number): string {
  if (tracks < 0) return ''
  if (tracks > 0) return `${tracks} track${tracks === 1 ? '' : 's'}`
  if (dirs > 0) return `${dirs} folder${dirs === 1 ? '' : 's'}`
  return 'empty'
}

interface Props {
  /** Directory to open at; falls back to the root list when empty. */
  initialPath?: string
  onPick: (path: string) => void
  onClose: () => void
}

/**
 * DirectoryBrowser picks a music directory without typing a path. It can
 * only show what /api/autodj/browse returns, which is confined to the
 * configured media roots, so "browse" here never means "browse the
 * filesystem".
 */
export function DirectoryBrowser({ initialPath, onPick, onClose }: Props) {
  useEffect(() => {
    load(initialPath || '')
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const data = listing.value
  const atTop = !data?.path

  return (
    <div
      class="fixed inset-0 bg-black/70 backdrop-blur-sm z-[60] overflow-y-auto"
      onClick={onClose}
    >
      <div class="flex min-h-full items-center justify-center p-4">
        <div
          class="bg-surface-raised border border-border rounded-xl w-full max-w-lg flex flex-col max-h-[80vh]"
          onClick={(e) => e.stopPropagation()}
        >
          <div class="p-4 border-b border-border">
            <div class="flex items-center justify-between gap-3 mb-2">
              <h3 class="text-sm font-bold text-text-primary">Choose music directory</h3>
              <button
                onClick={onClose}
                class="text-text-tertiary hover:text-text-primary text-lg leading-none px-1"
                aria-label="Close"
              >
                &times;
              </button>
            </div>
            <p class="font-mono text-[11px] text-text-tertiary break-all">
              {atTop ? 'Configured media roots' : data?.path}
            </p>
          </div>

          <div class="flex-1 overflow-y-auto p-2">
            {loading.value && <p class="text-text-tertiary text-sm p-3">Loading...</p>}

            {!loading.value && error.value && (
              <p class="text-red-400 text-sm p-3">{error.value}</p>
            )}

            {!loading.value && !error.value && data && (
              <>
                {data.parent && (
                  <button
                    onClick={() => load(data.parent)}
                    class="w-full text-left px-3 py-2 rounded-lg hover:bg-[rgba(255,255,255,0.04)] font-mono text-sm text-text-secondary"
                  >
                    .. up one level
                  </button>
                )}

                {data.entries.length === 0 && (
                  <p class="text-text-tertiary text-sm p-3">
                    {atTop
                      ? 'No media roots are configured and none could be derived. Set "media_roots" in tinyice.json.'
                      : 'No subdirectories here.'}
                  </p>
                )}

                {data.entries.map((entry) => (
                  <button
                    key={entry.path}
                    onClick={() => load(entry.path)}
                    class="w-full flex items-center justify-between gap-3 px-3 py-2 rounded-lg hover:bg-[rgba(255,255,255,0.04)] text-left"
                  >
                    <span class="font-mono text-sm text-text-primary truncate">
                      {entry.name}
                    </span>
                    <span class="font-mono text-[11px] text-text-tertiary shrink-0">
                      {contentsLabel(entry.track_count, entry.dir_count)}
                    </span>
                  </button>
                ))}

                {data.truncated && (
                  <p class="text-text-tertiary text-xs p-3">
                    Only the first entries are shown; this directory has too many
                    subdirectories to list.
                  </p>
                )}
              </>
            )}
          </div>

          <div class="p-4 border-t border-border flex items-center justify-between gap-3">
            <span class="font-mono text-[11px] text-text-tertiary">
              {data && !atTop ? contentsLabel(data.track_count, data.dir_count) : ''}
            </span>
            <div class="flex gap-2">
              <button
                onClick={onClose}
                class="px-4 py-2 rounded-lg border border-border text-text-secondary hover:text-text-primary font-mono text-xs"
              >
                CANCEL
              </button>
              <button
                onClick={() => data && onPick(data.path)}
                disabled={atTop}
                class="px-4 py-2 rounded-lg bg-accent text-surface-base font-mono text-xs font-bold disabled:opacity-40 disabled:cursor-not-allowed"
              >
                USE THIS DIRECTORY
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
