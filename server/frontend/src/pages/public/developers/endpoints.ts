/**
 * Hand-trimmed endpoint reference, derived from server/openapi.yaml (paths,
 * summaries, request bodies) and checked against server/server.go routing.
 * The full schemas live at /api/openapi.yaml and /api/docs; keep this list to
 * what a developer needs to make a first call.
 */
export type Method = 'GET' | 'POST' | 'PUT' | 'DELETE'
export type Access = 'public' | 'auth' | 'source'

export interface Endpoint {
  method: Method
  path: string
  group: string
  summary: string
  access: Access
  /** Query parameters, "name: meaning". */
  query?: string[]
  /** Example JSON request body. */
  body?: string
  /** Example response (trimmed). */
  response?: string
  note?: string
}

export const ACCESS_LABEL: Record<Access, string> = {
  public: 'No credentials',
  auth: 'Session cookie or Bearer token',
  source: 'Source password (Basic auth)',
}

export const GROUPS = ['Streaming', 'Streams', 'AutoDJ', 'Playlist', 'Relays', 'Transcoders', 'Users', 'Security', 'Auth', 'Branding and settings', 'Stats'] as const

const S = 'Streaming', ST = 'Streams', DJ = 'AutoDJ', PL = 'Playlist', RL = 'Relays', TC = 'Transcoders', US = 'Users', SE = 'Security', AU = 'Auth', BR = 'Branding and settings', STA = 'Stats'

