<script lang="ts">
  import { untrack } from 'svelte'
  import { api, type Script, type ScriptShell } from '../api'
  import { durationLabel } from '../format'
  import { withReauth } from '../reauth.svelte'
  import { shells, timeouts } from '../scripts'
  import { errorText } from '../systemActions'
  import { toast } from '../toast.svelte'
  import Modal from './Modal.svelte'

  let {
    open = $bindable(false),
    script = null,
    onsaved,
  }: { open?: boolean; script?: Partial<Script> | null; onsaved?: (s: Script) => void } = $props()

  let name = $state('')
  let description = $state('')
  let shell = $state<ScriptShell>('sh')
  let timeout = $state(300)
  let content = $state('')
  let error = $state('')
  let busy = $state(false)

  $effect(() => {
    if (!open) return
    untrack(() => {
      name = script?.name ?? ''
      description = script?.description ?? ''
      shell = script?.shell ?? 'sh'
      timeout = script?.timeout ?? 300
      content = script?.content ?? ''
      error = ''
    })
  })

  const choices = $derived(timeouts.includes(timeout) ? timeouts : [...timeouts, timeout].sort((a, b) => a - b))

  async function save(e: SubmitEvent) {
    e.preventDefault()
    busy = true
    error = ''
    try {
      const saved = await withReauth('Changing scripts needs your password again.', () =>
        api.saveScript({ id: script?.id, name, description, shell, timeout, content }),
      )
      if (!saved) return
      toast(script?.id ? 'Script saved' : 'Script added', 'success', 2500)
      open = false
      onsaved?.(saved)
    } catch (err) {
      error = errorText(err)
    } finally {
      busy = false
    }
  }

  // Tab indents instead of leaving the editor; Escape still closes the dialog.
  function keydown(e: KeyboardEvent) {
    if (e.key !== 'Tab' || e.shiftKey) return
    e.preventDefault()
    const el = e.currentTarget as HTMLTextAreaElement
    const { selectionStart: start, selectionEnd: end } = el
    content = content.slice(0, start) + '\t' + content.slice(end)
    queueMicrotask(() => el.setSelectionRange(start + 1, start + 1))
  }
</script>

<Modal bind:open title={script?.id ? 'Edit script' : 'New script'} width="48rem">
  <form id="script-form" class="grid gap-4" onsubmit={save}>
    <div class="grid gap-3 sm:grid-cols-[2fr_1fr_1fr]">
      <label class="text-sm">
        <span class="mb-1 block text-ink-2">Name</span>
        <input class="input" maxlength="64" required placeholder="Update packages" data-autofocus bind:value={name} />
      </label>
      <label class="text-sm">
        <span class="mb-1 block text-ink-2">Shell</span>
        <select class="input" bind:value={shell}>
          {#each shells as s (s.shell)}<option value={s.shell}>{s.label}</option>{/each}
        </select>
      </label>
      <label class="text-sm">
        <span class="mb-1 block text-ink-2">Time limit</span>
        <select class="input" bind:value={timeout}>
          {#each choices as t (t)}<option value={t}>{durationLabel(t)}</option>{/each}
        </select>
      </label>
    </div>
    <label class="text-sm">
      <span class="mb-1 block text-ink-2">Description <span class="text-muted">(optional)</span></span>
      <input class="input" maxlength="500" bind:value={description} />
    </label>
    <label class="text-sm">
      <span class="mb-1 block text-ink-2">
        Script <span class="text-muted">· runs as root (Windows: SYSTEM) · {shells.find((s) => s.shell === shell)?.os}</span>
      </span>
      <textarea
        class="input h-72 resize-y py-2 font-mono text-xs leading-relaxed"
        spellcheck="false"
        autocapitalize="off"
        required
        bind:value={content}
        onkeydown={keydown}
        placeholder={shell === 'powershell' ? 'Get-Service | Where-Object Status -eq Stopped' : 'apt-get update && apt-get -y upgrade'}
      ></textarea>
    </label>
    {#if error}<p class="text-sm text-critical" role="alert">{error}</p>{/if}
  </form>
  {#snippet footer()}
    <button type="button" class="btn" onclick={() => (open = false)}>Cancel</button>
    <button class="btn btn-primary" form="script-form" disabled={busy}>{script?.id ? 'Save' : 'Add script'}</button>
  {/snippet}
</Modal>
