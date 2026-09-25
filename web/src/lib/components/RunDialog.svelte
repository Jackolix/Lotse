<script lang="ts">
  import { untrack } from 'svelte'
  import { api, type Script, type ScriptShell } from '../api'
  import { durationLabel, osLabel } from '../format'
  import { withReauth } from '../reauth.svelte'
  import { navigate } from '../router.svelte'
  import { cannotRun, shellLabel, shells, timeouts } from '../scripts'
  import { errorText } from '../systemActions'
  import { systems } from '../systems.svelte'
  import Modal from './Modal.svelte'
  import StatusDot from './StatusDot.svelte'

  // Runs a saved script, or a one-off command when script is null, on chosen systems.
  let {
    open = $bindable(false),
    script = null,
    preselect = [],
  }: { open?: boolean; script?: Script | null; preselect?: number[] } = $props()

  let shell = $state<ScriptShell>('sh')
  let content = $state('')
  let timeout = $state(60)
  let selected = $state<number[]>([])
  let error = $state('')
  let busy = $state(false)

  const effectiveShell = $derived(script?.shell ?? shell)
  const rows = $derived(systems.list.map((s) => ({ s, why: cannotRun(s, effectiveShell) })))
  const eligible = $derived(rows.filter((r) => !r.why).map((r) => r.s.id))
  const chosen = $derived(selected.filter((id) => eligible.includes(id)))

  $effect(() => {
    if (!open) return
    untrack(() => {
      error = ''
      content = ''
      timeout = 60
      const os = systems.get(preselect[0] ?? 0)?.info.os
      shell = os === 'windows' ? 'powershell' : 'sh'
      selected = preselect.length ? [...preselect] : []
    })
  })

  function toggleAll() {
    selected = chosen.length === eligible.length ? [] : [...eligible]
  }

  async function run(e: SubmitEvent) {
    e.preventDefault()
    if (!chosen.length) {
      error = 'Choose at least one system.'
      return
    }
    busy = true
    error = ''
    try {
      const body = script
        ? { script_id: script.id, systems: chosen }
        : { name: content.trim().split('\n')[0].slice(0, 64), shell, content, timeout, systems: chosen }
      const res = await withReauth('Running scripts needs your password again.', () => api.startRun(body))
      if (!res) return
      open = false
      navigate(`/runs/${res.id}`)
    } catch (err) {
      error = errorText(err)
    } finally {
      busy = false
    }
  }
</script>

<Modal bind:open title={script ? `Run "${script.name}"` : 'Run a command'} width="40rem">
  <form id="run-form" class="grid gap-4" onsubmit={run}>
    {#if script}
      <p class="text-sm text-ink-2">
        {shellLabel(script.shell)} · time limit {durationLabel(script.timeout)}{script.description ? ` · ${script.description}` : ''}
      </p>
      <pre class="max-h-40 overflow-auto rounded-lg bg-sunken p-3 font-mono text-xs leading-relaxed">{script.content}</pre>
    {:else}
      <div class="grid gap-3 sm:grid-cols-2">
        <label class="text-sm">
          <span class="mb-1 block text-ink-2">Shell</span>
          <select class="input" bind:value={shell}>
            {#each shells as s (s.shell)}<option value={s.shell}>{s.label}</option>{/each}
          </select>
        </label>
        <label class="text-sm">
          <span class="mb-1 block text-ink-2">Time limit</span>
          <select class="input" bind:value={timeout}>
            {#each timeouts as t (t)}<option value={t}>{durationLabel(t)}</option>{/each}
          </select>
        </label>
      </div>
      <label class="text-sm">
        <span class="mb-1 block text-ink-2">Command <span class="text-muted">· runs as root (Windows: SYSTEM)</span></span>
        <textarea
          class="input h-28 resize-y py-2 font-mono text-xs leading-relaxed"
          spellcheck="false"
          autocapitalize="off"
          required
          data-autofocus
          placeholder={shell === 'powershell' || shell === 'cmd' ? 'ipconfig /all' : 'df -h; uptime'}
          bind:value={content}
        ></textarea>
      </label>
    {/if}

    <div class="text-sm" role="group" aria-labelledby="run-systems">
      <div class="mb-1.5 flex items-center justify-between">
        <span id="run-systems" class="text-ink-2">Systems <span class="text-muted">· {chosen.length} of {eligible.length} available</span></span>
        <button type="button" class="text-xs text-accent hover:underline" onclick={toggleAll} disabled={!eligible.length}>
          {chosen.length === eligible.length && eligible.length ? 'Select none' : 'Select all'}
        </button>
      </div>
      <div class="max-h-60 overflow-auto rounded-lg border border-line">
        {#each rows as { s, why } (s.id)}
          <label class="flex items-center gap-2.5 border-b border-line px-3 py-2 last:border-b-0" class:opacity-50={!!why}>
            <input type="checkbox" class="size-4 accent-[var(--accent)]" value={s.id} disabled={!!why} bind:group={selected} />
            <StatusDot online={s.online} label={false} />
            <span class="min-w-0 flex-1 truncate">{s.name}</span>
            <span class="shrink-0 text-xs text-muted">{why || osLabel(s.info)}</span>
          </label>
        {:else}
          <p class="px-3 py-3 text-muted">No systems yet.</p>
        {/each}
      </div>
    </div>
    {#if error}<p class="text-sm text-critical" role="alert">{error}</p>{/if}
  </form>
  {#snippet footer()}
    <button type="button" class="btn" onclick={() => (open = false)}>Cancel</button>
    <button class="btn btn-primary" form="run-form" disabled={busy || !chosen.length}>
      Run on {chosen.length} system{chosen.length === 1 ? '' : 's'}
    </button>
  {/snippet}
</Modal>
