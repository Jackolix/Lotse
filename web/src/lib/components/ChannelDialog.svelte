<script lang="ts">
  import { untrack } from 'svelte'
  import { api, ApiError, type Notifier, type NotifierConfig, type NotifierInput, type NotifierType } from '../api'
  import { toast } from '../toast.svelte'
  import Modal from './Modal.svelte'

  let {
    open = $bindable(false),
    channel = null,
    onsaved,
  }: { open?: boolean; channel?: Notifier | null; onsaved?: () => void } = $props()

  const types: { type: NotifierType; label: string; help: string }[] = [
    { type: 'ntfy', label: 'ntfy', help: 'Push notifications to the ntfy app, from ntfy.sh or your own server.' },
    { type: 'discord', label: 'Discord', help: 'Server settings → Integrations → Webhooks → New webhook, then copy its URL.' },
    { type: 'slack', label: 'Slack', help: 'Create an incoming webhook for a channel and paste its URL.' },
    { type: 'telegram', label: 'Telegram', help: 'Create a bot with @BotFather, send it a message, then use your chat ID.' },
    { type: 'email', label: 'Email', help: 'Any SMTP server, e.g. your mail provider or a relay.' },
    { type: 'webhook', label: 'Webhook', help: 'Lotse POSTs a JSON body with status, title, message, system, metric and value.' },
  ]

  let type = $state<NotifierType>('ntfy')
  let name = $state('')
  let enabled = $state(true)
  let cfg = $state<NotifierConfig>({})
  let error = $state('')
  let testResult = $state<{ ok: boolean; text: string } | null>(null)
  let busy = $state(false)

  $effect(() => {
    if (!open) return
    untrack(() => {
      type = channel?.type ?? 'ntfy'
      name = channel?.name ?? ''
      enabled = channel?.enabled ?? true
      cfg = { tls: 'starttls', ...(channel?.config ?? {}) }
      error = ''
      testResult = null
    })
  })

  const help = $derived(types.find((t) => t.type === type)?.help ?? '')

  function input(): NotifierInput {
    const config: NotifierConfig = { ...cfg, port: cfg.port ? Number(cfg.port) : undefined }
    delete config.token_set
    delete config.password_set
    return { id: channel?.id, name: name || types.find((t) => t.type === type)!.label, type, enabled, config }
  }

  async function test() {
    busy = true
    testResult = null
    try {
      await api.testNotifier(input())
      testResult = { ok: true, text: 'Test notification sent.' }
    } catch (err) {
      testResult = { ok: false, text: err instanceof ApiError ? err.message : 'Cannot reach the hub.' }
    } finally {
      busy = false
    }
  }

  async function save(e: SubmitEvent) {
    e.preventDefault()
    busy = true
    error = ''
    try {
      await api.saveNotifier(input())
      toast(channel ? 'Channel saved' : 'Channel added', 'success', 2500)
      open = false
      onsaved?.()
    } catch (err) {
      error = err instanceof ApiError ? err.message : 'Cannot reach the hub.'
    } finally {
      busy = false
    }
  }
</script>

