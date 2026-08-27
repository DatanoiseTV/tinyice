import { signal } from '@preact/signals'
import { useEffect } from 'preact/hooks'
import { api } from '../../lib/api'
import { reportError, showToast } from '../../lib/toast'

interface Stream {
  mount: string
  source_ip: string
  content_type: string
  listeners: number
  enabled: boolean
  visible: boolean
  health: number
  bitrate: string
  current_song: string
  name: string
  has_video?: boolean
  video_width?: number
  video_height?: number
  video_fps?: number
  video_gop?: number
  video_kbps?: number
}

const streams = signal<Stream[]>([])
const loading = signal(true)
const showModal = signal(false)
const editingMount = signal<string | null>(null) // null = creating
const formMount = signal('')
const formPassword = signal('')
const formBurst = signal(65536)
const formEnabled = signal(true)
const formVisible = signal(false)
const saveError = signal('')

// Green means "this mount has a live source". That is `source_ip`, which
// is a source *descriptor* rather than strictly an address — icecast and
// RTMP record the peer, the relay client uses "relay-pull", WebRTC uses
// "webrtc-source" and the AutoDJ uses "autodj". The AutoDJ was the one
// ingest that never set it, so a playing AutoDJ showed a permanently grey
// dot (#56).
//
// `health` only refines this when it is meaningful. The encoders skip
// work entirely while a mount has no listeners, so an idle-but-playing
// AutoDJ legitimately reports health 0 — treating that as "offline" would
// just move the false negative somewhere else. Amber is therefore
// reserved for a mount that has listeners waiting and still isn't
// delivering.
function statusDotClass(s: Stream): string {
  if (!s.source_ip) return 'bg-text-tertiary'
  if (s.listeners > 0 && s.health < 50) return 'bg-accent'
  return 'bg-live'
}

function statusLabel(s: Stream): string {
  if (!s.source_ip) return 'No source connected'
  if (s.listeners > 0 && s.health < 50) {
    return `Source connected but stalling (${Math.round(s.health)}% health)`
  }
  return `Live — source: ${s.source_ip}`
}

function resetForm() {
  formMount.value = ''
  formPassword.value = ''
  formBurst.value = 65536
  formEnabled.value = true
  formVisible.value = false
  editingMount.value = null
  saveError.value = ''
}

function openAddModal() {
  resetForm()
  showModal.value = true
}

function openEditModal(s: Stream) {
  resetForm()
  editingMount.value = s.mount
  formMount.value = s.mount
  formEnabled.value = s.enabled
  formVisible.value = s.visible
  showModal.value = true
}

async function load() {
  loading.value = true
  try {
    streams.value = await api.get<Stream[]>('/api/streams')
  } catch (e) {
    reportError(e, 'Failed to load streams')
  }
  loading.value = false
}

async function saveMount() {
  saveError.value = ''
  try {
    if (editingMount.value) {
      const body: Record<string, unknown> = {
        mount: editingMount.value,
        enabled: formEnabled.value,
        visible: formVisible.value,
      }
      if (formPassword.value) {
        body.password = formPassword.value
      }
      await api.put('/api/streams', body)
    } else {
      await api.post('/api/streams', {
        mount: formMount.value,
        password: formPassword.value,
        burstSize: formBurst.value,
      })
    }
    showModal.value = false
    resetForm()
    load()
  } catch (e) {
    saveError.value = (e as Error).message || 'Save failed'
  }
}

async function removeMount(mount: string) {
  try {
    await api.del(`/api/streams?mount=${encodeURIComponent(mount)}`)
    showToast('success', `Mount ${mount} removed`)
  } catch (e) {
    reportError(e, `Failed to remove ${mount}`)
  }
  load()
}

async function kickSource(mount: string) {
  try {
    await api.post('/api/streams/kick', { mount, type: 'source' })
  } catch (e) {
    reportError(e, `Failed to kick source on ${mount}`)
  }
  load()
}

async function kickListeners(mount: string) {
  try {
    await api.post('/api/streams/kick', { mount, type: 'listeners' })
  } catch (e) {
    reportError(e, `Failed to kick listeners on ${mount}`)
  }
  load()
}

