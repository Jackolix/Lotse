<script lang="ts">
  import { onMount } from 'svelte'
  import { api, ApiError, type Run, type RunTarget } from '../lib/api'
  import Icon from '../lib/components/Icon.svelte'
  import { confirmAction } from '../lib/dialogs.svelte'
  import { dateTime, durationLabel } from '../lib/format'
  import { withReauth } from '../lib/reauth.svelte'
  import { link, navigate } from '../lib/router.svelte'
  import { shellLabel, statusColor, statusLabel } from '../lib/scripts'
  import { copy, errorText } from '../lib/systemActions'
  import { systems } from '../lib/systems.svelte'
  import { toast } from '../lib/toast.svelte'

  let { id }: { id: number } = $props()

  // The hub keeps the last 128 KiB of output per system, and so does the page: a
  // chatty script would otherwise slow the tab down more and more. Trimming at twice
  // that keeps it from copying the whole output on every chunk.
  const MAX_OUTPUT = 128 * 1024

  let run = $state<Run | null>(null)
  let error = $state('')
  let collapsed = $state<Record<number, boolean>>({})

  onMount(() => {
    // The stream starts with the whole run, then sends status changes and output.
    const es = new EventSource(`/api/runs/${id}/events`)
    const target = (systemId: number) => run?.targets.find((t) => t.system_id === systemId)
    es.addEventListener('run', (e) => {
      run = JSON.parse(e.data) as Run
      // With many systems, only failures start expanded.
      if (run.targets.length > 3) for (const t of run.targets) collapsed[t.system_id] = t.status !== 'failed'
    })
    es.addEventListener('target', (e) => {
      const next = JSON.parse(e.data) as RunTarget
      const t = target(next.system_id)
      if (t) Object.assign(t, { ...next, output: t.output })
      if (next.status === 'failed') collapsed[next.system_id] = false
    })
    es.addEventListener('output', (e) => {
      const { system_id, data } = JSON.parse(e.data) as { system_id: number; data: string }
      const t = target(system_id)
      if (!t) return
      let output = t.output + data
      if (output.length > 2 * MAX_OUTPUT) {
        output = output.slice(-MAX_OUTPUT)
        if (/^[\uDC00-\uDFFF]/.test(output)) output = output.slice(1) // half of a split emoji
        t.truncated = true
      }
      t.output = output
    })
    es.addEventListener('done', (e) => {
      const { finished_at } = JSON.parse(e.data) as { finished_at: number | null }
      if (run && finished_at) run.finished_at = finished_at
      es.close()
    })
    es.onerror = () => {
      if (run) return // the browser reconnects and the hub resends the full run
      es.close()
      api
        .run(id)
        .then((r) => (run = r))
        .catch((err) => (error = errorText(err)))
    }
    return () => es.close()
  })

  // Terminal escape sequences (colors) are removed; a carriage return overwrites the line.
  function clean(output: string): string {
    return output
      .replace(/\x1b\[[0-9;?]*[ -/]*[@-~]/g, '')
      .split('\n')
      .map((line) => line.slice(line.lastIndexOf('\r', line.length - 2) + 1).replace(/\r$/, ''))
      .join('\n')
  }

  const running = $derived(!!run && !run.finished_at)
  const summary = $derived.by(() => {
    const c: Record<string, number> = {}
    for (const t of run?.targets ?? []) c[t.status] = (c[t.status] ?? 0) + 1
    return Object.entries(c) as [RunTarget['status'], number][]
  })

  async function cancel() {
    if (!(await confirmAction({ title: 'Stop this run?', message: 'Scripts still running are killed.', confirmLabel: 'Stop', danger: true }))) return
    try {
      await api.cancelRun(id)
    } catch (err) {
      toast(errorText(err), 'error')
    }
  }

  // Runs the saved script again (in its current version), or this run's copy of it
  // when it was a one-off command or has been deleted since.
  async function again() {
    if (!run) return
    const r = run
    const ids = r.targets.map((t) => t.system_id)
    const copyOfRun = { name: r.name, shell: r.shell, content: r.content, timeout: r.timeout, systems: ids }
    try {
      const res = await withReauth('Running scripts needs your password again.', async () => {
        if (r.script_id == null) return api.startRun(copyOfRun)
        try {
          return await api.startRun({ script_id: r.script_id, systems: ids })
        } catch (err) {
          if (err instanceof ApiError && err.status === 404) return api.startRun(copyOfRun)
          throw err
        }
      })
      if (res) navigate(`/runs/${res.id}`)
    } catch (err) {
      toast(errorText(err), 'error')
    }
  }

  // Keeps an output box scrolled to the end while new lines arrive, unless the
  // user scrolled up to read.
  function follow(node: HTMLElement, _output: string) {
    let pinned = true
    const onScroll = () => (pinned = node.scrollTop + node.clientHeight >= node.scrollHeight - 8)
    node.addEventListener('scroll', onScroll)
    node.scrollTop = node.scrollHeight
    return {
      update() {
        if (pinned) node.scrollTop = node.scrollHeight
      },
      destroy: () => node.removeEventListener('scroll', onScroll),
    }
  }

  const took = (t: RunTarget) =>
    t.started_at && t.finished_at ? durationLabel(Math.max(0, t.finished_at - t.started_at)) : ''
</script>

<a href="/scripts" onclick={link} class="inline-flex items-center gap-1 text-sm text-ink-2 hover:text-ink">
  <Icon name="arrow-left" size={14} /> Scripts
</a>

{#if error}
  <p class="mt-6 text-sm text-critical" role="alert">{error}</p>
{:else if !run}
  <p class="mt-6 text-sm text-muted">Loading…</p>
{:else}
  <section class="card mt-3 p-5">
    <div class="flex flex-wrap items-start gap-3">
      <div class="mr-auto min-w-0">
        <h1 class="truncate text-xl font-semibold">{run.name}</h1>
        <p class="mt-1 text-sm text-ink-2">
          {shellLabel(run.shell)} · started {dateTime(run.started_at)} by {run.username} · time limit {durationLabel(run.timeout)}
        </p>
      </div>
      <div class="flex gap-2">
        {#if running}
          <button class="btn btn-danger" onclick={cancel}><Icon name="square" size={12} /> Stop</button>
        {:else}
          <button class="btn" onclick={again}><Icon name="refresh" size={14} /> Run again</button>
        {/if}
      </div>
    </div>
    <div class="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-sm">
      {#if !running && run.finished_at}
        <span class="text-ink-2">Finished {dateTime(run.finished_at)}</span>
      {/if}
      {#each summary as [status, n] (status)}
        <span class="inline-flex items-center gap-1.5">
          <span class="size-2 rounded-full {status === 'running' ? 'animate-pulse' : ''}" style:background={statusColor[status]}></span>
          {statusLabel[status]}: {n}
        </span>
      {/each}
    </div>
    <details class="mt-3 text-sm">
      <summary class="cursor-pointer text-ink-2 hover:text-ink">Script</summary>
      <pre class="mt-2 max-h-64 overflow-auto rounded-lg bg-sunken p-3 font-mono text-xs leading-relaxed">{run.content}</pre>
    </details>
  </section>

  <div class="mt-4 grid gap-3">
    {#each run.targets as t (t.system_id)}
      {@const open = !collapsed[t.system_id]}
      <section class="card overflow-hidden">
        <div class="flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2.5 text-sm">
          <button
            class="flex min-w-0 items-center gap-2 text-left"
            onclick={() => (collapsed[t.system_id] = open)}
            aria-expanded={open}
          >
            <Icon name="chevron-down" size={14} class="text-muted transition-transform {open ? '' : '-rotate-90'}" />
            <span class="size-2 shrink-0 rounded-full {t.status === 'running' ? 'animate-pulse' : ''}" style:background={statusColor[t.status]}></span>
            <span class="truncate font-medium">{t.system_name}</span>
          </button>
          <span class="text-ink-2">
            {statusLabel[t.status]}{t.exit_code != null ? ` · exit status ${t.exit_code}` : ''}{took(t) ? ` · ${took(t)}` : ''}
          </span>
          {#if t.error}<span class="text-ink-2">· {t.error}</span>{/if}
          <span class="ml-auto flex gap-1">
            {#if systems.get(t.system_id)}
              <a class="btn h-7 px-2 text-xs" href="/systems/{t.system_id}" onclick={link}>Open system</a>
            {/if}
            <button class="btn h-7 px-2 text-xs" onclick={() => copy(clean(t.output), 'Output')} disabled={!t.output}>
              <Icon name="copy" size={12} /> Copy
            </button>
          </span>
        </div>
        {#if open && (t.output || t.status === 'running')}
          <pre
            use:follow={t.output}
            class="max-h-96 overflow-auto border-t border-line bg-[#1a1a19] px-4 py-3 font-mono text-xs leading-relaxed whitespace-pre-wrap text-[#e8e7e1]">{#if t.truncated}<span class="text-[#898781]">… earlier output was dropped (only the last 128 KiB are kept)
</span>{/if}{clean(t.output) || (t.status === 'running' ? 'Waiting for output…' : '')}</pre>
        {/if}
      </section>
    {/each}
  </div>
{/if}
