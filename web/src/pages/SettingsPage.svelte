<script lang="ts">
  import { onMount } from 'svelte'
  import { api, ApiError, type Passkey, type TOTPSetup } from '../lib/api'
  import { auth, refreshUser, roleLabel } from '../lib/auth.svelte'
  import Icon from '../lib/components/Icon.svelte'
  import { ask, confirmAction } from '../lib/dialogs.svelte'
  import { ago } from '../lib/format'
  import { createPasskey, passkeysSupported } from '../lib/passkey'
  import { withReauth } from '../lib/reauth.svelte'
  import { toast } from '../lib/toast.svelte'

  const message = (err: unknown) => (err instanceof ApiError ? err.message : 'Cannot reach the hub.')

  // ---- passkeys ----
  let passkeys = $state<Passkey[]>([])
  let pkBusy = $state(false)
  const supported = passkeysSupported()
  const here = location.hostname

  async function loadPasskeys() {
    try {
      passkeys = await api.passkeys()
    } catch {
      // 401 handled globally
    }
  }
  onMount(loadPasskeys)

  function deviceName() {
    const ua = navigator.userAgent
    const os = /iPhone|iPad/.test(ua) ? 'iPhone' : /Android/.test(ua) ? 'Android' : /Mac OS X/.test(ua) ? 'Mac' : /Windows/.test(ua) ? 'Windows' : 'Linux'
    const browser = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : 'browser'
    return `${browser} on ${os}`
  }

  async function addPasskey() {
    pkBusy = true
    try {
      const added = await withReauth('Adding a passkey needs your password again.', async () => {
        const credential = await createPasskey(await api.passkeyOptions('register'))
        return api.addPasskey(deviceName(), credential)
      })
      if (!added) return
      toast(`Passkey "${added.name}" added. You can now sign in with it.`, 'success', 5000)
      await Promise.all([loadPasskeys(), refreshUser()])
    } catch (err) {
      toast(err instanceof Error ? err.message : 'Could not add the passkey.', 'error')
    } finally {
      pkBusy = false
    }
  }

  async function renamePasskey(p: Passkey) {
    const name = await ask({ title: 'Rename passkey', value: p.name, confirmLabel: 'Rename' })
    if (!name || name === p.name) return
    try {
      await api.renamePasskey(p.id, name)
      await loadPasskeys()
    } catch (err) {
      toast(message(err), 'error')
    }
  }

  async function removePasskey(p: Passkey) {
    const ok = await confirmAction({
      title: `Remove "${p.name}"?`,
      message: 'It can no longer sign in to this hub. Also delete it in your password manager or device settings.',
      confirmLabel: 'Remove',
      danger: true,
    })
    if (!ok) return
    try {
      if (!(await withReauth('Removing a passkey needs your password again.', () => api.deletePasskey(p.id).then(() => true)))) return
      toast('Passkey removed', 'success', 2500)
      await Promise.all([loadPasskeys(), refreshUser()])
    } catch (err) {
      toast(message(err), 'error')
    }
  }

  // ---- password ----
  let current = $state('')
  let next = $state('')
  let repeat = $state('')
  let pwError = $state('')
  let pwDone = $state(false)
  let pwBusy = $state(false)

  async function changePassword(e: SubmitEvent) {
    e.preventDefault()
    pwError = ''
    pwDone = false
    if (next !== repeat) {
      pwError = 'The new passwords do not match.'
      return
    }
    pwBusy = true
    try {
      await api.changePassword(current, next)
      pwDone = true
      current = next = repeat = ''
    } catch (err) {
      pwError = message(err)
    } finally {
      pwBusy = false
    }
  }

  // ---- two-factor login ----
  let setup = $state<TOTPSetup | null>(null)
  let code = $state('')
  let password = $state('')
  let tfError = $state('')
  let tfBusy = $state(false)

  async function startSetup() {
    tfError = ''
    try {
      setup = await api.totpSetup()
    } catch (err) {
      tfError = message(err)
    }
  }

  async function enable(e: SubmitEvent) {
    e.preventDefault()
    if (!setup) return
    tfBusy = true
    tfError = ''
    try {
      await api.totpEnable(setup.secret, code, password)
      setup = null
      await refreshUser()
    } catch (err) {
      tfError = message(err)
    } finally {
      tfBusy = false
      code = password = ''
    }
  }

  async function disable(e: SubmitEvent) {
    e.preventDefault()
    tfBusy = true
    tfError = ''
    try {
      await api.totpDisable(password, code)
      await refreshUser()
    } catch (err) {
      tfError = message(err)
    } finally {
      tfBusy = false
      code = password = ''
    }
  }
</script>

<h1 class="text-xl font-semibold">Settings</h1>
<p class="text-sm text-ink-2">Signed in as {auth.user?.username} · {auth.user ? roleLabel[auth.user.role] : ''}</p>

