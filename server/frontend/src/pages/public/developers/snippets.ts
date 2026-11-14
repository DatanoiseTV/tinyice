/** Code samples with the reader's server URL and mount substituted in. */
export interface Env { base: string; mount: string; host: string; port: string; tls: boolean; origin: string }

export function parseBase(raw: string, mount: string): Env {
  let u: URL
  try { u = new URL(raw.includes('://') ? raw : `http://${raw}`) } catch { u = new URL(location.origin) }
  const m = mount.startsWith('/') ? mount : `/${mount || 'live'}`
  const tls = u.protocol === 'https:'
  return { base: u.origin, mount: m, host: u.hostname, port: u.port || (tls ? '443' : '80'), tls, origin: u.origin }
}

export type Tabbed = { id: string; label: string; lang: string; code: string }[]

export const sourceSnippets = (e: Env): Tabbed => [
  { id: 'ffmpeg', label: 'ffmpeg', lang: 'bash', code: `# MP3, 128 kbps. Replace PASSWORD with the mount's source password.
ffmpeg -re -i input.mp3 -c:a libmp3lame -b:a 128k -content_type audio/mpeg \\
  -f mp3 "icecast://source:PASSWORD@${e.host}:${e.port}${e.mount}"` },
  { id: 'curl', label: 'curl', lang: 'bash', code: `# Raw Icecast PUT: stream a file as if it were live
curl -T input.mp3 -H "Content-Type: audio/mpeg" \\
  -u source:PASSWORD "${e.base}${e.mount}"` },
  { id: 'butt', label: 'BUTT / Mixxx', lang: 'text', code: `Server type   Icecast
Address       ${e.host}
Port          ${e.port}
Mount         ${e.mount}
User          source
Password      PASSWORD
TLS           ${e.tls ? 'on' : 'off'}` },
]

export const webrtcSourceSnippet = (e: Env) => `// Publish the microphone from a browser. Needs HTTPS (or localhost).
const media = await navigator.mediaDevices.getUserMedia({ audio: true })
const pc = new RTCPeerConnection()
media.getTracks().forEach((t) => pc.addTrack(t, media))

await pc.setLocalDescription(await pc.createOffer())
// Wait for ICE gathering so the offer carries its candidates.
await new Promise((r) => {
  if (pc.iceGatheringState === 'complete') return r()
  pc.onicegatheringstatechange = () => pc.iceGatheringState === 'complete' && r()
})

const res = await fetch('${e.base}/webrtc/source-offer?mount=${encodeURIComponent(e.mount)}', {
  method: 'POST',
  headers: { Authorization: 'Basic ' + btoa('source:PASSWORD') },
  body: JSON.stringify(pc.localDescription),
})
if (!res.ok) throw new Error(await res.text())
await pc.setRemoteDescription(await res.json())`

export const listenSnippets = (e: Env): Tabbed => [
  { id: 'html', label: 'HTML', lang: 'html', code: `<audio src="${e.base}${e.mount}" controls preload="none"></audio>` },
  { id: 'vlc', label: 'VLC / ffplay', lang: 'bash', code: `vlc "${e.base}${e.mount}"
ffplay -nodisp "${e.base}${e.mount}"` },
  { id: 'hls', label: 'HLS', lang: 'html', code: `<video id="v" controls></video>
<script src="https://cdn.jsdelivr.net/npm/hls.js@1"></script>
<script>
  const src = '${e.base}${e.mount}/playlist.m3u8'
  const v = document.getElementById('v')
  if (v.canPlayType('application/vnd.apple.mpegurl')) v.src = src
  else if (Hls.isSupported()) { const h = new Hls(); h.loadSource(src); h.attachMedia(v) }
</script>` },
  { id: 'whep', label: 'WebRTC (WHEP)', lang: 'js', code: `const pc = new RTCPeerConnection()
pc.addTransceiver('audio', { direction: 'recvonly' })
pc.addTransceiver('video', { direction: 'recvonly' }) // drop if the mount has no video
pc.ontrack = (e) => { document.querySelector('video').srcObject = e.streams[0] }

await pc.setLocalDescription(await pc.createOffer())
const res = await fetch('${e.base}${e.mount}/whep', {
  method: 'POST',
  headers: { 'Content-Type': 'application/sdp' },
  body: pc.localDescription.sdp,
})
await pc.setRemoteDescription({ type: 'answer', sdp: await res.text() })` },
]

export const embedSnippet = (e: Env) => `<iframe src="${e.base}/embed${e.mount}" width="100%" height="80" frameborder="0" allow="autoplay"></iframe>`

export const sseSnippets = (e: Env): Tabbed => [
  { id: 'js', label: 'JavaScript', lang: 'js', code: `const es = new EventSource('${e.base}/events')
// One \`stream\` event per mount, twice a second.
// \`title\` is the current song, \`artist\` is the stream's name.
es.addEventListener('stream', (ev) => {
  const { mount, title, artist, listeners, live } = JSON.parse(ev.data)
  if (mount === '${e.mount}') console.log(live ? 'on air' : 'off air', title, artist, listeners)
})` },
  { id: 'curl', label: 'curl', lang: 'bash', code: `curl -N ${e.base}/events` },
  { id: 'icecast', label: 'Icecast JSON', lang: 'bash', code: `curl -s ${e.base}/status-json.xsl | jq '.icestats.source[] | {mount, title, listeners}'` },
]

export const authSnippets = (e: Env): Tabbed => [
  { id: 'bearer', label: 'Bearer token', lang: 'bash', code: `# Create a token under Admin, API keys. It is shown once.
curl -H "Authorization: Bearer ti_XXXXXXXX" ${e.base}/api/streams` },
  { id: 'session', label: 'Session cookie', lang: 'bash', code: `# Sign in, keep the cookie, call the API. JSON endpoints need no CSRF token.
curl -c jar -d "username=admin&password=PASSWORD" -H "Accept: application/json" ${e.base}/login
curl -b jar ${e.base}/api/stats` },
]

export const createMountSnippet = (e: Env) => `curl -X POST ${e.base}/api/streams \\
  -H "Authorization: Bearer ti_XXXXXXXX" -H "Content-Type: application/json" \\
  -d '{"mount":"${e.mount}","password":"secret123"}'`

export function curlFor(e: Env, ep: { method: string; path: string; body?: string; query?: string[] }): string {
  const path = ep.path.replace('{mount}', e.mount.replace(/^\//, ''))
  const q = ep.query?.length ? `?${ep.query.map((x) => `${x.split(':')[0]}=...`).join('&')}` : ''
  const lines = [`curl${ep.method === 'GET' ? '' : ` -X ${ep.method}`} "${e.base}${path}${q}"`]
  lines.push('  -H "Authorization: Bearer ti_XXXXXXXX"')
  if (ep.body && ep.body.startsWith('{')) { lines.push('  -H "Content-Type: application/json"'); lines.push(`  -d '${ep.body}'`) }
  else if (ep.body) lines.push(`  -d '${ep.body}'`)
  return lines.join(' \\\n')
}
