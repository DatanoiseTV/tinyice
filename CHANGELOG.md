# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [2.7.0] - 2026-08-10

### Added

- **`tinyice --version` / `tinyice version`.** Prints the version, short
  commit, Go version and platform, before any config is loaded, so it
  works on a half-configured install and gives bug reports something to
  quote.

- **Monitor player in the AutoDJ Studio.** A local `<audio>` bound to the
  mount, so the operator can hear what listeners hear. `preload="none"`
  — it doesn't open a listener connection until you press play.

### Fixed

- **`trusted_proxies` was ignored everywhere except OIDC login.** The
  setting documents itself as governing "scan-detection, bans and audit
  logging", but `clientIP()` had exactly one caller. Behind Caddy/nginx
  the audit log, auth log, API-token last-used IP, listener and source
  logs, the `source_connect` webhook, HLS/WHEP viewer tracking and the
  listener geo map all recorded the reverse proxy's address.

  Rate limiting and bans were worse than cosmetic: keyed on the proxy,
  five bad logins from anyone locked out every client behind it, and a
  per-client ban could never match again. Two matching bugs are fixed
  too — exact `trusted_proxies` entries were compared as strings (so a
  dual-stack listener reporting `::ffff:10.0.0.1` never matched a
  configured `10.0.0.1`), and `X-Forwarded-For` entries carrying a port
  were used verbatim as a map key.

- **Browser "Go Live" always returned 401.** The page sent no credentials
  at all while `/webrtc/source-offer` requires the mount's source
  password, and the failure was only logged to the console. The endpoint
  now also accepts an authenticated admin session or API token with
  access to the mount (CSRF-protected), which is what the page uses, and
  the page renders handshake failures instead of swallowing them.

- **Pausing an AutoDJ made it unrecoverable.** `Streamer.Stop` cancelled
  the streamer-lifetime context, ending the playback loop for good;
  every transport stop went through it, so a later play only flipped a
  flag nobody was listening for. Transport stop and teardown are now
  separate operations. Pause also cancels the in-flight track, which it
  previously didn't — the mount kept broadcasting until the file ended.

- **AutoDJ page refetched twice a second, flashing "Loading...".** Both
  admin AutoDJ pages subscribed to the public `/events` feed, which
  emits no `autodj` event; the page compensated by refetching
  `/api/autodj` on every `stream` event and toggling the full-page
  loading state each time. They now use `/admin/events` and apply
  events in place.

- **Studio's transport reported the wrong state.** The `AutoDJEvent`
  type described a payload the server never sent, so the handler threw
  inside the SSE dispatcher's per-listener `try/catch` after assigning
  the raw numeric state — leaving `state === 'playing'` permanently
  false and the button stuck on Play. Playlist rows and the file
  browser had the same class of key mismatch and rendered blank /
  non-navigable.

- **Studio endpoints that could never succeed.** `/playlist/remove`
  treated the item id it was handed as an index (removing the wrong
  track once ids and positions diverged); `/queue` and
  `/playlist/playnext` demanded `mount` in the request body while the
  path-based routes inject it as a query parameter, so every call 400'd.
  `playnext` now really queues to the front.

### Changed

- **AutoDJ playlist and queue items are snake_case on the wire.**
  `{"Title","Path","ID"}` became `{"title","path","id"}`, matching every
  other field of the AutoDJ API. Affects `/api/autodj/{mount}/playlist`,
  `/api/autodj/{mount}/queue`, and the `queue` / `playlist` arrays in the
  `autodj` SSE event.

- **`autodj` payloads gained `position` and `current_id`**, and
  `playlist_pos` is now actually populated.

## [2.6.2] - 2026-05-13

### Added

- **Debian + RPM packages on every release.** Per-arch `.deb` (amd64,
  arm64) and `.rpm` (x86_64, aarch64) packages are now built via nFPM
  and attached to the GitHub release alongside the raw binaries. The
  package installs the binary at `/usr/bin/tinyice` with
  `cap_net_bind_service=+ep`, creates a dedicated `tinyice` system
  user, ships a hardened systemd unit at
  `/lib/systemd/system/tinyice.service`, and lays down `/etc/tinyice`
  + `/var/lib/tinyice` for config and state.

  The unit is **masked** on install so a stray `systemctl start
  tinyice` or distro auto-enable hook can't bring up an unconfigured
  daemon. The post-install message walks the operator through unmask
  → `enable --now` → reading the auto-generated admin password from
  the journal.

  CI smoke-tests the amd64 deb on every tag (`dpkg -i` → file/user
  presence → `--help` execve → verifies the unit is masked →
  `dpkg -r`).

