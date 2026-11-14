/**
 * Video / low-latency transports. Imported dynamically from usePlayback so
 * audio-only listeners never download hls.js.
 */

export interface HlsHandle {
  destroy(): void
  startLoad?(): void
  latency?: number
  liveSyncPosition?: number | null
}

/** master.m3u8 when the mount publishes a variant ladder, else playlist.m3u8. */
export async function resolveHLSUrl(path: string): Promise<string> {
  try {
    const res = await fetch(`${path}/master.m3u8`, { method: 'HEAD' })
    if (res.ok) return `${path}/master.m3u8`
  } catch { /* network error: fall through to the plain playlist */ }
  return `${path}/playlist.m3u8`
}

/**
 * Negotiate a WHEP viewer session and bind the remote stream to the element.
 * Resolves to a cleanup function, or null when negotiation failed.
 */
export async function attachWHEP(path: string, el: HTMLMediaElement): Promise<(() => void) | null> {
  if (typeof RTCPeerConnection === 'undefined') return null
  const pc = new RTCPeerConnection({ iceServers: [{ urls: 'stun:stun.l.google.com:19302' }] })
  const remote = new MediaStream()
  pc.addTransceiver('video', { direction: 'recvonly' })
  pc.addTransceiver('audio', { direction: 'recvonly' })
  pc.ontrack = (ev) => {
    for (const track of ev.streams[0].getTracks()) remote.addTrack(track)
    el.srcObject = remote
  }
  try {
    const offer = await pc.createOffer()
    await pc.setLocalDescription(offer)
    // The server does not trickle: wait for gathering, with a short timeout
    // for networks that cannot reach the STUN server.
    await new Promise<void>((resolve) => {
      if (pc.iceGatheringState === 'complete') return resolve()
      const t = setTimeout(resolve, 2000)
      pc.addEventListener('icegatheringstatechange', () => {
        if (pc.iceGatheringState === 'complete') { clearTimeout(t); resolve() }
      })
    })
    const res = await fetch(`${path}/whep`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/sdp' },
      body: pc.localDescription?.sdp ?? offer.sdp ?? '',
    })
    if (!res.ok) { pc.close(); return null }
    await pc.setRemoteDescription({ type: 'answer', sdp: await res.text() })
    return () => { pc.close(); el.srcObject = null }
  } catch {
    pc.close()
    return null
  }
}

/**
 * Attach HLS. Safari / iOS / Android Chrome play it natively, so the 120 KB
 * hls.js bundle is only fetched where it is needed. Returns null for native
 * playback.
 */
export async function attachHLS(url: string, el: HTMLMediaElement): Promise<HlsHandle | null> {
  if (el.canPlayType('application/vnd.apple.mpegurl')) {
    el.src = url
    return null
  }
  const { default: Hls } = await import('hls.js')
  if (!Hls.isSupported()) {
    el.src = url
    return null
  }
  // lowLatencyMode needs LL-HLS tags (#EXT-X-PART) which the server does not
  // emit; with plain short segments it chases the live edge and judders.
  // liveSyncDuration keeps a few segments of headroom for jitter and
  // maxBufferLength caps the forward buffer.
  const hls = new Hls({ enableWorker: true, liveSyncDuration: 6, liveMaxLatencyDuration: 30, maxBufferLength: 30, backBufferLength: 60 })
  hls.loadSource(url)
  hls.attachMedia(el)
  return hls as unknown as HlsHandle
}
