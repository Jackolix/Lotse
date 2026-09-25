<script lang="ts">
  import { onMount } from 'svelte'
  import { auth, can, checkAuth, signOut } from './lib/auth.svelte'
  import ContextMenu from './lib/components/ContextMenu.svelte'
  import Dialogs from './lib/components/Dialogs.svelte'
  import Icon from './lib/components/Icon.svelte'
  import ReauthDialog from './lib/components/ReauthDialog.svelte'
  import Toaster from './lib/components/Toaster.svelte'
  import { reauth, settleReauth } from './lib/reauth.svelte'
  import { link, route } from './lib/router.svelte'
  import { systems } from './lib/systems.svelte'
  import { setTheme, theme, type ThemeMode } from './lib/theme.svelte'
  import ActivityPage from './pages/ActivityPage.svelte'
  import AlertsPage from './pages/AlertsPage.svelte'
  import AuthForm from './pages/AuthForm.svelte'
  import FilesPage from './pages/FilesPage.svelte'
  import Overview from './pages/Overview.svelte'
  import RunPage from './pages/RunPage.svelte'
  import ScriptsPage from './pages/ScriptsPage.svelte'
  import SettingsPage from './pages/SettingsPage.svelte'
  import SystemPage from './pages/SystemPage.svelte'
  import TerminalPage from './pages/TerminalPage.svelte'
  import UsersPage from './pages/UsersPage.svelte'

  onMount(checkAuth)

  $effect(() => {
    if (auth.user) systems.start()
  })

  const systemMatch = $derived(route.path.match(/^\/systems\/(\d+)(?:\/(terminal|files))?$/))
  const systemId = $derived(Number(systemMatch?.[1] ?? 0))
  const subpage = $derived(systemMatch?.[2] ?? '')
  const runId = $derived(Number(route.path.match(/^\/runs\/(\d+)$/)?.[1] ?? 0))
  const wide = $derived(subpage === 'terminal')

  const nav = $derived(
    [
      { href: '/', label: 'Systems', show: true, active: (p: string) => p === '/' || p.startsWith('/systems/') },
      {
        href: '/scripts',
        label: 'Scripts',
        show: can('operator'),
        active: (p: string) => p === '/scripts' || p.startsWith('/runs/'),
      },
      { href: '/alerts', label: 'Alerts', show: true, active: (p: string) => p === '/alerts' },
      { href: '/activity', label: 'Activity', show: true, active: (p: string) => p === '/activity' },
      { href: '/users', label: 'Users', show: can('admin'), active: (p: string) => p === '/users' },
      { href: '/settings', label: 'Settings', show: true, active: (p: string) => p === '/settings' },
    ].filter((item) => item.show),
  )

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
        <span class="hidden sm:inline">Lotse</span>
      </a>
      <nav class="ml-1 flex min-w-0 items-center gap-0.5 overflow-x-auto text-sm [scrollbar-width:none]">
        {#each nav as item (item.href)}
          <a
            href={item.href}
            onclick={link}
            class="rounded-md px-2.5 py-1.5 {item.active(route.path)
              ? 'bg-sunken font-medium text-ink'
              : 'text-ink-2 hover:text-ink'}"
            aria-current={item.active(route.path) ? 'page' : undefined}
            >{item.label}{#if item.href === '/alerts' && systems.alerts.length}<span
                class="ml-1.5 inline-grid min-w-4.5 place-items-center rounded-full bg-critical px-1 text-[0.7rem] leading-4.5 font-semibold text-white"
                aria-label="{systems.alerts.length} active">{systems.alerts.length}</span
              >{/if}</a
          >
        {/each}
      </nav>
      <span
        class="ml-1 hidden items-center gap-1.5 text-xs lg:inline-flex {systems.connected ? 'text-ink-2' : 'text-muted'}"
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
        <span class="hidden px-1 text-sm text-ink-2 md:inline" title="Role: {auth.user.role}">{auth.user.username}</span>
        <button class="btn px-2 md:px-3" onclick={signOut} aria-label="Sign out">
          <Icon name="logout" /><span class="hidden md:inline">Sign out</span>
        </button>
      </div>
    </div>
  </header>

  <main class="mx-auto px-4 py-6 {wide ? 'max-w-screen-2xl' : 'max-w-6xl'}">
    {#if route.path === '/'}
      <Overview />
    {:else if route.path === '/alerts'}
      <AlertsPage />
    {:else if route.path === '/activity'}
      <ActivityPage />
    {:else if route.path === '/settings'}
      <SettingsPage />
    {:else if route.path === '/scripts' && can('operator')}
      <ScriptsPage />
    {:else if runId && can('operator')}
      {#key runId}
        <RunPage id={runId} />
      {/key}
    {:else if route.path === '/users' && can('admin')}
      <UsersPage />
    {:else if systemId && subpage === 'terminal'}
      {#key systemId}
        <TerminalPage id={systemId} />
      {/key}
    {:else if systemId && subpage === 'files'}
      {#key systemId}
        <FilesPage id={systemId} />
      {/key}
    {:else if systemId}
      {#key systemId}
        <SystemPage id={systemId} />
      {/key}
    {:else}
      <p class="text-sm text-ink-2">Page not found. <a href="/" onclick={link} class="text-accent underline">Back to systems</a></p>
    {/if}
  </main>
{/if}

<ContextMenu />
<Dialogs />
<ReauthDialog
  bind:open={reauth.open}
  reason={reauth.reason}
  onconfirmed={() => settleReauth(true)}
  oncancel={() => settleReauth(false)}
/>
<Toaster />
