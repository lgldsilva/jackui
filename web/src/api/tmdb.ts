import { api } from './http'

// ─── TMDB enrichment ──────────────────────────────────────────────────────

export type TmdbMatch = {
  tmdbId: number
  imdbId?: string
  title: string
  originalTitle?: string // título original (não traduzido) — usado pra semear a busca
  year: number
  posterUrl: string
  overview: string
  voteAverage: number
  imdbRating?: number
  kind: 'movie' | 'tv'
  popularity?: number
  direction?: 'up' | 'down' | 'new' | 'same' // movimento no ranking vs semana passada
  rankDelta?: number
}

// In-memory dedupe for in-flight requests + soft session cache. Server already
// caches 30d but this prevents N visible cards from firing N parallel requests
// for the same title.
const tmdbInFlight = new Map<string, Promise<TmdbMatch | null>>()
const tmdbSessionCache = new Map<string, TmdbMatch | null>()

export const tmdbMatch = async (title: string): Promise<TmdbMatch | null> => {
  const key = title.trim().toLowerCase()
  if (tmdbSessionCache.has(key)) return tmdbSessionCache.get(key)!
  if (tmdbInFlight.has(key)) return tmdbInFlight.get(key)!
  const p = (async () => {
    try {
      const r = await api.get<TmdbMatch>(`/tmdb/match?title=${encodeURIComponent(title)}`, { validateStatus: () => true })
      if (r.status === 200) {
        tmdbSessionCache.set(key, r.data)
        return r.data
      }
      tmdbSessionCache.set(key, null)
      return null
    } catch {
      return null
    } finally {
      tmdbInFlight.delete(key)
    }
  })()
  tmdbInFlight.set(key, p)
  return p
}

// tmdbTrending returns this week's trending movies + shows for the Discover page.
// Empty array when TMDB is disabled (no key) or on error — the page degrades to
// an "enable TMDB" hint rather than failing.
export type TmdbGenre = { id: number; name: string }

// tmdbTrending returns the trending list. With year/genre it switches to TMDB
// /discover (filtered); without, the weekly trending (carrying ↑/↓ direction).
export const tmdbTrending = async (opts?: { year?: number; genre?: number }): Promise<TmdbMatch[]> => {
  try {
    const params = new URLSearchParams()
    if (opts?.year) params.set('year', String(opts.year))
    if (opts?.genre) params.set('genre', String(opts.genre))
    const qs = params.toString()
    const path = qs ? `/tmdb/trending?${qs}` : '/tmdb/trending'
    const { data } = await api.get<TmdbMatch[]>(path, { validateStatus: () => true })
    return Array.isArray(data) ? data : []
  } catch {
    return []
  }
}

// A recommendation is a TMDB match plus why it surfaced (the watched title that
// seeded it) and how many watched titles point to it.
export type TmdbRecommendation = TmdbMatch & {
  becauseOf?: string
  score?: number
}

// tmdbRecommendations returns personalized recommendations derived from the
// user's watched library. Empty array when TMDB is disabled or nothing has been
// watched yet — the Discover page just hides the section.
export const tmdbRecommendations = async (): Promise<TmdbRecommendation[]> => {
  try {
    const { data } = await api.get<TmdbRecommendation[]>('/recommendations', { validateStatus: () => true })
    return Array.isArray(data) ? data : []
  } catch {
    return []
  }
}

// tmdbGenres returns the merged movie+tv genre list for the Discover filter.
export const tmdbGenres = async (): Promise<TmdbGenre[]> => {
  try {
    const { data } = await api.get<TmdbGenre[]>('/tmdb/genres', { validateStatus: () => true })
    return Array.isArray(data) ? data : []
  } catch {
    return []
  }
}
