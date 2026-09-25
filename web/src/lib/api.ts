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
  /** Features of the connected agent ("shell", "wake", "update"); empty while offline. */
  features: string[]
  agent_version: string
  /** Newer signed agent version the hub can install, if any. */
  update?: string
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

export type Role = 'viewer' | 'operator' | 'admin'

export interface User {
  id: number
  username: string
  role: Role
  totp: boolean
  passkeys: number
  elevated_until: number
}

export interface Account {
  id: number
  username: string
  role: Role
  totp: boolean
  passkeys: number
  created_at: number
  last_login: number
}

export interface Passkey {
  id: number
  name: string
  rp_id: string
  created_at: number
  last_used: number
}

/** navigator.credentials.get() result, base64url encoded. */
export interface PasskeyAssertion {
  id: string
  client_data: string
  authenticator_data: string
  signature: string
  user_handle: string
}

/** navigator.credentials.create() result, base64url encoded. */
export interface PasskeyAttestation {
  id: string
  client_data: string
  authenticator_data: string
  public_key: string
  algorithm: number
}

export type ScriptShell = 'sh' | 'bash' | 'powershell' | 'cmd'

export interface Script {
  id: number
  name: string
  description: string
  shell: ScriptShell
  content: string
  timeout: number
  created_by: string
  created_at: number
  updated_at: number
}

export type TargetStatus = 'pending' | 'running' | 'done' | 'failed' | 'skipped' | 'canceled'

export interface RunTarget {
  system_id: number
  system_name: string
  status: TargetStatus
  exit_code: number | null
  error?: string
  output: string
  truncated?: boolean
  started_at?: number
  finished_at?: number
}

export interface Run {
  id: number
  script_id: number | null
  name: string
  shell: ScriptShell
  content: string
  timeout: number
  username: string
  started_at: number
  finished_at: number | null
  targets: RunTarget[]
}

export interface FileEntry {
  name: string
  size: number
  mode: string
  dir: boolean
  link: boolean
  mtime: number
}

export interface Service {
  name: string
  description?: string
  state: 'running' | 'stopped' | 'failed' | 'starting' | 'stopping'
  detail?: string
  start_type?: string
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
  if (!res.ok) throw await errorFrom(res, path)
  if (res.status === 204) return undefined as T
  return res.json() as Promise<T>
}

async function errorFrom(res: { status: number; statusText: string; json(): Promise<unknown> }, path: string) {
  let data: Record<string, unknown> = {}
  try {
    data = (await res.json()) as Record<string, unknown>
  } catch {
    // not JSON
  }
  if (res.status === 401 && path !== '/api/login') onUnauthorized()
  const message = typeof data.error === 'string' ? data.error : res.statusText || 'Request failed'
  return new ApiError(res.status, message.charAt(0).toUpperCase() + message.slice(1), data)
}

/** Uploads a file with progress reports (fetch cannot report upload progress). */
export function uploadFile(
  systemId: number,
  path: string,
  file: Blob,
  overwrite: boolean,
  onProgress: (fraction: number) => void,
  signal?: AbortSignal,
): Promise<void> {
  const url = `/api/systems/${systemId}/files?path=${encodeURIComponent(path)}${overwrite ? '&overwrite=1' : ''}`
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('PUT', url)
    xhr.upload.onprogress = (e) => e.lengthComputable && onProgress(e.loaded / e.total)
    xhr.onload = async () => {
      if (xhr.status < 300) return resolve()
      const res = { status: xhr.status, statusText: xhr.statusText, json: async () => JSON.parse(xhr.responseText) }
      reject(await errorFrom(res, url))
    }
    xhr.onerror = () => reject(new ApiError(0, 'Cannot reach the hub.'))
    xhr.onabort = () => reject(new ApiError(0, 'Upload cancelled.'))
    signal?.addEventListener('abort', () => xhr.abort())
    xhr.send(file)
  })
}

type Json = Record<string, unknown>

export const api = {
  setupNeeded: () => request<{ needed: boolean }>('GET', '/api/setup'),
  setup: (username: string, password: string) => request<User>('POST', '/api/setup', { username, password }),
  login: (username: string, password: string, code = '') =>
    request<User>('POST', '/api/login', { username, password, code }),
  loginPasskeyOptions: () => request<Json>('POST', '/api/login/passkey-options'),
  loginWithPasskey: (passkey: PasskeyAssertion) => request<User>('POST', '/api/login', { passkey }),
  passkeyOptions: (purpose: 'register' | 'elevate') => request<Json>('POST', '/api/me/passkeys/options', { purpose }),
  passkeys: () => request<Passkey[]>('GET', '/api/me/passkeys'),
  addPasskey: (name: string, credential: PasskeyAttestation) =>
    request<Passkey>('POST', '/api/me/passkeys', { name, credential }),
  renamePasskey: (id: number, name: string) => request<void>('PATCH', `/api/me/passkeys/${id}`, { name }),
  deletePasskey: (id: number) => request<void>('DELETE', `/api/me/passkeys/${id}`),
  elevateWithPasskey: (passkey: PasskeyAssertion) =>
    request<{ elevated_until: number }>('POST', '/api/elevate', { passkey }),
  users: () => request<Account[]>('GET', '/api/users'),
  createUser: (username: string, password: string, role: Role) =>
    request<Account>('POST', '/api/users', { username, password, role }),
  updateUser: (id: number, change: { role?: Role; password?: string; reset_totp?: boolean }) =>
    request<void>('PATCH', `/api/users/${id}`, change),
  deleteUser: (id: number) => request<void>('DELETE', `/api/users/${id}`),
  scripts: () => request<Script[]>('GET', '/api/scripts'),
  saveScript: (s: Pick<Script, 'name' | 'description' | 'shell' | 'content' | 'timeout'> & { id?: number }) =>
    s.id ? request<Script>('PUT', `/api/scripts/${s.id}`, s) : request<Script>('POST', '/api/scripts', s),
  deleteScript: (id: number) => request<void>('DELETE', `/api/scripts/${id}`),
  runs: () => request<Run[]>('GET', '/api/runs'),
  run: (id: number) => request<Run>('GET', `/api/runs/${id}`),
  startRun: (r: { script_id?: number; name?: string; shell?: ScriptShell; content?: string; timeout?: number; systems: number[] }) =>
    request<{ id: number }>('POST', '/api/runs', r),
  cancelRun: (id: number) => request<void>('POST', `/api/runs/${id}/cancel`),
  files: (id: number, path: string) =>
    request<{ path: string; entries: FileEntry[] }>('GET', `/api/systems/${id}/files?path=${encodeURIComponent(path)}`),
  fileAction: (id: number, action: { action: 'mkdir' | 'rename' | 'delete'; path: string; to?: string; recursive?: boolean }) =>
    request<void>('POST', `/api/systems/${id}/files`, action),
  downloadURL: (id: number, path: string) => `/api/systems/${id}/files/download?path=${encodeURIComponent(path)}`,
  power: (id: number, action: 'reboot' | 'shutdown') => request<void>('POST', `/api/systems/${id}/power`, { action }),
  services: (id: number) => request<{ services: Service[]; error?: string }>('GET', `/api/systems/${id}/services`),
  service: (id: number, name: string, action: 'start' | 'stop' | 'restart') =>
    request<void>('POST', `/api/systems/${id}/services`, { name, action }),
  updateAgent: (id: number) => request<{ version: string }>('POST', `/api/systems/${id}/update`),
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
