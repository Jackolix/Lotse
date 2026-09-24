export type ToastKind = 'success' | 'error' | 'info'

export interface Toast {
  id: number
  message: string
  kind: ToastKind
}

/** Short notices in the corner, rendered by <Toaster /> in App.svelte. */
export const toasts = $state<Toast[]>([])

let nextId = 1

export function toast(message: string, kind: ToastKind = 'info', ms = kind === 'error' ? 7000 : 4000) {
  const id = nextId++
  toasts.push({ id, message, kind })
  setTimeout(() => dismiss(id), ms)
}

export function dismiss(id: number) {
  const i = toasts.findIndex((t) => t.id === id)
  if (i >= 0) toasts.splice(i, 1)
}
