/** Shared by the player and the embed: keep this file free of heavy imports. */

export function mountPath(mount: string): string {
  return mount.startsWith('/') ? mount : `/${mount}`
}

/**
 * Swap to an MP3 sibling mount on browsers that cannot decode Ogg/Opus
 * (notably Safari and iOS). Tries `-mp3-256`, `-mp3-128`, then `-128`, then
 * falls back to the original. When the browser can play Ogg the original is
 * returned immediately. The server spawns the siblings when
 * AutoTranscodeMP3Bitrates is configured.
 */
export async function pickAudioSource(path: string): Promise<string> {
  const probe = document.createElement('audio')
  const canOgg =
    probe.canPlayType('audio/ogg; codecs=opus') !== '' ||
    probe.canPlayType('audio/ogg; codecs=vorbis') !== '' ||
    probe.canPlayType('audio/ogg') !== ''
  if (canOgg) return path
  for (const candidate of [`${path}-mp3-256`, `${path}-mp3-128`, `${path}-128`]) {
    try {
      // Icecast ignores Range and would stream forever: abort as soon as the
      // response headers are in so the probe does not leak a TCP connection
      // per candidate.
      const ctrl = new AbortController()
      const r = await fetch(candidate, { method: 'GET', headers: { Range: 'bytes=0-0' }, signal: ctrl.signal })
      const ct = (r.headers.get('content-type') || '').toLowerCase()
      try { await r.body?.cancel() } catch { /* already closed */ }
      ctrl.abort()
      if (r.ok && (ct.startsWith('audio/mpeg') || ct === 'audio/mp3')) return candidate
    } catch { /* candidate missing: try the next one */ }
  }
  return path
}

/** "audio/ogg; codecs=opus" -> "OPUS". */
export function codecFromMime(mime: string | undefined): string {
  if (!mime) return '-'
  const l = mime.toLowerCase()
  if (l.includes('opus')) return 'OPUS'
  if (l.includes('mp3') || l.includes('mpeg')) return 'MP3'
  if (l.includes('aac')) return 'AAC'
  if (l.includes('vorbis')) return 'VORBIS'
  if (l.includes('flac')) return 'FLAC'
  if (l.includes('ogg')) return 'OGG'
  return mime.toUpperCase()
}

/** The wire carries bitrate as a number in some payloads and "128" in others. */
export function kbps(v: unknown): number {
  if (typeof v === 'number') return v > 0 ? v : 0
  if (typeof v === 'string') {
    const n = parseInt(v, 10)
    return Number.isFinite(n) && n > 0 ? n : 0
  }
  return 0
}

export function audienceLabel(n: number, isVideo: boolean): string {
  if (isVideo) return n === 1 ? 'viewer' : 'viewers'
  return n === 1 ? 'listener' : 'listeners'
}

/** Icecast titles conventionally read "Artist - Title". */
export function parseSong(song: string): { artist: string; track: string } {
  const s = song.trim()
  const i = s.indexOf(' - ')
  if (i > 0 && i < s.length - 3) return { artist: s.slice(0, i).trim(), track: s.slice(i + 3).trim() }
  return { artist: '', track: s }
}
