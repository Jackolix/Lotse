import { api, type Metrics, type System } from './api'

type Listener = (t: number, m: Metrics) => void

/** How long a hidden tab keeps the event stream open (agents slow down once nobody watches). */
const HIDDEN_GRACE_MS = 30_000

/**
 * The list of systems, kept current by the hub's event stream. While the stream is
 * open the hub asks agents to report every 2 s; closing it lets them fall back to 60 s.
 */
class SystemsStore {
  list = $state<System[]>([])
  loaded = $state(false)
  connected = $state(false)

  #es: EventSource | null = null
  #listeners = new Map<number, Set<Listener>>()
  #hideTimer: ReturnType<typeof setTimeout> | undefined
  #retryTimer: ReturnType<typeof setTimeout> | undefined
  #running = false

  start() {
    if (this.#running) return
    this.#running = true
    document.addEventListener('visibilitychange', this.#onVisibility)
    this.#open()
  }

  stop() {
    this.#running = false
    document.removeEventListener('visibilitychange', this.#onVisibility)
    clearTimeout(this.#hideTimer)
    clearTimeout(this.#retryTimer)
    this.#close()
    this.list = []
    this.loaded = false
  }

  async refresh() {
    try {
      this.list = await api.systems()
      this.loaded = true
    } catch {
      // 401 is handled globally; other errors keep the last known list
    }
  }

  get(id: number): System | undefined {
    return this.list.find((s) => s.id === id)
  }

  /** Subscribe to live samples of one system. Returns an unsubscribe function. */
  onMetrics(id: number, fn: Listener): () => void {
    let set = this.#listeners.get(id)
    if (!set) this.#listeners.set(id, (set = new Set()))
    set.add(fn)
    return () => set.delete(fn)
  }

  #open() {
    if (this.#es) return
    const es = new EventSource('/api/events')
    this.#es = es
    es.onopen = () => {
      this.connected = true
      this.refresh() // resync after (re)connecting
    }
    es.onerror = () => {
      this.connected = false
      if (es.readyState === EventSource.CLOSED) {
        // The browser gave up (e.g. a 401). Check the session, then retry.
        this.#close()
        this.#retryTimer = setTimeout(async () => {
          if (!this.#running) return
          try {
            await api.me()
            this.#open()
          } catch {
            // logged out: the unauthorized handler stops the store
          }
        }, 5000)
      }
    }
    es.addEventListener('metrics', (e) => {
      const ev = JSON.parse(e.data) as { id: number; t: number; m: Metrics }
      const sys = this.get(ev.id)
      if (sys) {
        sys.metrics = ev.m
        sys.last_seen = ev.t
        sys.online = true
      }
      this.#listeners.get(ev.id)?.forEach((fn) => fn(ev.t, ev.m))
    })
    es.addEventListener('status', (e) => {
      const ev = JSON.parse(e.data) as { id: number; online: boolean }
      const sys = this.get(ev.id)
      if (sys) sys.online = ev.online
    })
    es.addEventListener('systems', () => this.refresh())
  }

  #close() {
    this.#es?.close()
    this.#es = null
    this.connected = false
  }

  #onVisibility = () => {
    clearTimeout(this.#hideTimer)
    if (document.hidden) {
      this.#hideTimer = setTimeout(() => this.#close(), HIDDEN_GRACE_MS)
    } else if (this.#running && !this.#es) {
      this.#open()
    }
  }
}

export const systems = new SystemsStore()
