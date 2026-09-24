<script lang="ts">
  import { dismiss, toasts } from '../toast.svelte'
  import Icon from './Icon.svelte'

  const style = {
    success: { icon: 'check', color: 'var(--good)' },
    error: { icon: 'x', color: 'var(--critical)' },
    info: { icon: 'activity', color: 'var(--accent)' },
  } as const
</script>

<div class="pointer-events-none fixed right-4 bottom-4 z-50 flex w-[min(24rem,calc(100vw-2rem))] flex-col gap-2" role="status" aria-live="polite">
  {#each toasts as t (t.id)}
    <div class="card pointer-events-auto flex items-start gap-2.5 px-3.5 py-2.5 text-sm shadow-[0_8px_30px_rgb(0_0_0/0.2)]">
      <span class="mt-0.5 grid size-4 shrink-0 place-items-center rounded-full text-white" style:background={style[t.kind].color}>
        <Icon name={style[t.kind].icon} size={10} />
      </span>
      <p class="min-w-0 flex-1 text-ink">{t.message}</p>
      <button class="-mr-1 text-muted hover:text-ink" onclick={() => dismiss(t.id)} aria-label="Dismiss"><Icon name="x" size={14} /></button>
    </div>
  {/each}
</div>
