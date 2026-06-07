import { SearchResult } from '../api/client'

// Layout item for the search list when "group series" is on: a season header
// followed by that season's episodes (in episode order).
export type SeriesLayoutItem =
  | { kind: 'header'; id: string; series: string; season: number; count: number }
  | { kind: 'result'; result: SearchResult }

// seriesKeyOf derives a canonical series name from a release name by cutting at
// the episode marker (S01E02 or 1x02) and normalizing separators. Returns ''
// when the title doesn't look like an episode.
export function seriesKeyOf(title: string): string {
  // A word boundary after the marker fails when the next separator is '_' (a
  // word char), so we use a negative-lookahead on a digit instead: the marker
  // ends and isn't followed by another digit (avoids matching "2x055" partially).
  const m = /^(.*?)[ ._-]+(?:s\d{1,2}e\d{1,3}|\d{1,2}x\d{1,3})(?![0-9])/i.exec(title)
  if (!m) return ''
  return m[1].replace(/[._]+/g, ' ').replace(/\s+/g, ' ').trim().toLowerCase()
}

function titleCase(s: string): string {
  return s.replace(/\b\w/g, c => c.toUpperCase())
}

type EpisodeInfo = { key: string; season: number; episode: number }

// The backend already parses season/episode into quality; we require both plus
// an extractable series name to treat a result as a groupable episode.
function episodeInfo(r: SearchResult): EpisodeInfo | null {
  const s = r.quality?.season
  const e = r.quality?.episode
  if (s == null || e == null) return null
  const key = seriesKeyOf(r.title)
  if (!key) return null
  return { key, season: s, episode: e }
}

// buildSeriesLayout reorganizes results into series→season blocks (a header plus
// episodes sorted by episode number), with everything else ("loose") appended
// afterwards in the original order. Only series with at least `minEpisodes`
// episodes are grouped — folding a lone episode just adds noise. Results
// themselves are never mutated.
export function buildSeriesLayout(results: readonly SearchResult[], minEpisodes = 2): SeriesLayoutItem[] {
  const info = new Map<SearchResult, EpisodeInfo>()
  const counts = new Map<string, number>()
  for (const r of results) {
    const i = episodeInfo(r)
    if (!i) continue
    info.set(r, i)
    counts.set(i.key, (counts.get(i.key) ?? 0) + 1)
  }
  const grouped = new Set(
    [...counts.entries()].filter(([, n]) => n >= minEpisodes).map(([k]) => k),
  )

  // series key -> season -> episodes
  const tree = new Map<string, Map<number, SearchResult[]>>()
  for (const r of results) {
    const i = info.get(r)
    if (!i || !grouped.has(i.key)) continue
    const seasons = tree.get(i.key) ?? new Map<number, SearchResult[]>()
    const eps = seasons.get(i.season) ?? []
    eps.push(r)
    seasons.set(i.season, eps)
    tree.set(i.key, seasons)
  }

  const out: SeriesLayoutItem[] = []
  for (const key of [...tree.keys()].sort()) {
    const seasons = tree.get(key)!
    for (const season of [...seasons.keys()].sort((a, b) => a - b)) {
      const eps = seasons.get(season)!.slice().sort((a, b) => (info.get(a)!.episode) - (info.get(b)!.episode))
      out.push({ kind: 'header', id: `${key}-s${season}`, series: titleCase(key), season, count: eps.length })
      for (const r of eps) out.push({ kind: 'result', result: r })
    }
  }
  // Loose results (non-episodes or series below the threshold), original order.
  for (const r of results) {
    const i = info.get(r)
    if (i && grouped.has(i.key)) continue
    out.push({ kind: 'result', result: r })
  }
  return out
}
