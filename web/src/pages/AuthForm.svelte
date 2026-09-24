<script lang="ts">
  import { api, ApiError } from '../lib/api'
  import { signedIn } from '../lib/auth.svelte'
  import Icon from '../lib/components/Icon.svelte'

  // Login, or creation of the first account when the hub has none yet.
  let { setup = false }: { setup?: boolean } = $props()

  let username = $state('')
  let password = $state('')
  let confirm = $state('')
  let code = $state('')
  let needCode = $state(false) // two-factor login: second step
  let error = $state('')
  let busy = $state(false)

  async function submit(e: SubmitEvent) {
    e.preventDefault()
    error = ''
    if (setup && password !== confirm) {
      error = 'The passwords do not match.'
      return
    }
    busy = true
    try {
      signedIn(setup ? await api.setup(username, password) : await api.login(username, password, code))
    } catch (err) {
      if (err instanceof ApiError && err.body.totp_required && !needCode) {
        needCode = true // password was right; ask for the code without an error
      } else {
        error = err instanceof ApiError ? err.message : 'Cannot reach the hub.'
      }
      code = ''
    } finally {
      busy = false
    }
  }
</script>

<main class="grid min-h-dvh place-items-center px-4 py-10">
  <form class="card w-full max-w-sm p-6" onsubmit={submit}>
    <div class="mb-6 flex items-center gap-2 font-semibold">
      <span class="grid size-8 place-items-center rounded-lg bg-accent text-white"><Icon name="activity" /></span>
      Lotse
    </div>
    <h1 class="text-lg font-semibold">
      {setup ? 'Create the admin account' : needCode ? 'Two-factor login' : 'Sign in'}
    </h1>
    {#if setup}
      <p class="mt-1 text-sm text-ink-2">This hub has no accounts yet. The first account you create is the administrator.</p>
    {/if}

    {#if needCode}
      <p class="mt-1 text-sm text-ink-2">Enter the 6-digit code from your authenticator app.</p>
      <label class="mt-5 block text-sm">
        <span class="mb-1 block text-ink-2">Code</span>
        <!-- svelte-ignore a11y_autofocus -->
        <input
          class="input tabular tracking-widest"
          inputmode="numeric"
          autocomplete="one-time-code"
          pattern="[0-9 ]*"
          maxlength="7"
          required
          autofocus
          bind:value={code}
        />
      </label>
    {:else}
      <label class="mt-5 block text-sm">
        <span class="mb-1 block text-ink-2">Username</span>
        <input class="input" autocomplete="username" required maxlength="64" bind:value={username} />
      </label>
      <label class="mt-3 block text-sm">
        <span class="mb-1 block text-ink-2">Password</span>
        <input
          class="input"
          type="password"
          autocomplete={setup ? 'new-password' : 'current-password'}
          required
          minlength={setup ? 8 : undefined}
          bind:value={password}
        />
      </label>
      {#if setup}
        <label class="mt-3 block text-sm">
          <span class="mb-1 block text-ink-2">Confirm password</span>
          <input class="input" type="password" autocomplete="new-password" required bind:value={confirm} />
        </label>
        <p class="mt-1.5 text-xs text-muted">At least 8 characters.</p>
      {/if}
    {/if}

    {#if error}<p class="mt-3 text-sm text-critical" role="alert">{error}</p>{/if}
    <button class="btn btn-primary mt-5 w-full justify-center" disabled={busy}>
      {setup ? 'Create account' : needCode ? 'Verify' : 'Sign in'}
    </button>
    {#if needCode}
      <button type="button" class="mt-2 w-full text-center text-sm text-ink-2 hover:text-ink" onclick={() => ((needCode = false), (error = ''))}>
        Back
      </button>
    {/if}
  </form>
</main>