## [2.6.1] - 2026-05-12

### Fixed

- **Auto-transcoded MP3 mounts dying on chained Ogg-Opus sources.**
  `kazzmir/opus-go`'s `PacketReader` pins the bitstream serial of
  the first page it sees and returns `ErrSerialMismatch` on the
  next BOS, which fires every time the upstream Ogg producer
  rotates its logical stream (entirely normal per RFC 3533 —
  robodj and similar sources do this between tracks). The pump
  goroutine exited cleanly on each rotation, the retry loop
  respawned, but never sustained PCM long enough for the
  downstream encoders to keep their output mounts above the
  HealthMonitor's 120 s silence threshold; the listener saw 404
  every couple of minutes. Fix: a small pure-Go Ogg page reader
  that does NOT enforce a single serial, driving the codec
  directly. Each BOS resets the per-stream state (channels,
  preskip) and re-initialises the decoder; audio decoding is
  continuous across rotations.

- **Strict-decoder rejection of real-world Opus packets.** Initial
  cut of the chain-aware decoder used `pion/opus` for the codec.
  That decoder enforces RFC 6716 packet validation strictly —
  code-1 even-payload, ≤120 ms duration, VBR overrun checks, CBR
  divisibility — and rejected several packets per minute on the
  production stream. Each reject was a silent ~20 ms gap, audible
  as a skip. Switched to `kazzmir/opus-go`'s codec, which is
  libopus transpiled to pure Go via `ccgo` (no cgo) and matches
  the reference C implementation's tolerance for real-world
  encoder output. `pion/opus` dropped from `go.mod`.

