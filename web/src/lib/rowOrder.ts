// Live tables refresh every few seconds. Sorting them again each time by a value that
// keeps changing (CPU, memory) moves rows under the reader, so for those the order is
// held instead: rows keep their place while their numbers update, rows that appear
// join at the end, and the table is only sorted again when asked. Names and IDs don't
// change, so sorting by them stays live and new rows land where they belong.

type Key = string | number

export interface Ordered<T> {
  rows: T[]
  /** The held order no longer matches the values; sorting again would move rows. */
  stale: boolean
}

/**
 * Returns an ordering function for one table, to be called inside $derived. It
 * remembers the order it returned last; with `hold` false, or a new `epoch` (bumped
 * when the user sorts), it sorts from scratch.
 */
export function rowOrder<T>(key: (item: T) => Key) {
  let prev: Key[] = []
  let prevEpoch = NaN
  let stale = false

  return (items: T[], compare: (a: T, b: T) => number, hold: boolean, epoch: number): Ordered<T> => {
    let rows: T[]
    if (hold && epoch === prevEpoch) {
      const byKey = new Map(items.map((item) => [key(item), item]))
      rows = []
      for (const k of prev) {
        const item = byKey.get(k)
        if (item) {
          rows.push(item)
          byKey.delete(k)
        }
      }
      rows.push(...[...byKey.values()].sort(compare))
      // Stays set until the next sort, so the hint doesn't flicker as values wobble.
      stale ||= rows.some((row, i) => i > 0 && compare(rows[i - 1], row) > 0)
    } else {
      rows = [...items].sort(compare)
      stale = false
    }
    prev = rows.map(key)
    prevEpoch = epoch
    return { rows, stale }
  }
}
