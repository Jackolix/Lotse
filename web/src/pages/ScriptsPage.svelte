<script lang="ts">
  import { onMount } from 'svelte'
  import { api, type Run, type Script } from '../lib/api'
  import Icon from '../lib/components/Icon.svelte'
  import RunDialog from '../lib/components/RunDialog.svelte'
  import ScriptDialog from '../lib/components/ScriptDialog.svelte'
  import { confirmAction } from '../lib/dialogs.svelte'
  import { ago, dateTime, durationLabel } from '../lib/format'
  import { openMenu, type MenuEntry } from '../lib/menu.svelte'
  import { withReauth } from '../lib/reauth.svelte'
  import { link, navigate, param } from '../lib/router.svelte'
  import { shellLabel, statusColor } from '../lib/scripts'
  import { errorText } from '../lib/systemActions'
  import { systems } from '../lib/systems.svelte'
  import { toast } from '../lib/toast.svelte'

  let scripts = $state<Script[]>([])
  let runs = $state<Run[]>([])
  let loaded = $state(false)
  let editOpen = $state(false)
  let editing = $state<Partial<Script> | null>(null)
  let runOpen = $state(false)
  let running = $state<Script | null>(null)
  let query = $state('')

  // "Run a script…" on a system opens this page with ?system=ID.
  const forSystem = $derived(Number(param('system')) || 0)
  const preselect = $derived(forSystem ? [forSystem] : [])

  async function load() {
    try {
      ;[scripts, runs] = await Promise.all([api.scripts(), api.runs()])
    } catch (err) {
      toast(errorText(err), 'error')
    } finally {
      loaded = true
    }
  }

  onMount(() => {
    load()
    // Keep the status of recent runs current while any is going.
    const timer = setInterval(() => {
      if (!document.hidden && runs.some((r) => !r.finished_at)) api.runs().then((r) => (runs = r)).catch(() => {})
    }, 3000)
    return () => clearInterval(timer)
  })

  const shown = $derived.by(() => {
    const q = query.trim().toLowerCase()
    return q ? scripts.filter((s) => `${s.name} ${s.description} ${s.content}`.toLowerCase().includes(q)) : scripts
  })

  function edit(s: Partial<Script> | null) {
    editing = s
    editOpen = true
  }

  function run(s: Script | null) {
    running = s
    runOpen = true
  }

  async function remove(s: Script) {
    const ok = await confirmAction({
      title: `Delete "${s.name}"?`,
      message: 'Past runs and their output stay in the history.',
      confirmLabel: 'Delete',
      danger: true,
    })
    if (!ok) return
    try {
      const deleted = await withReauth('Deleting scripts needs your password again.', () => api.deleteScript(s.id).then(() => true))
      if (!deleted) return
      toast('Script deleted', 'success', 2500)
      await load()
    } catch (err) {
      toast(errorText(err), 'error')
    }
  }

  const menu = (s: Script): MenuEntry[] => [
    { label: 'Run…', icon: 'play', action: () => run(s) },
    { label: 'Edit…', icon: 'pencil', action: () => edit(s) },
    { label: 'Duplicate…', icon: 'copy', action: () => edit({ ...s, id: undefined, name: `${s.name} (copy)` }) },
    'separator',
    { label: 'Delete…', icon: 'trash', danger: true, action: () => remove(s) },
  ]

  function counts(r: Run) {
    const c = { ok: 0, failed: 0, other: 0, running: 0 }
    for (const t of r.targets) {
      if (t.status === 'done') c.ok++
      else if (t.status === 'failed') c.failed++
      else if (t.status === 'running' || t.status === 'pending') c.running++
      else c.other++
    }
    return c
  }
</script>

<div class="mb-5 flex flex-wrap items-end gap-3">
  <div class="mr-auto">
    <h1 class="text-xl font-semibold">Scripts</h1>
    <p class="text-sm text-ink-2">Run commands on many machines at once. Output and exit codes are kept for 90 days.</p>
  </div>
  <button class="btn" onclick={() => run(null)}><Icon name="terminal" size={14} /> Run a command</button>
  <button class="btn btn-primary" onclick={() => edit(null)}><Icon name="plus" /> New script</button>
</div>

