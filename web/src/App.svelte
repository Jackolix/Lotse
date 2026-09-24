<script lang="ts">
  import { onMount } from 'svelte'
  import { auth, checkAuth, signOut } from './lib/auth.svelte'
  import Icon from './lib/components/Icon.svelte'
  import { link, route } from './lib/router.svelte'
  import { systems } from './lib/systems.svelte'
  import { setTheme, theme, type ThemeMode } from './lib/theme.svelte'
  import AuthForm from './pages/AuthForm.svelte'
  import Overview from './pages/Overview.svelte'
  import SystemPage from './pages/SystemPage.svelte'

  onMount(checkAuth)

  $effect(() => {
    if (auth.user) systems.start()
  })

  const systemId = $derived(Number(route.path.match(/^\/systems\/(\d+)$/)?.[1] ?? 0))

  const nextTheme: Record<ThemeMode, ThemeMode> = { system: 'light', light: 'dark', dark: 'system' }
  const themeLabel: Record<ThemeMode, string> = { system: 'Theme: system', light: 'Theme: light', dark: 'Theme: dark' }
</script>

{#if !auth.checked}
  <!-- checking the session -->
{:else if !auth.user}
  <AuthForm setup={auth.setupNeeded} />
{:else}
  <header class="sticky top-0 z-20 border-b border-line bg-surface/90 backdrop-blur">
    <div class="mx-auto flex h-14 max-w-6xl items-center gap-3 px-4">
      <a href="/" onclick={link} class="flex items-center gap-2 font-semibold">
        <span class="grid size-7 place-items-center rounded-lg bg-accent text-white"><Icon name="activity" /></span>
        Lotse
      </a>
      <span
        class="ml-1 hidden items-center gap-1.5 text-xs sm:inline-flex {systems.connected ? 'text-ink-2' : 'text-muted'}"
      >
        <span
          class="size-1.5 rounded-full {systems.connected ? 'animate-pulse' : ''}"
          style:background={systems.connected ? 'var(--good)' : 'var(--muted)'}
        ></span>
        {systems.connected ? 'Live' : 'Reconnecting…'}
      </span>
      <div class="ml-auto flex items-center gap-1.5">
        <button
          class="btn px-2"
          onclick={() => setTheme(nextTheme[theme.mode])}
          title={themeLabel[theme.mode]}
          aria-label={themeLabel[theme.mode]}
        >
          <Icon name={theme.mode === 'dark' ? 'moon' : theme.mode === 'light' ? 'sun' : 'monitor'} />
        </button>
        <span class="hidden px-1 text-sm text-ink-2 sm:inline">{auth.user.username}</span>
        <button class="btn px-2 sm:px-3" onclick={signOut} aria-label="Sign out">
          <Icon name="logout" /><span class="hidden sm:inline">Sign out</span>
        </button>
      </div>
    </div>
  </header>

  <main class="mx-auto max-w-6xl px-4 py-6">
    {#if route.path === '/'}
      <Overview />
    {:else if systemId}
      {#key systemId}
        <SystemPage id={systemId} />
      {/key}
    {:else}
      <p class="text-sm text-ink-2">Page not found. <a href="/" onclick={link} class="text-accent underline">Back to systems</a></p>
    {/if}
  </main>
{/if}
