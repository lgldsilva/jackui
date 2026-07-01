// dedupeMatches removes repeated TMDB titles from a list, keeping the first
// occurrence. The key is `${kind}-${tmdbId}` — the SAME identity the Discover
// grid uses as its React key — so a deduped list can never produce two children
// with the same key (which corrupts reconciliation and visibly duplicates cards
// as the filtered subset changes). Defense-in-depth: the server also dedupes,
// but this also cleans responses already cached server-side before the fix.
// Pure + generic so it covers TmdbMatch and TmdbRecommendation alike.

export function dedupeMatches<T extends { kind: string; tmdbId: number }>(
  list: readonly T[],
): T[] {
  const seen = new Set<string>()
  const out: T[] = []
  for (const m of list) {
    const key = `${m.kind}-${m.tmdbId}`
    if (seen.has(key)) continue
    seen.add(key)
    out.push(m)
  }
  return out
}
