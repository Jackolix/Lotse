<script lang="ts">
  import { untrack } from 'svelte'
  import { api, ApiError } from '../api'
  import { auth } from '../auth.svelte'
  import Icon from './Icon.svelte'

  // Asks for the password (and two-factor code) again before a sensitive action,
  // like sudo. The hub then allows such actions for 10 minutes.
  let {
    open = $bindable(false),
    reason = 'Confirm your password to continue.',
    onconfirmed,
    oncancel,
  }: { open?: boolean; reason?: string; onconfirmed?: () => void; oncancel?: () => void } = $props()

  let dialog: HTMLDialogElement
  let password = $state('')
  let code = $state('')
  let error = $state('')
  let busy = $state(false)
  let confirmed = false

  $effect(() => {
    if (open) {
      if (!dialog.open) dialog.showModal()
      untrack(() => {
        password = ''
        code = ''
        error = ''
        confirmed = false
      })
    } else if (dialog.open) {
      dialog.close()
    }
  })

  async function submit(e: SubmitEvent) {
    e.preventDefault()
    busy = true
    error = ''
    try {
      const { elevated_until } = await api.elevate(password, code)
      if (auth.user) auth.user.elevated_until = elevated_until
      confirmed = true
      open = false
      onconfirmed?.()
    } catch (err) {
      error = err instanceof ApiError ? err.message : 'Cannot reach the hub.'
      code = ''
    } finally {
      busy = false
    }
  }

  function closed() {
    open = false
    if (!confirmed) oncancel?.()
  }
</script>

<dialog
  bind:this={dialog}
  onclose={closed}
  class="card m-auto w-[min(24rem,calc(100vw-2rem))] p-0 text-ink"
  aria-labelledby="reauth-title"
>
  <form class="p-5" onsubmit={submit}>
    <div class="flex items-center gap-2">
      <span class="grid size-8 place-items-center rounded-lg bg-sunken text-ink-2"><Icon name="lock" /></span>
      <h2 id="reauth-title" class="text-base font-semibold">Confirm it's you</h2>
    </div>
    <p class="mt-2 text-sm text-ink-2">{reason}</p>
    <label class="mt-4 block text-sm">
      <span class="mb-1 block text-ink-2">Password</span>
      <!-- svelte-ignore a11y_autofocus -->
      <input class="input" type="password" autocomplete="current-password" required autofocus bind:value={password} />
    </label>
    {#if auth.user?.totp}
      <label class="mt-3 block text-sm">
        <span class="mb-1 block text-ink-2">Two-factor code</span>
        <input
          class="input tabular tracking-widest"
          inputmode="numeric"
          autocomplete="one-time-code"
          maxlength="7"
          required
          bind:value={code}
        />
      </label>
    {/if}
    {#if error}<p class="mt-3 text-sm text-critical" role="alert">{error}</p>{/if}
    <div class="mt-5 flex justify-end gap-2">
      <button type="button" class="btn" onclick={() => (open = false)}>Cancel</button>
      <button class="btn btn-primary" disabled={busy}>Confirm</button>
    </div>
  </form>
</dialog>
