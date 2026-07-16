import { SearchResult } from '../api/client'

// Layout item for the search list when "group series" is on: a season header
// followed by that season's episodes (in episode order).
export type SeriesLayoutItem =
  | { kind: 'header'; id: string; series: string; season: number; count: number }
  | { kind: 'result'; result: SearchResult }

// seriesKeyOf derives a canonical series name from a release name by cutting at
// the episode marker (S01E02 or 1x02) and normalizing separators. Returns ''
// when the title doesn't look like an episode.
// Episode marker preceded by a separator, matched with a fixed-width lookbehind
// instead of a `[ ._-]+` run. Both the old `/^(.*?)[ ._-]+…/` and a plain
// `[ ._-]+…` re-scan the whole separator run at every start position (O(n²) on
// long separator strings — Sonar S8786 / ReDoS). The lookbehind is O(1) per
// position and every quantifier below is bounded, so the scan is linear.
// The trailing `(?!\d)` (not a word boundary: '_' is a word char) stops "2x055"
// from matching as "2x05".
const EPISODE_MARKER = /(?<=[ ._-])(?:s\d{1,2}e\d{1,3}|\d{1,2}x\d{1,3})(?!\d)/i
const SEPARATORS = ' ._-'

export function seriesKeyOf(title: string): string {
  const m = EPISODE_MARKER.exec(title)
  if (!m) return ''
  // The lookbehind matches AT the marker, so the separator run is still in the
  // prefix — walk it back (linear) instead of capturing it in the regex.
  let end = m.index
  while (end > 0 && SEPARATORS.includes(title[end - 1])) end--
  return title.slice(0, end).replace(/[._]+/g, ' ').replace(/\s+/g, ' ').trim().toLowerCase()
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
type EpisodeIndex = Map<SearchResult, EpisodeInfo>
// series key -> season -> episodes
type SeasonTree = Map<string, Map<number, SearchResult[]>>

// Indexes every groupable episode and counts per series key.
function indexEpisodes(results: readonly SearchResult[]): { info: EpisodeIndex; counts: Map<string, number> } {
  const info: EpisodeIndex = new Map()
  const counts = new Map<string, number>()
  for (const r of results) {
    const i = episodeInfo(r)
    if (!i) continue
    info.set(r, i)
    counts.set(i.key, (counts.get(i.key) ?? 0) + 1)
  }
  return { info, counts }
}

function buildSeasonTree(results: readonly SearchResult[], info: EpisodeIndex, grouped: Set<string>): SeasonTree {
  const tree: SeasonTree = new Map()
  for (const r of results) {
    const i = info.get(r)
    if (!i || !grouped.has(i.key)) continue
    const seasons = tree.get(i.key) ?? new Map<number, SearchResult[]>()
    const eps = seasons.get(i.season) ?? []
    eps.push(r)
    seasons.set(i.season, eps)
    tree.set(i.key, seasons)
  }
  return tree
}

// Series alphabetically, seasons ascending, episodes by episode number.
function emitGroups(tree: SeasonTree, info: EpisodeIndex, out: SeriesLayoutItem[]): void {
  for (const key of [...tree.keys()].sort((a, b) => a.localeCompare(b))) {
    const seasons = tree.get(key)!
    for (const season of [...seasons.keys()].sort((a, b) => a - b)) {
      const eps = seasons.get(season)!.slice().sort((a, b) => (info.get(a)!.episode) - (info.get(b)!.episode))
      out.push({ kind: 'header', id: `${key}-s${season}`, series: titleCase(key), season, count: eps.length })
      for (const r of eps) out.push({ kind: 'result', result: r })
    }
  }
}

export function buildSeriesLayout(results: readonly SearchResult[], minEpisodes = 2): SeriesLayoutItem[] {
  const { info, counts } = indexEpisodes(results)
  const grouped = new Set(
    [...counts.entries()].filter(([, n]) => n >= minEpisodes).map(([k]) => k),
  )

  const out: SeriesLayoutItem[] = []
  emitGroups(buildSeasonTree(results, info, grouped), info, out)
  // Loose results (non-episodes or series below the threshold), original order.
  for (const r of results) {
    const i = info.get(r)
    if (i && grouped.has(i.key)) continue
    out.push({ kind: 'result', result: r })
  }
  return out
}
