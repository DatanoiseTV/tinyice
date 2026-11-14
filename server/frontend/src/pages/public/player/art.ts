import { useEffect, useState } from 'react'

const cache = new Map<string, string | null>()

/**
 * Cover lookup: MusicBrainz recording search (no key) then Cover Art Archive.
 * Runs in the listener's browser, so it is only attempted once there is a
 * real "Artist - Title" pair. Returns null when nothing is found.
 */
export async function fetchAlbumArt(artist: string, title: string, signal?: AbortSignal): Promise<string | null> {
  if (!artist || !title) return null
  const key = `${artist}::${title}`.toLowerCase()
  if (cache.has(key)) return cache.get(key) ?? null
  const miss = () => { cache.set(key, null); return null }
  try {
    const q = encodeURIComponent(`recording:"${title.replace(/"/g, '')}" AND artist:"${artist.replace(/"/g, '')}"`)
    const res = await fetch(`https://musicbrainz.org/ws/2/recording?query=${q}&limit=1&fmt=json`, { signal })
    if (!res.ok) return miss()
    const data = await res.json()
    const releaseId = data.recordings?.[0]?.releases?.[0]?.id
    if (!releaseId) return miss()
    const cover = await fetch(`https://coverartarchive.org/release/${releaseId}`, { signal })
    if (!cover.ok) return miss()
    const cd = await cover.json()
    const front = cd.images?.find((i: { front?: boolean }) => i.front)
    const url: string | null = front?.thumbnails?.['500'] || front?.thumbnails?.large || front?.thumbnails?.small || front?.image || null
    cache.set(key, url)
    return url
  } catch {
    // Aborted or offline: do not poison the cache for the next attempt.
    return null
  }
}

export function useAlbumArt(artist: string, title: string, enabled = true): string | null {
  const [url, setUrl] = useState<string | null>(null)
  useEffect(() => {
    setUrl(null)
    if (!enabled || !artist || !title) return
    const ctrl = new AbortController()
    const t = setTimeout(() => ctrl.abort(), 8000)
    fetchAlbumArt(artist, title, ctrl.signal).then((u) => { if (!ctrl.signal.aborted) setUrl(u) })
    return () => { clearTimeout(t); ctrl.abort() }
  }, [artist, title, enabled])
  return url
}
