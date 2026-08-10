# TinyIce API Documentation

## Overview

TinyIce is an Icecast-compatible streaming server with additional features like AutoDJ, transcoding, relays, and WebRTC streaming.

## Base URL

```
http://localhost:8080
```

## Authentication

- **Admin endpoints**: Session-based authentication via `/login`
- **Source endpoints**: Basic Auth (username can be empty, password is mount password or default source password)
- **Public endpoints**: No authentication required

## Admin Endpoints

### Authentication

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/login` | GET/POST | User login |
| `/logout` | GET | User logout |

### Stream Management

| Endpoint | Method | Parameters | Description |
|----------|--------|------------|-------------|
| `/admin` | GET | - | Admin dashboard |
| `/admin/add-mount` | POST | `mount`, `password` | Add new mount point |
| `/admin/remove-mount` | POST | `mount` | Remove mount point |
| `/admin/toggle-mount` | POST | `mount` | Enable/disable mount |
| `/admin/toggle-visible` | POST | `mount` | Toggle mount visibility |
| `/admin/kick` | POST | `mount` | Kick/disconnect source |
| `/admin/kick-all-listeners` | POST | - | Disconnect all listeners |
| `/admin/toggle-latency` | POST | - | Toggle low latency mode |
| `/admin/update-fallback` | POST | `mount`, `fallback` | Set fallback mount |
| `/admin/hotswap` | POST | - | Zero-downtime server restart |

### Metadata

| Endpoint | Method | Parameters | Description |
|----------|--------|------------|-------------|
| `/admin/metadata` | GET/POST | `mount`, `song` | Update stream metadata (also supports Icecast-style source auth) |
| `/admin/golive` | GET | - | Go live web interface |
| `/admin/golive/chunk` | POST | `mount` | Push audio chunks via HTTP |

### Statistics & Events

| Endpoint | Method | Parameters | Description |
|----------|--------|------------|-------------|
| `/admin/stats` | GET | - | Basic bytes in/out stats |
| `/admin/events` | GET | - | **SSE endpoint** - Real-time server statistics |
| `/admin/statistics` | GET | - | Top streams and UA statistics |
| `/admin/insights` | GET | - | Historical stats (24h) |
| `/admin/history` | GET | `mount` (query) | Stream playback history |

### Security (SuperAdmin only)

| Endpoint | Method | Parameters | Description |
|----------|--------|------------|-------------|
| `/admin/add-user` | POST | `username`, `password` | Add admin user |
| `/admin/remove-user` | POST | `username` | Remove admin user |
| `/admin/add-banned-ip` | POST | `ip` | Ban IP address |
| `/admin/remove-banned-ip` | POST | `ip` | Unban IP address |
| `/admin/add-whitelisted-ip` | POST | `ip` | Whitelist IP (returns JSON) |
| `/admin/remove-whitelisted-ip` | POST | `ip` | Remove from whitelist (returns JSON) |
| `/admin/clear-auth-lockout` | POST | `ip` | Clear auth lockout |
| `/admin/clear-scan-lockout` | POST | `ip` | Clear scan lockout |
| `/admin/security-stats` | GET | - | Security statistics (JSON) |

### Webhooks

| Endpoint | Method | Parameters | Description |
|----------|--------|------------|-------------|
| `/admin/add-webhook` | POST | `url`, `events` (multiple) | Add webhook |
| `/admin/delete-webhook` | POST | `url` | Delete webhook |

### AutoDJ Management

| Endpoint | Method | Parameters | Description |
|----------|--------|------------|-------------|
| `/admin/autodj/add` | POST | `name`, `mount`, `music_dir`, `format`, `bitrate`, `loop`, `inject_metadata`, `mpd_enabled`, `mpd_port`, `mpd_password`, `visible` | Add AutoDJ |
| `/admin/autodj/delete` | POST | `mount` | Delete AutoDJ |
| `/admin/autodj/toggle` | POST | `mount` | Enable/disable AutoDJ |
| `/admin/autodj/update` | POST | `old_mount`, `name`, `mount`, `music_dir`, `format`, `bitrate`, `loop`, `inject_metadata`, `mpd_enabled`, `mpd_port`, `mpd_password`, `visible` | Update AutoDJ |
| `/admin/autodj/studio` | GET | `mount` (query) | AutoDJ studio interface |

### AutoDJ Controls

| Endpoint | Method | Parameters | Description |
|----------|--------|------------|-------------|
| `/admin/player/toggle` | POST | `mount` | Play/pause |
| `/admin/player/restart` | POST | `mount` | Restart playback |
| `/admin/player/next` | POST | `mount` | Skip to next track |
| `/admin/player/shuffle` | POST | `mount` | Toggle shuffle |
| `/admin/player/loop` | POST | `mount` | Toggle loop |
| `/admin/player/metadata` | POST | `mount` | Toggle metadata injection |
| `/admin/player/scan` | POST | `mount` | Scan music directory |
| `/admin/player/clear-playlist` | POST | `mount` | Clear playlist |
| `/admin/player/clear-queue` | POST | `mount` | Clear queue |
| `/admin/player/save-playlist` | POST | `mount` | Save playlist |
| `/admin/player/load-playlist` | POST | `mount`, `file` | Load playlist |
| `/admin/player/reorder` | POST | `mount`, `from`, `to` | Reorder playlist item |
| `/admin/player/queue` | POST | `mount`, `path`, `action` | Queue management (`add`, `remove`, `reorder`) |
| `/admin/player/files` | GET | `mount`, `path` (query) | Browse music files (JSON) |
| `/admin/player/playlist-action` | POST | `mount`, `action`, `file`, `index` | Playlist actions (`add`, `remove`) |
| `/admin/player/playlist-info` | GET | `mount` (query) | Get playlist info (JSON) |

### Relay Management

| Endpoint | Method | Parameters | Description |
|----------|--------|------------|-------------|
| `/admin/add-relay` | POST | `url`, `mount`, `password`, `burst_size` | Add relay |
| `/admin/toggle-relay` | POST | `mount` | Enable/disable relay |
| `/admin/restart-relay` | POST | `mount` | Restart relay |
| `/admin/delete-relay` | POST | `mount` | Delete relay |

### Transcoder Management

| Endpoint | Method | Parameters | Description |
|----------|--------|------------|-------------|
| `/admin/add-transcoder` | POST | `name`, `input`, `output`, `format`, `bitrate` | Add transcoder |
| `/admin/toggle-transcoder` | POST | `name` | Enable/disable transcoder |
| `/admin/delete-transcoder` | POST | `name` | Delete transcoder |
| `/admin/transcoder-stats` | GET | - | Transcoder statistics (JSON) |

## Public Endpoints

### Stream Playback

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/{mount}` | GET | Audio stream listener |
| `/{mount}.m3u` | GET | M3U playlist |
| `/{mount}.m3u8` | GET | M3U8 playlist |
| `/{mount}.pls` | GET | PLS playlist |

