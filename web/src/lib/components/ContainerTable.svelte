<script lang="ts">
  import type { Container } from '../api'
  import { bytes, pct, rate } from '../format'
  import { openMenu, type MenuEntry } from '../menu.svelte'
  import { rowOrder } from '../rowOrder'
  import { copy } from '../systemActions'
  import Icon from './Icon.svelte'

  let { containers }: { containers: Container[] } = $props()

  // By name unless asked otherwise; sorting by CPU or memory holds the order between
  // refreshes (see rowOrder), so rows don't trade places while you read them.
  type SortKey = 'name' | 'cpu' | 'mem'
  let sortKey = $state<SortKey>('name')
  let epoch = $state(0)
  const sortBy = (key: SortKey) => {
    sortKey = key
    epoch++
  }

  const byName = (a: Container, b: Container) => a.name.localeCompare(b.name)
  const runningFirst = (a: Container, b: Container) => Number(b.state === 'running') - Number(a.state === 'running')
  const by: Record<SortKey, (a: Container, b: Container) => number> = {
    name: byName,
    cpu: (a, b) => runningFirst(a, b) || b.cpu - a.cpu || byName(a, b),
    mem: (a, b) => runningFirst(a, b) || b.mem - a.mem || byName(a, b),
  }
  const order = rowOrder((c: Container) => c.id)
  const ordered = $derived(order(containers, by[sortKey], sortKey !== 'name', epoch))
  const labels: Record<SortKey, string> = { name: 'name', cpu: 'CPU', mem: 'memory' }
  const running = $derived(containers.filter((c) => c.state === 'running').length)

  const menu = (c: Container): MenuEntry[] => [
    { label: 'Copy name', icon: 'copy', action: () => copy(c.name, 'Name') },
    { label: 'Copy ID', icon: 'copy', action: () => copy(c.id, 'ID') },
    { label: 'Copy image', icon: 'copy', action: () => copy(c.image, 'Image') },
  ]
</script>

{#snippet sortable(key: SortKey, label: string)}
  <button class="hover:text-ink {sortKey === key ? 'text-ink' : ''}" onclick={() => sortBy(key)} aria-pressed={sortKey === key}>
    {label}{sortKey === key ? ' ↓' : ''}
  </button>
{/snippet}

<section class="card mt-4 overflow-hidden">
  <div class="flex flex-wrap items-center gap-3 px-4 pt-4">
    <h2 class="text-sm font-medium">
      Containers <span class="font-normal text-ink-2">· {running} of {containers.length} running</span>
    </h2>
    {#if ordered.stale}
      <button
        class="inline-flex items-center gap-1 text-xs text-ink-2 hover:text-ink"
        title="Rows keep their place while the numbers update"
        onclick={() => sortBy(sortKey)}><Icon name="refresh" size={12} /> Sort by {labels[sortKey]} again</button
      >
    {/if}
  </div>
  <div class="overflow-x-auto">
    <table class="mt-2 w-full text-sm">
      <thead class="text-left text-xs text-muted">
        <tr>
          <th class="px-4 py-2 font-medium">{@render sortable('name', 'Name')}</th>
          <th class="py-2 pr-4 font-medium">State</th>
          <th class="w-16 py-2 pr-4 text-right font-medium">{@render sortable('cpu', 'CPU')}</th>
          <th class="w-36 py-2 pr-4 text-right font-medium">{@render sortable('mem', 'Memory')}</th>
          <th class="hidden w-28 py-2 pr-4 text-right font-medium sm:table-cell">Network</th>
        </tr>
      </thead>
      <tbody>
        {#each ordered.rows as c (c.id)}
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
