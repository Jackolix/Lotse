<script lang="ts">
  import { onMount } from 'svelte'
  import { api, type Account, type Role } from '../lib/api'
  import { auth, can, refreshUser, roleLabel } from '../lib/auth.svelte'
  import Icon from '../lib/components/Icon.svelte'
  import Modal from '../lib/components/Modal.svelte'
  import { confirmAction } from '../lib/dialogs.svelte'
  import { ago } from '../lib/format'
  import { openMenu, type MenuEntry } from '../lib/menu.svelte'
  import { withReauth } from '../lib/reauth.svelte'
  import { navigate } from '../lib/router.svelte'
  import { errorText } from '../lib/systemActions'
  import { toast } from '../lib/toast.svelte'

  let users = $state<Account[]>([])
  let loaded = $state(false)

  // Add user / set password dialog
  let editing = $state<Account | null>(null) // null: new user
  let dialogOpen = $state(false)
  let username = $state('')
  let password = $state('')
  let role = $state<Role>('viewer')
  let error = $state('')
  let busy = $state(false)

  const roles: { role: Role; text: string }[] = [
    { role: 'viewer', text: 'Sees systems, charts and alerts. Cannot change anything.' },
    { role: 'operator', text: 'Also uses terminals, files and scripts, and restarts services and machines.' },
    { role: 'admin', text: 'Also manages users, systems, alert rules and agent updates.' },
  ]

  async function load() {
    try {
      users = await api.users()
    } catch (err) {
      toast(errorText(err), 'error')
    } finally {
      loaded = true
    }
  }
  onMount(load)

  const reason = 'Changing accounts needs your password again.'

  function openNew() {
    editing = null
    username = password = error = ''
    role = 'viewer'
    dialogOpen = true
  }

  function openPassword(u: Account) {
    editing = u
    password = error = ''
    dialogOpen = true
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault()
    busy = true
    error = ''
    try {
      const target = editing
      const done = await withReauth(reason, async () => {
        if (target) await api.updateUser(target.id, { password })
        else await api.createUser(username, password, role)
        return true
      })
      if (!done) return
      toast(target ? `New password set for ${target.username}` : `${username} can now sign in`, 'success')
      dialogOpen = false
      await load()
    } catch (err) {
      error = errorText(err)
    } finally {
      busy = false
    }
  }

  async function change(u: Account, what: { role?: Role; reset_totp?: boolean }, message: string) {
    try {
      if (!(await withReauth(reason, () => api.updateUser(u.id, what).then(() => true)))) return
      toast(message, 'success')
      if (u.id === auth.user?.id) {
        await refreshUser()
        if (!can('admin')) return navigate('/')
      }
      await load()
    } catch (err) {
      toast(errorText(err), 'error')
    }
  }

  async function setRole(u: Account, select: HTMLSelectElement) {
    const r = select.value as Role
    if (r === u.role) return
    if (u.id === auth.user?.id && r !== 'admin') {
      const ok = await confirmAction({
        title: 'Give up administration?',
        message: `You will be ${roleLabel[r].toLowerCase()} and can no longer manage users.`,
        confirmLabel: 'Change my role',
        danger: true,
      })
      if (!ok) {
        select.value = u.role
        return
      }
    }
    await change(u, { role: r }, `${u.username} is now ${roleLabel[r].toLowerCase()}`)
    select.value = users.find((x) => x.id === u.id)?.role ?? u.role // unchanged if it failed
  }

  async function resetTOTP(u: Account) {
    const ok = await confirmAction({
      title: `Turn off two-factor login for ${u.username}?`,
      message: 'Use this when they lost their authenticator app. They can set it up again in Settings.',
      confirmLabel: 'Turn off',
      danger: true,
    })
    if (ok) await change(u, { reset_totp: true }, `Two-factor login turned off for ${u.username}`)
  }

  async function remove(u: Account) {
    const ok = await confirmAction({
      title: `Delete ${u.username}?`,
      message: 'They are signed out and their open terminals close. The activity log keeps their entries.',
      confirmLabel: 'Delete',
      danger: true,
    })
    if (!ok) return
    try {
      if (!(await withReauth(reason, () => api.deleteUser(u.id).then(() => true)))) return
      toast(`${u.username} deleted`, 'success', 2500)
      await load()
    } catch (err) {
      toast(errorText(err), 'error')
    }
  }

  const menu = (u: Account): MenuEntry[] => [
    { label: 'Set a new password…', icon: 'key', action: () => openPassword(u) },
    {
      label: 'Turn off two-factor login…',
      icon: 'lock',
      disabled: !u.totp,
      hint: 'Two-factor login is off',
      action: () => resetTOTP(u),
    },
    'separator',
    {
      label: 'Delete…',
      icon: 'trash',
      danger: true,
      disabled: u.id === auth.user?.id,
      hint: 'You cannot delete your own account',
      action: () => remove(u),
    },
  ]
