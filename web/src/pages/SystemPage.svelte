<script lang="ts">
  import type uPlot from 'uplot'
  import { untrack } from 'svelte'
  import { api, ApiError, type MetricKey, type Metrics, type RangeKey, type Series } from '../lib/api'
  import ChartCard from '../lib/components/ChartCard.svelte'
  import Icon from '../lib/components/Icon.svelte'
  import Meter from '../lib/components/Meter.svelte'
  import StatusDot from '../lib/components/StatusDot.svelte'
  import { ago, archLabel, bytes, duration, osLabel, pct, rate, ratio } from '../lib/format'
  import { link, navigate } from '../lib/router.svelte'
  import { canShell as shellAllowed, deleteSystem, ipv4Of, macsOf, wakeSystem } from '../lib/systemActions'
  import { systems } from '../lib/systems.svelte'

  let { id }: { id: number } = $props()

  const RANGES: { key: RangeKey; label: string }[] = [
    { key: 'live', label: 'Live' },
    { key: '1h', label: '1h' },
    { key: '24h', label: '24h' },
    { key: '7d', label: '7d' },
    { key: '30d', label: '30d' },
    { key: '1y', label: '1y' },
  ]
  const KEYS: MetricKey[] = [
    'cpu', 'mem_used', 'mem_total', 'swap_used', 'swap_total', 'disk_used', 'disk_total',
    'disk_read', 'disk_write', 'net_rx', 'net_tx', 'load1', 'load5', 'load15',
  ]
  const LIVE_SPAN = 300 // seconds, matches the hub

  let range = $state<RangeKey>(storedRange())
  let series = $state.raw<Series | null>(null)
  let loading = $state(false)
  let renaming = $state(false)
  let newName = $state('')
  let actionError = $state('')
  let waking = $state(false)

  const sys = $derived(systems.get(id))
  const m = $derived(sys?.metrics ?? null)

  function storedRange(): RangeKey {
    try {
      const r = localStorage.getItem('range')
      if (RANGES.some((x) => x.key === r)) return r as RangeKey
    } catch {
      // storage unavailable
    }
    return '1h'
  }

  function setRange(r: RangeKey) {
    range = r
    try {
      localStorage.setItem('range', r)
    } catch {
      // storage unavailable
    }
  }

  async function load(r: RangeKey) {
    loading = true
    try {
      const s = await api.metrics(id, r)
      if (r === range) series = s
    } catch (e) {
      if (!(e instanceof ApiError && e.status === 401)) console.error(e)
    } finally {
      loading = false
    }
  }

  // History ranges refresh once a minute; "Live" appends every sample the hub pushes.
  $effect(() => {
    const r = range
    untrack(() => load(r))
    if (r === 'live') {
      return systems.onMetrics(id, (t, sample) => (series = appendLive(series, t, sample)))
    }
    const timer = setInterval(() => load(r), r === '1h' ? 20_000 : 60_000)
    return () => clearInterval(timer)
  })

  function appendLive(s: Series | null, t: number, sample: Metrics): Series | null {
    if (!s || s.step !== 0) return s // a history range is still loading
    const from = t - LIVE_SPAN
    let start = 0
    while (start < s.t.length && s.t[start] < from) start++
    const values = {} as Series['values']
    for (const k of KEYS) values[k] = [...s.values[k].slice(start), sample[k]]
    return { ...s, from, to: t, t: [...s.t.slice(start), t], values }
  }

  function cols(...keys: MetricKey[]): uPlot.AlignedData {
    if (!series) return [[], ...keys.map(() => [])]
    return [series.t, ...keys.map((k) => series!.values[k])]
  }

  function maxOf(values: (number | null)[] | undefined): number {
    let max = 0
    for (const v of values ?? []) if (v != null && v > max) max = v
    return max
  }

  const now = Math.floor(Date.now() / 1000)
  const xRange = $derived<[number, number]>(series ? [series.from, series.to] : [now - 3600, now])
  const memTotal = $derived(m?.mem_total || sys?.info.mem_total || 0)
  const memMax = $derived(Math.max(memTotal, maxOf(series?.values.swap_used)))
  const diskMax = $derived(Math.max(m?.disk_total ?? 0, maxOf(series?.values.disk_total)))
  const isWindows = $derived(sys?.info.os === 'windows')
  const canShell = $derived(sys ? shellAllowed(sys) : false)
  const macs = $derived(sys ? macsOf(sys) : [])
  const ipv4 = $derived(sys ? ipv4Of(sys) : undefined)

  async function wake() {
    if (!sys) return
    waking = true
    await wakeSystem(sys)
    waking = false
  }
  const load2 = (v: number) => v.toFixed(2)

  function startRename() {
    newName = sys?.name ?? ''
    renaming = true
  }

  async function saveName(e: SubmitEvent) {
    e.preventDefault()
    actionError = ''
    try {
      await api.rename(id, newName)
      await systems.refresh()
      renaming = false
    } catch (err) {
      actionError = err instanceof Error ? err.message : 'Rename failed'
    }
  }

  async function remove() {
    if (sys && (await deleteSystem(sys))) navigate('/')
  }
