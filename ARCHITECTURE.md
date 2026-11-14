# TinyIce Architecture & Technical Documentation

> **For AI Agents & LLMs:** This document is your primary source of truth for understanding the system's design philosophy, concurrency models, and architectural boundaries. Read this before suggesting large-scale refactors.

## 1. Project Vision
**TinyIce** is a modern, standalone audio streaming server compatible with the Icecast protocol.
*   **Goal:** Provide a "single binary" radio station solution (Server + AutoDJ + Transcoder + SSL).
*   **Philosophy:** Minimal external dependencies (Pure Go preferred), zero-allocation broadcasting paths, and high concurrency.
*   **Aesthetic:** A flat, broadcast-console look in dark and light themes: etched mono legends, tally lamps, tabular numerals. React and Tailwind, built to static assets and embedded in the binary (design rules in `server/frontend/DESIGN.md`).

## 2. Core Subsystems

### A. The Relay Engine (`relay/`)
The heart of TinyIce is the **Relay**. It manages the lifecycle of streams (`relay/stream.go`) and listeners.
*   **Circular Buffers:** Every stream uses a fixed-size `CircularBuffer` (`relay/relay.go`). This allows new listeners to receive an "instant burst" of audio to fill their client-side buffers immediately, minimizing startup latency.
*   **Concurrency:**
    *   **Global Lock:** `Relay` uses a `sync.RWMutex` to manage the map of streams.
    *   **Stream Lock:** Each `Stream` has its own `sync.RWMutex` to protect metadata and listener maps.
    *   **Atomic Stats:** Bandwidth (`BytesIn`, `BytesOut`) is tracked via `sync/atomic` for performance.

### B. AutoDJ & Streamer (`relay/streamer.go`)
The **Streamer** is an internal audio source that behaves like an external source client.
*   **Decoding:** Uses `hajimehoshi/go-mp3` for decoding.
*   **Encoding:** Uses a custom native implementation (`relay/transcode.go`) to re-encode audio into chunks suitable for streaming (MP3/Opus).
*   **Pacing:** Crucial. The streamer must manually pace itself (sleep) to match the playback duration of the audio frames, otherwise, it would flood the buffer.
*   **Metadata:** ID3 tags are extracted using `bogem/id3v2` (pure Go).

### C. Database & Persistence (`relay/history.go`)
Since v0.9.3-exp, TinyIce uses **GORM** with a non-CGO SQLite driver (`github.com/glebarez/sqlite`) for data persistence.
*   **Decoupled Driver:** The non-CGO driver ensures "single binary" portability without requiring a GCC toolchain.
*   **Schema Migration:** Automated via GORM's `AutoMigrate`.
*   **History Tracking:** All track plays and listener statistics are archived in `history.db`.

### D. Observability & Monitoring (`server/handlers_api.go`)
TinyIce exposes a secured **Prometheus** exporter at `/metrics`.
*   **Basic Auth:** The exporter is secured using the primary admin credentials.
*   **Metrics:** Provides real-time gauges for memory usage, goroutines, GC cycles, and per-stream bandwidth/health.
*   **Infrastructure:** Native support for Grafana via bundled dashboard templates in the `monitoring/` directory.

### E. Networking & Security (`server/`)
*   **Dual Stack:** Binds to both IPv4 and IPv6.
*   **Hot Swap (`SO_REUSEPORT`):** Allows a new process to bind to the *same* port while the old one is still running. The old process hands off duties and shuts down gracefully (`server/server.go` -> `HotSwap()`).
*   **IP Access Control:**
    *   **Banning:** Dropping connections from known malicious IPs at the `Accept()` level.
    *   **Whitelisting:** Bypassing security checks for trusted IPs (e.g., local admin, monitoring nodes).
    *   **404 Scanning Protection:** Automatic temporary lockout for IPs performing path scanning.

### D. Web Interface (`server/templates/`)
*   **Technology:** Server-Side Rendered (SSR) Go templates + Vanilla JS.
*   **Real-time:** Uses **Server-Sent Events (SSE)** (`/admin/events`) to push JSON state updates (listeners, current song, VU meters) to the frontend.
*   **Studio UI:** A specific focus on "app-like" behavior using AJAX forms (`submitForm`) to avoid full page reloads during broadcast operations.
*   **Go Live Studio:** Enables direct browser-to-server streaming using WebAudio API.
    *   **WebRTC Mode:** Uses `pion/webrtc` for ultra-low latency Opus streaming.
    *   **HTTP Fallback:** Uses `MediaRecorder` to stream chunks via POST requests.

