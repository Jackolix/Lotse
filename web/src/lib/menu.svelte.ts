import type { IconName } from './components/Icon.svelte'

export interface MenuItem {
  label: string
  icon?: IconName
  action?: () => void
  disabled?: boolean
  /** Explains why an item is disabled. */
  hint?: string
  danger?: boolean
}

export type MenuEntry = MenuItem | 'separator'

/** The one context menu of the app, rendered by <ContextMenu /> in App.svelte. */
export const menu = $state({
  open: false,
  x: 0,
  y: 0,
  items: [] as MenuEntry[],
})

let returnFocus: HTMLElement | null = null

/**
 * Use as an oncontextmenu handler, or as onclick of a button that opens a menu. Also
 * works for the keyboard (Menu key, Shift+F10).
 */
export function openMenu(e: MouseEvent, items: MenuEntry[]) {
  e.preventDefault()
  e.stopPropagation()
  let { clientX: x, clientY: y } = e
  const target = e.currentTarget
  if (e.type === 'click' && target instanceof HTMLElement) {
    // A menu button: drop down below it.
    const r = target.getBoundingClientRect()
    x = r.left
    y = r.bottom + 4
  } else if (x === 0 && y === 0 && target instanceof HTMLElement) {
    // Opened from the keyboard: anchor to the element instead of the pointer.
    const r = target.getBoundingClientRect()
    x = r.left + 16
    y = r.top + Math.min(r.height, 32)
  }
  returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  menu.x = x
  menu.y = y
  menu.items = items
  menu.open = true
}

export function closeMenu(restoreFocus = true) {
  if (!menu.open) return
  menu.open = false
  if (restoreFocus) returnFocus?.focus()
  returnFocus = null
}
