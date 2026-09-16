# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [2.10.1] - 2026-09-16

### Fixed

- **`ScanMusicDir` never scanned the music directory.** It re-cached
  titles for the tracks already in the playlist and returned, so on an
  empty playlist it did nothing. The boot path calls it in exactly that
  case (`if len(adj.Playlist) == 0`) and then calls `Play()`, so an
  AutoDJ configured with nothing but a `music_dir` started, played
  nothing and never appeared as a source; the Studio's SCAN button was
  equally inert. It now walks the directory (skipping dotfiles and dot
  directories) and appends every playable file not already present, so a
  scan on a live playlist picks up new music without reordering or
  re-adding what is queued.
- **Loading a `.pls` was invisible in the Studio.** `LoadPlaylist`
  replaced the playlist without bumping `playlist_version`, and since
  2.8.0 the SSE `autodj` event carries only that version rather than the
  playlist itself — so nothing told the UI to refetch. It also left the
  playback cursor pointing into the old playlist, potentially past the
  end of the new one.
- The playable-extension list existed in three copies (scanner, file
  browser, directory picker) and had begun to drift; it is now one
  exported set.

## [2.10.0] - 2026-09-16

### Added

- **Sandboxed directory browser for the AutoDJ music path.** The music
  directory was a free-text field, so configuring one meant knowing an
  absolute server path and typing it correctly — the existing file
  browser only walks *inside* an already-configured directory, which
  cannot help you choose one. `GET /api/autodj/browse` lists directories
  (never files) at or below the configured media roots, reporting the
  tracks and subfolders in each so the library is recognisable without
  opening every candidate. Superadmin only, matching AutoDJ create and
  update. Confinement is the same function the MPD path arguments use,
  so a symlink inside a root that points out of it is refused rather
  than followed.
- **`media_roots`** bounds what that browser can list. Unset, it defaults
  to the process working directory plus the music directory of every
  configured AutoDJ — useful on an install that already works, and
  exposing nothing the operator has not already pointed the server at.
- **MPD control port and visibility are editable.** `mpd_enabled`,
  `mpd_port` and `visible` have always existed in the config, the API and
  the instance response, with no fields in the form: an MPD control port
  could only be configured by editing tinyice.json by hand.
- **Shuffle has a control.** It was in the model, the SSE event and the
  API, and absent from the card.
- **`burst_size` on mount creation** (2.9.0 shipped the handler side; the
  form now sends the field it always collected).

### Changed

- **Deleting an AutoDJ asks first.** It took the playlist and queue with
  it on the first click of a small icon sitting between the transport
  controls.
- A stopped AutoDJ names the playlist it would resume and its music
  directory instead of showing only a track count, which is the answer
  whenever that count is zero.

### Fixed

- **The OGG format was offered, stored, displayed and never
  implemented.** Every encoder path is `Format == "opus"` with MP3 as the
  fallback, so an AutoDJ configured as OGG encoded MP3 and advertised
  `audio/mpeg` while every surface reported OGG. The option is gone, the
  API rejects anything but mp3 and opus, and a stored format the encoder
  does not implement is rewritten to mp3 on load — which cannot change
  what any listener receives, since mp3 is what was already being
  produced for that value. The update handler validates before tearing
  the running streamer down, so a rejected format no longer costs the
  operator their AutoDJ.

## [2.9.0] - 2026-09-16

### Security

- **CSRF via cached HTTP Basic credentials.** `isCSRFSafe` treated a
  request with no session cookie as safe, on the reasoning that it could
  not be authenticated anyway. It could: `checkAuth` also accepts Basic,
  and `/admin/metadata` answers with `WWW-Authenticate: Basic
  realm="TinyIce"`, so a browser that has ever authenticated there
  re-attaches those credentials to every same-origin request — including
  one a third-party page triggers. A token-free cross-site POST to
  `/admin/add-user` created a superadmin. Such requests are refused now;
  Bearer-authenticated ones stay exempt, since a cross-origin form
  cannot set an `Authorization` header.
- **SSRF past `validateOutboundURL`.** The check only ever inspected
  literal IPs in the string an operator typed. Both DNS and any redirect
  the remote returns are under the other side's control, so a webhook
  aimed at a hostname resolving to `169.254.169.254`, or at a public URL
  that 302s to `127.0.0.1`, went straight through — and for a relay pull
  the response body is broadcast to listeners. Every outbound client
  (webhooks, YP directory, GeoIP downloads, relay pulls) now applies the
  address policy at connect time, to the address actually being dialled,
  on every hop of a redirect chain. The policy also gained RFC 6598
  carrier-grade NAT space and `0.0.0.0/8`.

### Fixed

