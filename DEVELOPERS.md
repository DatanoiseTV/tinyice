# Developer Guide

Welcome to the TinyIce codebase! This document provides an architectural overview and technical deep-dive to help you understand how TinyIce works and how to contribute effectively.

## Tech Stack

- **Language**: Go 1.21+
- **Database**: SQLite (via `modernc.org/sqlite` pure-Go driver) for persistent song history.
- **Frontend**: React 19, Tailwind 4 and Radix primitives, built with Vite into `server/frontend/dist` and embedded via `embed.FS` (see `server/frontend/DESIGN.md`).
- **Real-time**: Server-Sent Events (SSE) for dashboard metrics.
- **Security**: Argon2/Bcrypt for password hashing, TLS via ACME (autocert).

## Core Architecture

TinyIce is built around a central **Pub/Sub** engine located in the `relay/` package. Unlike traditional servers that use individual channels per listener, TinyIce utilizes a **Shared Circular Buffer** architecture. 

### Codebase Map
...
- `relay/`:
    - `relay.go`: The heart of the server. Manages `Stream` objects and the `CircularBuffer`.
...

## Performance & Resource Consumption

TinyIce is designed for high-density streaming. Below are approximate resource requirements based on current architecture:

### Memory Usage (RAM)
- **Base Footprint**: ~15MB (Static binary + embedded templates + SQLite overhead).
- **Per Listener Connection**: **~15KB to 30KB**.
    - Since transitioning to the **Shared Circular Buffer**, per-listener overhead is minimized to the TCP connection state and a small signal channel.
    - 10,000 listeners $\approx$ 250MB RAM.
    - 100,000 listeners $\approx$ 2.5GB RAM.
- **Per Stream Mount**: **~512KB** (Shared pre-allocated circular buffer).

### CPU Usage
- **Broadcasting**: O(1) for data ingestion, O(N) for signaling listeners.
- **Efficiency**: Signaling 100,000 listeners is an extremely fast bit-mask/channel operation in Go, typically consuming less than 5% CPU on modern multi-core systems.

## The Streaming Flow

1.  **Source Connection**: A streamer (BUTT, OBS) sends a `SOURCE` or `PUT` request.
2.  **Hijacking**: The server hijacks the TCP connection (`http.Hijacker`) to provide a raw, low-latency pipe for binary data.
3.  **Broadcasting**: Data chunks are written to a stream-specific `CircularBuffer`.
4.  **Zero-Allocation Distribution**: Listeners subscribe by maintaining an offset into the shared buffer. 
    -   On connection, listeners receive a **64KB "Instant Start" burst** from the buffer history.
    -   A signal channel triggers a read loop that pumps data directly from the shared memory to the network socket.

## Zero-Downtime Updates

TinyIce supports zero-downtime binary updates and configuration reloading:

### 1. Binary Updates (`SO_REUSEPORT`)
The server uses `SO_REUSEADDR` and `SO_REUSEPORT` on its listening sockets. This allows you to start a new instance of TinyIce while the old one is still running. Both processes will temporarily share the incoming traffic. Once the new process is ready, send a `SIGTERM` to the old one to trigger a **Graceful Shutdown**.

### 2. Config Reloading (`SIGHUP`)
Send a `SIGHUP` signal to the running TinyIce process to reload the `tinyice.json` configuration from disk without dropping any active listeners. This will:
- Re-read all mount settings and passwords.
- Re-sync Edge Relays (starting new ones or stopping deleted ones).
- Update the public UI settings.

## Key Technical Decisions

### 1. Pure Go SQLite
We use `github.com/glebarez/go-sqlite` because it is a CGO-free port of SQLite. This allows us to maintain "Zero-Config" portability, meaning you can cross-compile TinyIce for Linux, Windows, or FreeBSD from any OS without needing a GCC toolchain.

### 2. Intelligent Routing
TinyIce uses a custom `Mux` logic to handle dual-protocols. It automatically redirects web browsers to HTTPS (if configured) while allowing legacy hardware encoders (which often don't support TLS) to remain on plain HTTP.

### 3. SSE for Metrics
Instead of polling a JSON API every second, the dashboard uses a single long-lived **Server-Sent Events** connection. This reduces CPU usage and allows for the 500ms "superfast" dashboard response time.

## Development Workflow

### Prerequisites
- Go 1.21 or later.
- `make` (optional).

### Running in Development
```bash
# Clean start (deletes previous config/history)
rm -f tinyice.json history.db && go run main.go
```

### Adding New Features
1.  **Backend**: Add logic to `relay/` or `config/` first.
2.  **API**: Expose the logic via a new handler in `server/server.go`.
3.  **UI**: Add a new tab or element in `server/templates/admin.html` and update the SSE `onmessage` handler if real-time data is needed.

## Performance Tuning

When testing high-load scenarios:
- Monitor **Goroutine counts**: Each listener uses exactly one goroutine.
- Check **Memory usage**: Go channels use ~400KB per listener. 10,000 listeners will require ~4GB of RAM.
- Use the **Low Latency Mode**: Toggle this in the Admin panel to disable `X-Accel-Buffering` and other HTTP-level caching.

## Contributing

1.  Keep it **CGO-free**.
2.  Ensure templates remain **self-contained** (no external CDNs).
3.  Update `DEVELOPERS.md` if you change core architectural patterns.

---
Developed by [DatanoiseTV](https://github.com/DatanoiseTV)
