// Actions on a system, shared by context menus and the detail page.
import { api, ApiError, type System } from './api'
import { can } from './auth.svelte'
import { copyText } from './clipboard'
import { ask, confirmAction } from './dialogs.svelte'
import type { MenuEntry } from './menu.svelte'
import { withReauth } from './reauth.svelte'
import { navigate } from './router.svelte'
import { systems } from './systems.svelte'
import { toast } from './toast.svelte'

export const errorText = (err: unknown) => (err instanceof ApiError ? err.message : 'Cannot reach the hub.')

/** Hint for actions that need the agent installed with --allow-shell. */
export const needsAllowShell = 'Needs the agent installed with --allow-shell'

export const macsOf = (s: System) => [...new Set((s.info.interfaces ?? []).map((i) => i.mac))]

export const ipv4Of = (s: System) =>
  (s.info.interfaces ?? [])
    .flatMap((i) => i.addrs)
    .find((a) => /^\d+\.\d+\.\d+\.\d+\//.test(a))
    ?.split('/')[0]

/** Remote control (shell, files, scripts, processes, services, power) is allowed by the agent. */
export const canShell = (s: System) => s.online && s.features.includes('shell')

export async function powerSystem(s: System, action: 'reboot' | 'shutdown') {
  const reboot = action === 'reboot'
  const ok = await confirmAction({
    title: `${reboot ? 'Reboot' : 'Shut down'} ${s.name}?`,
    message: reboot
      ? 'Running programs are closed without asking. The machine should be back within a few minutes.'
      : `Running programs are closed without asking. ${s.name} stays off until someone turns it on${macsOf(s).length ? ' or wakes it' : ''}.`,
    confirmLabel: reboot ? 'Reboot' : 'Shut down',
    danger: true,
  })
  if (!ok) return
  try {
    const done = await withReauth(`${reboot ? 'Rebooting' : 'Shutting down'} ${s.name} needs your password again.`, async () => {
      await api.power(s.id, action)
      return true
    })
    if (done) toast(`${s.name} is ${reboot ? 'rebooting' : 'shutting down'}`, 'success')
  } catch (err) {
    toast(errorText(err), 'error')
  }
}

export async function updateAgent(s: System): Promise<boolean> {
  try {
    const r = await api.updateAgent(s.id)
    toast(`${s.name} updated to ${r.version}; the agent restarts now`, 'success', 6000)
    return true
  } catch (err) {
    toast(`${s.name}: ${errorText(err)}`, 'error')
    return false
  }
}

export async function copy(text: string, what: string) {
  if (await copyText(text)) toast(`${what} copied`, 'success', 2500)
  else toast(`Could not copy the ${what.toLowerCase()}`, 'error')
}

export async function wakeSystem(s: System) {
  try {
    const r = await api.wake(s.id)
    if (r.via === 'hub') {
      toast(
        `Magic packet for ${s.name} sent by the hub. No online agent shares its network, so it only arrives if the hub uses host networking.`,
        'info',
        9000,
      )
    } else {
      toast(`Magic packet for ${s.name} sent via ${r.via}. It should come online within a minute.`, 'success', 6000)
    }
  } catch (err) {
    toast(errorText(err), 'error')
  }
}

export async function renameSystem(s: System) {
  const name = await ask({ title: 'Rename system', value: s.name, confirmLabel: 'Rename' })
  if (!name || name === s.name) return
  try {
    await api.rename(s.id, name)
    await systems.refresh()
    toast(`Renamed to ${name}`, 'success', 2500)
  } catch (err) {
    toast(errorText(err), 'error')
  }
}

/** Returns true when the system was deleted. */
export async function deleteSystem(s: System): Promise<boolean> {
  const ok = await confirmAction({
    title: `Delete ${s.name}?`,
    message: 'Its history is removed. The agent can only reconnect after it is enrolled again.',
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok) return false
  try {
    await api.remove(s.id)
    await systems.refresh()
    toast(`${s.name} deleted`, 'success', 2500)
    return true
  } catch (err) {
    toast(errorText(err), 'error')
    return false
  }
}

export function systemMenu(s: System): MenuEntry[] {
  const ip = ipv4Of(s)
  const items: MenuEntry[] = [{ label: 'Open', icon: 'server', action: () => navigate(`/systems/${s.id}`) }]
  if (s.online && can('operator')) {
    items.push(
      {
        label: 'Open terminal',
        icon: 'terminal',
        disabled: !canShell(s),
        hint: needsAllowShell,
        action: () => navigate(`/systems/${s.id}/terminal`),
      },
      {
        label: 'Browse files',
        icon: 'folder',
        disabled: !canShell(s),
        hint: needsAllowShell,
        action: () => navigate(`/systems/${s.id}/files`),
      },
      {
        label: 'Run a script…',
        icon: 'code',
        disabled: !canShell(s),
        hint: needsAllowShell,
        action: () => navigate(`/scripts?system=${s.id}`),
      },
    )
  } else if (!s.online && can('operator')) {
    items.push({
      label: 'Wake',
      icon: 'power',
      disabled: macsOf(s).length === 0,
      hint: 'No MAC address known for this machine yet',
      action: () => wakeSystem(s),
    })
  }
  items.push('separator', { label: 'Copy hostname', icon: 'copy', action: () => copy(s.info.hostname, 'Hostname') })
  if (ip) items.push({ label: `Copy IP address (${ip})`, icon: 'copy', action: () => copy(ip, 'IP address') })
  if (s.online && can('operator')) {
    items.push(
      'separator',
      { label: 'Reboot…', icon: 'refresh', disabled: !canShell(s), hint: needsAllowShell, action: () => powerSystem(s, 'reboot') },
      { label: 'Shut down…', icon: 'power', disabled: !canShell(s), hint: needsAllowShell, action: () => powerSystem(s, 'shutdown') },
    )
  }
  if (s.update && can('admin')) {
    items.push({ label: `Update agent to ${s.update}`, icon: 'package', action: () => updateAgent(s) })
  }
  if (can('operator')) {
    items.push('separator', { label: 'Rename…', icon: 'pencil', action: () => renameSystem(s) })
  }
  if (can('admin')) {
    items.push({ label: 'Delete…', icon: 'trash', danger: true, action: () => deleteSystem(s) })
  }
  return items
}
