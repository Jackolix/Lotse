<script lang="ts">
  import type { Battery } from '../api'

  // A battery glyph filled to its charge. The fill color flags a low or charging
  // battery; the percentage is always shown as text, so the state never depends on
  // color alone.
  let { battery, label = false }: { battery: Battery; label?: boolean } = $props()

  const STATES: Record<string, string> = { charging: 'charging', discharging: 'on battery', idle: 'plugged in' }

  const percent = $derived(Math.max(0, Math.min(100, Math.round(battery.percent))))
  const charging = $derived(battery.state === 'charging')
  const state = $derived(STATES[battery.state] ?? battery.state)
  const color = $derived.by(() => {
    if (charging) return 'var(--good)'
    if (battery.state !== 'discharging') return 'currentColor'
    if (percent <= 10) return 'var(--critical)'
    if (percent <= 20) return 'var(--warning)'
    return 'currentColor'
  })
</script>

<span class="tabular inline-flex shrink-0 items-center gap-1 text-xs text-ink-2" title="Battery {percent}%, {state}">
  {#if charging}
    <svg width="8" height="12" viewBox="0 0 8 12" fill="var(--good)" class="-mr-0.5 shrink-0" aria-hidden="true">
      <path d="M5 0 0 7h3.2L2.6 12 8 4.8H4.6z" />
    </svg>
  {/if}
  <svg width="22" height="12" viewBox="0 0 22 12" fill="none" class="shrink-0" aria-hidden="true">
    <rect x="0.75" y="0.75" width="18" height="10.5" rx="3" stroke="currentColor" stroke-width="1.5" opacity="0.55" />
    <path d="M21 4.25v3.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" opacity="0.55" />
    <rect x="2.75" y="2.75" width={(14 * percent) / 100} height="6.5" rx="1.25" fill={color} />
  </svg>
  <span><span class="sr-only">Battery</span> {percent}%{#if label}<span class="ml-1 text-muted">· {state}</span>{/if}</span>
</span>
