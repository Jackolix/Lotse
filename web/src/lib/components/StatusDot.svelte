<script lang="ts">
  let { online, label = true }: { online: boolean; label?: boolean } = $props()
  const text = $derived(online ? 'Online' : 'Offline')
</script>

<span class="inline-flex items-center gap-1.5 text-xs font-medium {online ? 'text-good-ink' : 'text-muted'}" title={text}>
  <span class="size-2 shrink-0 rounded-full" style:background={online ? 'var(--good)' : 'var(--muted)'}></span>
  {#if label}{text}{:else}<span class="sr-only">{text}</span>{/if}
</span>
