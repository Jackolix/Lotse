<script lang="ts">
  import type { Snippet } from 'svelte'
  import Icon from './Icon.svelte'

  let {
    open = $bindable(false),
    title,
    width = '32rem',
    children,
    footer,
  }: { open?: boolean; title: string; width?: string; children: Snippet; footer?: Snippet } = $props()

  let el: HTMLDialogElement

  // Fields marked data-autofocus get the focus each time the dialog opens; the
  // autofocus attribute only works once, when the always-mounted dialog is created.
  $effect(() => {
    if (open && !el.open) {
      el.showModal()
      queueMicrotask(() => el.querySelector<HTMLElement>('[data-autofocus]')?.focus())
    } else if (!open && el.open) el.close()
  })
</script>

<dialog
  bind:this={el}
  onclose={() => (open = false)}
  class="card m-auto max-h-[calc(100dvh-2rem)] overflow-auto p-0 text-ink"
  style:width="min({width}, calc(100vw - 2rem))"
  aria-label={title}
>
  <div class="flex items-center justify-between gap-4 border-b border-line px-5 py-3">
    <h2 class="text-base font-semibold">{title}</h2>
    <button class="btn -mr-2 h-8 px-2" onclick={() => (open = false)} aria-label="Close"><Icon name="x" /></button>
  </div>
  <div class="p-5">{@render children()}</div>
  {#if footer}
    <div class="flex flex-wrap items-center justify-end gap-2 border-t border-line px-5 py-3">{@render footer()}</div>
  {/if}
</dialog>
