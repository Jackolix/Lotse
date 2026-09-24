<script lang="ts">
  import type uPlot from 'uplot'
  import type { ChartSeries } from '../chart'
  import Chart from './Chart.svelte'

  interface Props {
    title: string
    /** Current value shown next to the title. */
    value?: string
    data: uPlot.AlignedData
    series: ChartSeries[]
    format: (v: number) => string
    xRange: [number, number]
    yMax?: number
    fill?: boolean
    binary?: boolean
    loading?: boolean
  }
  let { title, value = '', series, ...rest }: Props = $props()
</script>

<section class="card min-w-0 p-4">
  <header class="mb-2 flex flex-wrap items-baseline justify-between gap-x-3">
    <h3 class="text-sm font-medium text-ink">{title}</h3>
    {#if value}<span class="tabular text-sm text-ink-2">{value}</span>{/if}
  </header>
  {#if series.length > 1}
    <ul class="mb-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-ink-2">
      {#each series as s (s.label)}
        <li class="flex items-center gap-1.5">
          <span class="h-0.5 w-3 rounded-full" style:background="var({s.color})"></span>{s.label}
        </li>
      {/each}
    </ul>
  {/if}
  <Chart {series} label="{title} chart" {...rest} />
</section>
