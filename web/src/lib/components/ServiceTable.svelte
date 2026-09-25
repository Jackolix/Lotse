<script lang="ts">
  import { onMount } from 'svelte'
  import { api, type Service } from '../api'
  import { confirmAction } from '../dialogs.svelte'
  import { openMenu, type MenuEntry } from '../menu.svelte'
  import { withReauth } from '../reauth.svelte'
  import { copy, errorText, needsAllowShell } from '../systemActions'
  import { toast } from '../toast.svelte'
  import Icon from './Icon.svelte'

  // Loaded on demand and refreshed every 10 seconds while shown.
  let { systemId, canControl }: { systemId: number; canControl: boolean } = $props()

  type Filter = 'all' | 'running' | 'stopped' | 'failed'
  let services = $state<Service[]>([])
  let notice = $state('')
  let error = $state('')
  let loading = $state(true)
  let query = $state('')
  let filter = $state<Filter>('all')

  async function load(force = false) {
    if (!force && document.hidden) return
    try {
      const r = await api.services(systemId)
      services = r.services
      notice = r.error ?? ''
      error = ''
    } catch (err) {
      error = errorText(err)
    } finally {
      loading = false
    }
  }

  onMount(() => {
    load(true)
    const timer = setInterval(() => load(), 10_000)
    return () => clearInterval(timer)
  })

  const counts = $derived({
    all: services.length,
    running: services.filter((s) => s.state === 'running').length,
    stopped: services.filter((s) => s.state === 'stopped').length,
    failed: services.filter((s) => s.state === 'failed').length,
  })

  const rows = $derived.by(() => {
    const q = query.trim().toLowerCase()
    return services.filter(
      (s) =>
        (filter === 'all' || s.state === filter) &&
        (!q || `${s.name} ${s.description ?? ''}`.toLowerCase().includes(q)),
    )
  })

  const stateColor: Record<Service['state'], string> = {
    running: 'var(--good)',
    starting: 'var(--accent)',
    stopping: 'var(--warning)',
    stopped: 'var(--muted)',
    failed: 'var(--critical)',
  }

  async function control(s: Service, action: 'start' | 'stop' | 'restart') {
    if (action !== 'start') {
      const verb = action === 'stop' ? 'Stop' : 'Restart'
      const ok = await confirmAction({
        title: `${verb} ${s.name}?`,
        message: s.description ? `${s.description}.` : 'Whatever depends on it is interrupted.',
        confirmLabel: verb,
        danger: action === 'stop',
      })
      if (!ok) return
    }
    try {
      const done = await withReauth('Controlling services needs your password again.', () =>
        api.service(systemId, s.name, action).then(() => true),
      )
      if (!done) return
      toast(`${action === 'start' ? 'Starting' : action === 'stop' ? 'Stopping' : 'Restarting'} ${s.name}`, 'success', 2500)
      setTimeout(() => load(true), 1500)
    } catch (err) {
      toast(errorText(err), 'error')
    }
  }

  const menu = (s: Service): MenuEntry[] => [
    { label: 'Start', icon: 'play', disabled: !canControl || s.state === 'running', hint: canControl ? 'Already running' : needsAllowShell, action: () => control(s, 'start') },
    { label: 'Restart…', icon: 'refresh', disabled: !canControl, hint: needsAllowShell, action: () => control(s, 'restart') },
    { label: 'Stop…', icon: 'square', danger: true, disabled: !canControl || s.state === 'stopped', hint: canControl ? 'Not running' : needsAllowShell, action: () => control(s, 'stop') },
    'separator',
    { label: 'Copy name', icon: 'copy', action: () => copy(s.name, 'Name') },
  ]
</script>

<div class="flex flex-wrap items-center gap-3 px-4 pt-4">
  <h2 class="text-sm font-medium">Services</h2>
  {#if services.length}
    <div role="radiogroup" aria-label="Show services" class="inline-flex rounded-lg border border-line bg-surface p-0.5 text-xs">
      {#each ['all', 'running', 'stopped', 'failed'] as const as f (f)}
        <button
          role="radio"
          aria-checked={filter === f}
          class="h-6 rounded-md px-2 {filter === f ? 'bg-sunken font-medium text-ink' : 'text-ink-2 hover:text-ink'}"
          onclick={() => (filter = f)}>{f[0].toUpperCase() + f.slice(1)} {counts[f]}</button
        >
      {/each}
    </div>
    <label class="relative ml-auto block w-full sm:w-56">
      <span class="sr-only">Filter services</span>
      <Icon name="search" size={14} class="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-muted" />
      <input class="input h-8 pl-8" placeholder="Filter" bind:value={query} />
    </label>
  {/if}
</div>
{#if error}
  <p class="px-4 py-3 text-sm text-critical" role="alert">{error}</p>
{:else if loading}
  <p class="px-4 py-3 text-sm text-muted">Reading services…</p>
{:else if notice && !services.length}
  <p class="px-4 py-3 text-sm text-ink-2">{notice[0].toUpperCase() + notice.slice(1)}.</p>
{:else}
  <div class="max-h-[32rem] overflow-auto">
    <table class="mt-2 w-full text-sm">
      <thead class="sticky top-0 bg-surface text-left text-xs text-muted">
        <tr>
          <th class="px-4 py-2 font-medium">Service</th>
          <th class="py-2 pr-4 font-medium">State</th>
          <th class="hidden py-2 pr-4 font-medium md:table-cell">Starts</th>
          <th class="py-2 pr-4"><span class="sr-only">Actions</span></th>
        </tr>
      </thead>
      <tbody>
        {#each rows as s (s.name)}
          <tr class="border-t border-line align-top" oncontextmenu={(e) => openMenu(e, menu(s))}>
            <td class="max-w-md py-1.5 pl-4 pr-4">
              <div class="truncate font-medium" title={s.name}>{s.name}</div>
              {#if s.description && s.description !== s.name}<div class="truncate text-xs text-muted" title={s.description}>{s.description}</div>{/if}
            </td>
            <td class="py-1.5 pr-4 whitespace-nowrap" title={s.detail}>
              <span class="mr-1.5 inline-block size-2 rounded-full" style:background={stateColor[s.state]}></span>{s.state}
            </td>
            <td class="hidden py-1.5 pr-4 text-ink-2 md:table-cell">{s.start_type ?? ''}</td>
            <td class="py-1 pr-3 text-right whitespace-nowrap">
              {#if canControl}
                {#if s.state === 'running'}
                  <button class="btn h-7 px-2 text-xs" onclick={() => control(s, 'restart')}>Restart</button>
                {:else}
                  <button class="btn h-7 px-2 text-xs" onclick={() => control(s, 'start')}>Start</button>
                {/if}
              {/if}
              <button class="btn h-7 px-2" onclick={(e) => openMenu(e, menu(s))} aria-label="More for {s.name}">
                <Icon name="chevron-down" size={13} />
              </button>
            </td>
          </tr>
        {:else}
          <tr><td colspan="4" class="px-4 py-4 text-muted">No service matches.</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}
