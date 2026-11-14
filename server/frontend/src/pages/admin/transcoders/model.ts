import { api, adminPost } from '@/lib/api'

/** GET /api/transcoders row (apiGetTranscoders). */
export interface Transcoder {
  name: string; input: string; output: string; format: string; bitrate: number; enabled: boolean; active: boolean
  frames_processed: number; bytes_encoded: number; uptime: string
  visibility?: '' | 'public' | 'unlisted'
  sample_rate?: number; channels?: number
  opus_application?: string; opus_vbr?: boolean; opus_complexity?: number; opus_frame_size_ms?: number
}
/** GET /admin/transcoder-stats row (relay.TranscoderStats). Note `frames`/`bytes`, not `*_processed`/`*_encoded`. */
export interface TranscoderStat { name: string; input: string; output: string; format: string; bitrate: number; active: boolean; frames: number; bytes: number; uptime: string }

/** POST body of /api/transcoders and PUT ?name=. PUT overwrites every encoder tuning field, so always send all of them. */
export interface TranscoderBody {
  name: string; input_mount: string; output_mount: string; format: string; bitrate: number; enabled: boolean; visibility: string
  sample_rate?: number; channels?: number; opus_application?: string; opus_vbr?: boolean; opus_complexity?: number; opus_frame_size_ms?: number
}

export const TC_KEY = ['admin', 'transcoders'] as const
export const TC_STATS_KEY = ['admin', 'transcoder-stats'] as const
export const listTranscoders = () => api.get<Transcoder[]>('/api/transcoders')
export const transcoderStats = () => api.get<TranscoderStat[] | null>('/admin/transcoder-stats')
export const createTranscoder = (b: TranscoderBody) => api.post('/api/transcoders', b)
export const updateTranscoder = (name: string, b: TranscoderBody) => api.put(`/api/transcoders?name=${encodeURIComponent(name)}`, b)
export const deleteTranscoder = (name: string) => api.del(`/api/transcoders?name=${encodeURIComponent(name)}`)
/** Legacy form endpoint: flips enabled and starts or stops the encoder. A JSON PUT would reset tuning fields. */
export const toggleTranscoder = (name: string) => adminPost('/admin/toggle-transcoder', { name })

export const BITRATE: Record<string, [number, number]> = { mp3: [32, 320], opus: [6, 510] }
export const MP3_RATES = [0, 8000, 11025, 12000, 16000, 22050, 24000, 32000, 44100, 48000]
