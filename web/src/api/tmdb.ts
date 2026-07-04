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

// Soft session cache. Server already caches 30d but this keeps repeated titles
// (same card re-mounting) free within a session.
const tmdbSessionCache = new Map<string, TmdbMatch | null>()

// COALESCING: instead of N visible cards each firing a GET /tmdb/match, calls
// within a short window (~40ms) become ONE POST /tmdb/match/batch. Preserves the
// lazy-load (the IntersectionObserver decides WHEN each card asks; only the burst
// is grouped) — no change needed in the 5 list pages that call tmdbMatch.
const tmdbQueue: { title: string; resolve: (m: TmdbMatch | null) => void }[] = []
let tmdbFlushTimer: ReturnType<typeof setTimeout> | null = null

function flushTmdbQueue() {
  if (tmdbFlushTimer) { clearTimeout(tmdbFlushTimer); tmdbFlushTimer = null }
  const batch = tmdbQueue.splice(0)
  if (batch.length === 0) return
  const titles = [...new Set(batch.map(b => b.title))]
  api.post<{ matches?: Record<string, TmdbMatch> }>('/tmdb/match/batch', { titles }, { validateStatus: () => true })
    .then(r => {
      const matches = r.status === 200 ? (r.data?.matches ?? {}) : {}
      for (const b of batch) {
        const m = matches[b.title] ?? null
        tmdbSessionCache.set(b.title.trim().toLowerCase(), m)
        b.resolve(m)
      }
    })
    .catch(() => { for (const b of batch) b.resolve(null) })
}

export const tmdbMatch = (title: string): Promise<TmdbMatch | null> => {
  const key = title.trim().toLowerCase()
  if (tmdbSessionCache.has(key)) return Promise.resolve(tmdbSessionCache.get(key)!)
  return new Promise(resolve => {
    tmdbQueue.push({ title, resolve })
    if (tmdbQueue.length >= 80) flushTmdbQueue()
    else if (!tmdbFlushTimer) tmdbFlushTimer = setTimeout(flushTmdbQueue, 40)
  })
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

// tmdbDismissRecommendation persists a per-user "never show me this again" for a
// recommended title. The server excludes it from every future rebuild, so the
// dismissal is durable (unlike a client-only hide that a reload would undo).
// Best-effort: the UI removes the card optimistically and tolerates a failure.
export const tmdbDismissRecommendation = async (kind: 'movie' | 'tv', tmdbId: number): Promise<void> => {
  await api.post('/recommendations/dismiss', { kind, tmdbId }, { validateStatus: () => true })
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

// ─── Trailers ───────────────────────────────────────────────────────────────

export type TmdbVideo = {
  key: string // YouTube video id
  name: string
  type: 'Trailer' | 'Teaser'
  official: boolean
}

// Session cache: trailers don't change mid-session and the button can be
// clicked repeatedly from cards sharing the same title.
const videosCache = new Map<string, TmdbVideo[]>()

// tmdbVideos returns the YouTube trailers of a title, best first. Empty array
// when there's none / TMDB off / error — callers degrade to "no trailer".
export const tmdbVideos = async (kind: 'movie' | 'tv', id: number): Promise<TmdbVideo[]> => {
  const cacheKey = `${kind}:${id}`
  const cached = videosCache.get(cacheKey)
  if (cached) return cached
  try {
    const { data } = await api.get<TmdbVideo[]>(`/tmdb/videos?kind=${kind}&id=${id}`, { validateStatus: () => true })
    const videos = Array.isArray(data) ? data : []
    videosCache.set(cacheKey, videos)
    return videos
  } catch {
    return []
  }
}
