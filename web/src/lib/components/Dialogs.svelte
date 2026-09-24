<script lang="ts">
  import { dialog, settle } from '../dialogs.svelte'

  let el: HTMLDialogElement
  let input = $state<HTMLInputElement>()

  $effect(() => {
    if (dialog.open && !el.open) {
      el.showModal()
      queueMicrotask(() => input?.select())
    } else if (!dialog.open && el.open) {
      el.close()
    }
  })

  function submit(e: SubmitEvent) {
    e.preventDefault()
    if (dialog.input && !dialog.value.trim()) return
    settle(dialog.input ? dialog.value.trim() : 'ok')
  }
</script>

<dialog
  bind:this={el}
  onclose={() => dialog.open && settle(null)}
  class="card m-auto w-[min(26rem,calc(100vw-2rem))] p-0 text-ink"
  aria-labelledby="dialog-title"
>
  <form class="p-5" onsubmit={submit}>
    <h2 id="dialog-title" class="text-base font-semibold">{dialog.title}</h2>
    {#if dialog.message}<p class="mt-2 text-sm whitespace-pre-line text-ink-2">{dialog.message}</p>{/if}
    {#if dialog.input}
      <input bind:this={input} class="input mt-4" maxlength="64" aria-label={dialog.title} bind:value={dialog.value} />
    {/if}
    <div class="mt-5 flex justify-end gap-2">
      <button type="button" class="btn" onclick={() => settle(null)}>Cancel</button>
      <button class="btn {dialog.danger ? 'btn-danger' : 'btn-primary'}">{dialog.confirmLabel}</button>
    </div>
  </form>
</dialog>