{#if forSystem}
  <p
    class="mb-4 flex items-center gap-2 rounded-lg px-3 py-2 text-sm"
    style:background="color-mix(in oklab, var(--accent) 10%, var(--surface))"
  >
    <Icon name="server" size={14} class="text-accent" />
    <span>Choose a script to run on <strong class="font-medium">{systems.get(forSystem)?.name ?? 'the system'}</strong>, or run a command.</span>
    <a class="ml-auto text-xs text-ink-2 hover:text-ink" href="/scripts" onclick={link}>Clear</a>
  </p>
{/if}

<section class="card">
  <div class="flex flex-wrap items-center gap-3 px-4 pt-4">
    <h2 class="font-semibold">Saved scripts</h2>
    {#if scripts.length > 5}
      <label class="relative ml-auto block w-full sm:w-56">
        <span class="sr-only">Filter scripts</span>
        <Icon name="search" size={14} class="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-muted" />
        <input class="input h-8 pl-8" placeholder="Filter" bind:value={query} />
      </label>
    {/if}
  </div>
  <ul class="mt-3">
    {#each shown as s (s.id)}
      <li class="flex items-center gap-3 border-t border-line px-4 py-2.5 text-sm" oncontextmenu={(e) => openMenu(e, menu(s))}>
        <span class="grid size-8 shrink-0 place-items-center rounded-lg bg-sunken text-ink-2"><Icon name="code" size={14} /></span>
        <div class="min-w-0 flex-1">
          <div class="truncate font-medium">{s.name}</div>
          <div class="truncate text-xs text-ink-2">
            {shellLabel(s.shell)} · up to {durationLabel(s.timeout)}{s.description ? ` · ${s.description}` : ''}
          </div>
        </div>
        <span class="hidden text-xs text-muted md:inline">{s.created_by ? `by ${s.created_by} · ` : ''}changed {ago(s.updated_at)}</span>
        <button class="btn h-8" onclick={() => run(s)}><Icon name="play" size={12} /> Run</button>
        <button class="btn h-8 px-2" onclick={(e) => openMenu(e, menu(s))} aria-label="More for {s.name}">
          <Icon name="chevron-down" size={14} />
        </button>
      </li>
    {:else}
      <li class="border-t border-line px-4 py-6 text-sm text-muted">
        {#if !loaded}
          Loading…
        {:else if query}
          No script matches "{query}".
        {:else}
          No saved scripts yet. Save the commands you run often, like updating packages or clearing caches.
        {/if}
      </li>
    {/each}
  </ul>
</section>

<section class="card mt-4 overflow-hidden">
  <h2 class="px-4 pt-4 font-semibold">Recent runs</h2>
  <div class="overflow-x-auto">
    <table class="mt-2 w-full text-sm">
      <thead class="text-left text-xs text-muted">
        <tr>
          <th class="px-4 py-2 font-medium whitespace-nowrap">Started</th>
          <th class="py-2 pr-4 font-medium">Script</th>
          <th class="hidden py-2 pr-4 font-medium sm:table-cell">By</th>
          <th class="py-2 pr-4 font-medium">Result</th>
        </tr>
      </thead>
      <tbody>
        {#each runs as r (r.id)}
          {@const c = counts(r)}
          <tr class="cursor-pointer border-t border-line hover:bg-sunken" onclick={() => navigate(`/runs/${r.id}`)}>
            <td class="tabular px-4 py-2 whitespace-nowrap text-ink-2">
              <a href="/runs/{r.id}" onclick={link} class="hover:underline">{dateTime(r.started_at)}</a>
            </td>
            <td class="max-w-72 truncate py-2 pr-4">{r.name}</td>
            <td class="hidden py-2 pr-4 text-ink-2 sm:table-cell">{r.username}</td>
            <td class="py-2 pr-4 whitespace-nowrap">
              {#if c.running}
                <span class="inline-flex items-center gap-1.5 text-accent">
                  <span class="size-1.5 animate-pulse rounded-full bg-accent"></span> Running on {c.running}
                </span>
              {:else}
                {#if c.ok}<span class="mr-2" style:color={statusColor.done}>✓ {c.ok}</span>{/if}
                {#if c.failed}<span class="mr-2" style:color={statusColor.failed}>✗ {c.failed}</span>{/if}
                {#if c.other}<span class="text-muted">{c.other} skipped</span>{/if}
              {/if}
            </td>
          </tr>
        {:else}
          <tr><td colspan="4" class="px-4 py-4 text-muted">{loaded ? 'Nothing has run yet.' : 'Loading…'}</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
</section>

<ScriptDialog bind:open={editOpen} script={editing} onsaved={load} />
<RunDialog bind:open={runOpen} script={running} {preselect} />
