import { useSyncExternalStore } from 'react'

export type ToastKind = 'success' | 'error' | 'info'
export interface ToastItem { id: number; kind: ToastKind; message: string }

let items: ToastItem[] = []
let nextId = 1
const subs = new Set<() => void>()
const emit = () => subs.forEach((f) => f())

function push(kind: ToastKind, message: string) {
  const id = nextId++
  items = [...items, { id, kind, message }].slice(-4)
  emit()
  setTimeout(() => dismiss(id), kind === 'error' ? 7000 : 3500)
}
export function dismiss(id: number) {
  items = items.filter((t) => t.id !== id)
  emit()
}
export const toast = {
  success: (m: string) => push('success', m),
  error: (m: string) => push('error', m),
  info: (m: string) => push('info', m),
}
export function useToasts(): ToastItem[] {
  return useSyncExternalStore((cb) => { subs.add(cb); return () => subs.delete(cb) }, () => items)
}
