import { useSyncExternalStore } from 'react'
import { registerIcons } from '@/ui/icons'
import { broadcaster, type Snapshot } from './engine'

// Own glyphs, 20px grid, 1.5 stroke.
registerIcons({
  'gl-screen': { d: 'M3 4.5h14v9H3z M7.5 16.5h5 M10 13.5v3' },
})

export const useBroadcast = (): Snapshot => useSyncExternalStore(broadcaster.subscribe, broadcaster.getSnapshot)

/** Operator preferences persisted per browser. Never contains the source password. */
export interface Prefs {
  mount: string
  authMode: 'session' | 'password'
  source: 'mic' | 'system' | 'camera' | 'screen' | 'file'
  audioDeviceId: string
  videoDeviceId: string
  videoHeight: 480 | 720 | 1080
  voiceProcessing: boolean
  screenMic: boolean
  screenSystemAudio: boolean
  gainDb: number
  limiter: boolean
}
const KEY = 'tinyice.golive.v1'
export const DEFAULT_PREFS: Prefs = {
  mount: '', authMode: 'session', source: 'mic', audioDeviceId: '', videoDeviceId: '', videoHeight: 720,
  voiceProcessing: false, screenMic: true, screenSystemAudio: true, gainDb: 0, limiter: true,
}
export function loadPrefs(): Prefs {
  try {
    const raw = localStorage.getItem(KEY)
    if (raw) return { ...DEFAULT_PREFS, ...(JSON.parse(raw) as Partial<Prefs>) }
  } catch { /* storage unavailable */ }
  return DEFAULT_PREFS
}
export function savePrefs(p: Prefs) {
  try { localStorage.setItem(KEY, JSON.stringify(p)) } catch { /* storage unavailable */ }
}