export const ENDPOINTS: Endpoint[] = [
  { method: 'GET', path: '/{mount}', group: S, access: 'public', summary: 'Listen to a stream over HTTP.', note: 'Returns the raw audio in the mount\'s content type. Works in audio elements, VLC and ffplay.' },
  { method: 'GET', path: '/{mount}/playlist.m3u8', group: S, access: 'public', summary: 'HLS playlist.', note: 'Not available for Opus, FLAC and Vorbis mounts (MPEG-TS cannot carry them). Use WHEP for those.' },
  { method: 'GET', path: '/events', group: S, access: 'public', summary: 'Server-Sent Events feed of public streams.', note: 'Unnamed frame: array of visible mounts. Named `stream` events: one per mount, twice a second.', response: '[{ "mount": "/live", "name": "Main stage", "song": "Morning Show", "listeners": 142, "bitrate": "128", "live": true, "has_video": false }]' },
  { method: 'GET', path: '/status-json.xsl', group: S, access: 'public', summary: 'Icecast-compatible status document.', response: '{ "icestats": { "server_id": "Icecast 2.4.4 (TinyIce)", "source": [{ "mount": "/live", "listeners": 142, "title": "Morning Show" }] } }' },
  { method: 'PUT', path: '/{mount}', group: S, access: 'source', summary: 'Publish audio (Icecast source protocol).', note: 'The `SOURCE` method is accepted too. The username is not checked (use `source`); the password is the source password of the mount. Used by ffmpeg, BUTT, Mixxx and OBS.' },
  { method: 'POST', path: '/webrtc/source-offer', group: S, access: 'source', summary: 'Publish from a browser over WebRTC.', query: ['mount: mount to publish to (required)', 'password: source password, as an alternative to Basic auth'], body: '{ "type": "offer", "sdp": "v=0..." }', note: 'Returns the SDP answer. A logged-in admin session with access to the mount works as well.' },
  { method: 'POST', path: '/webrtc/offer', group: S, access: 'public', summary: 'Listen over WebRTC.', query: ['mount: mount to listen to (required)'], body: '{ "type": "offer", "sdp": "v=0..." }', note: 'Returns the SDP answer.' },
  { method: 'POST', path: '/{mount}/whep', group: S, access: 'public', summary: 'WHEP playback (Opus and H.264 without transcoding).', note: 'Content-Type application/sdp. This is the path for mounts that HLS cannot carry.' },

  { method: 'GET', path: '/api/streams', group: ST, access: 'auth', summary: 'List stream mounts, including configured mounts with no source.', response: '[{ "mount": "/live", "content_type": "audio/mpeg", "bitrate": "128", "listeners": 142, "live": true, "visible": true, "enabled": true, "health": 98, "current_song": "Morning Show" }]' },
  { method: 'POST', path: '/api/streams', group: ST, access: 'auth', summary: 'Create a mount.', body: '{ "mount": "/live", "password": "secret123" }', response: '{ "status": "created", "mount": "/live" }' },
  { method: 'PUT', path: '/api/streams', group: ST, access: 'auth', summary: 'Change password, enabled state or visibility. Omitted fields are unchanged.', body: '{ "mount": "/live", "enabled": true, "visible": false }' },
  { method: 'DELETE', path: '/api/streams', group: ST, access: 'auth', summary: 'Delete a mount.', query: ['mount: mount path (required)'] },
  { method: 'POST', path: '/api/streams/kick', group: ST, access: 'auth', summary: 'Disconnect the source or all listeners of a mount.', body: '{ "mount": "/live", "type": "source" }', note: '`type` is `source` (default) or `listeners`.' },

  { method: 'GET', path: '/api/autodj', group: DJ, access: 'auth', summary: 'List AutoDJ instances.', response: '[{ "name": "My Station", "mount": "/radio", "state": 1, "current_song": "Blue in Green", "playlist_pos": 4, "playlist_len": 120 }]', note: '`state` is 0 stopped, 1 playing, 2 paused.' },
  { method: 'POST', path: '/api/autodj', group: DJ, access: 'auth', summary: 'Create an AutoDJ instance.', body: '{ "name": "My Station", "mount": "/radio", "music_dir": "/home/music", "format": "mp3", "bitrate": 128 }' },
  { method: 'DELETE', path: '/api/autodj', group: DJ, access: 'auth', summary: 'Delete an AutoDJ instance.', query: ['mount: mount path (required)'] },
  { method: 'POST', path: '/api/autodj/play', group: DJ, access: 'auth', summary: 'Start or resume playback.' },
  { method: 'POST', path: '/api/autodj/pause', group: DJ, access: 'auth', summary: 'Pause playback.' },
  { method: 'POST', path: '/api/autodj/next', group: DJ, access: 'auth', summary: 'Skip to the next track.' },
  { method: 'POST', path: '/api/autodj/shuffle', group: DJ, access: 'auth', summary: 'Toggle shuffle.' },
  { method: 'POST', path: '/api/autodj/loop', group: DJ, access: 'auth', summary: 'Toggle loop.' },
  { method: 'GET', path: '/api/autodj/files', group: DJ, access: 'auth', summary: 'Browse the music directory.' },
  { method: 'GET', path: '/api/autodj/queue', group: DJ, access: 'auth', summary: 'Get the play-next queue.' },
  { method: 'POST', path: '/api/autodj/queue', group: DJ, access: 'auth', summary: 'Add a track to the play-next queue.' },
  { method: 'GET', path: '/api/autodj/playlist', group: PL, access: 'auth', summary: 'Get the playlist.' },
  { method: 'POST', path: '/api/autodj/playlist', group: PL, access: 'auth', summary: 'Add files to the playlist.' },
  { method: 'DELETE', path: '/api/autodj/playlist', group: PL, access: 'auth', summary: 'Remove a track from the playlist.' },
  { method: 'POST', path: '/api/autodj/playlist/clear', group: PL, access: 'auth', summary: 'Clear the playlist.' },
  { method: 'POST', path: '/api/autodj/playlist/reorder', group: PL, access: 'auth', summary: 'Move a playlist item.' },

  { method: 'GET', path: '/api/relays', group: RL, access: 'auth', summary: 'List relays.', response: '[{ "url": "http://upstream:8000/live", "mount": "/relay", "enabled": true, "active": true }]' },
  { method: 'POST', path: '/api/relays', group: RL, access: 'auth', summary: 'Create or update a relay.', body: '{ "url": "http://upstream:8000/live", "mount": "/relay", "burst_size": 20 }' },
  { method: 'POST', path: '/api/relays/toggle', group: RL, access: 'auth', summary: 'Enable or disable a relay.' },
  { method: 'DELETE', path: '/api/relays', group: RL, access: 'auth', summary: 'Delete a relay.', query: ['mount: local mount (required)'] },

  { method: 'GET', path: '/api/transcoders', group: TC, access: 'auth', summary: 'List transcoders.' },
  { method: 'POST', path: '/api/transcoders', group: TC, access: 'auth', summary: 'Create a transcoder.', body: '{ "name": "128k MP3", "input_mount": "/live", "output_mount": "/live-128", "format": "mp3", "bitrate": 128 }' },
  { method: 'DELETE', path: '/api/transcoders', group: TC, access: 'auth', summary: 'Delete a transcoder.', query: ['name: transcoder name (required)'] },

  { method: 'GET', path: '/api/users', group: US, access: 'auth', summary: 'List users.', response: '[{ "username": "admin", "role": "superadmin", "mounts": [] }]' },
  { method: 'POST', path: '/api/users', group: US, access: 'auth', summary: 'Create a user (superadmin).', body: '{ "username": "dj", "password": "...", "role": "admin" }' },
  { method: 'PUT', path: '/api/users', group: US, access: 'auth', summary: 'Update a user (superadmin).', body: '{ "username": "dj", "role": "admin" }' },
  { method: 'DELETE', path: '/api/users', group: US, access: 'auth', summary: 'Delete a user (superadmin).', query: ['username: user (required)'] },
  { method: 'GET', path: '/api/pending-users', group: US, access: 'auth', summary: 'List OIDC access requests.' },
  { method: 'POST', path: '/api/pending-users/approve', group: US, access: 'auth', summary: 'Approve an access request.' },
  { method: 'POST', path: '/api/pending-users/deny', group: US, access: 'auth', summary: 'Deny an access request.' },

  { method: 'GET', path: '/api/security/bans', group: SE, access: 'auth', summary: 'List banned IPs and CIDR ranges.' },
  { method: 'POST', path: '/api/security/bans', group: SE, access: 'auth', summary: 'Ban an IP or CIDR range.', body: '{ "ip": "192.168.1.0/24" }' },
  { method: 'DELETE', path: '/api/security/bans', group: SE, access: 'auth', summary: 'Remove a ban.', query: ['ip: address or range (required)'] },
  { method: 'GET', path: '/api/security/whitelist', group: SE, access: 'auth', summary: 'List whitelisted IPs.' },
  { method: 'POST', path: '/api/security/whitelist', group: SE, access: 'auth', summary: 'Whitelist an IP.', body: '{ "ip": "10.0.0.5" }' },
  { method: 'DELETE', path: '/api/security/whitelist', group: SE, access: 'auth', summary: 'Remove a whitelist entry.', query: ['ip: address (required)'] },

  { method: 'POST', path: '/login', group: AU, access: 'public', summary: 'Sign in with a form post; sets the `sid` cookie.', body: 'username=admin&password=...&next=/admin', note: 'application/x-www-form-urlencoded. Send `Accept: application/json` for a JSON error (401) instead of an HTML page. Repeated failures lock the client IP out for a while.' },
  { method: 'GET', path: '/logout', group: AU, access: 'auth', summary: 'End the session.' },
  { method: 'GET', path: '/api/tokens', group: AU, access: 'auth', summary: 'List API tokens (the secret is never shown again).' },
  { method: 'POST', path: '/api/tokens', group: AU, access: 'auth', summary: 'Create an API token.', body: '{ "name": "CI/CD Pipeline", "expires_at": "2027-01-01T00:00:00Z" }', response: '{ "id": "...", "token": "ti_...", "name": "CI/CD Pipeline" }', note: 'The token value is returned once.' },
  { method: 'DELETE', path: '/api/tokens', group: AU, access: 'auth', summary: 'Revoke a token.', query: ['id: token id (required)'] },
  { method: 'GET', path: '/api/oidc/providers', group: AU, access: 'public', summary: 'List enabled OIDC providers.' },
  { method: 'POST', path: '/api/passkey/login/begin', group: AU, access: 'public', summary: 'Begin passkey sign-in (WebAuthn options).' },
  { method: 'POST', path: '/api/passkey/login/finish', group: AU, access: 'public', summary: 'Complete passkey sign-in.', query: ['challengeKey: value from the begin response (required)'] },

  { method: 'GET', path: '/api/branding', group: BR, access: 'auth', summary: 'Get branding.' },
  { method: 'PUT', path: '/api/branding', group: BR, access: 'auth', summary: 'Update branding. Only provided fields change.', body: '{ "page_title": "My Radio", "accent_color": "#ff6600", "landing_markdown": "## Hello" }' },
  { method: 'POST', path: '/api/branding/logo', group: BR, access: 'auth', summary: 'Upload the logo image.' },
  { method: 'GET', path: '/api/settings', group: BR, access: 'auth', summary: 'Get server settings.' },
  { method: 'PUT', path: '/api/settings', group: BR, access: 'auth', summary: 'Update settings. Only provided fields change.', body: '{ "max_listeners": 500, "low_latency_mode": false }' },
  { method: 'GET', path: '/api/stats', group: STA, access: 'auth', summary: 'Server statistics and per-stream state.', response: '{ "total_listeners": 296, "total_streams": 4, "server_uptime": "3d 4h", "goroutines": 212, "version": "2.13.0", "streams": [] }' },
]