## 3. Directory Structure

```text
tinyice/
├── main.go                 # Entry point. Flags, config loading, and Updater initialization.
├── config/                 # JSON configuration struct and defaults.
├── monitoring/             # Observability assets (Grafana dashboards, Prometheus configs).
├── relay/                  # AUDIO CORE.
│   ├── buffer.go           # CircularBuffer implementation for stream data.
│   ├── stream.go           # Stream struct and core stream operations.
│   ├── stats.go            # Stream statistics, health monitoring, and snapshots.
│   ├── ogg.go              # Ogg/Opus specific functionality and synchronization.
│   ├── io.go               # Reusable I/O components (StreamWriter, StreamReader).
│   ├── interfaces.go       # Core interfaces for better abstraction and testability.
│   ├── relay.go            # Core Relay management and stream coordination.
│   ├── streamer.go         # AutoDJ logic (playlist, queue, playback loop).
│   ├── transcode.go        # Native MP3/Opus encoding logic.
│   ├── mpd.go              # Minimal implementation of MPD protocol.
│   ├── webrtc.go           # WebRTC PeerConnection and Source management.
│   ├── client.go           # Logic for pulling external relay streams.
│   └── history.go          # GORM Models and database logic.
├── server/                 # HTTP/TCP LAYER.
│   ├── server.go           # Server initialization and routing logic.
│   ├── auth.go             # Authentication, sessions, and IP security.
│   ├── listener.go         # Streaming listener lifecycle management.
│   ├── handlers_*.go       # Domain-specific HTTP handlers (admin, api, player, etc).
│   ├── tasks.go            # Background tasks (reporting, stats recording).
│   ├── socket_*.go         # OS-specific socket syscalls (SO_REUSEPORT).
│   └── templates/          # HTML/CSS/JS assets.
```

## 4. Key Data Flows

### A. AutoDJ Playback
1.  `StreamerManager` selects a file from `Playlist` or `Queue`.
2.  File is decoded to PCM.
3.  Transcoder (`EncodeMP3`/`EncodeOpus`) converts PCM to streaming chunks.
4.  Chunks are written to the `Stream.Buffer`.
5.  `Stream.Broadcast` signals all waiting Listener goroutines via channels.

### B. WebRTC Source (Go Live)
1.  Browser captures microphone via `getUserMedia`.
2.  Opus packets are sent via WebRTC `TrackRemote`.
3.  `WebRTCManager` receives RTP packets and uses `oggwriter` to mux them into Ogg pages.
4.  The resulting Ogg data is written directly to the `Stream.Buffer` for distribution.

### C. Listener Connection
1.  `handleListener` (`server/server.go`) accepts HTTP request.
2.  `Stream.Subscribe` registers the listener and returns a buffer offset (rewound by `burst_size`).
3.  The listener loop reads from the `CircularBuffer` starting at that offset.
4.  Loop waits on a signal channel for new data to arrive.

## 5. Future Roadmap & Missing Features

If you are an AI agent looking to contribute, focus on these areas:

### High Priority (functionality)
1.  **Scheduler:** The AutoDJ needs a time-based scheduler (e.g., "Play 'Jazz' playlist every Friday at 8 PM").
2.  **Jingles/Sweepers:** A mechanism to inject short audio files (station IDs) between tracks or every $N$ songs.
3.  **Live Mic Injection:** Integrate `pion/webrtc` more deeply to allow "Go Live" directly from the browser (WebRTC -> Opus -> Internal Buffer mix).
4.  **S3/MinIO Support:** Allow the AutoDJ to stream files directly from object storage instead of the local filesystem (cloud-native scaling).

### Medium Priority (UX/Polish)
1.  **Visualizer:** A server-side or client-side FFT visualizer in the Studio.
2.  **Request System:** A public-facing widget where listeners can request songs from the library (with rate limiting).
3.  **OIDC/OAuth:** Support for logging in via GitHub/Google/OIDC instead of just Basic Auth/Cookies.

### Architecture Improvements
1.  **HLS/DASH Support:** Currently only supports progressive HTTP (Icecast). Adding HLS would improve mobile compatibility.
2.  **Clustering:** Allow multiple TinyIce nodes to share state/streams (Relay-chaining is already supported, but shared state is not).
