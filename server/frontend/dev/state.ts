import type { MockStream } from './types'

/** Shared mutable mock state. Fixtures may read and mutate it (e.g. kick a stream). */
const g = globalThis as unknown as { __tinyiceMockState?: State }
interface State {
  startedAt: number
  mounts: string[]
  sources: MockStream[]
}

/** One instance per process: the vite config bundle and ssrLoadModule would otherwise each get their own copy. */
export const state: State = (g.__tinyiceMockState ??= {
  startedAt: Date.now() - 3 * 86400_000 - 4 * 3600_000,
  mounts: ['/live', '/radio', '/jazz', '/talk'],
  sources: [
    { mount: '/live', title: 'Morning Show', artist: 'DJ Orbit', format: 'audio/mpeg', bitrate: 128, base: 142, health: 98, has_video: true },
    { mount: '/radio', title: 'Blue in Green', artist: 'Miles Davis', format: 'audio/mpeg', bitrate: 192, base: 88, health: 100 },
    { mount: '/jazz', title: 'Take Five', artist: 'Dave Brubeck', format: 'audio/ogg; codecs=opus', bitrate: 96, base: 31, health: 64 },
    { mount: '/talk', title: 'Evening Briefing', artist: '', format: 'audio/aac', bitrate: 64, base: 12, health: 100, is_transcoded: true, source_mount: '/live', source_type: 'audio/mpeg', source_bitrate: '128' },
  ] as MockStream[],
})
