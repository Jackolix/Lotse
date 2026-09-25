<script lang="ts">
  import { untrack } from 'svelte'
  import { api, ApiError, type AlertMetric, type AlertRule, type Notifier } from '../api'
  import { durationLabel, isPercentMetric, metricLabel } from '../format'
  import { systems } from '../systems.svelte'
  import { toast } from '../toast.svelte'
  import Modal from './Modal.svelte'

  let {
    open = $bindable(false),
    rule = null,
    notifiers,
    onsaved,
  }: { open?: boolean; rule?: AlertRule | null; notifiers: Notifier[]; onsaved?: () => void } = $props()

  const metrics: AlertMetric[] = ['offline', 'cpu', 'memory', 'disk', 'load']
  const durations = [0, 30, 60, 120, 300, 600, 900, 1800, 3600]

  let name = $state('')
  let metric = $state<AlertMetric>('cpu')
  let threshold = $state(90)
  let duration = $state(300)
  let systemId = $state<number | null>(null)
  let selected = $state<number[]>([])
  let enabled = $state(true)
  let error = $state('')
  let busy = $state(false)

  $effect(() => {
    if (!open) return
    untrack(() => {
      name = rule?.name ?? ''
      metric = rule?.metric ?? 'cpu'
      threshold = rule?.threshold ?? 90
      duration = rule?.duration ?? 300
      systemId = rule?.system_id ?? null
      selected = [...(rule?.notifiers ?? [])]
      enabled = rule?.enabled ?? true
      error = ''
    })
  })

  // Offline rules need a grace period; very short blips are just reconnects.
  const choices = $derived(metric === 'offline' ? durations.filter((d) => d >= 30) : durations)
  $effect(() => {
    if (!choices.includes(duration)) duration = choices.find((d) => d >= duration) ?? choices.at(-1)!
  })

  async function save(e: SubmitEvent) {
    e.preventDefault()
    busy = true
    error = ''
    try {
      await api.saveAlertRule({
        id: rule?.id,
        name,
        metric,
        threshold: metric === 'offline' ? 0 : Number(threshold),
        duration,
        system_id: systemId,
        notifiers: selected,
        enabled,
      })
      toast(rule ? 'Rule saved' : 'Rule added', 'success', 2500)
      open = false
      onsaved?.()
    } catch (err) {
      error = err instanceof ApiError ? err.message : 'Cannot reach the hub.'
    } finally {
      busy = false
    }
  }
</script>

<Modal bind:open title={rule ? 'Edit alert rule' : 'New alert rule'}>
  <form id="rule-form" class="grid gap-4" onsubmit={save}>
    <div class="grid gap-3 sm:grid-cols-2">
      <label class="text-sm">
        <span class="mb-1 block text-ink-2">Watch</span>
        <select class="input" bind:value={metric}>
          {#each metrics as m (m)}
            <option value={m}>{m === 'offline' ? 'System goes offline' : `${metricLabel(m)} ${m === 'load' ? 'average (1 min)' : 'usage'}`}</option>
          {/each}
        </select>
      </label>
      {#if metric !== 'offline'}
        <label class="text-sm">
          <span class="mb-1 block text-ink-2">Alert above</span>
          <div class="flex items-center gap-2">
            <input
              class="input tabular"
              type="number"
              min={isPercentMetric(metric) ? 1 : 0.1}
              max={isPercentMetric(metric) ? 99 : undefined}
              step={isPercentMetric(metric) ? 1 : 0.1}
              required
              bind:value={threshold}
            />
            {#if isPercentMetric(metric)}<span class="text-ink-2">%</span>{/if}
          </div>
        </label>
      {/if}
      <label class="text-sm">
        <span class="mb-1 block text-ink-2">{metric === 'offline' ? 'After being offline for' : 'For at least'}</span>
        <select class="input" bind:value={duration}>
          {#each choices as d (d)}
            <option value={d}>{d === 0 ? 'Immediately' : durationLabel(d)}</option>
          {/each}
        </select>
      </label>
      <label class="text-sm">
        <span class="mb-1 block text-ink-2">Applies to</span>
        <select class="input" bind:value={systemId}>
          <option value={null}>All systems</option>
          {#each systems.list as s (s.id)}
            <option value={s.id}>{s.name}</option>
          {/each}
        </select>
      </label>
    </div>

    <label class="text-sm">
      <span class="mb-1 block text-ink-2">Name</span>
      <input class="input" maxlength="64" placeholder={metricLabel(metric)} bind:value={name} />
    </label>

    <fieldset class="text-sm">
      <legend class="mb-1.5 text-ink-2">Notify via</legend>
      {#if notifiers.length === 0}
        <p class="text-muted">No notification channels yet. The alert still shows up in Lotse.</p>
      {:else}
        <div class="grid gap-1.5 sm:grid-cols-2">
          {#each notifiers as n (n.id)}
            <label class="flex items-center gap-2">
              <input type="checkbox" class="size-4 accent-[var(--accent)]" value={n.id} bind:group={selected} />
              <span>{n.name}</span>
              <span class="text-xs text-muted">{n.type}</span>
            </label>
          {/each}
        </div>
      {/if}
    </fieldset>

    <label class="flex items-center gap-2 text-sm">
      <input type="checkbox" role="switch" class="size-4 accent-[var(--accent)]" bind:checked={enabled} /> Enabled
    </label>
    {#if error}<p class="text-sm text-critical" role="alert">{error}</p>{/if}
  </form>

  {#snippet footer()}
    <button type="button" class="btn" onclick={() => (open = false)}>Cancel</button>
    <button class="btn btn-primary" form="rule-form" disabled={busy}>{rule ? 'Save' : 'Add rule'}</button>
  {/snippet}
</Modal>
