<script lang="ts">
  import { untrack } from 'svelte'
  import { api, ApiError, uploadFile, type FileEntry } from '../lib/api'
  import Icon from '../lib/components/Icon.svelte'
  import StatusDot from '../lib/components/StatusDot.svelte'
  import { ask, confirmAction } from '../lib/dialogs.svelte'
  import { bytes, dateTime } from '../lib/format'
  import { openMenu, type MenuEntry } from '../lib/menu.svelte'
  import { confirmIdentity, ensureElevated, withReauth } from '../lib/reauth.svelte'
  import { link, navigate, param } from '../lib/router.svelte'
  import { canShell, copy, errorText } from '../lib/systemActions'
  import { systems } from '../lib/systems.svelte'
  import { toast } from '../lib/toast.svelte'

  let { id }: { id: number } = $props()

  const reason = 'Browsing files needs your password again. It stays unlocked for 10 minutes.'
  const sys = $derived(systems.get(id))
  const path = $derived(param('path') || '/')
  const windows = $derived(sys?.info.os === 'windows')

  let entries = $state<FileEntry[]>([])
  let loading = $state(true)
  let error = $state('')
  let needsConfirm = $state(false)
  let query = $state('')
  let pathInput = $state('')
  let dragging = $state(0)

  interface Upload {
    key: number
    name: string
    progress: number
    error?: string
    abort: AbortController
  }
  let uploads = $state<Upload[]>([])
  let nextKey = 0

  const join = (dir: string, name: string) => (dir === '/' ? '/' : dir + '/') + name
  const parent = (p: string) => (p === '/' ? '/' : p.slice(0, p.lastIndexOf('/')) || '/')

  function go(p: string) {
    navigate(`/systems/${id}/files?path=${encodeURIComponent(p)}`)
  }

  async function load(p: string) {
    loading = true
    error = ''
    needsConfirm = false
    try {
      const res = await withReauth(reason, () => api.files(id, p))
      if (!res) {
        needsConfirm = true
        entries = []
        return
      }
      if (p === path) {
        entries = res.entries
        query = ''
      }
    } catch (err) {
      error = errorText(err)
      entries = []
    } finally {
      loading = false
    }
  }

  $effect(() => {
    const p = path
    pathInput = p
    untrack(() => load(p))
  })

  const shown = $derived.by(() => {
    const q = query.trim().toLowerCase()
    return q ? entries.filter((e) => e.name.toLowerCase().includes(q)) : entries
  })

  const crumbs = $derived.by(() => {
    const parts = path.split('/').filter(Boolean)
    return parts.map((name, i) => ({ name, path: '/' + parts.slice(0, i + 1).join('/') }))
  })

  function open(e: FileEntry) {
    if (e.dir) go(join(path, e.name))
    else download(e)
  }

  async function download(e: FileEntry) {
    if (!(await ensureElevated(reason))) return
    const a = document.createElement('a')
    a.href = api.downloadURL(id, join(path, e.name))
    a.download = e.name
    document.body.append(a)
    a.click()
    a.remove()
  }

  async function act(body: Parameters<typeof api.fileAction>[1], done: string) {
    try {
      if ((await withReauth(reason, () => api.fileAction(id, body).then(() => true))) === undefined) return
      toast(done, 'success', 2500)
      await load(path)
    } catch (err) {
      toast(errorText(err), 'error')
    }
  }

  async function newFolder() {
    const name = await ask({ title: 'New folder', confirmLabel: 'Create' })
    if (name) await act({ action: 'mkdir', path: join(path, name) }, `Created ${name}`)
  }

  async function rename(e: FileEntry) {
    const name = await ask({ title: `Rename ${e.name}`, value: e.name, confirmLabel: 'Rename' })
    if (name && name !== e.name) await act({ action: 'rename', path: join(path, e.name), to: join(path, name) }, `Renamed to ${name}`)
  }

  async function remove(e: FileEntry) {
    const ok = await confirmAction({
      title: `Delete ${e.name}?`,
      message: e.dir ? 'The folder and everything in it are deleted. This cannot be undone.' : 'This cannot be undone.',
      confirmLabel: 'Delete',
      danger: true,
    })
    if (ok) await act({ action: 'delete', path: join(path, e.name), recursive: e.dir }, `Deleted ${e.name}`)
  }

  // ---- uploads ----

  async function upload(files: File[]) {
    if (!files.length || !(await ensureElevated(reason))) return
    const dir = path
    for (const [index, file] of files.entries()) {
      const up: Upload = { key: nextKey++, name: file.name, progress: 0, abort: new AbortController() }
      uploads.push(up)
      const item = uploads.find((u) => u.key === up.key)!
      const send = (overwrite: boolean) =>
        uploadFile(id, join(dir, file.name), file, overwrite, (f) => (item.progress = f), up.abort.signal)
      try {
        try {
          await send(false)
        } catch (err) {
          if (!(err instanceof ApiError) || err.status !== 409 || /folder/.test(err.message)) throw err
          const ok = await confirmAction({
            title: `Replace ${file.name}?`,
            message: 'A file with this name exists already. It keeps its permissions and owner.',
            confirmLabel: 'Replace',
            danger: true,
          })
          if (!ok) {
            dismiss(up.key)
            continue
          }
          await send(true)
        }
        dismiss(up.key)
        toast(`Uploaded ${file.name}`, 'success', 2500)
      } catch (err) {
        // The confirmation ran out during a long upload: confirm again and go on.
        if (err instanceof ApiError && err.body.reauth_required && (await confirmIdentity(reason))) {
          dismiss(up.key)
          return upload(files.slice(index))
        }
        item.error = errorText(err)
      }
      if (dir === path) load(path)
    }
  }

  function dismiss(key: number) {
    const i = uploads.findIndex((u) => u.key === key)
    if (i >= 0) uploads.splice(i, 1)
  }

  function pick(e: Event) {
    const input = e.currentTarget as HTMLInputElement
    if (input.files) upload([...input.files])
    input.value = ''
  }

  function drop(e: DragEvent) {
    e.preventDefault()
    dragging = 0
    if (e.dataTransfer?.files.length) upload([...e.dataTransfer.files])
  }

  const menu = (e: FileEntry): MenuEntry[] => [
    e.dir
      ? { label: 'Open', icon: 'folder', action: () => open(e) }
      : { label: 'Download', icon: 'download', action: () => download(e) },
    { label: 'Copy path', icon: 'copy', action: () => copy(join(path, e.name), 'Path') },
    'separator',
    { label: 'Rename…', icon: 'pencil', action: () => rename(e) },
    { label: 'Delete…', icon: 'trash', danger: true, action: () => remove(e) },
  ]

  function submitPath(e: SubmitEvent) {
    e.preventDefault()
    const p = pathInput.trim().replaceAll('\\', '/')
    go(p.startsWith('/') ? p : '/' + p)
  }
