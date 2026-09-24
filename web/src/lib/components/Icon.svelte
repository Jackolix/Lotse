<script lang="ts" module>
  // 24px stroke icons; multiple subpaths are separated by "|".
  const paths = {
    activity: 'M3 12h4l3-8 4 16 3-8h4',
    plus: 'M12 5v14|M5 12h14',
    'arrow-left': 'M19 12H5|M12 19l-7-7 7-7',
    copy: 'M9 9h11v11H9z|M5 15H4V4h11v1',
    check: 'M20 6 9 17l-5-5',
    trash: 'M3 6h18|M8 6V4h8v2|M19 6l-1 14H6L5 6',
    pencil: 'M17 3l4 4L7 21H3v-4z',
    sun: 'M12 8a4 4 0 1 0 0 8a4 4 0 1 0 0-8|M12 2v2|M12 20v2|M4.9 4.9l1.4 1.4|M17.7 17.7l1.4 1.4|M2 12h2|M20 12h2|M4.9 19.1l1.4-1.4|M17.7 6.3l1.4-1.4',
    moon: 'M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z',
    monitor: 'M3 4h18v12H3z|M8 20h8|M12 16v4',
    logout: 'M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4|M16 17l5-5-5-5|M21 12H9',
    search: 'M11 4a7 7 0 1 0 0 14a7 7 0 1 0 0-14|M21 21l-4.3-4.3',
    x: 'M18 6 6 18|M6 6l12 12',
    server: 'M3 4h18v7H3z|M3 13h18v7H3z|M7 7.5h.01|M7 16.5h.01',
  } as const
  export type IconName = keyof typeof paths
</script>

<script lang="ts">
  let { name, size = 16, class: cls = '' }: { name: IconName; size?: number; class?: string } = $props()
</script>

<svg
  width={size}
  height={size}
  viewBox="0 0 24 24"
  fill="none"
  stroke="currentColor"
  stroke-width="2"
  stroke-linecap="round"
  stroke-linejoin="round"
  class="shrink-0 {cls}"
  aria-hidden="true"
>
  {#each paths[name].split('|') as d (d)}
    <path {d} />
  {/each}
</svg>
