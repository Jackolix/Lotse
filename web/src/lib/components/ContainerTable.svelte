<script lang="ts">
  import type { Container } from '../api'
  import { bytes, pct, rate } from '../format'
  import { openMenu, type MenuEntry } from '../menu.svelte'
  import { copy } from '../systemActions'

  let { containers }: { containers: Container[] } = $props()

  const sorted = $derived(
    [...containers].sort((a, b) => Number(b.state === 'running') - Number(a.state === 'running') || b.cpu - a.cpu),
  )
  const running = $derived(containers.filter((c) => c.state === 'running').length)

  const menu = (c: Container): MenuEntry[] => [
    { label: 'Copy name', icon: 'copy', action: () => copy(c.name, 'Name') },
    { label: 'Copy ID', icon: 'copy', action: () => copy(c.id, 'ID') },
    { label: 'Copy image', icon: 'copy', action: () => copy(c.image, 'Image') },
  ]
</script>

<section class="card mt-4 overflow-hidden">
  <h2 class="px-4 pt-4 text-sm font-medium">
    Containers <span class="font-normal text-ink-2">· {running} of {containers.length} running</span>
  </h2>
  <div class="overflow-x-auto">
    <table class="mt-2 w-full text-sm">
      <thead class="text-left text-xs text-muted">
        <tr>
          <th class="px-4 py-2 font-medium">Name</th>
          <th class="py-2 pr-4 font-medium">State</th>
          <th class="py-2 pr-4 text-right font-medium">CPU</th>
          <th class="py-2 pr-4 text-right font-medium">Memory</th>
          <th class="hidden py-2 pr-4 text-right font-medium sm:table-cell">Network</th>
        </tr>
      </thead>
      <tbody>
        {#each sorted as c (c.id)}
          {@const up = c.state === 'running'}
          <tr class="border-t border-line align-top" oncontextmenu={(e) => openMenu(e, menu(c))}>
            <td class="max-w-72 px-4 py-2">
              <div class="truncate font-medium" title={c.name}>{c.name}</div>
              <div class="truncate text-xs text-muted" title={c.image}>{c.image}</div>
            </td>
            <td class="py-2 pr-4">
              <span class="inline-flex items-center gap-1.5">
                <span class="size-2 shrink-0 rounded-full" style:background={up ? 'var(--good)' : 'var(--muted)'}></span>
                <span class="text-ink-2">{c.status || c.state}</span>
              </span>
            </td>
            <td class="tabular py-2 pr-4 text-right">{up ? pct(c.cpu) : '–'}</td>
            <td class="tabular py-2 pr-4 text-right whitespace-nowrap">
              {up ? bytes(c.mem) : '–'}
              {#if up && c.mem_limit && c.mem_limit < 2 ** 50}<span class="text-xs text-muted"> / {bytes(c.mem_limit)}</span>{/if}
            </td>
            <td class="tabular hidden py-2 pr-4 text-right text-xs whitespace-nowrap text-ink-2 sm:table-cell">
              {#if up}↓ {rate(c.net_rx)}<br />↑ {rate(c.net_tx)}{:else}–{/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
</section>
