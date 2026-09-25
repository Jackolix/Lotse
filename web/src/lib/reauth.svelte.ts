// A promise-based re-authentication prompt, rendered by <ReauthDialog> in App.svelte.
// Sensitive actions need a password (or passkey) confirmation within the last 10
// minutes; the hub answers 403 with reauth_required otherwise.
import { ApiError } from './api'
import { auth } from './auth.svelte'

export const reauth = $state({ open: false, reason: '' })

let resolver: ((ok: boolean) => void) | null = null

/** Asks for the password again. Resolves true once the hub has confirmed it. */
export function confirmIdentity(reason: string): Promise<boolean> {
  resolver?.(false)
  reauth.reason = reason
  reauth.open = true
  return new Promise((resolve) => (resolver = resolve))
}

export function settleReauth(ok: boolean) {
  reauth.open = false
  resolver?.(ok)
  resolver = null
}

/** Whether a sensitive action would currently go through without asking. */
export const isElevated = () => (auth.user?.elevated_until ?? 0) > Date.now() / 1000 + 5

/**
 * Runs fn; if the hub wants a fresh confirmation, asks for it and runs fn again.
 * Returns undefined when the user cancels.
 */
export async function withReauth<T>(reason: string, fn: () => Promise<T>): Promise<T | undefined> {
  try {
    return await fn()
  } catch (err) {
    if (!(err instanceof ApiError && err.body.reauth_required)) throw err
    if (!(await confirmIdentity(reason))) return undefined
    return await fn()
  }
}

/** Makes sure the session is confirmed before an action that cannot retry (downloads). */
export async function ensureElevated(reason: string): Promise<boolean> {
  return isElevated() || confirmIdentity(reason)
}