- **Transcoded outputs removed during transient source flaps.**
  When the upstream source briefly disconnected (8-36 s gaps are
  routine for robodj), the HealthMonitor reaped every transcoded
  output mount after 120 s of silence. Player saw 404, gave up,
  and on auto-reconnect got a burst of stale MP3 from the
  pre-flap buffer ("playing, silence, loading, old fragments,
  jumps"). Fix: HealthMonitor.check skips auto-remove for streams
  flagged `IsTranscoded`. They're tied to a configured encoder
  goroutine that re-attaches on every source resume; the mount
  stays at 200 throughout the gap.

- **Stale audio burst replayed after source flap recovery.** New
  `Stream.FlushAtHead` bumps a per-stream flush generation, signals
  every listener, and snaps `MinListenerOffset` to the current
  buffer head. The listener handler observes the generation on
  every signal iteration and jumps its offset forward, so listeners
  who survived the gap (and any auto-reconnecting subscribers) hear
  the encoder's fresh output, not stale buffered MP3 from before.
  `performTranscode` calls `FlushAtHead` before each encode loop,
  making the source-resume path the only place this fires in
  practice.

- **Stalled-pump watchdog (now a safety net, not the primary fix).**
  Per-pump 30 s no-write watchdog cancels the pump context if the
  decoder genuinely stops producing PCM (kept from `2.6.0`'s
  initial 8 s window, extended to 30 s since the chain-rotation
  case is now handled by the parser itself).

## [2.6.0] - 2026-05-12

### Fixed

- **Goroutine leak in transcoder retry loop.** `performTranscode`
  spawned `mirrorTranscodeMetadata` with the outer transcoder
  context, which only cancels on full transcoder stop. Every
  source disconnect/reconnect cycle (~1/min on a typical
  long-running deployment) entered the retry loop and spawned a
  fresh mirror goroutine; the old ones kept ticking forever.
  Production showed 1052 leaked goroutines and RSS of 777 MB
  (baseline 44 MB) after ~73 hours uptime. Fix: per-invocation
  child context inside performTranscode, defer-cancel; passed to
  mirrorTranscodeMetadata + EncodeMP3 + EncodeOpus + the decoder
  hub Acquire call. Goroutines that belong to one retry cycle now
  exit when that cycle returns.
- **Transcoder auto-restart gap reduced from ~2 min to ~5 s.**
  When the decoder hub's pump exited (source EOF / disconnect),
  nothing closed the orphaned PCM-fanout stream — transcoders
  subscribed to it stayed blocked on its dead signal channel and
  only recovered when HealthMonitor's 2-minute auto-remove
  finally swept the stale stream. Fix: the pump's defer now
  RemoveStream's the PCM mount, so subscribers see EOF
  immediately and the retry loop produces a fresh pump within
  the standard 5-second tick.

### Changed

- **Map rewrite: MapLibre GL + OpenFreeMap, fixes zoom/marker/
  autofit thrash.** The previous Leaflet + CARTO dashboard map
  re-ran `flyToBounds` and `clearLayers` on every SSE 'geo' tick
  (~500 ms when listeners are active). Each fly is an 800 ms
  animation; ticks at 0.5 s stack on each other — the camera
  never settled, markers flickered as they were torn down and
  re-created. New shared `<LiveGeoMap>` component (used by both
  GeoMapCard and KioskDashboard):
  - Camera refits only when the SET of cities changes
    (signature of iso/city/lat/lon tuples). Listener-count
    changes update marker sizes in place — no fly.
  - Markers diff in place via a Map keyed by city signature.
    New cities added; gone cities removed; existing cities
    update radius with a 200 ms animation. No clearLayers
    churn.
  - Provider switch from CARTO basemaps to openfreemap.org —
    no signup, no API key, explicit free-for-any-use policy.
    Tile and OSM attribution wired into the MapLibre default
    AttributionControl plus a footer line.
  - `leaflet` removed from frontend deps; `maplibre-gl` added
    (~285 KB gzipped, in a route-lazy chunk so the landing
    page is unaffected).

[2.6.0]: https://github.com/DatanoiseTV/tinyice/releases/tag/v2.6.0

## [2.5.0] - 2026-05-09

### Security

- **[CVE-2026-45327](https://github.com/DatanoiseTV/tinyice/security/advisories/GHSA-p7c4-8x34-8j8f)** ([GHSA-p7c4-8x34-8j8f](https://github.com/DatanoiseTV/tinyice/security/advisories/GHSA-p7c4-8x34-8j8f)) — Missing authentication on
  the WebRTC source-ingest endpoint. `POST /webrtc/source-offer`
  accepted any inbound SDP offer with no source-password check;
  any internet user able to reach the server could hijack any
  mount's broadcast and replace the legitimate publisher's audio.
  The icecast SOURCE / RTMP / SRT ingest paths already required
  the per-mount source password — this one didn't. Affected
  versions: **>= 0.8.95, <= 2.4.1** (introduced 2026-02-21 in
  `e2b60d6`). Fixed in this release. CWE-306. CVSS 3.1: **7.4
  High** (`AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:H/A:L`). The same fix
  also adds source-password rate-limit hookup so wrong-password
  attempts contribute to the IP-level brute-force lockout.
- Hardened additional auth gaps surfaced during the audit that
  shipped this release:
  - `POST /admin/golive/chunk` now requires CSRF + per-mount
    access. Previously, any authenticated user could broadcast
    raw audio bytes to any mount.
  - AutoDJ create / update / delete (both `/api/v2/autodjs` and
    the legacy `/admin/autodj/*` form handlers) now require
    `superadmin`. Previously a `dj`-role user could register an
    AutoDJ with arbitrary `song_command` / `on_play_command`
    shell strings that the server executes via `sh -c` —
    privilege escalation to the tinyice service user.
  - `POST /api/pending-users/approve` and `/deny` now check CSRF.
    Without it, an attacker page that a logged-in admin visited
    could submit a form-encoded POST with a JSON body that the
    handler would still decode and use to promote an attacker-
    controlled pending user to `superadmin`.
  - `/admin/clear-auth-lockout` and `/admin/clear-scan-lockout`
    now require `admin` or `superadmin`. A `dj` could
    previously clear an attacker's brute-force lockout, undoing
    the rate-limiter.
  - `handleLogin` now runs bcrypt against a dummy hash for
    unknown users to close the account-enumeration timing
    oracle (~250 ms vs <1 ms).
  - `checkAuthLimit` now bypasses the lockout when the IP is
    whitelisted, so an operator's own IP can recover after a
    misconfiguration without restarting the service.
  - MPD `password` comparison now uses
    `crypto/subtle.ConstantTimeCompare`. The MPD
    `command_list_*` accumulator is now bounded at 10 000 lines
    so an authenticated client can't OOM the process by opening
    a list and never closing it.

### Fixed

- **Production hang root cause:** `FindNextPageBoundary` infinite
  loop at the circular-buffer wrap. When the search window was
  clamped to `≤ 3` bytes, the iterator advanced by `n - 3 ≤ 0`
  and never moved forward, holding `cb.mu.RLock` until the
  process restarted. Every concurrent `Buffer.Write` then queued
  on the buffer's write lock, which queued every `Stream.Snapshot`,
  which queued every `GetStream`, freezing the relay. r4dio's
  pprof showed one stuck listener and 100+ goroutines blocked
  behind it. Fix: never advance by less than 1 byte; skip the
  search when `n < magicLen`.
- `Stream.Broadcast` could panic with `send on closed channel`
  when a listener's `Unsubscribe` raced with the listener-signal
  fan-out (production journal showed 4 occurrences in 7 days,
  all from the icecast SOURCE goroutine). Two-phase fix:
  recover() in the signal loop as a defense, and remove the
  `close(ch)` from `Unsubscribe` — every caller is a self-exit
  defer that doesn't need the wake-up signal. Eliminates the
  race entirely.
- `Stream.SetCurrentSong` was holding `s.mu.Lock` across five
  GORM/SQLite queries inside `History.Add`. Every ICY
  metadata update froze every broadcast / subscribe / snapshot
  / listener handler on the stream for the duration. Capture
  the diff under the lock, release, then call `History.Add`
  outside.
- Icecast SOURCE hijacked TCP connections now have a 60s idle
  read deadline; a silent encoder (NAT idle drop, frozen
  process) used to pin a goroutine + FD + mounted Stream
  forever, eventually exhausting FDs.
- Same idle-read deadline now also applies to the icecast
  pull-relay body, RTMP per-conn reads, and SRT publish reads.
- WebRTC `OnConnectionStateChange` is now registered ONCE per
  PeerConnection (in HandleWHEPOffer / HandleOffer) rather than
  by both `streamToTrack` and `streamVideoToTrack` — pion's
  API replaces the prior handler, so the loser was leaking a
  goroutine + Stream subscription per WHEP listener disconnect.
- WebRTC source-ingest now drains the previous publisher's pump
  goroutine (up to 3s) before letting the successor start
  writing — without this, two pumps briefly ran in parallel and
  produced torn Ogg pages on listener tabs.
- `pc.Close()` is now called explicitly in the WebRTC terminal
  state-change handler to release pion's UDP sockets / DTLS
  state.
- `Track.ResolveCodec` no longer panics on a nil receiver. The
  function's nil-check branch dereferenced `t.Codec` after
  asserting `t == nil`.
- `SavePlaylist` no longer takes `s.mu.RLock` recursively via
  `GetSongTitle`. Recursive RLock is undefined behaviour in
  Go's writer-preferring `RWMutex` and deadlocks if a writer
  queues between the outer and inner acquisitions.
- `Pipeline.Stats` now uses `atomic.LoadInt64` for `BytesIn` /
  `BytesOut` (matching the atomic writes from `Broadcast`) and
  `Stream.GetLastDataReceived` / `GetOggHead` synchronise
  reads of those fields against their locked writes (avoids
  torn `time.Time` and torn slice headers).
- TS demuxer now resyncs byte-by-byte when the sync byte is
  missing instead of jumping by 188 — silent data loss on
  misaligned SRT inputs is gone.
- HLS `RegisterHLS` is now race-safe; two concurrent first
  listeners no longer each spawn their own `segmentLoop`.
- `decoder_hub.contextDeadline` no longer leaks a 30 s goroutine
  per pump cycle (replaced with `time.After`).
- `OnTrackStart` callback runs in a goroutine outside any HTTP
  handler — recover() now contains panics in user-supplied
  webhook subscribers so they can't crash the process.
- `Streamer.Stop` now cancels the streamer-lifetime context, so
  `on_play_command` child shells get SIGKILL via
  `exec.CommandContext` instead of lingering up to their
  per-command timeout (default 10s) past the operator's Stop.
- YP directory `POST` now has a 15 s context timeout. Previously
  used `http.PostForm` against the default client with no
  timeout, so a hung directory server pinned the
  `directoryReportingTask` goroutine forever.

### Added

- `feat(transcoder): per-output visibility` — TranscoderConfig has
  a new `visibility` field with `""` (follow input, default),
  `"public"` (listed), or `"unlisted"` (hidden, still
  streamable). Surfaces in the admin add-transcoder form and
  the v2 API.
- `feat(metrics): /debug/pprof on the metrics server` — the
  metrics-server bootstrap function existed but was never
  called from `Server.Start()`. Now it is, and it also
  registers `net/http/pprof` so a stuck production instance
  can be triaged with `curl
  http://HOST:8081/debug/pprof/goroutine?debug=2` etc.
  Mutex- and block-profile sampling is enabled at rate 1.

### Changed

- `relay.Broadcast` now releases the stream's write lock before
  fanning out listener signal channels (committed earlier in
  this release line as `daf5368`). The previous full-lock fan-
  out was the dominant lock-contention vector under load.

[2.7.0]: https://github.com/DatanoiseTV/tinyice/releases/tag/v2.7.0
[2.5.0]: https://github.com/DatanoiseTV/tinyice/releases/tag/v2.5.0
