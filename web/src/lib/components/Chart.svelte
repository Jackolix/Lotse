<script lang="ts">
  import uPlot from 'uplot'
  import 'uplot/dist/uPlot.min.css'
  import { untrack } from 'svelte'
  import type { ChartSeries } from '../chart'
  import { cssVar, theme } from '../theme.svelte'

  interface Props {
    data: uPlot.AlignedData
    series: ChartSeries[]
    format: (v: number) => string
    xRange: [number, number]
    /** Fixed top of the y axis (100 for percentages, capacity for usage). Auto when omitted. */
    yMax?: number
    /** Light area wash under the line; only for single-series charts. */
    fill?: boolean
    /** Byte values: put y ticks on powers of two (8, 16, 24 GiB instead of 9.3, 18.6 GiB). */
    binary?: boolean
    loading?: boolean
    label: string
    height?: number
  }
  let { data, series, format, xRange, yMax, fill = false, binary = false, loading = false, label, height = 176 }: Props = $props()

  // Powers of two, so byte axes land on 256 MiB, 8 GiB, 512 GiB …
  const BINARY_INCRS = Array.from({ length: 60 }, (_, i) => 2 ** i)

  let wrap: HTMLDivElement
  let el: HTMLDivElement
  let tip: HTMLDivElement
  let chart: uPlot | undefined

  // Canvas colors are baked in at creation, so rebuild when the theme changes.
  $effect(() => {
    theme.version
    const u = untrack(create)
    chart = u
    const ro = new ResizeObserver(() => u.setSize({ width: el.clientWidth, height }))
    ro.observe(el)
    return () => {
      ro.disconnect()
      u.destroy()
      chart = undefined
    }
  })

  $effect(() => {
    const d = data
    void xRange // scale ranges read the current props when data is set
    void yMax
    chart?.setData(d)
  })

  function create(): uPlot {
    const colors = series.map((s) => cssVar(s.color))
    const muted = cssVar('--muted')
    const grid = cssVar('--grid')
    const surface = cssVar('--surface')
    const font = '11px system-ui, -apple-system, "Segoe UI", sans-serif'
    const axis = { stroke: muted, font, grid: { stroke: grid, width: 1 }, ticks: { stroke: grid, width: 1, size: 4 } }

    const opts: uPlot.Options = {
      width: el.clientWidth || 300,
      height,
      padding: [8, 8, 0, 0],
      legend: { show: false },
      cursor: {
        y: false,
        drag: { x: false, y: false },
        points: { size: 8, width: 2, stroke: () => surface, fill: (_u, i) => colors[i - 1] },
      },
      scales: {
        x: { time: true, range: () => [xRange[0], xRange[1]] },
        y: { range: (_u, _min, max) => [0, yMax ?? (max > 0 ? max * 1.1 : 1)] },
      },
      axes: [
        { ...axis, space: 70, values: (_u, splits) => splits.map(tickLabel) },
        {
          ...axis,
          size: 64,
          space: 32,
          ticks: { show: false },
          incrs: binary ? BINARY_INCRS : undefined,
          values: (_u, splits) => splits.map((v) => format(v)),
        },
      ],
      series: [
        {},
        ...series.map((s, i) => ({
          label: s.label,
          stroke: colors[i],
          width: 2,
          fill: fill ? colors[i] + '1a' : undefined,
          points: { size: 8, width: 2, stroke: surface, fill: colors[i] },
          spanGaps: false,
        })),
      ],
      hooks: { setCursor: [showTooltip] },
    }
    return new uPlot(opts, data, el)
  }

  // Axis labels match the range: times within two days, dates beyond.
  function tickLabel(t: number): string {
    const span = xRange[1] - xRange[0]
    const d = new Date(t * 1000)
    if (span > 120 * 86400) return d.toLocaleDateString([], { month: 'short', year: '2-digit' })
    if (span > 2 * 86400) return d.toLocaleDateString([], { month: 'short', day: 'numeric' })
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  }

  function timeLabel(t: number): string {
    const span = xRange[1] - xRange[0]
    const d = new Date(t * 1000)
    if (span > 86400) return d.toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: span <= 900 ? '2-digit' : undefined })
  }

  // One tooltip lists every series at the hovered time. Built with textContent only.
  function showTooltip(u: uPlot) {
    const idx = u.cursor.idx
    const left = u.cursor.left ?? -1
    if (idx == null || left < 0) {
      tip.hidden = true
      return
    }
    const colors = series.map((s) => cssVar(s.color))
    const rows: HTMLElement[] = []
    const time = document.createElement('div')
    time.className = 'tip-time'
    time.textContent = timeLabel(u.data[0][idx])
    rows.push(time)
    series.forEach((s, i) => {
      const v = u.data[i + 1][idx]
      const row = document.createElement('div')
      row.className = 'tip-row'
      const key = document.createElement('span')
      key.className = 'tip-key'
      key.style.background = colors[i]
      const value = document.createElement('span')
      value.className = 'tip-value'
      value.textContent = v == null ? '–' : format(v)
      const name = document.createElement('span')
      name.className = 'tip-label'
      name.textContent = s.label
      row.append(key, value, name)
      rows.push(row)
    })
    tip.replaceChildren(...rows)
    tip.hidden = false

    const over = u.over.getBoundingClientRect()
    const box = wrap.getBoundingClientRect()
    const x = over.left - box.left + left
    const y = over.top - box.top + (u.cursor.top ?? 0)
    const w = tip.offsetWidth
    tip.style.left = `${x + 14 + w > box.width ? x - 14 - w : x + 14}px`
    tip.style.top = `${Math.max(0, Math.min(y - 20, box.height - tip.offsetHeight))}px`
  }
</script>

<div bind:this={wrap} class="relative transition-opacity duration-200" class:opacity-50={loading} role="img" aria-label={label}>
  <div bind:this={el}></div>
  <div bind:this={tip} class="tip" hidden></div>
</div>

<style>
  /* crosshair: a solid hairline instead of uPlot's dashed default */
  :global(.uplot .u-cursor-x) {
    border-right: 1px solid var(--axis);
  }
  .tip {
    position: absolute;
    z-index: 10;
    pointer-events: none;
    min-width: 8rem;
    padding: 0.4rem 0.55rem;
    border: 1px solid var(--line);
    border-radius: 0.5rem;
    background: var(--surface);
    box-shadow: 0 6px 20px rgb(0 0 0 / 0.14);
    font-size: 0.75rem;
    white-space: nowrap;
  }
  .tip :global(.tip-time) {
    margin-bottom: 0.15rem;
    color: var(--muted);
  }
  .tip :global(.tip-row) {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    line-height: 1.5;
  }
  .tip :global(.tip-key) {
    width: 0.75rem;
    height: 2px;
    border-radius: 1px;
  }
  .tip :global(.tip-value) {
    color: var(--ink);
    font-weight: 600;
    font-variant-numeric: tabular-nums;
  }
  .tip :global(.tip-label) {
    color: var(--ink-2);
  }
</style>