### Web Interface

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/` | GET | Landing page |
| `/explore` | GET | Browse visible streams |
| `/player/{mount}` | GET | Web player page |
| `/player-webrtc/{mount}` | GET | WebRTC player |
| `/embed/{mount}` | GET | Embeddable player |

### Events (SSE)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/events` | GET | **SSE endpoint** - Public stream events (mount, name, listeners, bitrate, uptime, genre, description, song) |

### Legacy Compatibility

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/status-json.xsl` | GET | Icecast-compatible stats (JSON) |
| `/metrics` | GET | Prometheus metrics |

## WebRTC Endpoints

| Endpoint | Method | Parameters | Description |
|----------|--------|------------|-------------|
| `/webrtc/offer` | POST | `mount` (query), body: WebRTC SDP offer | Listener WebRTC stream |
| `/webrtc/source-offer` | POST | `mount` (query), body: WebRTC SDP offer | Source WebRTC connection |

`/webrtc/source-offer` is an ingest endpoint and requires credentials. Send
either the mount's source password (HTTP Basic auth, or a `password` query
parameter) or an authenticated admin session / API token that has access to
the mount. Session callers must also send the `X-CSRF-Token` header — this is
what the admin **Go Live** page does, so no source password is needed there.

## Server-Sent Events (SSE)

### `/admin/events` (Authenticated SSE)

Returns real-time server statistics every 500ms.

**Response Format:**
```json
{
  "bytes_in": 123456789,
  "bytes_out": 987654321,
  "total_listeners": 100,
  "total_sources": 5,
  "total_relays": 2,
  "total_streamers": 3,
  "streams": [
    {
      "mount": "/live",
      "name": "Live Station",
      "listeners": 50,
      "bitrate": "128",
      "uptime": "2h30m",
      "type": "audio/mpeg",
      "ip": "192.168.1.100",
      "bytes_in": 50000000,
      "bytes_out": 100000000,
      "bytes_dropped": 0,
      "song": "Artist - Track Name",
      "health": 1.0,
      "is_transcoded": false
    }
  ],
  "relays": [
    {
      "url": "http://remote:8000/stream",
      "mount": "/relay",
      "active": true,
      "enabled": true
    }
  ],
  "streamers": [
    {
      "name": "AutoDJ",
      "mount": "/autodj",
      "state": 1,
      "song": "Artist - Track",
      "start_time": 1700000000,
      "duration": 180.5,
      "position": 42.5,
      "current_id": 6,
      "playlist_pos": 5,
      "playlist_len": 100,
      "shuffle": false,
      "loop": true,
      "queue": [],
      "playlist": []
    }
  ],
  "visible_mounts": {"/live": true},
  "sys_ram": 1000000000,
  "heap_alloc": 50000000,
  "stack_sys": 2000000,
  "num_gc": 10,
  "goroutines": 50,
  "total_dropped": 0,
  "server_uptime": "24h0m0s"
}
```

`state` is `0` stopped, `1` playing, `2` paused. `position` is seconds
elapsed in the current track (0 unless playing) and `duration` is `0` when
the input format doesn't expose a track length up-front. `current_id` is
the id of the track actually playing, `-1` when it came from the queue or
an external song command. `queue` and `playlist` entries are
`{"title": …, "path": …, "id": …}`.

Note that `autodj` events are only emitted on the authenticated
`/admin/events` feed, not on the public `/events` one.

### `/events` (Public SSE)

Returns visible stream information every 500ms.

**Response Format:**
```json
[
  {
    "mount": "/live",
    "name": "Live Station",
    "listeners": 50,
    "bitrate": "128",
    "uptime": "2h30m",
    "genre": "Various",
    "description": "Live broadcasts",
    "song": "Artist - Track Name"
  }
]
```

## Source Connection (Icecast-compatible)

Connect a source client using Icecast protocol:

```
SOURCE /mount HTTP/1.1
Host: server:port
Authorization: Basic base64(username:password)
Ice-Name: Station Name
Ice-Description: Station description
Ice-Genre: Genre
Ice-Public: 0
Ice-Bitrate: 128
Content-Type: audio/mpeg

[raw audio data]
```

## Response Codes

- `200 OK` - Success
- `301/302 Redirect` - Navigation
- `400 Bad Request` - Invalid parameters
- `401 Unauthorized` - Authentication required
- `403 Forbidden` - Access denied
- `404 Not Found` - Resource not found
- `405 Method Not Allowed` - HTTP method not supported
- `503 Service Unavailable` - Server full or unavailable
