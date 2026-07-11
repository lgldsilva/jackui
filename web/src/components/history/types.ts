export type Mode = 'browse' | 'global'

export type EntrySortKey = 'recent' | 'oldest' | 'most' | 'alpha'
export type ResultSortKey = 'seeders' | 'size' | 'date' | 'title'

export type SortDef = { key: ResultSortKey; label: string }