<div class="mt-5 grid gap-4 lg:grid-cols-2">
  <section class="card p-5">
    <h2 class="font-semibold">Password</h2>
    <p class="mt-1 text-sm text-ink-2">Changing it signs you out on every other device.</p>
    <form class="mt-4 grid gap-3" onsubmit={changePassword}>
      <label class="text-sm">
        <span class="mb-1 block text-ink-2">Current password</span>
        <input class="input" type="password" autocomplete="current-password" required bind:value={current} />
      </label>
      <label class="text-sm">
        <span class="mb-1 block text-ink-2">New password</span>
        <input class="input" type="password" autocomplete="new-password" minlength="8" required bind:value={next} />
      </label>
      <label class="text-sm">
        <span class="mb-1 block text-ink-2">Repeat new password</span>
        <input class="input" type="password" autocomplete="new-password" required bind:value={repeat} />
      </label>
      {#if pwError}<p class="text-sm text-critical" role="alert">{pwError}</p>{/if}
      {#if pwDone}<p class="text-sm text-good-ink" role="status">Password changed.</p>{/if}
      <div><button class="btn btn-primary" disabled={pwBusy}>Change password</button></div>
    </form>
  </section>

  <section class="card p-5">
    <div class="flex items-center gap-3">
      <h2 class="font-semibold">Two-factor login</h2>
      <span
        class="rounded-full px-2 py-0.5 text-xs font-medium"
        style:background={auth.user?.totp ? 'color-mix(in oklab, var(--good) 15%, var(--surface))' : 'var(--sunken)'}
        class:text-good-ink={auth.user?.totp}
        class:text-ink-2={!auth.user?.totp}>{auth.user?.totp ? 'On' : 'Off'}</span
      >
    </div>
    <p class="mt-1 text-sm text-ink-2">
      Asks for a code from an authenticator app (1Password, Bitwarden, Google Authenticator, …) when you sign in and
      before opening a shell.
    </p>

    {#if auth.user?.totp}
      <form class="mt-4 grid gap-3" onsubmit={disable}>
        <p class="text-sm text-ink-2">To turn it off, confirm your password and a current code.</p>
        <label class="text-sm">
          <span class="mb-1 block text-ink-2">Password</span>
          <input class="input" type="password" autocomplete="current-password" required bind:value={password} />
        </label>
        <label class="text-sm">
          <span class="mb-1 block text-ink-2">Code</span>
          <input class="input tabular tracking-widest" inputmode="numeric" autocomplete="one-time-code" maxlength="7" required bind:value={code} />
        </label>
        {#if tfError}<p class="text-sm text-critical" role="alert">{tfError}</p>{/if}
        <div><button class="btn btn-danger" disabled={tfBusy}>Turn off two-factor login</button></div>
      </form>
    {:else if setup}
      <form class="mt-4 grid gap-3" onsubmit={enable}>
        <div class="flex flex-wrap items-start gap-4">
          <img src={setup.qr} alt="QR code for your authenticator app" width="160" height="160" class="rounded-lg bg-white p-2" />
          <div class="min-w-0 flex-1 text-sm text-ink-2">
            <p>1. Scan the code with your authenticator app, or enter this key manually:</p>
            <code class="mt-1 block font-mono text-xs break-all text-ink">{setup.secret}</code>
            <p class="mt-3">2. Enter the 6-digit code it shows, and your password.</p>
          </div>
        </div>
        <div class="grid gap-3 sm:grid-cols-2">
          <label class="text-sm">
            <span class="mb-1 block text-ink-2">Code</span>
            <input class="input tabular tracking-widest" inputmode="numeric" autocomplete="one-time-code" maxlength="7" required bind:value={code} />
          </label>
          <label class="text-sm">
            <span class="mb-1 block text-ink-2">Password</span>
            <input class="input" type="password" autocomplete="current-password" required bind:value={password} />
          </label>
        </div>
        {#if tfError}<p class="text-sm text-critical" role="alert">{tfError}</p>{/if}
        <div class="flex gap-2">
          <button class="btn btn-primary" disabled={tfBusy}>Turn on</button>
          <button type="button" class="btn" onclick={() => (setup = null)}>Cancel</button>
        </div>
      </form>
    {:else}
      {#if tfError}<p class="mt-3 text-sm text-critical" role="alert">{tfError}</p>{/if}
      <button class="btn btn-primary mt-4" onclick={startSetup}>Set up two-factor login</button>
    {/if}
  </section>

  <section class="card p-5 lg:col-span-2">
    <div class="flex flex-wrap items-center gap-3">
      <h2 class="font-semibold">Passkeys</h2>
      {#if supported}
        <button class="btn ml-auto h-8" onclick={addPasskey} disabled={pkBusy}><Icon name="plus" size={14} /> Add a passkey</button>
      {/if}
    </div>
    <p class="mt-1 text-sm text-ink-2">
      Sign in with your fingerprint, face or device PIN instead of password and code, and confirm shells and other
      sensitive actions the same way. Passkeys are stored in your password manager, phone or security key.
    </p>
    {#if !supported}
      <p class="mt-3 rounded-lg bg-sunken px-3 py-2 text-sm text-ink-2">
        Browsers only allow passkeys over https (or on localhost). Put the hub behind a reverse proxy with a
        certificate and open it by its hostname to use them.
      </p>
    {/if}
    {#if passkeys.length}
      <ul class="mt-4 divide-y divide-line rounded-lg border border-line">
        {#each passkeys as p (p.id)}
          <li class="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-sm">
            <Icon name="key" size={14} class="text-ink-2" />
            <span class="font-medium">{p.name}</span>
            <span class="text-xs text-muted">
              {p.rp_id}{p.rp_id !== here ? ' (another address)' : ''} · added {ago(p.created_at)} ·
              {p.last_used ? `last used ${ago(p.last_used)}` : 'not used yet'}
            </span>
            <span class="ml-auto flex gap-1">
              <button class="btn h-7 px-2" onclick={() => renamePasskey(p)} aria-label="Rename {p.name}"><Icon name="pencil" size={12} /></button>
              <button class="btn btn-danger h-7 px-2" onclick={() => removePasskey(p)} aria-label="Remove {p.name}"><Icon name="trash" size={12} /></button>
            </span>
          </li>
        {/each}
      </ul>
    {:else if supported}
      <p class="mt-3 text-sm text-muted">No passkeys yet.</p>
    {/if}
  </section>
</div>
