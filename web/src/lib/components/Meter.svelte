<script lang="ts">
  import { pct, severity } from '../format'

  // A usage bar. The fill color carries severity; the percentage is always shown
  // as text, so the state never depends on color alone.
  let { value, title = '' }: { value: number; title?: string } = $props()

  const clamped = $derived(Math.max(0, Math.min(100, Number.isFinite(value) ? value : 0)))
  const color = $derived(severity(clamped))
</script>

<div class="flex items-center gap-2" {title}>
  <div
    class="h-1.5 min-w-8 flex-1 rounded-full"
    style:background="color-mix(in oklab, {color} 18%, var(--surface))"
  >
    <div class="h-full rounded-full transition-[width] duration-500" style:width="{clamped}%" style:background={color}></div>
  </div>
  <span class="tabular w-11 shrink-0 text-right text-sm text-ink">{pct(value)}</span>
</div>
