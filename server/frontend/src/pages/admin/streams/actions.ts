import { api, adminPost } from '@/lib/api'

const q = encodeURIComponent

export const createMount = (mount: string, password: string, burst_size: number) => api.post('/api/streams', { mount, password, burst_size })
export const updateMount = (mount: string, patch: { password?: string; enabled?: boolean; visible?: boolean }) => api.put('/api/streams', { mount, ...patch })
export const deleteMount = (mount: string) => api.del(`/api/streams?mount=${q(mount)}`)
export const kickSource = (mount: string) => api.post('/api/streams/kick', { mount, type: 'source' })
export const kickListeners = (mount: string) => api.post('/api/streams/kick', { mount, type: 'listeners' })

/** Legacy form endpoint: the mount and song travel in the query string, the CSRF token in the header. */
export const setSong = (mount: string, song: string) => adminPost(`/admin/metadata?mount=${q(mount)}&song=${q(song)}`)
/** Empty fallback clears it. */
export const setFallback = (mount: string, fallback: string) => adminPost('/admin/update-fallback', { mount, fallback })

export const kickAllListeners = () => adminPost('/admin/kick-all-listeners')
export const hotSwap = () => adminPost('/admin/hotswap')
export const getLowLatency = () => api.get<{ low_latency_mode: boolean }>('/api/settings')
export const setLowLatency = (low_latency_mode: boolean) => api.put('/api/settings', { low_latency_mode })
