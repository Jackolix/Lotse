import type { ScriptShell, System, TargetStatus } from './api'
import { canShell } from './systemActions'

export const shells: { shell: ScriptShell; label: string; os: string }[] = [
  { shell: 'sh', label: 'sh', os: 'Linux, macOS' },
  { shell: 'bash', label: 'Bash', os: 'Linux, macOS' },
  { shell: 'powershell', label: 'PowerShell', os: 'Windows (and pwsh elsewhere)' },
  { shell: 'cmd', label: 'cmd', os: 'Windows' },
]

export const shellLabel = (s: ScriptShell) => shells.find((x) => x.shell === s)?.label ?? s

/** Why a script cannot run on a system right now, or '' if it can. Mirrors the hub. */
export function cannotRun(s: System, shell: ScriptShell): string {
  if (!s.online) return 'offline'
  if (!canShell(s)) return 'no --allow-shell'
  if ((shell === 'sh' || shell === 'bash') && s.info.os === 'windows') return 'Windows'
  if (shell === 'cmd' && s.info.os !== 'windows') return 'not Windows'
  return ''
}

export const statusLabel: Record<TargetStatus, string> = {
  pending: 'Waiting',
  running: 'Running',
  done: 'Succeeded',
  failed: 'Failed',
  skipped: 'Skipped',
  canceled: 'Stopped',
}

export const statusColor: Record<TargetStatus, string> = {
  pending: 'var(--muted)',
  running: 'var(--accent)',
  done: 'var(--good)',
  failed: 'var(--critical)',
  skipped: 'var(--warning)',
  canceled: 'var(--muted)',
}

export const timeouts = [10, 30, 60, 300, 600, 1800, 3600, 3 * 3600, 12 * 3600]