<Modal bind:open title={channel ? 'Edit notification channel' : 'New notification channel'} width="34rem">
  <form id="channel-form" class="grid gap-4" onsubmit={save}>
    <div class="grid gap-3 sm:grid-cols-2">
      <label class="text-sm">
        <span class="mb-1 block text-ink-2">Type</span>
        <select class="input" bind:value={type}>
          {#each types as t (t.type)}<option value={t.type}>{t.label}</option>{/each}
        </select>
      </label>
      <label class="text-sm">
        <span class="mb-1 block text-ink-2">Name</span>
        <input class="input" maxlength="64" placeholder={types.find((t) => t.type === type)?.label} bind:value={name} />
      </label>
    </div>
    <p class="-mt-1 text-xs text-ink-2">{help}</p>

    {#if type === 'ntfy'}
      <label class="text-sm">
        <span class="mb-1 block text-ink-2">Topic URL</span>
        <input class="input font-mono" type="url" required placeholder="https://ntfy.sh/my-servers" bind:value={cfg.url} />
      </label>
      <label class="text-sm">
        <span class="mb-1 block text-ink-2">Access token (optional)</span>
        <input class="input" type="password" autocomplete="off" placeholder={cfg.token_set ? 'Unchanged' : ''} bind:value={cfg.token} />
      </label>
    {:else if type === 'discord' || type === 'slack' || type === 'webhook'}
      <label class="text-sm">
        <span class="mb-1 block text-ink-2">{type === 'webhook' ? 'URL' : 'Webhook URL'}</span>
        <input class="input font-mono" type="url" required bind:value={cfg.url} />
      </label>
    {:else if type === 'telegram'}
      <div class="grid gap-3 sm:grid-cols-2">
        <label class="text-sm">
          <span class="mb-1 block text-ink-2">Bot token</span>
          <input
            class="input"
            type="password"
            autocomplete="off"
            required={!cfg.token_set}
            placeholder={cfg.token_set ? 'Unchanged' : '123456:ABC…'}
            bind:value={cfg.token}
          />
        </label>
        <label class="text-sm">
          <span class="mb-1 block text-ink-2">Chat ID</span>
          <input class="input tabular" required bind:value={cfg.chat_id} />
        </label>
      </div>
    {:else if type === 'email'}
      <div class="grid gap-3 sm:grid-cols-[1fr_6rem_8rem]">
        <label class="text-sm">
          <span class="mb-1 block text-ink-2">SMTP server</span>
          <input class="input" required placeholder="smtp.example.com" bind:value={cfg.host} />
        </label>
        <label class="text-sm">
          <span class="mb-1 block text-ink-2">Port</span>
          <input class="input tabular" type="number" min="1" max="65535" placeholder={cfg.tls === 'tls' ? '465' : '587'} bind:value={cfg.port} />
        </label>
        <label class="text-sm">
          <span class="mb-1 block text-ink-2">Encryption</span>
          <select class="input" bind:value={cfg.tls}>
            <option value="starttls">STARTTLS</option>
            <option value="tls">TLS</option>
            <option value="none">None</option>
          </select>
        </label>
      </div>
      <div class="grid gap-3 sm:grid-cols-2">
        <label class="text-sm">
          <span class="mb-1 block text-ink-2">Username (optional)</span>
          <input class="input" autocomplete="off" bind:value={cfg.username} />
        </label>
        <label class="text-sm">
          <span class="mb-1 block text-ink-2">Password</span>
          <input class="input" type="password" autocomplete="off" placeholder={cfg.password_set ? 'Unchanged' : ''} bind:value={cfg.password} />
        </label>
        <label class="text-sm">
          <span class="mb-1 block text-ink-2">From</span>
          <input class="input" type="email" required placeholder="lotse@example.com" bind:value={cfg.from} />
        </label>
        <label class="text-sm">
          <span class="mb-1 block text-ink-2">To</span>
          <input class="input" required placeholder="you@example.com, team@example.com" bind:value={cfg.to} />
        </label>
      </div>
    {/if}

    <label class="flex items-center gap-2 text-sm">
      <input type="checkbox" role="switch" class="size-4 accent-[var(--accent)]" bind:checked={enabled} /> Enabled
    </label>
    {#if testResult}
      <p class="text-sm {testResult.ok ? 'text-good-ink' : 'text-critical'}" role="status">{testResult.text}</p>
    {/if}
    {#if error}<p class="text-sm text-critical" role="alert">{error}</p>{/if}
  </form>

  {#snippet footer()}
    <button type="button" class="btn mr-auto" onclick={test} disabled={busy}>Send test</button>
    <button type="button" class="btn" onclick={() => (open = false)}>Cancel</button>
    <button class="btn btn-primary" form="channel-form" disabled={busy}>{channel ? 'Save' : 'Add channel'}</button>
  {/snippet}
</Modal>
