import type { SystemInfo } from './api'

const UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']

export function bytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '0 B'
  const i = Math.min(UNITS.length - 1, Math.floor(Math.log(n) / Math.log(1024)))
  const v = n / 1024 ** i
  return `${i === 0 || v >= 100 ? Math.round(v) : v.toFixed(1)} ${UNITS[i]}`
}

export const rate = (n: number) => `${bytes(n)}/s`

export function pct(n: number): string {
  if (!Number.isFinite(n)) return '–'
  return n > 0 && n < 10 ? `${n.toFixed(1)}%` : `${Math.round(n)}%`
}

/** Percentage, or NaN when there is no capacity to compare against (shown as "–"). */
export function ratio(used: number, total: number): number {
  return total > 0 ? (used / total) * 100 : NaN
}

export function duration(seconds: number): string {
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m`
}

export function ago(unix: number): string {
  if (!unix) return 'never'
  const s = Math.max(0, Date.now() / 1000 - unix)
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.floor(s / 60)} min ago`
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`
  return new Date(unix * 1000).toLocaleDateString()
}

export function dateTime(unix: number): string {
  return new Date(unix * 1000).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })
}

export function osLabel(info: SystemInfo): string {
  switch (info.os) {
    case 'darwin':
      return `macOS ${info.platform_version}`.trim()
    case 'windows':
      return info.platform.replace(/^Microsoft\s+/, '') || 'Windows'
    case 'linux': {
      const name = info.platform ? info.platform[0].toUpperCase() + info.platform.slice(1) : 'Linux'
      return `${name} ${info.platform_version}`.trim()
    }
    default:
      return info.os || 'Unknown OS'
  }
}

export function archLabel(arch: string): string {
  return { amd64: 'x86-64', arm64: 'ARM64' }[arch] ?? arch
}

/** Meter severity: accent below 70 %, warning below 90 %, critical above. */
export function severity(percent: number): string {
  if (percent >= 90) return 'var(--critical)'
  if (percent >= 70) return 'var(--warning)'
  return 'var(--accent)'
}
