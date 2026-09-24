<script lang="ts">
  import { untrack } from 'svelte'
  import { api, type Enrollment } from '../api'
  import { copyText } from '../clipboard'
  import { systems } from '../systems.svelte'
  import Icon from './Icon.svelte'

  let { open = $bindable(false) }: { open?: boolean } = $props()

  type OS = 'linux' | 'darwin' | 'windows'
  const tabs: { os: OS; label: string }[] = [
    { os: 'linux', label: 'Linux' },
    { os: 'darwin', label: 'macOS' },
    { os: 'windows', label: 'Windows' },
  ]

  let dialog: HTMLDialogElement
  let enrollment = $state<Enrollment | null>(null)
  let error = $state('')
  let hubUrl = $state('')
  let os = $state<OS>(guessOS())
  let copied = $state(false)
  let allowShell = $state(false)
  let knownIds = $state.raw(new Set<number>())

  $effect(() => {
    if (open) {
      if (!dialog.open) dialog.showModal()
      untrack(start)
    } else if (dialog.open) {
      dialog.close()
    }
  })

  async function start() {
    enrollment = null
    error = ''
    copied = false
    knownIds = new Set(systems.list.map((s) => s.id))
    try {
      enrollment = await api.enroll()
      hubUrl = enrollment.hub_url
    } catch (e) {
      error = e instanceof Error ? e.message : 'Could not create an enrollment token'
    }
  }

  function guessOS(): OS {
    const p = navigator.userAgent
    if (/Windows/i.test(p)) return 'windows'
    if (/Mac OS X/i.test(p)) return 'darwin'
    return 'linux'
  }

  // Same quoting as hub.InstallCommands on the Go side.
  const sh = (s: string) => `'${s.replaceAll("'", `'\\''`)}'`
  const ps = (s: string) => `'${s.replaceAll("'", "''")}'`

  const base = $derived(hubUrl.trim().replace(/\/+$/, ''))
  const command = $derived.by(() => {
    if (!enrollment) return ''
    const { hub_key: key, token } = enrollment
    if (os === 'windows') {
      const flag = allowShell ? ' -AllowShell' : ''
      return `& ([scriptblock]::Create((irm ${ps(base + '/install.ps1')}))) -Hub ${ps(base)} -Key ${ps(key)} -Token ${ps(token)}${flag}`
    }
    const flag = allowShell ? ' --allow-shell' : ''
    return `curl -fsSL ${sh(base + '/install.sh')} | sudo sh -s -- --hub ${sh(base)} --key ${sh(key)} --token ${sh(token)}${flag}`
  })
  const isLocalhost = $derived(/^https?:\/\/(localhost|127\.|\[::1\])/i.test(base))
  const expires = $derived(
    enrollment ? new Date(enrollment.expires_at * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : '',
  )
  const joined = $derived(systems.list.filter((s) => !knownIds.has(s.id)))

  async function copy() {
    await copyText(command)
    copied = true
    setTimeout(() => (copied = false), 2000)
  }
</script>

<dialog
  bind:this={dialog}
  onclose={() => (open = false)}
  class="card m-auto max-h-[calc(100dvh-2rem)] w-[min(42rem,calc(100vw-2rem))] overflow-auto p-0 text-ink"
  aria-labelledby="add-system-title"
>
  <div class="p-5">
    <div class="flex items-start justify-between gap-4">
      <h2 id="add-system-title" class="text-lg font-semibold">Add a system</h2>
      <button class="btn -mt-1 -mr-1 h-8 px-2" onclick={() => (open = false)} aria-label="Close"><Icon name="x" /></button>
    </div>
    <p class="mt-1 text-sm text-ink-2">
      Run this command on the machine you want to monitor. It downloads the agent from this hub, installs it as a
      system service and connects back.
    </p>

    {#if error}
      <p class="mt-4 text-sm text-critical" role="alert">{error}</p>
    {:else if !enrollment}
      <p class="mt-4 text-sm text-muted">Creating an enrollment token…</p>
    {:else}
      <label class="mt-4 block text-sm">
        <span class="mb-1 block text-ink-2">Hub address the machine connects to</span>
        <input class="input font-mono" bind:value={hubUrl} spellcheck="false" autocomplete="off" />
      </label>
      {#if isLocalhost}
        <p class="mt-1.5 text-xs text-ink-2">
          <strong class="font-medium text-ink">Other machines can't reach "localhost".</strong> Use this computer's LAN
          address or hostname, or set <code class="font-mono">HUB_URL</code> on the hub.
        </p>
      {/if}

      <label class="mt-4 flex items-start gap-2.5 text-sm">
        <input type="checkbox" class="mt-0.5 size-4 accent-[var(--accent)]" bind:checked={allowShell} />
        <span>
          <span class="font-medium">Allow remote shell</span>
          <span class="block text-xs text-ink-2">
            Lets hub users open a root (Windows: SYSTEM) terminal on this machine after confirming their password. The
            setting lives on the machine; the hub cannot turn it on later.
          </span>
        </span>
      </label>

      <div role="tablist" aria-label="Operating system" class="mt-5 flex gap-1 border-b border-line">
        {#each tabs as t (t.os)}
          <button
            role="tab"
            aria-selected={os === t.os}
            class="-mb-px border-b-2 px-3 py-1.5 text-sm {os === t.os
              ? 'border-accent font-medium text-ink'
              : 'border-transparent text-ink-2 hover:text-ink'}"
            onclick={() => (os = t.os)}>{t.label}</button
          >
        {/each}
      </div>
      <div class="relative mt-3">
        <pre
          class="max-h-40 overflow-auto rounded-lg bg-sunken p-3 pr-12 font-mono text-xs leading-relaxed break-all whitespace-pre-wrap">{command}</pre>
        <button class="btn absolute top-2 right-2 h-8 px-2" onclick={copy} aria-label="Copy command">
          <Icon name={copied ? 'check' : 'copy'} />
        </button>
      </div>
      <p class="mt-2 text-xs text-ink-2">
        {os === 'windows'
          ? 'Run it in PowerShell as Administrator.'
          : 'Run it in a terminal; it asks for your sudo password.'}
        The token works for any number of machines until {expires}.
      </p>

      {#if joined.length}
        <p
          class="mt-4 flex items-center gap-2 rounded-lg px-3 py-2 text-sm text-ink"
          style:background="color-mix(in oklab, var(--good) 12%, var(--surface))"
        >
          <Icon name="check" class="text-good-ink" />
          Connected: {joined.map((s) => s.name).join(', ')}
        </p>
      {:else}
        <p class="mt-4 flex items-center gap-2 text-sm text-muted">
          <span class="size-2 animate-pulse rounded-full bg-muted"></span> Waiting for a new system to connect…
        </p>
      {/if}
    {/if}
  </div>
  <footer class="flex justify-end border-t border-line px-5 py-3">
    <button class="btn" onclick={() => (open = false)}>Done</button>
  </footer>
</dialog>
