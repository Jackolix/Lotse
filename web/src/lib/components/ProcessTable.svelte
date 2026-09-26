<script lang="ts">
  import { onMount } from 'svelte'
  import { api, ApiError, type Process } from '../api'
  import { confirmAction } from '../dialogs.svelte'
  import { bytes, pct } from '../format'
  import { openMenu, type MenuEntry } from '../menu.svelte'
  import { rowOrder } from '../rowOrder'
  import { copy } from '../systemActions'
  import { toast } from '../toast.svelte'
  import Icon from './Icon.svelte'
  import ReauthDialog from './ReauthDialog.svelte'

  // Loaded on demand: listing processes costs the agent half a second of sampling,
  // so it only refreshes while this table is shown and the tab is visible.
  let { systemId, canControl }: { systemId: number; canControl: boolean } = $props()

  type SortKey = 'cpu' | 'mem' | 'name' | 'pid'
  let processes = $state<Process[]>([])
  let total = $state(0)
  let error = $state('')
  let loading = $state(true)
  let query = $state('')
  let sortKey = $state<SortKey>('cpu')
  let epoch = $state(0)
  let reauth = $state(false)
  let pending: (() => void) | null = null

  async function load(force = false) {
    if (!force && document.hidden) return // background refresh only while the tab is visible
    try {
      const r = await api.processes(systemId, 100)
      processes = r.processes
      total = r.total
      error = ''
    } catch (err) {
      error = err instanceof ApiError ? err.message : 'Cannot reach the hub.'
    } finally {
      loading = false
    }
  }

  onMount(() => {
    load(true)
    const timer = setInterval(() => load(), 5000)
    return () => clearInterval(timer)
  })

  // Sorting by CPU or memory holds the order between refreshes (see rowOrder), so
  // rows don't trade places every five seconds while you read them.
  const by: Record<SortKey, (a: Process, b: Process) => number> = {
    cpu: (a, b) => b.cpu - a.cpu || b.mem - a.mem || a.pid - b.pid,
    mem: (a, b) => b.mem - a.mem || a.pid - b.pid,
    name: (a, b) => a.name.localeCompare(b.name) || a.pid - b.pid,
    pid: (a, b) => a.pid - b.pid,
  }
  const order = rowOrder((p: Process) => p.pid)
  const ordered = $derived(order(processes, by[sortKey], sortKey === 'cpu' || sortKey === 'mem', epoch))
  const sortBy = (key: SortKey) => {
    sortKey = key
    epoch++
  }

  const rows = $derived.by(() => {
    const q = query.trim().toLowerCase()
    if (!q) return ordered.rows
    return ordered.rows.filter((p) => `${p.pid} ${p.name} ${p.user ?? ''} ${p.cmd ?? ''}`.toLowerCase().includes(q))
  })

  async function signal(p: Process, sig: 'terminate' | 'kill') {
    const verb = sig === 'kill' ? 'Kill' : 'Terminate'
    const ok = await confirmAction({
      title: `${verb} ${p.name} (PID ${p.pid})?`,
      message:
        sig === 'kill'
          ? 'The process is stopped immediately and cannot clean up.'
          : 'The process is asked to exit (SIGTERM; on Windows it is stopped immediately).',
      confirmLabel: verb,
      danger: true,
    })
    if (ok) send(p, sig)
  }

  async function send(p: Process, sig: 'terminate' | 'kill') {
    try {
      await api.signal(systemId, p.pid, sig, p.name)
      toast(`${sig === 'kill' ? 'Killed' : 'Terminated'} ${p.name}`, 'success', 2500)
      setTimeout(() => load(true), 700)
    } catch (err) {
      if (err instanceof ApiError && err.body.reauth_required) {
        pending = () => send(p, sig) // retry once the password is confirmed
        reauth = true
      } else {
        toast(err instanceof ApiError ? err.message : 'Cannot reach the hub.', 'error')
      }
    }
  }

  const hint = 'Stopping processes needs the agent installed with --allow-shell'
  const menu = (p: Process): MenuEntry[] => [
    { label: `Copy PID (${p.pid})`, icon: 'copy', action: () => copy(String(p.pid), 'PID') },
    { label: 'Copy command line', icon: 'copy', disabled: !p.cmd, hint: 'Only shown by agents that allow the shell', action: () => copy(p.cmd ?? '', 'Command line') },
    'separator',
    { label: 'Terminate…', icon: 'x', disabled: !canControl, hint, action: () => signal(p, 'terminate') },
    { label: 'Kill…', icon: 'trash', danger: true, disabled: !canControl, hint, action: () => signal(p, 'kill') },
  ]

  function header(key: SortKey, label: string, width = '', right = false) {
    return { key, label, width, right }
  }
  const columns = [header('pid', 'PID', 'w-24'), header('name', 'Process'), header('cpu', 'CPU', 'w-16', true), header('mem', 'Memory', 'w-24', true)]
