// Generic clickable-column table sorting. The hook owns {key, dir} state
// (optionally persisted), and compareWithMissing is the shared comparator
// primitive: "missing" values sink to the END in BOTH directions, so failures/
// blanks never pollute the top of an ascending sort.

import { useEffect, useState } from 'react'
import { load, save } from './storage'

export type SortDir = 'asc' | 'desc'
export type AriaSort = 'ascending' | 'descending' | 'none'

export type SortState<K extends string> = { key: K; dir: SortDir }

export type TableSort<K extends string> = {
  sortKey: K
  dir: SortDir
  toggle: (key: K) => void
  ariaSort: (key: K) => AriaSort
}

// Pure transition so the toggle policy is unit-testable without React:
// clicking the active column inverts it; a new column enters with its own
// default direction (desc when listed in descFirst, asc otherwise).
export function nextSortState<K extends string>(
  current: SortState<K>,
  clicked: K,
  descFirst: readonly K[] = []
): SortState<K> {
  if (clicked === current.key) {
    return { key: clicked, dir: current.dir === 'asc' ? 'desc' : 'asc' }
  }
  return { key: clicked, dir: descFirst.includes(clicked) ? 'desc' : 'asc' }
}

// compareWithMissing orders two values for the given direction, except that
// values flagged by `missing` always sort AFTER present ones, regardless of
// direction. Default comparison: numeric for numbers, localeCompare for strings.
export function compareWithMissing<T extends number | string>(
  a: T,
  b: T,
  dir: SortDir,
  missing?: (v: T) => boolean
): number {
  const missA = missing ? missing(a) : false
  const missB = missing ? missing(b) : false
  if (missA || missB) return Number(missA) - Number(missB)
  const c = typeof a === 'number' && typeof b === 'number'
    ? a - b
    : String(a).localeCompare(String(b))
  return dir === 'asc' ? c : -c
}

export type TableSortOptions<K extends string> = {
  defaultKey: K
  defaultDir: SortDir
  descFirst?: readonly K[]
  persistKey?: string
}

function isSortState<K extends string>(v: unknown): v is SortState<K> {
  const s = v as SortState<K> | null
  return !!s && typeof s.key === 'string' && (s.dir === 'asc' || s.dir === 'desc')
}

export function useTableSort<K extends string>(
  { defaultKey, defaultDir, descFirst = [], persistKey }: TableSortOptions<K>
): TableSort<K> {
  const fallback: SortState<K> = { key: defaultKey, dir: defaultDir }
  // Same load/save pair usePersistedState (storage.ts) is built on, but gated
  // on persistKey — calling usePersistedState conditionally would break the
  // rules of hooks, and calling it with a throwaway key would litter storage.
  const [state, setState] = useState<SortState<K>>(() => {
    if (!persistKey) return fallback
    const stored = load<unknown>(persistKey, fallback)
    return isSortState<K>(stored) ? stored : fallback
  })
  useEffect(() => {
    if (persistKey) save(persistKey, state)
  }, [persistKey, state])

  return {
    sortKey: state.key,
    dir: state.dir,
    toggle: (key: K) => setState(s => nextSortState(s, key, descFirst)),
    ariaSort: (key: K) => {
      if (key !== state.key) return 'none'
      return state.dir === 'asc' ? 'ascending' : 'descending'
    },
  }
}
