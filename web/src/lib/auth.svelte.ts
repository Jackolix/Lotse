import { api, setUnauthorizedHandler, type User } from './api'
import { systems } from './systems.svelte'

export const auth = $state({
  checked: false,
  user: null as User | null,
  setupNeeded: false,
})

export async function checkAuth() {
  try {
    auth.user = await api.me()
  } catch {
    auth.user = null
    try {
      auth.setupNeeded = (await api.setupNeeded()).needed
    } catch {
      // hub unreachable; the login form will report errors
    }
  }
  auth.checked = true
}

export function signedIn(user: User) {
  auth.user = user
  auth.setupNeeded = false
}

export async function signOut() {
  try {
    await api.logout()
  } finally {
    systems.stop()
    auth.user = null
  }
}

setUnauthorizedHandler(() => {
  systems.stop()
  auth.user = null
})
