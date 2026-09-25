<script lang="ts">
  import { onMount } from 'svelte'
  import { api, type AuditEntry } from '../lib/api'
  import { dateTime } from '../lib/format'
  import { openMenu, type MenuEntry } from '../lib/menu.svelte'
  import { link, navigate } from '../lib/router.svelte'
  import { copy } from '../lib/systemActions'
  import { systems } from '../lib/systems.svelte'

  let entries = $state<AuditEntry[]>([])
  let more = $state(false)
  let loading = $state(false)

  const labels: Record<string, string> = {
    setup: 'Created the admin account',
    login: 'Signed in',
    login_failed: 'Sign-in failed',
    logout: 'Signed out',
    reauth: 'Confirmed password',
    reauth_failed: 'Password confirmation failed',
    password_changed: 'Changed password',
    password_change_failed: 'Password change failed',
    totp_enabled: 'Turned on two-factor login',
    totp_enable_failed: 'Two-factor setup failed',
    totp_disabled: 'Turned off two-factor login',
    totp_disable_failed: 'Turning off two-factor login failed',
    shell_opened: 'Opened a shell',
    shell_closed: 'Closed a shell',
    shell_denied: 'Shell refused',
    wake: 'Sent Wake-on-LAN',
    wake_failed: 'Wake-on-LAN failed',
    system_enrolled: 'System enrolled',
    system_renamed: 'Renamed a system',
    system_deleted: 'Deleted a system',
    token_created: 'Created an enrollment token',
    alert_rule_saved: 'Saved an alert rule',
    alert_rule_deleted: 'Deleted an alert rule',
    notifier_saved: 'Saved a notification channel',
    notifier_deleted: 'Deleted a notification channel',
    process_signaled: 'Stopped a process',
    process_signal_failed: 'Stopping a process failed',
  }
  const failed = (action: string) => action.endsWith('_failed') || action === 'shell_denied'

  async function load() {
    loading = true
    try {
      const page = await api.audit(entries.at(-1)?.id ?? 0)
      entries = [...entries, ...page.entries]
      more = page.more
    } finally {
      loading = false
    }
  }

  onMount(load)

  function entryMenu(e: AuditEntry): MenuEntry[] {
    const sys = e.system_id ? systems.get(e.system_id) : undefined
    const line = [dateTime(e.t), labels[e.action] ?? e.action, e.username, e.system_name, e.detail, e.remote]
      .filter(Boolean)
      .join(' · ')
    return [
      {
        label: 'Open system',
        icon: 'server',
        disabled: !sys,
        hint: e.system_name ? 'This system no longer exists' : 'No system involved',
        action: () => navigate(`/systems/${e.system_id}`),
      },
      'separator',
      { label: 'Copy entry', icon: 'copy', action: () => copy(line, 'Entry') },
      {
        label: `Copy source address${e.remote ? ` (${e.remote})` : ''}`,
        icon: 'copy',
        disabled: !e.remote,
        action: () => copy(e.remote ?? '', 'Address'),
      },
    ]
  }
</script>

<h1 class="text-xl font-semibold">Activity</h1>
<p class="text-sm text-ink-2">Sign-ins, shells, Wake-on-LAN and changes to systems. Kept for a year.</p>

<section class="card mt-5 overflow-hidden">
  <div class="overflow-x-auto">
    <table class="w-full text-sm">
      <thead class="text-left text-xs text-muted">
        <tr>
          <th class="px-4 py-2 font-medium whitespace-nowrap">Time</th>
          <th class="py-2 pr-4 font-medium">Event</th>
          <th class="py-2 pr-4 font-medium">User</th>
          <th class="py-2 pr-4 font-medium">System</th>
          <th class="hidden py-2 pr-4 font-medium md:table-cell">Details</th>
          <th class="hidden py-2 pr-4 font-medium sm:table-cell">From</th>
        </tr>
      </thead>
      <tbody>
        {#each entries as e (e.id)}
          <tr class="border-t border-line align-top" oncontextmenu={(ev) => openMenu(ev, entryMenu(e))}>
            <td class="tabular px-4 py-2 whitespace-nowrap text-ink-2">{dateTime(e.t)}</td>
            <td class="py-2 pr-4">
              {#if failed(e.action)}
                <span class="mr-1 inline-block size-1.5 -translate-y-0.5 rounded-full bg-critical" aria-hidden="true"></span>
              {/if}
              <span class:text-critical={failed(e.action)}>{labels[e.action] ?? e.action}</span>
            </td>
            <td class="py-2 pr-4">{e.username || '–'}</td>
            <td class="py-2 pr-4">
              {#if e.system_id && systems.get(e.system_id)}
                <a class="text-accent hover:underline" href="/systems/{e.system_id}" onclick={link}>{e.system_name}</a>
              {:else}
                {e.system_name || '–'}
              {/if}
            </td>
            <td class="hidden max-w-md py-2 pr-4 text-ink-2 md:table-cell">{e.detail || ''}</td>
            <td class="tabular hidden py-2 pr-4 text-ink-2 sm:table-cell">{e.remote || ''}</td>
          </tr>
        {:else}
          <tr><td colspan="6" class="px-4 py-6 text-muted">{loading ? 'Loading…' : 'Nothing recorded yet.'}</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
  {#if more}
    <div class="border-t border-line px-4 py-3">
      <button class="btn" onclick={load} disabled={loading}>Load older entries</button>
    </div>
  {/if}
</section>
