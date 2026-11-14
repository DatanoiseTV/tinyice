/** Drag payload types shared by the library (source) and the playlist (target and source). */
export const ROWS_MIME = 'application/x-tinyice-rows' // JSON number[]: playlist entry ids being reordered
export const FILES_MIME = 'application/x-tinyice-files' // JSON string[]: library paths relative to the music dir

/**
 * Turn a drag of `group` (items in list order) to the gap `insertAt` (0..n, in
 * pre-move indices) into the sequence of (from, to) calls the server understands.
 * Its reorder removes the item at `from`, then inserts it so it ends at index `to`.
 */
export function planMoves(order: number[], group: number[], insertAt: number): { moves: [number, number][]; result: number[] } {
  const inGroup = new Set(group)
  const idxs = order.map((id, i) => (inGroup.has(id) ? i : -1)).filter((i) => i >= 0)
  if (idxs.length === 0) return { moves: [], result: order }
  if (idxs.length === 1) {
    const from = idxs[0]
    const to = insertAt > from ? insertAt - 1 : insertAt
    if (to === from) return { moves: [], result: order }
    const result = order.slice()
    result.splice(to, 0, ...result.splice(from, 1))
    return { moves: [[from, to]], result }
  }
  const rest = order.filter((id) => !inGroup.has(id))
  const p = insertAt - idxs.filter((i) => i < insertAt).length
  const picked = idxs.map((i) => order[i])
  const target = [...rest.slice(0, p), ...picked, ...rest.slice(p)]
  const cur = order.slice()
  const moves: [number, number][] = []
  for (let i = 0; i < target.length; i++) {
    if (cur[i] === target[i]) continue
    const j = cur.indexOf(target[i])
    cur.splice(i, 0, ...cur.splice(j, 1))
    moves.push([j, i])
  }
  return { moves, result: target }
}
