// The SSE feeds send one `stream` event per LIVE mount on every tick and
// nothing when a mount goes away. Pages that accumulated those events
// therefore kept showing a stream whose source had disconnected until the
// page was reloaded. Expiry closes that: anything not seen for a few
// ticks is gone.
//
// Ticks are 500 ms, so 5 s is ten missed ticks — long enough to ride out
// a slow tick or a brief reconnect, short enough that a stopped stream
// disappears while the operator is still looking at it.
export const STREAM_TTL_MS = 5000

export class LiveStreamTracker<T extends { mount: string }> {
  private lastSeen = new Map<string, number>()

  /** Record an event and return the list with expired mounts dropped. */
  upsert(current: T[], incoming: T): T[] {
    const now = Date.now()
    this.lastSeen.set(incoming.mount, now)
    const merged = [...current.filter((s) => s.mount !== incoming.mount), incoming]
    return this.prune(merged, now)
  }

  /** Drop mounts not seen within the TTL. */
  prune(list: T[], now = Date.now()): T[] {
    return list
      .filter((s) => {
        const seen = this.lastSeen.get(s.mount)
        // Entries that arrived with the page (never via an event) have no
        // timestamp; give them one so they expire like the rest rather
        // than living forever.
        if (seen === undefined) {
          this.lastSeen.set(s.mount, now)
          return true
        }
        return now - seen < STREAM_TTL_MS
      })
      .sort((a, b) => a.mount.localeCompare(b.mount))
  }

  forget(mount: string) {
    this.lastSeen.delete(mount)
  }
}
