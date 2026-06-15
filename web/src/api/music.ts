import { api } from './http'

// MusicAlbum is one trending album from Apple's keyless RSS (via the backend
// proxy at /api/music/trending). Artist + Name seed a search; Artwork is the
// 512px cover.
export type MusicAlbum = {
  artist: string
  name: string
  artwork: string
  appleUrl: string
  releaseDate: string
}

// musicTrending returns the top albums for a country (Discover in Música mode).
// Empty on error — the grid degrades to a hint instead of failing, mirroring
// tmdbTrending.
export const musicTrending = async (opts?: { country?: string; limit?: number }): Promise<MusicAlbum[]> => {
  try {
    const params = new URLSearchParams()
    if (opts?.country) params.set('country', opts.country)
    if (opts?.limit) params.set('limit', String(opts.limit))
    const qs = params.toString()
    const path = qs ? `/music/trending?${qs}` : '/music/trending'
    const { data } = await api.get<{ albums: MusicAlbum[] }>(path, { validateStatus: () => true })
    return Array.isArray(data?.albums) ? data.albums : []
  } catch {
    return []
  }
}
