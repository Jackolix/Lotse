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
  containers?: Container[]
}

export interface Container {
  id: string
  name: string
  image: string
  state: string
  status: string
  cpu: number
  mem: number
  mem_limit: number
  net_rx: number
  net_tx: number
}

export interface Process {
  pid: number
  name: string
  user?: string
  cpu: number
  mem: number
  started?: number
  cmd?: string
}

export type MetricKey = Exclude<keyof Metrics, 'uptime' | 'fs' | 'containers'>

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

export type AlertMetric = 'offline' | 'cpu' | 'memory' | 'disk' | 'load'

export interface AlertRule {
  id: number
  name: string
  system_id: number | null
  metric: AlertMetric
  threshold: number
  duration: number
  notifiers: number[]
  enabled: boolean
}

export interface Alert {
  id: number
  rule_id: number
  rule_name: string
  system_id: number
  system_name: string
  metric: AlertMetric
  threshold: number
  value: number
  started_at: number
  resolved_at: number | null
}

export type NotifierType = 'ntfy' | 'discord' | 'slack' | 'telegram' | 'webhook' | 'email'

export interface NotifierConfig {
  url?: string
  token?: string
  token_set?: boolean
  chat_id?: string
  host?: string
  port?: number
  username?: string
  password?: string
  password_set?: boolean
  from?: string
  to?: string
  tls?: 'starttls' | 'tls' | 'none'
}

export interface Notifier {
  id: number
  name: string
  type: NotifierType
  enabled: boolean
  last_sent: number
  last_error: string
  config: NotifierConfig
}

export type NotifierInput = Pick<Notifier, 'name' | 'type' | 'enabled' | 'config'> & { id?: number }

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
  processes: (id: number, limit = 50) =>
    request<{ processes: Process[]; total: number }>('GET', `/api/systems/${id}/processes?limit=${limit}`),
  signal: (id: number, pid: number, signal: 'terminate' | 'kill', name: string) =>
    request<void>('POST', `/api/systems/${id}/processes/${pid}/signal`, { signal, name }),
  elevate: (password: string, code = '') =>
    request<{ elevated_until: number }>('POST', '/api/elevate', { password, code }),
  changePassword: (current: string, next: string) => request<void>('POST', '/api/me/password', { current, new: next }),
  totpSetup: () => request<TOTPSetup>('POST', '/api/me/totp/setup'),
  totpEnable: (secret: string, code: string, password: string) =>
    request<void>('POST', '/api/me/totp/enable', { secret, code, password }),
  totpDisable: (password: string, code: string) => request<void>('POST', '/api/me/totp/disable', { password, code }),
  audit: (before = 0) => request<{ entries: AuditEntry[]; more: boolean }>('GET', `/api/audit?before=${before}&limit=50`),
  alerts: () => request<{ active: Alert[]; history: Alert[] }>('GET', '/api/alerts'),
  alertRules: () => request<AlertRule[]>('GET', '/api/alert-rules'),
  saveAlertRule: (r: Omit<AlertRule, 'id'> & { id?: number }) =>
    r.id ? request<AlertRule>('PUT', `/api/alert-rules/${r.id}`, r) : request<AlertRule>('POST', '/api/alert-rules', r),
  deleteAlertRule: (id: number) => request<void>('DELETE', `/api/alert-rules/${id}`),
  notifiers: () => request<Notifier[]>('GET', '/api/notifiers'),
  saveNotifier: (n: NotifierInput) =>
    n.id ? request<{ id: number }>('PUT', `/api/notifiers/${n.id}`, n) : request<{ id: number }>('POST', '/api/notifiers', n),
  deleteNotifier: (id: number) => request<void>('DELETE', `/api/notifiers/${id}`),
  testNotifier: (n: NotifierInput) => request<void>('POST', '/api/notifiers/test', n),
}
