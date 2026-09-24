// Promise-based replacements for window.prompt and window.confirm, rendered by
// <Dialogs /> in App.svelte.

interface DialogState {
  open: boolean
  title: string
  message: string
  input: boolean
  value: string
  confirmLabel: string
  danger: boolean
}

export const dialog = $state<DialogState>({
  open: false,
  title: '',
  message: '',
  input: false,
  value: '',
  confirmLabel: 'OK',
  danger: false,
})

let resolver: ((value: string | null) => void) | null = null

function show(state: Omit<DialogState, 'open'>): Promise<string | null> {
  resolver?.(null) // a newer dialog replaces an unanswered one
  Object.assign(dialog, state, { open: true })
  return new Promise((resolve) => (resolver = resolve))
}

/** Settles the open dialog: a string confirms, null cancels. */
export function settle(value: string | null) {
  dialog.open = false
  resolver?.(value)
  resolver = null
}

export function ask(opts: { title: string; message?: string; value?: string; confirmLabel?: string }) {
  return show({
    title: opts.title,
    message: opts.message ?? '',
    input: true,
    value: opts.value ?? '',
    confirmLabel: opts.confirmLabel ?? 'OK',
    danger: false,
  })
}

export async function confirmAction(opts: { title: string; message: string; confirmLabel: string; danger?: boolean }) {
  const result = await show({
    title: opts.title,
    message: opts.message,
    input: false,
    value: '',
    confirmLabel: opts.confirmLabel,
    danger: opts.danger ?? false,
  })
  return result !== null
}