- **AutoDJ edits switched off settings the form never submitted.** Absent
  `mpd_enabled` / `visible` / `loop` decoded as `false`, so any unrelated
  edit disabled the MPD server and hid the mount. Omitted fields are now
  left alone.
- **A rejected AutoDJ update left the mount with no AutoDJ at all.** The
  old instance was torn down and the new config saved before the restart
  was attempted, so a taken MPD port produced a config describing an
  AutoDJ that does not run. It now starts first and restores the previous
  streamer and config on failure.
- **The Studio's metadata switch could invert.** The endpoint always
  flipped the flag while the UI posts the state it wants; a double click
  or a second tab left the switch showing the opposite of reality.
- **The volume knob jumped to full at 1%.** The endpoint guessed its unit
  by range, which makes every value in 0..1 ambiguous. The body carries
  an explicit `unit` now (`percent` / `fraction`); callers that send none
  keep the old guess.
- **"Load playlist" loaded nothing and then remembered it.** With no file
  named, `filepath.Base("")` is `"."`, which was accepted and persisted
  as `last_playlist`. The Studio now tracks the selected library entry.
- **The mount create form's burst size was discarded.** It was posted as
  `burstSize` and no handler read it, so every mount ran on the 512 KiB
  default. It is `burst_size` on both sides now, stored per mount and
  bounded by a cap the listener path also applies.
- **The audit-log category filter hid 16 of the 36 recorded actions.** It
  was a hand-written list of exact action names that had gone stale:
  "Streams" hid every `mount_updated` / `mount_enabled` /
  `mount_disabled` / kick, and webhooks had no category at all. Filtering
  is prefix-based now, an unknown category matches nothing instead of
  everything, and a test scans the handlers so a new action cannot fall
  outside the filter.
- **Login ignored `?next=`.** `/kiosk` has always redirected through it,
  but every login landed on `/admin`. It is followed now, refusing
  anything that is not a same-origin path.
- **Disconnected sources stayed on screen until reload.** The SSE feeds
  send one event per live mount per tick and nothing when a mount goes
  away, so the dashboard, kiosk wall, landing page and explore
  accumulated stale entries. They expire now, and the public feed carries
  the source's actual live state rather than leaving the client to assume
  that receiving an event means "on air".
- **The admin `stream` event dropped half its fields.** `artist` was
  hardcoded empty and the whole transcode group never left the server, so
  the dashboard's "source format to output format" display had nothing to
  render.
- **The explore page marked video mounts as audio-only** — it built its
  own stream list, which had drifted from the landing page's and omitted
  `has_video`.
- **The kiosk wall never showed the station name**, reading `title` from
  the page data where the server injects `pageTitle`.
- **A silent relay upstream parked its goroutine forever.** The idle
  watchdog cancelled a context derived after the request was already in
  flight, which does nothing to a parked `body.Read` — precisely the
  stall it exists for.
- **Every HLS source flap leaked two goroutines, permanently.** A
  `FrameHub` subscriber's watcher waited on the caller's context alone,
  so a subscription ended by `Close` stayed parked, and the framed loop
  resubscribes with the same long-lived context on every flap.
- **MPD `lsinfo` held the streamer lock across `os.ReadDir`**, stalling
  playback for as long as the filesystem took.
- **Pausing an Opus AutoDJ on a file that was not already 48 kHz** made
  the encoder think it was behind schedule on resume and dump the rest of
  the track into the ring buffer at full speed: the resampler sat on top
  of the pause gate and hid it from the encoder's pacing.
- Data race on `Buffer.Head` in `Subscribe` and the decoder hub.
- A source connection whose `ResponseWriter` cannot carry a read deadline
  is reported instead of silently running the ingest with no idle timeout.
- `Login.tsx` and `Embed.tsx` typecheck again (a duplicate
  `window.__TINYICE__` declaration), and `Embed` no longer throws when
  opened without a mount.

### Removed

- The `streams` SSE event type and the landing page's subscription to it.
  No handler has ever emitted that event.

## [2.8.2] - 2026-09-11

### Security

- **Any DJ could store HTML at `/branding/logo`** — the upload had no role
  check and trusted the file extension, and the logo is served from the
  site's origin: stored XSS against every visitor, the superadmin
  included. Superadmin only, raster images only, content sniffed, and
  served with `nosniff` and a sandboxing CSP.
- **MPD `rm` deleted arbitrary `.pls` files** via a traversing playlist
  name. Names are bare filenames now.
- **`/admin/hotswap` restarted the server for any account**; superadmin
  only.
- **Icecast SOURCE passwords could be brute-forced** with no lockout;
  they now count like every other credential check.
- **API tokens could mint tokens**, so an expiring token could create a
  permanent one. Token creation needs a logged-in session.
- **A deleted original superadmin was recreated on every restart** with
  its original password (legacy `admin_user` migration). The lift now
  only bootstraps an empty user table.
