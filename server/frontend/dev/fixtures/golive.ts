import type { Fixture } from '../types'

/**
 * Go Live fixtures. Shapes copied from the reference server:
 *  - POST /webrtc/source-offer?mount=/x  200 -> SessionDescription {type:'answer', sdp}
 *    failures are plain text bodies: 401 "Unauthorized" (bad source password), 403 "Forbidden"
 *    (no access or mount disabled), 400 {"error": "..."} when the offer cannot be negotiated.
 * Special mounts drive the error states: /forbidden -> 403, /broken -> 400 JSON error.
 * A Basic password of "wrong" is rejected with 401.
 */
const fixture: Fixture = {
  default: [
    {
      method: 'POST',
      path: '/webrtc/source-offer',
      handle: ({ url, req }) => {
        const mount = url.searchParams.get('mount') ?? ''
        if (!mount) return { status: 400, body: 'mount query param required' }
        const auth = req.headers.authorization ?? ''
        if (auth.startsWith('Basic ')) {
          const pass = Buffer.from(auth.slice(6), 'base64').toString().replace(/^[^:]*:/, '')
          if (pass === 'wrong') return { status: 401, body: 'Unauthorized' }
        } else if (!req.headers['x-csrf-token']) {
          return { status: 403, body: 'Forbidden' }
        }
        if (mount === '/forbidden') return { status: 403, body: 'Forbidden' }
        if (mount === '/broken') return { status: 400, body: { error: 'webrtc: could not negotiate a codec' } }
        return { type: 'answer', sdp: 'v=0 (mock answer)' }
      },
    },
  ],
}
export default fixture.default
