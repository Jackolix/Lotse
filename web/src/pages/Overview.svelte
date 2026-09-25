<script lang="ts">
  import AddSystemDialog from '../lib/components/AddSystemDialog.svelte'
  import Icon from '../lib/components/Icon.svelte'
  import Meter from '../lib/components/Meter.svelte'
  import StatusDot from '../lib/components/StatusDot.svelte'
  import { ago, archLabel, bytes, duration, osLabel, rate, ratio } from '../lib/format'
  import { link } from '../lib/router.svelte'
  import { openMenu } from '../lib/menu.svelte'
  import { systemMenu } from '../lib/systemActions'
  import { systems } from '../lib/systems.svelte'

  let adding = $state(false)
  let query = $state('')

  const rows = $derived.by(() => {
    const q = query.trim().toLowerCase()
    if (!q) return systems.list
    return systems.list.filter((s) =>
      [s.name, s.info.hostname, osLabel(s.info)].some((v) => v.toLowerCase().includes(q)),
    )
  })
  const online = $derived(systems.list.filter((s) => s.online).length)

  // Shared by the header row and every system row (cards on phones, a table from md up).
  const cols =
    'grid grid-cols-2 gap-x-6 gap-y-3 md:grid-cols-[minmax(0,1.7fr)_repeat(3,minmax(0,1fr))_minmax(0,1.1fr)_minmax(0,0.8fr)] md:items-center'
</script>

<div class="mb-5 flex flex-wrap items-end gap-3">
  <div class="mr-auto">
    <h1 class="text-xl font-semibold">Systems</h1>
    {#if systems.loaded}
      <p class="text-sm text-ink-2">{online} of {systems.list.length} online</p>
    {/if}
  </div>
  {#if systems.list.length > 5}
    <label class="relative block w-full sm:w-56">
      <span class="sr-only">Filter systems</span>
      <Icon name="search" size={14} class="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-muted" />
      <input class="input pl-8" placeholder="Filter" bind:value={query} />
    </label>
  {/if}
  <button class="btn btn-primary" onclick={() => (adding = true)}><Icon name="plus" /> Add system</button>
</div>

{#if !systems.loaded}
  <p class="text-sm text-muted">Loading…</p>
{:else if systems.list.length === 0}
  <section class="card grid place-items-center px-6 py-16 text-center">
    <span class="grid size-11 place-items-center rounded-xl bg-sunken text-ink-2"><Icon name="server" size={22} /></span>
    <h2 class="mt-4 font-semibold">No systems yet</h2>
    <p class="mt-1 max-w-sm text-sm text-ink-2">
      Install the agent on a Linux, macOS or Windows machine and it shows up here within seconds.
    </p>
    <button class="btn btn-primary mt-5" onclick={() => (adding = true)}><Icon name="plus" /> Add your first system</button>
  </section>
{:else}
  <div class="card overflow-hidden">
    <div class="{cols} hidden border-b border-line px-4 py-2 text-xs font-medium text-muted md:grid">
      <span>System</span><span>CPU</span><span>Memory</span><span>Disk</span><span>Network</span>
      <span class="text-right">Uptime</span>
    </div>
    {#each rows as s (s.id)}
      {@const m = s.metrics}
      <a
        href="/systems/{s.id}"
        onclick={link}
        oncontextmenu={(e) => openMenu(e, systemMenu(s))}
        class="{cols} border-b border-line px-4 py-3 last:border-b-0 hover:bg-sunken"
      >
        <div class="col-span-2 min-w-0 md:col-span-1">
          <div class="flex items-center gap-2">
            <StatusDot online={s.online} label={false} />
            <span class="truncate font-medium">{s.name}</span>
            {#if systems.alertsFor(s.id).length}
              {@const n = systems.alertsFor(s.id).length}
              <span
                class="shrink-0 rounded-full px-1.5 text-[0.7rem] leading-4.5 font-medium text-critical"
                style:background="color-mix(in oklab, var(--critical) 14%, var(--surface))"
                title={systems.alertsFor(s.id).map((a) => a.rule_name).join(', ')}>{n} alert{n > 1 ? 's' : ''}</span
              >
            {/if}
          </div>
          <div class="truncate pl-4 text-xs text-muted">{osLabel(s.info)} · {archLabel(s.info.arch)}</div>
        </div>

        {#if m}
          <div class:opacity-50={!s.online}>
            <span class="text-xs text-muted md:hidden">CPU</span>
            <Meter value={m.cpu} title="{s.info.cores} cores" />
          </div>
          <div class:opacity-50={!s.online}>
            <span class="text-xs text-muted md:hidden">Memory</span>
            <Meter value={ratio(m.mem_used, m.mem_total)} title="{bytes(m.mem_used)} of {bytes(m.mem_total)}" />
          </div>
          <div class:opacity-50={!s.online}>
            <span class="text-xs text-muted md:hidden">Disk</span>
            <Meter value={ratio(m.disk_used, m.disk_total)} title="{bytes(m.disk_used)} of {bytes(m.disk_total)}" />
          </div>
          <div class="tabular text-sm leading-tight" class:opacity-50={!s.online}>
            <span class="text-xs text-muted md:hidden">Network</span>
            <div><span class="text-muted">↓</span> {rate(m.net_rx)}</div>
            <div><span class="text-muted">↑</span> {rate(m.net_tx)}</div>
          </div>
        {:else}
          <p class="col-span-2 text-sm text-muted md:col-span-4">Waiting for the first report…</p>
        {/if}

        <div class="tabular text-sm md:text-right">
          <span class="text-xs text-muted md:hidden">Uptime</span>
          {#if s.online && m}
            <div>{duration(m.uptime)}</div>
          {:else}
            <div class="text-ink-2">Offline</div>
            <div class="text-xs text-muted">{ago(s.last_seen)}</div>
          {/if}
        </div>
      </a>
    {:else}
      <p class="px-4 py-6 text-sm text-muted">No system matches "{query}".</p>
    {/each}
  </div>
{/if}

<AddSystemDialog bind:open={adding} />