</script>

<div class="flex flex-wrap items-center gap-3 px-4 pt-4">
  <h2 class="text-sm font-medium">
    Processes {#if total}<span class="font-normal text-ink-2">· busiest {processes.length} of {total}</span>{/if}
  </h2>
  {#if ordered.stale}
    <button
      class="inline-flex items-center gap-1 text-xs text-ink-2 hover:text-ink"
      title="Rows keep their place while the numbers update"
      onclick={() => sortBy(sortKey)}><Icon name="refresh" size={12} /> Sort by {sortKey === 'cpu' ? 'CPU' : 'memory'} again</button
    >
  {/if}
  <label class="relative ml-auto block w-full sm:w-56">
    <span class="sr-only">Filter processes</span>
    <Icon name="search" size={14} class="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-muted" />
    <input class="input h-8 pl-8" placeholder="Filter" bind:value={query} />
  </label>
</div>
{#if error}
  <p class="px-4 py-3 text-sm text-critical" role="alert">{error}</p>
{:else if loading}
  <p class="px-4 py-3 text-sm text-muted">Sampling processes…</p>
{:else}
  <div class="max-h-[32rem] overflow-auto">
    <table class="mt-2 w-full text-sm">
      <thead class="sticky top-0 bg-surface text-left text-xs text-muted">
        <tr>
          {#each columns as c (c.key)}
            <th class="py-2 font-medium {c.key === 'pid' ? 'px-4' : 'pr-4'} {c.width} {c.right ? 'text-right' : ''}">
              <button class="hover:text-ink {sortKey === c.key ? 'text-ink' : ''}" onclick={() => sortBy(c.key)} aria-pressed={sortKey === c.key}>
                {c.label}{sortKey === c.key ? ' ↓' : ''}
              </button>
            </th>
          {/each}
          <th class="hidden py-2 pr-4 font-medium md:table-cell">User</th>
        </tr>
      </thead>
      <tbody>
        {#each rows as p (p.pid)}
          <tr class="border-t border-line align-top" oncontextmenu={(e) => openMenu(e, menu(p))}>
            <td class="tabular px-4 py-1.5 text-ink-2">{p.pid}</td>
            <td class="max-w-md py-1.5 pr-4">
              <div class="truncate font-medium" title={p.name}>{p.name}</div>
              {#if p.cmd}<div class="truncate font-mono text-xs text-muted" title={p.cmd}>{p.cmd}</div>{/if}
            </td>
            <td class="tabular py-1.5 pr-4 text-right">{pct(p.cpu)}</td>
            <td class="tabular py-1.5 pr-4 text-right whitespace-nowrap">{bytes(p.mem)}</td>
            <td class="hidden max-w-40 truncate py-1.5 pr-4 text-ink-2 md:table-cell">{p.user ?? ''}</td>
          </tr>
        {:else}
          <tr><td colspan="5" class="px-4 py-4 text-muted">No process matches "{query}".</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<ReauthDialog
  bind:open={reauth}
  reason="Stopping a process needs your password again. It stays unlocked for 10 minutes."
  onconfirmed={() => pending?.()}
  oncancel={() => (pending = null)}
/>
