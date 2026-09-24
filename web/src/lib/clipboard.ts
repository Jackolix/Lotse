/** Copies text. navigator.clipboard only exists on https or localhost, so on a
 *  plain-http LAN address this falls back to the older execCommand path. */
export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.append(ta)
    ta.select()
    const ok = document.execCommand('copy')
    ta.remove()
    return ok
  }
}

/** Reading the clipboard needs a secure context (https or localhost). */
export const canReadClipboard = () => window.isSecureContext && !!navigator.clipboard?.readText
