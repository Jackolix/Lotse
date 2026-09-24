// Typed client for the hub API. Types mirror internal/protocol and internal/hub.

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
}

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
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
    let message = res.statusText
    try {
      message = (await res.json()).error ?? message
    } catch {
      // not JSON
    }
    if (res.status === 401 && path !== '/api/login') onUnauthorized()
    throw new ApiError(res.status, message)
  }
  if (res.status === 204) return undefined as T
  return res.json() as Promise<T>
}

export const api = {
  setupNeeded: () => request<{ needed: boolean }>('GET', '/api/setup'),
  setup: (username: string, password: string) => request<User>('POST', '/api/setup', { username, password }),
  login: (username: string, password: string) => request<User>('POST', '/api/login', { username, password }),
  logout: () => request<void>('POST', '/api/logout'),
  me: () => request<User>('GET', '/api/me'),
  systems: () => request<System[]>('GET', '/api/systems'),
  system: (id: number) => request<System>('GET', `/api/systems/${id}`),
  rename: (id: number, name: string) => request<System>('PATCH', `/api/systems/${id}`, { name }),
  remove: (id: number) => request<void>('DELETE', `/api/systems/${id}`),
  metrics: (id: number, range: RangeKey) => request<Series>('GET', `/api/systems/${id}/metrics?range=${range}`),
  enroll: () => request<Enrollment>('POST', '/api/enroll'),
}
