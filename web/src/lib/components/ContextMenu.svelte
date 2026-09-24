<script lang="ts">
  import { tick } from 'svelte'
  import { closeMenu, menu, type MenuItem } from '../menu.svelte'
  import Icon from './Icon.svelte'

  let el = $state<HTMLDivElement>()
  let pos = $state({ left: 0, top: 0, ready: false })

  $effect(() => {
    if (!menu.open || !el) return
    void menu.items
    // Keep the menu inside the viewport.
    const { width, height } = el.getBoundingClientRect()
    pos = {
      left: Math.max(4, Math.min(menu.x, innerWidth - width - 4)),
      top: Math.max(4, Math.min(menu.y, innerHeight - height - 4)),
      ready: true,
    }
    // Focus the menu once it is visible; arrow keys then move into the items.
    const menuEl = el
    tick().then(() => menuEl.focus())

    const outside = (e: PointerEvent) => {
      if (!el?.contains(e.target as Node)) closeMenu(false)
    }
    const escape = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        closeMenu()
      }
    }
    const dismiss = () => closeMenu(false)
    addEventListener('pointerdown', outside, true)
    addEventListener('keydown', escape, true)
    addEventListener('resize', dismiss)
    addEventListener('scroll', dismiss, true)
    addEventListener('blur', dismiss)
    return () => {
      pos.ready = false
      removeEventListener('pointerdown', outside, true)
      removeEventListener('keydown', escape, true)
      removeEventListener('resize', dismiss)
      removeEventListener('scroll', dismiss, true)
      removeEventListener('blur', dismiss)
    }
  })

  function enabledItems(): HTMLElement[] {
    return [...(el?.querySelectorAll<HTMLElement>('[role=menuitem]:not([aria-disabled=true])') ?? [])]
  }

  function onKey(e: KeyboardEvent) {
    const items = enabledItems()
    const i = items.indexOf(document.activeElement as HTMLElement) // -1: the menu itself
    const move = (to: number) => {
      e.preventDefault()
      items[(to + items.length) % items.length]?.focus()
    }
    if (e.key === 'ArrowDown') move(i + 1)
    else if (e.key === 'ArrowUp') move(i === -1 ? items.length - 1 : i - 1)
    else if (e.key === 'Home') move(0)
    else if (e.key === 'End') move(items.length - 1)
    else if (e.key === 'Tab') {
      e.preventDefault()
      closeMenu()
    }
  }

  function run(item: MenuItem) {
    if (item.disabled) return
    closeMenu(false)
    item.action?.()
  }
</script>

{#if menu.open}
  <div
    bind:this={el}
    role="menu"
    tabindex="-1"
    class="fixed z-50 min-w-52 rounded-lg border outline-none border-line bg-surface p-1 text-sm shadow-[0_8px_30px_rgb(0_0_0/0.25)]"
    style:left="{pos.left}px"
    style:top="{pos.top}px"
    style:visibility={pos.ready ? 'visible' : 'hidden'}
    onkeydown={onKey}
    oncontextmenu={(e) => e.preventDefault()}
  >
    {#each menu.items as item, i (i)}
      {#if item === 'separator'}
        <div role="separator" class="mx-1 my-1 h-px bg-line"></div>
      {:else}
        <button
          role="menuitem"
          tabindex="-1"
          aria-disabled={item.disabled || undefined}
          title={item.disabled ? item.hint : undefined}
          class="flex w-full items-center gap-2.5 rounded-md px-2.5 py-1.5 text-left outline-none
            {item.disabled
            ? 'cursor-default text-muted'
            : `hover:bg-sunken focus:bg-sunken ${item.danger ? 'text-critical' : 'text-ink'}`}"
          onclick={() => run(item)}
        >
          {#if item.icon}
            <Icon name={item.icon} size={14} class={item.danger || item.disabled ? '' : 'text-ink-2'} />
          {:else}
            <span class="w-3.5"></span>
          {/if}
          {item.label}
        </button>
      {/if}
    {/each}
  </div>
{/if}