</script>

<div class="mb-5 flex flex-wrap items-end gap-3">
  <div class="mr-auto">
    <h1 class="text-xl font-semibold">Users</h1>
    <p class="text-sm text-ink-2">Who can sign in, and what they may do.</p>
  </div>
  <button class="btn btn-primary" onclick={openNew}><Icon name="plus" /> Add user</button>
</div>

<section class="card overflow-hidden">
  <div class="overflow-x-auto">
    <table class="w-full text-sm">
      <thead class="text-left text-xs text-muted">
        <tr>
          <th class="px-4 py-2 font-medium">User</th>
          <th class="py-2 pr-4 font-medium">Role</th>
          <th class="hidden py-2 pr-4 font-medium sm:table-cell">Sign-in</th>
          <th class="hidden py-2 pr-4 font-medium md:table-cell">Last sign-in</th>
          <th class="py-2 pr-4"><span class="sr-only">Actions</span></th>
        </tr>
      </thead>
      <tbody>
        {#each users as u (u.id)}
          <tr class="border-t border-line" oncontextmenu={(e) => openMenu(e, menu(u))}>
            <td class="px-4 py-2.5">
              <span class="font-medium">{u.username}</span>
              {#if u.id === auth.user?.id}<span class="ml-1 text-xs text-muted">(you)</span>{/if}
            </td>
            <td class="py-2 pr-4">
              <select
                class="input h-8 w-auto pr-8"
                aria-label="Role of {u.username}"
                value={u.role}
                onchange={(e) => setRole(u, e.currentTarget)}
              >
                {#each roles as r (r.role)}<option value={r.role}>{roleLabel[r.role]}</option>{/each}
              </select>
            </td>
            <td class="hidden py-2 pr-4 text-ink-2 sm:table-cell">
              {[
                'Password',
                u.totp ? 'two-factor code' : '',
                u.passkeys ? `${u.passkeys} passkey${u.passkeys > 1 ? 's' : ''}` : '',
              ]
                .filter(Boolean)
                .join(' · ')}
            </td>
            <td class="hidden py-2 pr-4 text-ink-2 md:table-cell">{u.last_login ? ago(u.last_login) : 'Never'}</td>
            <td class="py-2 pr-4 text-right">
              <button class="btn h-8 px-2" onclick={(e) => openMenu(e, menu(u))} aria-label="More for {u.username}">
                <Icon name="chevron-down" size={14} />
              </button>
            </td>
          </tr>
        {:else}
          <tr><td colspan="5" class="px-4 py-6 text-muted">{loaded ? 'No users.' : 'Loading…'}</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
</section>

<section class="mt-4 grid gap-3 text-sm sm:grid-cols-3">
  {#each roles as r (r.role)}
    <div class="card p-4">
      <h2 class="font-medium">{roleLabel[r.role]}</h2>
      <p class="mt-1 text-ink-2">{r.text}</p>
    </div>
  {/each}
</section>

<Modal bind:open={dialogOpen} title={editing ? `New password for ${editing.username}` : 'Add a user'} width="28rem">
  <form id="user-form" class="grid gap-4" onsubmit={submit}>
    {#if !editing}
      <label class="text-sm">
        <span class="mb-1 block text-ink-2">Username</span>
        <input class="input" autocomplete="off" required maxlength="64" data-autofocus bind:value={username} />
      </label>
    {/if}
    <label class="text-sm">
      <span class="mb-1 block text-ink-2">{editing ? 'New password' : 'Initial password'}</span>
      <input
        class="input"
        type="password"
        autocomplete="new-password"
        minlength="8"
        required
        data-autofocus={editing ? '' : undefined}
        bind:value={password}
      />
      <span class="mt-1 block text-xs text-muted">
        {editing ? 'They are signed out everywhere.' : 'At least 8 characters. They can change it in Settings.'}
      </span>
    </label>
    {#if !editing}
      <fieldset class="text-sm">
        <legend class="mb-1.5 text-ink-2">Role</legend>
        <div class="grid gap-2">
          {#each roles as r (r.role)}
            <label class="flex items-start gap-2.5">
              <input type="radio" class="mt-0.5 size-4 accent-[var(--accent)]" value={r.role} bind:group={role} />
              <span><span class="font-medium">{roleLabel[r.role]}</span> <span class="block text-xs text-ink-2">{r.text}</span></span>
            </label>
          {/each}
        </div>
      </fieldset>
    {/if}
    {#if error}<p class="text-sm text-critical" role="alert">{error}</p>{/if}
  </form>
  {#snippet footer()}
    <button type="button" class="btn" onclick={() => (dialogOpen = false)}>Cancel</button>
    <button class="btn btn-primary" form="user-form" disabled={busy}>{editing ? 'Set password' : 'Add user'}</button>
  {/snippet}
</Modal>