- **Rejected WebRTC offers leaked the peer connection** (four goroutines
  plus tickers each) on the unauthenticated `/webrtc/offer`.
- **Passkey login/begin** could grow server state without bound; pending
  challenges are capped.

### Fixed

- **OIDC login behind a TLS-terminating reverse proxy** registered an
  `http://` redirect URI and was refused by the provider. It now uses
  `base_url`, else `X-Forwarded-Proto`/`Host` from a trusted proxy.
- Poster uploads are limited to mounts with video and to decodable JPEGs.

## [2.8.1] - 2026-09-11

### Security

- **Setup could be completed with an empty token after a restart.** A
  restart before the first-run wizard finished booted with no setup
  token, and the completion handler compared the client's token against
  `""` with a constant-time compare that reports two empty strings as
  equal — anyone reaching `/setup/complete` became superadmin. A fresh
  token is minted (and printed) on every boot that still needs setup,
  and an empty token is refused outright.
- **Legacy `/admin/player/*` handlers never checked per-mount access.** A
  DJ with `/a` could drive, clear, reorder, set metadata on and browse
  the music directory of `/b`. All 17 handlers now enforce it.
- **CSRF via GET on every legacy admin form handler.** `isCSRFSafe`
  whitelisted GET, the handlers read parameters from the query string and
  never checked the method, and the session cookie is `SameSite=Lax`
  (sent on cross-site top-level navigations). A link to
  `/admin/add-user?username=..&password=..` — or `/admin/autodj/add`
  with a `song_command`, which is a shell exec — was a one-click,
  token-free mutation. Mutating handlers reached by GET now return 403.
  The Icecast `updinfo` GET keeps working with Basic auth but no longer
  accepts the session cookie.
- **`/branding/logo` could serve any file, including `tinyice.json`.**
  Any authenticated account (no role check) could set `logo_path`
  verbatim; the logo endpoint served it unauthenticated. Superadmin
  only, and the path must be a regular file under `branding/`.
- **A DJ could take over an AutoDJ, relay or transcoder mount** by
  creating a stream with that mount name; the "mount taken" check never
  consulted those. It does now.

### Fixed

- **Two process-ending panics from wire input**: an MPEG-TS packet with
  an out-of-range `pointer_field` (SRT publishers), and MPD
  `albumart "x" -1`. Both are bounds-checked, and the SRT/RTMP/MPD
  goroutines now recover a panic per connection instead of taking the
  server down.
- **A WebRTC source offer with two tracks crashed the server** on
  disconnect (`close of closed channel`); the first Opus track now owns
  the pump and extra tracks are declined.
- **WebRTC/WHEP viewer pump spun at 100% CPU** forever when its mount
  closed before it found the first Ogg page.
- **MPD `currentsong` / `playlistid` could deadlock the AutoDJ** via a
  recursive `RLock` racing the playback loop's writer.
- **Audio-only HLS output died permanently after the first source
  drop**; it now resubscribes like the A/V path.
- **Listeners on a mount with a fallback hung forever** with no response
  when both mounts were down, leaking a goroutine per reconnect; fresh
  connections get 404, mid-stream listeners wait bounded and honour
  disconnects. A fallback *cycle* (`/a → /b → /a`) additionally spun at
  100% CPU; the fallback is now followed once per pass.
- **`SaveConfig` raced handler map writes** — Go's fatal "concurrent map
  iteration and map write", which ends the process. It runs from a
  background timer (API-token last-used tracking) as well as from
  handlers; an API client plus one admin action at the wrong moment was
  enough. Config maps are now guarded by a lock taken by every mutator
  and by the marshal.
- **MPD password authentication could never succeed.** With
  `mpd_password` set, a correct `password` was answered OK and every
  following command "permission denied". Fixed, and the argument is
  unquoted as libmpdclient sends it.
- **MPD `idle` froze clients.** `noidle` sat unread for up to 30 s (every
  keypress in ncmpcpp/cantata froze) and was then answered "unknown
  command", desynchronising later replies. `idle` is interruptible,
  reports the subsystem that changed, and play/pause/track changes now
  wake idle clients.
- **The player's now-playing title never updated**: it listened for a
  `metadata` event the server never emits. It follows the `stream` event
  now.

## [2.8.0] - 2026-09-11

### Fixed

- **The LIVE badge and Stop Broadcast button flickered while
  broadcasting** (reported on iOS, but present everywhere). Both carried
  the `pulse-glow` animation, which dips `opacity` from 1 to 0.5 twice
  every two seconds. That's the intended effect on a 2.5 px status dot,
  but on a full-width 56 px button it fades the whole control and its
  label in and out. They now use a new `pulse-live-glow` keyframe that
  pulses only the box-shadow and never touches opacity (measured: the
  opacity swing goes from 0.50 to 0.00).

  `pulse-glow`'s midpoint also hard-coded the green live colour, so a red
  indicator pulsed red-to-green; both stops now derive from
  `--color-live`. Added a `prefers-reduced-motion` opt-out for both.