</script>

<a href="/" onclick={link} class="inline-flex items-center gap-1 text-sm text-ink-2 hover:text-ink">
  <Icon name="arrow-left" size={14} /> All systems
</a>

{#if !sys}
  <p class="mt-6 text-sm text-muted">
    {systems.loaded ? 'This system does not exist (any more).' : 'Loading…'}
  </p>
{:else}
  <section class="card mt-3 p-5">
    <div class="flex flex-wrap items-start gap-3">
      <div class="mr-auto min-w-0">
        {#if renaming}
          <form class="flex flex-wrap gap-2" onsubmit={saveName}>
            <input class="input w-64 max-w-full" aria-label="System name" maxlength="64" required bind:value={newName} />
            <button class="btn btn-primary">Save</button>
            <button type="button" class="btn" onclick={() => (renaming = false)}>Cancel</button>
          </form>
        {:else}
          <h1 class="truncate text-xl font-semibold">{sys.name}</h1>
        {/if}
        <div class="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-ink-2">
          <StatusDot online={sys.online} />
          {#if sys.online && m}
            <span>Up {duration(m.uptime)}</span>
          {:else}
            <span>Last seen {ago(sys.last_seen)}</span>
          {/if}
        </div>
      </div>
      {#if !renaming}
        <div class="flex flex-wrap gap-2">
          {#if sys.online}
            {#if canShell}
              <a class="btn btn-primary" href="/systems/{id}/terminal" onclick={link}><Icon name="terminal" size={14} /> Terminal</a>
            {:else}
              <button
                class="btn"
                disabled
                title="Remote shell is disabled on this machine. Reinstall the agent with --allow-shell to enable it."
              >
                <Icon name="terminal" size={14} /> Terminal
              </button>
            {/if}
          {:else if macs.length}
            <button class="btn btn-primary" onclick={wake} disabled={waking}><Icon name="power" size={14} /> Wake</button>
          {/if}
          <button class="btn" onclick={startRename}><Icon name="pencil" size={14} /> Rename</button>
          <button class="btn btn-danger" onclick={remove}><Icon name="trash" size={14} /> Delete</button>
        </div>
      {/if}
    </div>
    {#if actionError}<p class="mt-3 text-sm text-critical" role="alert">{actionError}</p>{/if}
    {#if sys.online && !canShell}
      <p class="mt-3 text-xs text-muted">
        Remote shell is off for this machine. To allow it, re-run the install command with
        <code class="font-mono">--allow-shell</code> (Windows: <code class="font-mono">-AllowShell</code>).
      </p>
    {/if}

    <dl class="mt-5 grid grid-cols-2 gap-x-6 gap-y-4 text-sm sm:grid-cols-4">
      {#each [
        ['Operating system', osLabel(sys.info)],
        ['Kernel', sys.info.kernel || '–'],
        ['CPU', `${sys.info.cpu_model || archLabel(sys.info.arch)} · ${sys.info.cores} threads`],
        ['Memory', bytes(memTotal)],
        ['Hostname', sys.info.hostname],
        ['IP address', ipv4 ?? '–'],
        ['MAC address', macs.join(', ') || '–'],
        ['Agent', sys.agent_version],
      ] as [label, value] (label)}
        <div class="min-w-0">
          <dt class="text-xs text-muted">{label}</dt>
          <dd class="mt-0.5 truncate" title={value}>{value}</dd>
        </div>
      {/each}
    </dl>
  </section>

  <div class="mt-6 mb-3 flex flex-wrap items-center gap-3">
    <div role="radiogroup" aria-label="Time range" class="inline-flex rounded-lg border border-line bg-surface p-0.5">
      {#each RANGES as r (r.key)}
        <button
          role="radio"
          aria-checked={range === r.key}
          class="h-8 rounded-md px-3 text-sm {range === r.key
            ? 'bg-sunken font-medium text-ink'
            : 'text-ink-2 hover:text-ink'}"
          onclick={() => setRange(r.key)}>{r.label}</button
        >
      {/each}
    </div>
    <span class="text-xs text-muted">
      {range === 'live'
        ? 'Last 5 minutes, updated as samples arrive'
        : range === '1h'
          ? 'Refreshes every 20 seconds'
          : 'Refreshes every minute'}
    </span>
  </div>

  <div class="grid gap-4 md:grid-cols-2">
    <ChartCard
      title="CPU"
      value={m ? pct(m.cpu) : ''}
      data={cols('cpu')}
      series={[{ label: 'CPU', color: '--s1' }]}
      format={pct}
      yMax={100}
      fill
      {xRange}
      {loading}
    />
    <ChartCard
      title="Memory"
      binary
      value={m ? `${bytes(m.mem_used)} of ${bytes(m.mem_total)}` : ''}
      data={cols('mem_used', 'swap_used')}
      series={[
        { label: 'Used', color: '--s1' },
        { label: 'Swap', color: '--s2' },
      ]}
      format={bytes}
      yMax={memMax || undefined}
      {xRange}
      {loading}
    />
    <ChartCard
      title="Disk usage"
      binary
      value={m ? `${bytes(m.disk_used)} of ${bytes(m.disk_total)}` : ''}
      data={cols('disk_used')}
      series={[{ label: 'Used', color: '--s1' }]}
      format={bytes}
      yMax={diskMax || undefined}
      fill
      {xRange}
      {loading}
    />
    <ChartCard
      title="Disk I/O"
      binary
      value={m ? `R ${rate(m.disk_read)} · W ${rate(m.disk_write)}` : ''}
      data={cols('disk_read', 'disk_write')}
      series={[
        { label: 'Read', color: '--s1' },
        { label: 'Write', color: '--s2' },
      ]}
      format={rate}
      {xRange}
      {loading}
    />
    <ChartCard
      title="Network"
      binary
      value={m ? `↓ ${rate(m.net_rx)} · ↑ ${rate(m.net_tx)}` : ''}
      data={cols('net_rx', 'net_tx')}
      series={[
        { label: 'Received', color: '--s1' },
        { label: 'Sent', color: '--s2' },
      ]}
      format={rate}
      {xRange}
      {loading}
    />
    {#if !isWindows}
      <ChartCard
        title="Load average"
        value={m ? `${load2(m.load1)} · ${load2(m.load5)} · ${load2(m.load15)}` : ''}
        data={cols('load1', 'load5', 'load15')}
        series={[
          { label: '1 min', color: '--s1' },
          { label: '5 min', color: '--s2' },
          { label: '15 min', color: '--s3' },
        ]}
        format={load2}
        {xRange}
        {loading}
      />
    {/if}
  </div>

  {#if m?.fs?.length}
    <section class="card mt-4 overflow-hidden">
      <h2 class="px-4 pt-4 text-sm font-medium">Filesystems</h2>
      <div class="overflow-x-auto">
        <table class="mt-2 w-full text-sm">
          <thead class="text-left text-xs text-muted">
            <tr>
              <th class="px-4 py-2 font-medium">Mount</th>
              <th class="hidden py-2 pr-4 font-medium sm:table-cell">Device</th>
              <th class="hidden py-2 pr-4 font-medium sm:table-cell">Type</th>
              <th class="w-2/5 py-2 pr-4 font-medium">Usage</th>
              <th class="px-4 py-2 text-right font-medium">Used / total</th>
            </tr>
          </thead>
          <tbody>
            {#each m.fs as f (f.mount)}
              <tr class="border-t border-line">
                <td class="px-4 py-2 font-mono text-xs">{f.mount}</td>
                <td class="hidden max-w-48 truncate py-2 pr-4 text-ink-2 sm:table-cell" title={f.device}>{f.device}</td>
                <td class="hidden py-2 pr-4 text-ink-2 sm:table-cell">{f.type}</td>
                <td class="py-2 pr-4"><Meter value={ratio(f.used, f.total)} /></td>
                <td class="tabular px-4 py-2 text-right whitespace-nowrap">{bytes(f.used)} / {bytes(f.total)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </section>
  {/if}
{/if}
