import { TmdbRecommendation } from '../api/client'

// Groups personalized recommendations by their `becauseOf` attribution (the
// watched title that seeded them) so the Discover page can render one collapsible
// topic per source title instead of a single flat grid. Pure + client-side: it
// works over the already-loaded list, so it never triggers extra requests.

// A topic bundles every recommendation that shares the same `becauseOf` source.
// `key` is a stable identifier (used for the localStorage collapse set);
// `label` is the human-readable "Porque você viu X" header. `items` keeps the
// order in which the recs arrived (the server already ranks them).
export type RecGroup = {
  readonly key: string
  readonly label: string
  readonly items: readonly TmdbRecommendation[]
}

// Recs without a `becauseOf` fall into one generic topic at the very end so the
// UI never silently drops them. The key is reserved (no real title collides with
// it because real keys are prefixed with "because:").
export const OTHER_GROUP_KEY = 'other'
export const OTHER_GROUP_LABEL = 'Outras recomendações'

// groupRecommendations folds the flat list into topics keyed by `becauseOf`,
// preserving first-seen order both for the groups and the items within them.
// Recs lacking a `becauseOf` are collected into a single trailing "Outras
// recomendações" group. Returns [] for an empty/falsy input.
export function groupRecommendations(recs: readonly TmdbRecommendation[] | null | undefined): RecGroup[] {
  if (!recs || recs.length === 0) return []

  const order: string[] = []
  const byKey = new Map<string, { label: string; items: TmdbRecommendation[] }>()

  let other: TmdbRecommendation[] | null = null

  for (const r of recs) {
    const source = r.becauseOf?.trim()
    if (!source) {
      other ??= []
      other.push(r)
      continue
    }
    // Namespacing the key keeps it distinct from OTHER_GROUP_KEY and stable as a
    // localStorage identifier even if a title happens to be "other".
    const key = `because:${source.toLowerCase()}`
    const existing = byKey.get(key)
    if (existing) {
      existing.items.push(r)
    } else {
      byKey.set(key, { label: `Porque você viu ${source}`, items: [r] })
      order.push(key)
    }
  }

  const groups: RecGroup[] = order.map(key => {
    const g = byKey.get(key)!
    return { key, label: g.label, items: g.items }
  })

  if (other && other.length > 0) {
    groups.push({ key: OTHER_GROUP_KEY, label: OTHER_GROUP_LABEL, items: other })
  }

  return groups
}