export function Streams() {
  useEffect(() => { load() }, [])

  return (
    <div class="p-7">
      <div class="font-mono text-[10px] tracking-[2px] text-text-tertiary mb-1">MANAGE</div>
      <div class="flex items-center justify-between mb-6">
        <h1 class="text-xl font-bold text-text-primary">Streams</h1>
        <button
          onClick={openAddModal}
          class="bg-accent text-surface-base font-mono font-bold text-xs tracking-[1px] px-4 py-2.5 rounded-lg"
        >
          ADD MOUNT
        </button>
      </div>

      {/* Table */}
      <div class="border border-border rounded-xl overflow-hidden">
        <table class="w-full">
          <thead>
            <tr class="border-b border-border">
              <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Status</th>
              <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Mount</th>
              <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Source IP</th>
              <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Format</th>
              <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Listeners</th>
              <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-right px-4 py-3">Actions</th>
            </tr>
          </thead>
          <tbody>
            {loading.value ? (
              <tr><td colSpan={6} class="px-4 py-8 text-center text-text-tertiary text-sm">Loading...</td></tr>
            ) : streams.value.length === 0 ? (
              <tr><td colSpan={6} class="px-4 py-8 text-center text-text-tertiary text-sm">No streams configured</td></tr>
            ) : (
              streams.value.map((s) => (
                <tr key={s.mount} class="border-b border-[rgba(255,255,255,0.03)]">
                  <td class="px-4 py-3.5">
                    <span
                      class={`inline-block w-2 h-2 rounded-full ${statusDotClass(s)}`}
                      title={statusLabel(s)}
                      aria-label={statusLabel(s)}
                    />
                  </td>
                  <td class="px-4 py-3.5 font-mono font-bold text-sm text-text-primary">
                    <div>{s.mount}</div>
                    {s.has_video && s.video_width ? (
                      <div class="font-mono text-[10px] tracking-wider text-text-tertiary font-normal mt-0.5">
                        {s.video_width}×{s.video_height}
                        {s.video_fps ? ` · ${s.video_fps.toFixed(0)} fps` : ''}
                        {s.video_kbps ? ` · ${s.video_kbps} kbps` : ''}
                        {s.video_gop ? ` · GOP ${s.video_gop.toFixed(1)}s` : ''}
                      </div>
                    ) : null}
                  </td>
                  <td class="px-4 py-3.5 text-sm text-text-secondary">{s.source_ip || 'No source'}</td>
                  <td class="px-4 py-3.5 text-sm text-text-secondary">{s.content_type || '—'}</td>
                  <td class="px-4 py-3.5 font-mono text-sm text-text-primary">{s.listeners}</td>
                  <td class="px-4 py-3.5 text-right">
                    <div class="flex items-center justify-end gap-1">
                      <button
                        onClick={() => openEditModal(s)}
                        title="Edit mount"
                        aria-label="Edit mount"
                        class="border border-border text-text-secondary font-mono text-xs px-2 py-1.5 rounded-lg hover:border-border-hover"
                      >
                        <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-7" /><path d="M18.5 2.5a2.121 2.121 0 013 3L12 15l-4 1 1-4 9.5-9.5z" /></svg>
                      </button>
                      <button
                        onClick={() => kickSource(s.mount)}
                        title="Kick source"
                        class="border border-border text-text-secondary font-mono text-xs px-2 py-1.5 rounded-lg hover:border-border-hover"
                      >
                        <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18.36 6.64A9 9 0 005.64 18.36M5.64 5.64A9 9 0 0018.36 18.36" /><line x1="1" y1="1" x2="23" y2="23" /></svg>
                      </button>
                      <button
                        onClick={() => kickListeners(s.mount)}
                        title="Kick listeners"
                        class="border border-border text-text-secondary font-mono text-xs px-2 py-1.5 rounded-lg hover:border-border-hover"
                      >
                        <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4-4v2" /><circle cx="9" cy="7" r="4" /><line x1="18" y1="8" x2="23" y2="13" /><line x1="23" y1="8" x2="18" y2="13" /></svg>
                      </button>
                      <button
                        onClick={() => removeMount(s.mount)}
                        title="Remove mount"
                        class="border border-border text-danger font-mono text-xs px-2 py-1.5 rounded-lg hover:border-danger/30"
                      >
                        <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6" /><path d="M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2" /></svg>
                      </button>
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      {/* Add / Edit Mount Modal */}
      {showModal.value && (
        <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
          <div class="bg-surface-overlay border border-border rounded-xl p-6 max-w-md w-full mx-4">
            <h2 class="text-lg font-bold text-text-primary mb-4">
              {editingMount.value ? `Edit Mount ${editingMount.value}` : 'Add Mount'}
            </h2>
            <div class="flex flex-col gap-3">
              {!editingMount.value && (
                <div>
                  <label class="font-mono text-[10px] tracking-[2px] text-text-tertiary mb-1 block">MOUNT PATH</label>
                  <input
                    type="text"
                    value={formMount.value}
                    onInput={(e) => { formMount.value = (e.target as HTMLInputElement).value }}
                    placeholder="/stream"
                    class="w-full bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none"
                  />
                </div>
              )}
              <div>
                <label class="font-mono text-[10px] tracking-[2px] text-text-tertiary mb-1 block">
                  {editingMount.value ? 'NEW SOURCE PASSWORD (LEAVE BLANK TO KEEP)' : 'SOURCE PASSWORD'}
                </label>
                <input
                  type="password"
                  value={formPassword.value}
                  onInput={(e) => { formPassword.value = (e.target as HTMLInputElement).value }}
                  class="w-full bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none"
                />
              </div>
              {!editingMount.value && (
                <div>
                  <label class="font-mono text-[10px] tracking-[2px] text-text-tertiary mb-1 block">BURST SIZE</label>
                  <input
                    type="number"
                    value={formBurst.value}
                    onInput={(e) => { formBurst.value = parseInt((e.target as HTMLInputElement).value) || 0 }}
                    class="w-full bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none"
                  />
                </div>
              )}
              {editingMount.value && (
                <>
                  <label class="flex items-center gap-2 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={formEnabled.value}
                      onChange={(e) => { formEnabled.value = (e.target as HTMLInputElement).checked }}
                      class="accent-accent"
                    />
                    <span class="font-mono text-xs text-text-secondary">ENABLED (accept new source connections)</span>
                  </label>
                  <label class="flex items-center gap-2 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={formVisible.value}
                      onChange={(e) => { formVisible.value = (e.target as HTMLInputElement).checked }}
                      class="accent-accent"
                    />
                    <span class="font-mono text-xs text-text-secondary">VISIBLE (show in public listing)</span>
                  </label>
                </>
              )}
              {saveError.value && (
                <div class="text-danger font-mono text-xs">{saveError.value}</div>
              )}
            </div>
            <div class="flex justify-end gap-2 mt-6">
              <button
                onClick={() => { showModal.value = false; resetForm() }}
                class="border border-border text-text-secondary font-mono text-xs px-4 py-2.5 rounded-lg hover:border-border-hover"
              >
                CANCEL
              </button>
              <button
                onClick={saveMount}
                class="bg-accent text-surface-base font-mono font-bold text-xs tracking-[1px] px-4 py-2.5 rounded-lg"
              >
                SAVE
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
