<script lang="ts">
  import { api, ApiError, type Alert, type AlertRule, type Notifier } from '../lib/api'
  import ChannelDialog from '../lib/components/ChannelDialog.svelte'
  import Icon from '../lib/components/Icon.svelte'
  import RuleDialog from '../lib/components/RuleDialog.svelte'
  import { confirmAction } from '../lib/dialogs.svelte'
  import { ago, dateTime, durationLabel, isPercentMetric, metricLabel, ruleSummary } from '../lib/format'
  import { openMenu, type MenuEntry } from '../lib/menu.svelte'
  import { link, navigate } from '../lib/router.svelte'
  import { systems } from '../lib/systems.svelte'
  import { toast } from '../lib/toast.svelte'

  let rules = $state<AlertRule[]>([])
  let channels = $state<Notifier[]>([])
  let history = $state<Alert[]>([])
  let ruleOpen = $state(false)
  let editingRule = $state<AlertRule | null>(null)
  let channelOpen = $state(false)
  let editingChannel = $state<Notifier | null>(null)

  async function load() {
    try {
      const [r, c, a] = await Promise.all([api.alertRules(), api.notifiers(), api.alerts()])
      rules = r
      channels = c
      history = a.history
    } catch {
      // 401 handled globally
    }
  }

  // Reload whenever the hub reports alert changes (also after a notification was sent).
  $effect(() => {
    void systems.alertsVersion
    load()
  })

  const errorText = (err: unknown) => (err instanceof ApiError ? err.message : 'Cannot reach the hub.')
  const channelName = (id: number) => channels.find((c) => c.id === id)?.name
  const systemName = (id: number | null) => (id === null ? 'All systems' : (systems.get(id)?.name ?? `#${id}`))
  const valueText = (a: Alert) =>
    a.metric === 'offline' ? '' : isPercentMetric(a.metric) ? `${Math.round(a.value)} %` : a.value.toFixed(2)

  function editRule(r: AlertRule | null) {
    editingRule = r
    ruleOpen = true
  }

  function editChannel(c: Notifier | null) {
    editingChannel = c
    channelOpen = true
  }

  async function toggleRule(r: AlertRule) {
    try {
      await api.saveAlertRule({ ...r, enabled: !r.enabled })
      await load()
    } catch (err) {
      toast(errorText(err), 'error')
    }
  }

  async function deleteRule(r: AlertRule) {
    if (!(await confirmAction({ title: `Delete rule "${r.name}"?`, message: 'Alerts it raised are closed.', confirmLabel: 'Delete', danger: true }))) return
    try {
      await api.deleteAlertRule(r.id)
      toast('Rule deleted', 'success', 2500)
      await load()
    } catch (err) {
      toast(errorText(err), 'error')
    }
  }

  async function deleteChannel(c: Notifier) {
    if (!(await confirmAction({ title: `Delete channel "${c.name}"?`, message: 'Rules using it will no longer notify through it.', confirmLabel: 'Delete', danger: true }))) return
    try {
      await api.deleteNotifier(c.id)
      toast('Channel deleted', 'success', 2500)
      await load()
    } catch (err) {
      toast(errorText(err), 'error')
    }
  }

  async function testChannel(c: Notifier) {
    try {
      // The browser only has the masked config; the hub fills in the stored secrets.
      await api.testNotifier({ id: c.id, name: c.name, type: c.type, enabled: c.enabled, config: c.config })
      toast(`Test sent through ${c.name}`, 'success')
    } catch (err) {
      toast(`${c.name}: ${errorText(err)}`, 'error')
    }
  }

  const ruleMenu = (r: AlertRule): MenuEntry[] => [
    { label: 'Edit…', icon: 'pencil', action: () => editRule(r) },
    { label: r.enabled ? 'Disable' : 'Enable', icon: 'power', action: () => toggleRule(r) },
    'separator',
    { label: 'Delete…', icon: 'trash', danger: true, action: () => deleteRule(r) },
  ]
  const channelMenu = (c: Notifier): MenuEntry[] => [
    { label: 'Send test', icon: 'send', action: () => testChannel(c) },
    { label: 'Edit…', icon: 'pencil', action: () => editChannel(c) },
    'separator',
    { label: 'Delete…', icon: 'trash', danger: true, action: () => deleteChannel(c) },
  ]
  const alertMenu = (a: Alert): MenuEntry[] => [
    { label: 'Open system', icon: 'server', disabled: !systems.get(a.system_id), action: () => navigate(`/systems/${a.system_id}`) },
    { label: 'Edit rule…', icon: 'pencil', disabled: !rules.some((r) => r.id === a.rule_id), action: () => editRule(rules.find((r) => r.id === a.rule_id)!) },
  ]
</script>

<h1 class="text-xl font-semibold">Alerts</h1>
<p class="text-sm text-ink-2">Rules watch every system; channels deliver the notifications.</p>

