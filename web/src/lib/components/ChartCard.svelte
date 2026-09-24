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

<!-- The legend sits in the header row, so cards with one or several series are the
     same height and line up in the grid. -->
<section class="card min-w-0 p-4 pb-3">
  <header class="mb-1 flex flex-wrap items-baseline gap-x-4 gap-y-1">
    <h3 class="text-sm font-medium text-ink">{title}</h3>
    {#if series.length > 1}
      <ul class="flex flex-wrap gap-x-3 text-xs text-ink-2">
        {#each series as s (s.label)}
          <li class="flex items-center gap-1.5">
            <span class="h-0.5 w-3 rounded-full" style:background="var({s.color})"></span>{s.label}
          </li>
        {/each}
      </ul>
    {/if}
    {#if value}<span class="tabular ml-auto text-sm text-ink-2">{value}</span>{/if}
  </header>
  <Chart {series} label="{title} chart" {...rest} />
</section>