</script>

<div class="flex flex-wrap items-center gap-x-4 gap-y-2">
  <a href="/systems/{id}" onclick={link} class="inline-flex items-center gap-1 text-sm text-ink-2 hover:text-ink">
    <Icon name="arrow-left" size={14} />
    {sys?.name ?? 'System'}
  </a>
  {#if sys}<StatusDot online={sys.online} />{/if}
  <span class="text-sm text-ink-2">Files · as {windows ? 'SYSTEM' : 'root'} · uploads and downloads are noted in Activity</span>
</div>

{#if sys && !canShell(sys)}
  <p class="mt-6 text-sm text-ink-2">
    {sys.online
      ? 'File access is off for this machine. Re-run the install command with --allow-shell (Windows: -AllowShell) to allow it.'
      : 'The system is offline.'}
  </p>
{:else}
  <section
    class="card mt-3 overflow-hidden {dragging ? 'outline-2 outline-accent' : ''}"
    ondragenter={(e) => (e.preventDefault(), dragging++)}
    ondragover={(e) => e.preventDefault()}
    ondragleave={() => dragging--}
    ondrop={drop}
    aria-label="Files"
  >
    <div class="flex flex-wrap items-center gap-2 border-b border-line px-3 py-2.5">
      <button class="btn h-8 px-2" onclick={() => go(parent(path))} disabled={path === '/'} aria-label="Up one folder" title="Up one folder">
        <Icon name="arrow-up" size={14} />
      </button>
      <form class="min-w-48 flex-1" onsubmit={submitPath}>
        <input class="input h-8 font-mono text-xs" aria-label="Path" spellcheck="false" bind:value={pathInput} />
      </form>
      <label class="relative block w-full sm:w-44">
        <span class="sr-only">Filter this folder</span>
        <Icon name="search" size={14} class="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-muted" />
        <input class="input h-8 pl-8" placeholder="Filter" bind:value={query} />
      </label>
      <button class="btn h-8 px-2" onclick={() => load(path)} aria-label="Refresh" title="Refresh"><Icon name="refresh" size={14} /></button>
      <button class="btn h-8" onclick={newFolder} disabled={path === '/' && windows}><Icon name="folder-plus" size={14} /> New folder</button>
      <label class="btn btn-primary h-8 {path === '/' && windows ? 'pointer-events-none opacity-55' : ''}">
        <Icon name="upload" size={14} /> Upload
        <input type="file" multiple class="sr-only" onchange={pick} disabled={path === '/' && windows} />
      </label>
    </div>

    <nav class="flex flex-wrap items-center gap-1 px-4 pt-2 text-sm text-ink-2" aria-label="Folder path">
      <button class="hover:text-ink" onclick={() => go('/')}>{windows ? 'Drives' : '/'}</button>
      {#each crumbs as c, i (c.path)}
        {#if i > 0 || windows}<span class="text-muted">/</span>{/if}
        <button class="hover:text-ink {i === crumbs.length - 1 ? 'font-medium text-ink' : ''}" onclick={() => go(c.path)}>{c.name}</button>
      {/each}
    </nav>

    {#if uploads.length}
      <ul class="mx-4 mt-2 grid gap-1.5">
        {#each uploads as u (u.key)}
          <li class="flex items-center gap-3 rounded-lg bg-sunken px-3 py-1.5 text-sm">
            <Icon name="upload" size={14} class="text-ink-2" />
            <span class="min-w-0 flex-1 truncate">{u.name}</span>
            {#if u.error}
              <span class="truncate text-xs text-critical">{u.error}</span>
            {:else}
              <span class="h-1.5 w-32 overflow-hidden rounded-full bg-line"><span class="block h-full bg-accent" style:width="{u.progress * 100}%"></span></span>
              <span class="tabular w-10 text-right text-xs text-ink-2">{Math.round(u.progress * 100)}%</span>
            {/if}
            <button
              class="btn h-6 px-1.5"
              onclick={() => (u.error ? dismiss(u.key) : u.abort.abort())}
              aria-label={u.error ? 'Dismiss' : 'Cancel upload'}><Icon name="x" size={12} /></button
            >
          </li>
        {/each}
      </ul>
    {/if}

    {#if error}
      <p class="px-4 py-4 text-sm text-critical" role="alert">{error}</p>
    {:else if needsConfirm}
      <div class="px-4 py-6 text-sm text-ink-2">
        Confirm your password to see the files.
        <button class="btn ml-2 h-8" onclick={() => load(path)}>Confirm</button>
      </div>
    {:else}
      <div class="overflow-x-auto">
        <table class="mt-2 w-full text-sm">
          <thead class="text-left text-xs text-muted">
            <tr>
              <th class="px-4 py-2 font-medium">Name</th>
              <th class="py-2 pr-4 text-right font-medium">Size</th>
              <th class="hidden py-2 pr-4 font-medium md:table-cell">Modified</th>
              <th class="hidden py-2 pr-4 font-medium lg:table-cell">Permissions</th>
              <th class="py-2 pr-4"><span class="sr-only">Actions</span></th>
            </tr>
          </thead>
          <tbody>
            {#each shown as e (e.name)}
              <tr class="border-t border-line hover:bg-sunken" oncontextmenu={(ev) => openMenu(ev, menu(e))}>
                <td class="max-w-md px-4 py-1.5">
                  <button class="flex max-w-full items-center gap-2 text-left" ondblclick={() => open(e)} onclick={() => e.dir && open(e)}>
                    <Icon name={e.dir ? 'folder' : 'file'} size={15} class={e.dir ? 'text-accent' : 'text-ink-2'} />
                    <span class="truncate" class:font-medium={e.dir}>{e.name}</span>
                    {#if e.link}<span class="text-xs text-muted">link</span>{/if}
                  </button>
                </td>
                <td class="tabular py-1.5 pr-4 text-right whitespace-nowrap text-ink-2">{e.dir ? '' : bytes(e.size)}</td>
                <td class="tabular hidden py-1.5 pr-4 whitespace-nowrap text-ink-2 md:table-cell">{e.mtime ? dateTime(e.mtime) : ''}</td>
                <td class="hidden py-1.5 pr-4 font-mono text-xs text-ink-2 lg:table-cell">{e.mode}</td>
                <td class="py-1 pr-3 text-right whitespace-nowrap">
                  {#if !e.dir}
                    <button class="btn h-7 px-2" onclick={() => download(e)} aria-label="Download {e.name}" title="Download">
                      <Icon name="download" size={13} />
                    </button>
                  {/if}
                  <button class="btn h-7 px-2" onclick={(ev) => openMenu(ev, menu(e))} aria-label="More for {e.name}">
                    <Icon name="chevron-down" size={13} />
                  </button>
                </td>
              </tr>
            {:else}
              <tr>
                <td colspan="5" class="px-4 py-6 text-muted">
                  {loading ? 'Loading…' : query ? `Nothing matches "${query}".` : 'This folder is empty. Drop files here to upload them.'}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>
{/if}