<section class="card mt-5">
  <h2 class="px-4 pt-4 font-semibold">Active</h2>
  {#if systems.alerts.length === 0}
    <p class="flex items-center gap-2 px-4 py-4 text-sm text-ink-2">
      <span class="grid size-5 place-items-center rounded-full text-white" style:background="var(--good)"><Icon name="check" size={12} /></span>
      All clear. Nothing is firing right now.
    </p>
  {:else}
    <ul class="mt-2">
      {#each systems.alerts as a (a.id)}
        <li class="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-line px-4 py-2.5 text-sm" oncontextmenu={(e) => openMenu(e, alertMenu(a))}>
          <span class="size-2 shrink-0 rounded-full bg-critical" aria-hidden="true"></span>
          <a class="font-medium hover:underline" href="/systems/{a.system_id}" onclick={link}>{a.system_name}</a>
          <span>{a.metric === 'offline' ? 'Offline' : `${metricLabel(a.metric)} ${valueText(a)}`}</span>
          <span class="text-ink-2">rule "{a.rule_name}"</span>
          <span class="ml-auto text-ink-2">since {ago(a.started_at).replace(' ago', '')}</span>
        </li>
      {/each}
    </ul>
  {/if}
</section>

<div class="mt-4 grid gap-4 lg:grid-cols-[3fr_2fr]">
  <section class="card min-w-0">
    <div class="flex items-center justify-between px-4 pt-4">
      <h2 class="font-semibold">Rules</h2>
      <button class="btn h-8" onclick={() => editRule(null)}><Icon name="plus" size={14} /> Add rule</button>
    </div>
    <ul class="mt-3">
      {#each rules as r (r.id)}
        <li class="flex items-center gap-3 border-t border-line px-4 py-2.5 text-sm" oncontextmenu={(e) => openMenu(e, ruleMenu(r))}>
          <input
            type="checkbox"
            role="switch"
            class="size-4 shrink-0 accent-[var(--accent)]"
            checked={r.enabled}
            onchange={() => toggleRule(r)}
            aria-label="{r.enabled ? 'Disable' : 'Enable'} {r.name}"
          />
          <div class="min-w-0 flex-1" class:opacity-50={!r.enabled}>
            <div class="font-medium">{r.name}</div>
            <div class="text-xs text-ink-2">
              {ruleSummary(r)} · {systemName(r.system_id)} ·
              {r.notifiers.length ? `notifies ${r.notifiers.map(channelName).filter(Boolean).join(', ')}` : 'shown in Lotse only'}
            </div>
          </div>
          <button class="btn h-8 px-2" onclick={() => editRule(r)} aria-label="Edit {r.name}"><Icon name="pencil" size={14} /></button>
        </li>
      {:else}
        <li class="border-t border-line px-4 py-4 text-sm text-muted">No rules. Add one to get alerted.</li>
      {/each}
    </ul>
  </section>

  <section class="card min-w-0">
    <div class="flex items-center justify-between px-4 pt-4">
      <h2 class="font-semibold">Notification channels</h2>
      <button class="btn h-8" onclick={() => editChannel(null)}><Icon name="plus" size={14} /> Add channel</button>
    </div>
    <ul class="mt-3">
      {#each channels as c (c.id)}
        <li class="flex items-center gap-3 border-t border-line px-4 py-2.5 text-sm" oncontextmenu={(e) => openMenu(e, channelMenu(c))}>
          <div class="min-w-0 flex-1" class:opacity-50={!c.enabled}>
            <div class="font-medium">{c.name} <span class="text-xs font-normal text-muted">{c.type}</span></div>
            {#if c.last_error}
              <div class="truncate text-xs text-critical" title={c.last_error}>Failed {ago(c.last_sent)}: {c.last_error}</div>
            {:else if c.last_sent}
              <div class="text-xs text-ink-2">Last delivered {ago(c.last_sent)}</div>
            {:else}
              <div class="text-xs text-muted">Nothing sent yet</div>
            {/if}
          </div>
          <button class="btn h-8 px-2" onclick={() => testChannel(c)} title="Send a test notification" aria-label="Test {c.name}"><Icon name="send" size={14} /></button>
          <button class="btn h-8 px-2" onclick={() => editChannel(c)} aria-label="Edit {c.name}"><Icon name="pencil" size={14} /></button>
        </li>
      {:else}
        <li class="border-t border-line px-4 py-4 text-sm text-muted">
          No channels yet. Add ntfy, Discord, Slack, Telegram, email or a webhook to get alerts on your phone.
        </li>
      {/each}
    </ul>
  </section>
</div>

<section class="card mt-4 overflow-hidden">
  <h2 class="px-4 pt-4 font-semibold">History</h2>
  <div class="overflow-x-auto">
    <table class="mt-2 w-full text-sm">
      <thead class="text-left text-xs text-muted">
        <tr>
          <th class="px-4 py-2 font-medium">System</th>
          <th class="py-2 pr-4 font-medium">Alert</th>
          <th class="py-2 pr-4 font-medium whitespace-nowrap">Started</th>
          <th class="py-2 pr-4 font-medium">Lasted</th>
        </tr>
      </thead>
      <tbody>
        {#each history as a (a.id)}
          <tr class="border-t border-line" oncontextmenu={(e) => openMenu(e, alertMenu(a))}>
            <td class="px-4 py-2">{a.system_name}</td>
            <td class="py-2 pr-4">{a.metric === 'offline' ? 'Offline' : `${metricLabel(a.metric)} above ${a.threshold}${isPercentMetric(a.metric) ? ' %' : ''}`} <span class="text-ink-2">· {a.rule_name}</span></td>
            <td class="tabular py-2 pr-4 whitespace-nowrap text-ink-2">{dateTime(a.started_at)}</td>
            <td class="tabular py-2 pr-4 text-ink-2">{durationLabel(Math.max(0, (a.resolved_at ?? 0) - a.started_at))}</td>
          </tr>
        {:else}
          <tr><td colspan="4" class="px-4 py-4 text-muted">No alerts so far.</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
</section>

<RuleDialog bind:open={ruleOpen} rule={editingRule} notifiers={channels} onsaved={load} />
<ChannelDialog bind:open={channelOpen} channel={editingChannel} onsaved={load} />
