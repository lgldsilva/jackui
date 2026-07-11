// Metadata de áudio local (tags ID3/Vorbis/MP4), capa embarcada e letras
// (proxy LrcLib). Extraído de local.ts (#417 follow-up).
import { api, withToken } from './http'
import { appendViewAs, withViewAs } from './local-base'

// AudioMeta is the tag metadata for a local audio file (server reads ID3/Vorbis/
// MP4 tags via dhowden/tag and caches them). Empty fields → fall back to filename.
export type AudioMeta = {
  title: string
  artist: string
  album: string
  albumArtist: string
  genre: string
  year: number
  trackNumber: number
  discNumber: number
  hasCover: boolean
}

// localAudioMeta fetches cached tags for a local audio file. Best-effort: a
// parse failure returns empty fields (200), never throws server-side.
export const localAudioMeta = async (mount: string, path: string): Promise<AudioMeta> => {
  const params = appendViewAs(new URLSearchParams({ mount, path }))
  const { data } = await api.get<AudioMeta>(`/local/audio/meta?${params}`)
  return data
}

// localAudioCoverURL builds the <img> URL for a local audio file's embedded
// album art (204 when none). Carries ?token= because <img> can't set headers.
export const localAudioCoverURL = (mount: string, path: string, tokenOverride?: string): string => {
  const params = new URLSearchParams({ mount, path })
  return withViewAs(withToken(`/api/local/audio/cover?${params}`, tokenOverride))
}

// Lyrics mirrors the backend LrcLib proxy result. source="" means none found.
export type Lyrics = { synced: string; plain: string; source: string }

// lyricsGet resolves lyrics for a track via the backend LrcLib proxy. Best-effort.
export const lyricsGet = async (
  title: string, artist: string, album: string, durationSec: number,
): Promise<Lyrics> => {
  const sp = new URLSearchParams({ title })
  if (artist) sp.set('artist', artist)
  if (album) sp.set('album', album)
  if (durationSec > 0) sp.set('duration', String(Math.round(durationSec)))
  const { data } = await api.get<Lyrics>(`/lyrics?${sp}`)
  return data
}
