<script lang="ts">
  import type { FitAddon } from '@xterm/addon-fit'
  import type { Terminal } from '@xterm/xterm'
  import '@xterm/xterm/css/xterm.css'
  import { onMount } from 'svelte'
  import Icon from '../lib/components/Icon.svelte'
  import ReauthDialog from '../lib/components/ReauthDialog.svelte'
  import StatusDot from '../lib/components/StatusDot.svelte'
  import { canReadClipboard, copyText } from '../lib/clipboard'
  import { openMenu } from '../lib/menu.svelte'
  import { link } from '../lib/router.svelte'
  import { toast } from '../lib/toast.svelte'
  import { systems } from '../lib/systems.svelte'

  let { id }: { id: number } = $props()

  type Phase = 'loading' | 'connecting' | 'ready' | 'exited' | 'error' | 'closed'
  let phase = $state<Phase>('loading')
  let message = $state('')
  let reauth = $state(false)
  let host: HTMLDivElement

  let term: Terminal | undefined
  let fit: FitAddon | undefined
  let ws: WebSocket | undefined
  const encoder = new TextEncoder()

  const sys = $derived(systems.get(id))

  // The terminal always uses a dark palette, independent of the page theme.
  const palette = {
    background: '#1a1a19',
    foreground: '#e8e7e1',
    cursor: '#3987e5',
    cursorAccent: '#1a1a19',
    selectionBackground: 'rgba(57, 135, 229, 0.35)',
    black: '#2c2c2a',
    brightBlack: '#6b6a65',
  }

  onMount(() => {
    let disposed = false
    let resizeObserver: ResizeObserver | undefined
    ;(async () => {
      // Loaded on demand: xterm.js is only needed when a terminal opens.
      const [{ Terminal }, { FitAddon }] = await Promise.all([import('@xterm/xterm'), import('@xterm/addon-fit')])
      if (disposed) return
      term = new Terminal({
        cursorBlink: true,
        fontFamily: 'ui-monospace, "SF Mono", Menlo, Consolas, monospace',
        fontSize: 13,
        scrollback: 5000,
        theme: palette,
      })
      fit = new FitAddon()
      term.loadAddon(fit)
      term.open(host)
      fit.fit()
      term.onData((d) => send(encoder.encode(d)))
      term.onBinary((d) => send(Uint8Array.from(d, (c) => c.charCodeAt(0))))
      term.onResize(({ cols, rows }) => {
        if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'resize', cols, rows }))
      })
      resizeObserver = new ResizeObserver(() => fit?.fit())
      resizeObserver.observe(host)
      connect()
    })()
    return () => {
      disposed = true
      resizeObserver?.disconnect()
      ws?.close()
      term?.dispose()
    }
  })

  function send(data: Uint8Array<ArrayBuffer>) {
    if (ws?.readyState === WebSocket.OPEN && phase === 'ready') ws.send(data)
  }

  function connect() {
    if (!term) return
    ws?.close()
    phase = 'connecting'
    message = ''
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const socket = new WebSocket(`${proto}//${location.host}/api/systems/${id}/shell?cols=${term.cols}&rows=${term.rows}`)
    socket.binaryType = 'arraybuffer'
    ws = socket
    socket.onmessage = (e) => {
      if (typeof e.data !== 'string') {
        term?.write(new Uint8Array(e.data as ArrayBuffer))
        return
      }
      const m = JSON.parse(e.data) as { type: string; code?: string; message?: string; status?: number }
      if (m.type === 'ready') {
        phase = 'ready'
        term?.focus()
      } else if (m.type === 'exit') {
        phase = 'exited'
        message = m.status != null ? `The shell exited with status ${m.status}.` : 'The shell exited.'
      } else if (m.type === 'error') {
        if (m.code === 'reauth_required') {
          phase = 'closed'
          reauth = true
        } else {
          phase = 'error'
          message = m.message ?? 'The shell could not be opened.'
        }
      }
    }
    socket.onclose = () => {
      if (ws !== socket) return
      if (phase === 'ready' || phase === 'connecting') {
        phase = 'closed'
        message = 'The connection to the hub was lost.'
      }
    }
  }

  // Captured before xterm sees the event, so the browser's own menu never shows.
  function terminalMenu(e: MouseEvent) {
    if (!term) return
    const t = term
    const selection = t.getSelection()
    const mod = navigator.platform.startsWith('Mac') ? '⌘' : 'Ctrl+Shift+'
    openMenu(e, [
      {
        label: 'Copy',
        icon: 'copy',
        disabled: !selection,
        hint: 'Select text first',
        action: async () => {
          if (!(await copyText(selection))) toast('Could not copy the selection', 'error')
          t.focus()
        },
      },
      {
        label: 'Paste',
        icon: 'clipboard',
        disabled: !canReadClipboard() || phase !== 'ready',
        hint: phase !== 'ready' ? 'Not connected' : `Browsers only allow this over https; use ${mod}V`,
        action: async () => {
          try {
            t.paste(await navigator.clipboard.readText())
          } catch {
            toast(`Clipboard access was blocked; use ${mod}V`, 'error')
          }
          t.focus()
        },
      },
      { label: 'Select all', icon: 'list', action: () => t.selectAll() },
      { label: 'Clear screen', icon: 'x', action: () => (t.clear(), t.focus()) },
      'separator',
      phase === 'ready'
        ? { label: 'Disconnect', icon: 'logout', action: disconnect }
        : { label: 'Reconnect', icon: 'refresh', action: connect },
    ])
  }

  function disconnect() {
    ws?.close()
    phase = 'closed'
    message = 'Disconnected.'
  }
</script>

<div class="flex flex-wrap items-center gap-x-4 gap-y-2">
  <a href="/systems/{id}" onclick={link} class="inline-flex items-center gap-1 text-sm text-ink-2 hover:text-ink">
    <Icon name="arrow-left" size={14} />
    {sys?.name ?? 'System'}
  </a>
  {#if sys}<StatusDot online={sys.online} />{/if}
  <span class="text-sm text-ink-2" role="status">
    {#if phase === 'loading' || phase === 'connecting'}
      Connecting…
    {:else if phase === 'ready'}
      Connected · who opened this shell, and when, is noted in Activity
    {:else}
      {message}
    {/if}
  </span>
  <div class="ml-auto flex gap-2">
    {#if phase === 'ready'}
      <button class="btn" onclick={disconnect}><Icon name="x" size={14} /> Disconnect</button>
    {:else if phase !== 'loading' && phase !== 'connecting'}
      <button class="btn btn-primary" onclick={connect}><Icon name="refresh" size={14} /> Reconnect</button>
    {/if}
  </div>
</div>

<div
  class="mt-3 overflow-hidden rounded-xl border border-line p-2"
  style:background={palette.background}
  class:opacity-60={phase !== 'ready'}
>
  <div bind:this={host} class="h-[calc(100dvh-11rem)] min-h-72" oncontextmenucapture={terminalMenu}></div>
</div>

<ReauthDialog
  bind:open={reauth}
  reason="Opening a shell on {sys?.name ?? 'this system'} needs your password again. It stays unlocked for 10 minutes."
  onconfirmed={connect}
  oncancel={() => (message = 'A shell needs you to confirm your password.')}
/>
