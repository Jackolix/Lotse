// Typed client for the hub API. Types mirror internal/protocol and internal/hub.

export interface NetInterface {
  name: string
  mac: string
  addrs: string[]
}

export interface SystemInfo {
  hostname: string
  os: string
  platform: string
  platform_version: string
  kernel: string
  arch: string
  cpu_model: string
  cores: number
  mem_total: number
  interfaces?: NetInterface[]
}

export interface Filesystem {
  mount: string
  device: string
  type: string
  used: number
  total: number
}

export interface Metrics {
  cpu: number
  mem_used: number
  mem_total: number
  swap_used: number
  swap_total: number
  disk_used: number
  disk_total: number
  disk_read: number
  disk_write: number
  net_rx: number
  net_tx: number
  load1: number
  load5: number
  load15: number
  uptime: number
  fs?: Filesystem[]
}

export type MetricKey = Exclude<keyof Metrics, 'uptime' | 'fs'>

export interface System {
  id: number
  name: string
  online: boolean
  fingerprint: string
  info: SystemInfo
  /** Features of the connected agent ("shell", "wake"); empty while offline. */
  features: string[]
  agent_version: string
  last_seen: number
  created_at: number
  metrics: Metrics | null
}

export interface Series {
  step: number
  from: number
  to: number
  t: number[]
  values: Record<MetricKey, (number | null)[]>
}

export type RangeKey = 'live' | '1h' | '24h' | '7d' | '30d' | '1y'

export interface Enrollment {
  token: string
  expires_at: number
  hub_url: string
  hub_key: string
}

export interface User {
  username: string
  totp: boolean
  elevated_until: number
}

export interface TOTPSetup {
  secret: string
  uri: string
  qr: string
}

export interface AuditEntry {
  id: number
  t: number
  username: string
  action: string
  system_id?: number
  system_name?: string
  remote?: string
  detail?: string
}

export interface WakeResult {
  via: string
  broadcast: string
  macs: string[]
}

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    public body: Record<string, unknown> = {},
  ) {
    super(message)
  }
}

let onUnauthorized: () => void = () => {}

/** Called whenever the session turns out to be gone. */
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) {
    let data: Record<string, unknown> = {}
    try {
      data = await res.json()
    } catch {
      // not JSON
    }
    if (res.status === 401 && path !== '/api/login') onUnauthorized()
    const message = typeof data.error === 'string' ? data.error : res.statusText
    throw new ApiError(res.status, message.charAt(0).toUpperCase() + message.slice(1), data)
  }
  if (res.status === 204) return undefined as T
  return res.json() as Promise<T>
}

export const api = {
  setupNeeded: () => request<{ needed: boolean }>('GET', '/api/setup'),
  setup: (username: string, password: string) => request<User>('POST', '/api/setup', { username, password }),
  login: (username: string, password: string, code = '') =>
    request<User>('POST', '/api/login', { username, password, code }),
  logout: () => request<void>('POST', '/api/logout'),
  me: () => request<User>('GET', '/api/me'),
  systems: () => request<System[]>('GET', '/api/systems'),
  system: (id: number) => request<System>('GET', `/api/systems/${id}`),
  rename: (id: number, name: string) => request<System>('PATCH', `/api/systems/${id}`, { name }),
  remove: (id: number) => request<void>('DELETE', `/api/systems/${id}`),
  metrics: (id: number, range: RangeKey) => request<Series>('GET', `/api/systems/${id}/metrics?range=${range}`),
  enroll: () => request<Enrollment>('POST', '/api/enroll'),
  wake: (id: number) => request<WakeResult>('POST', `/api/systems/${id}/wake`),
  elevate: (password: string, code = '') =>
    request<{ elevated_until: number }>('POST', '/api/elevate', { password, code }),
  changePassword: (current: string, next: string) => request<void>('POST', '/api/me/password', { current, new: next }),
  totpSetup: () => request<TOTPSetup>('POST', '/api/me/totp/setup'),
  totpEnable: (secret: string, code: string, password: string) =>
    request<void>('POST', '/api/me/totp/enable', { secret, code, password }),
  totpDisable: (password: string, code: string) => request<void>('POST', '/api/me/totp/disable', { password, code }),
  audit: (before = 0) => request<{ entries: AuditEntry[]; more: boolean }>('GET', `/api/audit?before=${before}&limit=50`),
}
