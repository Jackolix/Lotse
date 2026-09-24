// Theme follows the OS unless the user picks one. `version` bumps on every change
// so charts (which bake colors into canvas) know to redraw.

export type ThemeMode = 'system' | 'light' | 'dark'

function stored(): ThemeMode {
  try {
    const v = localStorage.getItem('theme')
    if (v === 'light' || v === 'dark') return v
  } catch {
    // storage unavailable
  }
  return 'system'
}

export const theme = $state({ mode: stored(), version: 0 })

function apply() {
  if (theme.mode === 'system') delete document.documentElement.dataset.theme
  else document.documentElement.dataset.theme = theme.mode
  theme.version++
}

export function setTheme(mode: ThemeMode) {
  theme.mode = mode
  try {
    if (mode === 'system') localStorage.removeItem('theme')
    else localStorage.setItem('theme', mode)
  } catch {
    // storage unavailable
  }
  apply()
}

matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => theme.version++)
apply()

export function cssVar(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}
