import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'

/**
 * Fixed-row windowing for long lists (a library can hold thousands of tracks).
 * Rows are absolutely sized by `rowH`, so the caller renders only [start, end)
 * between two spacers and keeps scroll height exact.
 */
export function useVirtual(count: number, rowH: number, overscan = 8) {
  const ref = useRef<HTMLDivElement>(null)
  const [range, setRange] = useState({ start: 0, end: Math.min(count, 40) })

  const update = useCallback(() => {
    const el = ref.current
    if (!el) return
    const vh = el.clientHeight || 480
    const start = Math.max(0, Math.floor(el.scrollTop / rowH) - overscan)
    const end = Math.min(count, Math.ceil((el.scrollTop + vh) / rowH) + overscan)
    setRange((r) => (r.start === start && r.end === end ? r : { start, end }))
  }, [count, rowH, overscan])

  useLayoutEffect(update, [update])
  useEffect(() => {
    const el = ref.current
    if (!el || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(update)
    ro.observe(el)
    return () => ro.disconnect()
  }, [update])

  /** Scroll just far enough to bring row `i` fully into view. */
  const reveal = useCallback((i: number, center = false) => {
    const el = ref.current
    if (!el) return
    const top = i * rowH
    if (center) { el.scrollTop = Math.max(0, top - el.clientHeight / 2 + rowH / 2); return }
    if (top < el.scrollTop) el.scrollTop = top
    else if (top + rowH > el.scrollTop + el.clientHeight) el.scrollTop = top + rowH - el.clientHeight
  }, [rowH])

  const end = Math.min(range.end, count)
  const start = Math.min(range.start, end)
  return { ref, start, end, padTop: start * rowH, padBottom: Math.max(0, (count - end) * rowH), onScroll: update, reveal }
}