- **The Go Live page re-rendered on every animation frame while live.**
  The level meters wrote their values to signals that the page read
  during render, so a 60 Hz meter update meant a 60 Hz full re-render of
  the page. The meters now write to the DOM through refs, as the bars and
  peak markers already did.

- **Icecast sources behind an HTTP reverse proxy** (#58). A source that
  frames its body properly (chunked, or a length) is now read through
  `r.Body` instead of the raw hijacked socket. Hijacking a framed request
  broadcast the chunk headers to listeners as if they were audio — a
  stream would begin `d08\r\n` instead of an MP3 frame sync. Direct
  encoders, which send no framing at all, keep the hijack path unchanged.

  A source that connects and then sends nothing for 10 s now logs a
  warning naming the likely cause. The classic Icecast source protocol
  has no `Content-Length` and no `Transfer-Encoding`, so an HTTP reverse
  proxy sees a bodiless request and forwards no body; that case cannot be
  rescued server-side, but it no longer looks like an unexplained dead
  mount. Point the encoder at TinyIce directly (it terminates TLS itself)
  or proxy that port at TCP/stream level.

- **The player's volume control was unreachable** (#53). The audio layout
  reserved no space for its own fixed bottom strip, so on shorter
  viewports the controls sat underneath it and the strip — being above
  them — swallowed every click and drag. `overflow-hidden` meant they
  could not be scrolled to either.

  The speaker icon is now a real mute toggle (it was decorative), the
  slider responds to touch drags and to arrow/Home/End keys, and it
  reports whole percentages instead of `74.59677419354838`.

- **AutoDJ pause then play skipped a track** (#54, a regression in
  2.7.0). Pause cancelled the current track, so resuming could only start
  the next one. It now suspends the encoder's reads and resumes the same
  file where it stopped. Reported position no longer counts paused time
  as playback progress.

- **A playing AutoDJ showed as offline in Streams** (#56). The status dot
  keys off the mount's source descriptor, which every ingest sets except
  the AutoDJ; it now identifies itself as `autodj` while playing and
  releases the mount when stopped.

### Changed

- **Dependencies updated** (the three open Dependabot PRs, rolled up):
  pion/webrtc 4.2.12 → 4.2.16 and its ice/dtls/srtp/sctp/stun/turn
  stack, coreos/go-oidc 3.18 → 3.20, go-webauthn 0.17.3 → 0.17.4,
  wneessen/go-mail 0.7.3 → 0.8.1, gorm 1.31.1 → 1.31.2, x/crypto
  0.51 → 0.54 plus x/sys, x/net and x/text; actions/checkout, setup-go
  and setup-node v6 → v7; alpine 3.23 → 3.24 in the runtime image.

- **`/admin/events` is roughly 40x smaller** (#55). The `autodj` event
  carried the entire playlist array on every tick — twice, counting the
  legacy unnamed frame — which on a 200-track AutoDJ was ~99% of all
  bytes on the feed (measured 126 KB/s, now 2.9 KB/s). It now sends
  `playlist_version`; clients refetch `/api/autodj/{mount}/playlist` when
  that number changes, which also picks up edits made elsewhere.

- **Dependencies updated** (Dependabot #59, #61): shine-mp3 0.1.0 → 0.2.0
  (needed a source change — see the bitrate-override test), coreos/go-oidc
  3.20 → 3.21, go-webauthn 0.17.4 → 0.18.0, mewkiz/flac 1.0.13 → 1.0.14,
  pion/webrtc 4.2.16 → 4.2.19 with its ice/dtls/srtp/sctp/stun/turn stack,
  x/crypto 0.54 → 0.55, x/net, x/text; the build image moves to
  golang:1.27-alpine.

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

[2.10.1]: https://github.com/DatanoiseTV/tinyice/releases/tag/v2.10.1
[2.10.0]: https://github.com/DatanoiseTV/tinyice/releases/tag/v2.10.0
[2.9.0]: https://github.com/DatanoiseTV/tinyice/releases/tag/v2.9.0
[2.8.2]: https://github.com/DatanoiseTV/tinyice/releases/tag/v2.8.2
[2.8.1]: https://github.com/DatanoiseTV/tinyice/releases/tag/v2.8.1
[2.8.0]: https://github.com/DatanoiseTV/tinyice/releases/tag/v2.8.0
[2.7.0]: https://github.com/DatanoiseTV/tinyice/releases/tag/v2.7.0
[2.5.0]: https://github.com/DatanoiseTV/tinyice/releases/tag/v2.5.0
