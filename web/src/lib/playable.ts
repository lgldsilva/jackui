import { SearchResult } from '../api/client'

import { VIDEO_EXT_RE, AUDIO_EXT_RE } from './mediaExtensions'

// Jackett categories that typically contain playable media (video OR audio)
const VIDEO_CATEGORIES = new Set([
  2000, 2010, 2020, 2030, 2040, 2045, 2050, 2060, 2070, 2080,
  5000, 5010, 5020, 5030, 5040, 5045, 5050, 5060, 5070, 5080, 5090,
  100022,
])
const AUDIO_CATEGORIES = new Set([
  3000, 3010, 3020, 3030, 3040, 3050, 3060,
])

const VIDEO_HINT_RE = /\b(1080p|720p|480p|2160p|4k|bluray|web-dl|webrip|hdtv|x264|x265|hevc|h264|h265)\b/i
const AUDIO_HINT_RE = /\b(flac|mp3|320kbps|256kbps|192kbps|lossless|hi-?res|24bit|discography|album|ost|soundtrack)\b/i

// Truly non-playable: archives, ebooks, ISOs
const NEVER_PLAY_RE = /\.(epub|pdf|mobi|cbr|cbz|zip|rar|7z|tar|gz|iso|exe|dmg)$/i
const NEVER_PLAY_TAGS = /\b(ebook|audiobook[. ]?pdf|programs?|software|game[. ]?iso)\b/i

/**
 * Heuristic kind detection: best guess at whether this torrent is audio
 * (music album, audiobook, podcast) or video (movie, series, TV).
 *
 * Used by PlayerProvider to choose between the audio UI (cover + EQ) and the
 * full-screen video player. `fallback` is the user's Cinema/Música preference
 * (NavHeader): it ONLY decides the uncertain case — a title with a clear video
 * or audio signal always follows the content. Defaults to 'video' (the prior
 * behaviour) so callers that don't pass it are unaffected.
 */
export function detectKind(title: string, categoryId = 0, fallback: 'audio' | 'video' = 'video'): 'audio' | 'video' {
  if (AUDIO_EXT_RE.test(title)) return 'audio'
  if (VIDEO_EXT_RE.test(title)) return 'video'
  if (AUDIO_CATEGORIES.has(categoryId)) return 'audio'
  if (VIDEO_CATEGORIES.has(categoryId)) return 'video'
  if (AUDIO_HINT_RE.test(title)) return 'audio'
  if (VIDEO_HINT_RE.test(title)) return 'video'
  return fallback
}

/**
 * Kind of a single FILE inside a torrent (by path + the backend's isVideo flag).
 * Used to build the in-torrent track/episode queue: navigation stays within the
 * same kind (audio↔audio in an album, video↔video in a series). 'other' files
 * (e.g. .nfo, .jpg) are excluded from the queue.
 */
export function fileKind(path: string, isVideo?: boolean): 'audio' | 'video' | 'other' {
  if (isVideo || VIDEO_EXT_RE.test(path)) return 'video'
  if (AUDIO_EXT_RE.test(path)) return 'audio'
  return 'other'
}

/**
 * Is this result audio (music/audiobook)? Prefers the backend-resolved mediaKind
 * (parser.DetectKind), falling back to the title/category heuristic. Used by the
 * music-mode search filter — 'other'/unknown falls back to the heuristic, which
 * defaults to video, so the "mostrar tudo" escape exists for ambiguous releases.
 */
export function isAudioResult(r: SearchResult): boolean {
  if (r.mediaKind === 'audio') return true
  if (r.mediaKind === 'video') return false
  return detectKind(r.title, r.categoryId) === 'audio'
}

/**
 * Heuristic: can we stream this torrent (video or audio) in our player?
 * Uses positive signals (allowlist of extensions/categories/hints).
 * Falls back to "yes" for unknown — better to offer than to hide.
 */
export function isPlayable(result: SearchResult): boolean {
  if (!result.magnetUri) return false

  // Hard rejection: known non-media files
  if (NEVER_PLAY_RE.test(result.title)) return false
  if (NEVER_PLAY_TAGS.test(result.title)) return false

  // Positive signals (video OR audio)
  if (VIDEO_CATEGORIES.has(result.categoryId)) return true
  if (AUDIO_CATEGORIES.has(result.categoryId)) return true
  if (result.quality?.resolution) return true
  if (VIDEO_EXT_RE.test(result.title) || AUDIO_EXT_RE.test(result.title)) return true
  if (VIDEO_HINT_RE.test(result.title) || AUDIO_HINT_RE.test(result.title)) return true

  // Unknown — offer Play; player will tell user if the file can't be decoded.
  return true
}

/**
 * Build a minimal SearchResult suitable for "deep-link entrant" playback when
 * we only know an info_hash (and optionally a title + magnet). All non-essential
 * fields are zero/empty — the player only requires `infoHash` + `magnetUri`
 * to bootstrap streaming; metadata (file list, size, name) is fetched from
 * the streamer once the torrent is added.
 *
 * Why this helper: the SearchResult shape has 12+ required fields, most of
 * which are search-time only (seeders, tracker, age, etc.). For URL deep
 * links we don't have those — we only have `?play=HASH` and at best a
 * library entry name. Centralising the placeholder construction keeps the
 * PlayerProvider effect concise and ensures everyone uses the same defaults.
 */
export function syntheticResult(
  hash: string,
  title: string,
  magnet: string,
  mediaKind?: 'audio' | 'video' | 'other',
): SearchResult {
  return {
    title: title || hash,
    tracker: '',
    categoryId: 0,
    category: '',
    size: 0,
    seeders: 0,
    leechers: 0,
    age: '',
    magnetUri: magnet || `magnet:?xt=urn:btih:${hash}`,
    link: '',
    infoHash: hash,
    publishDate: '',
    // Authoritative kind when known (e.g. from the library entry on a deep-link
    // refresh) — lets currentKind pick audio UI without relying on the title
    // heuristic, which failed for audio playlists opened via ?play= on reload.
    mediaKind,
  }
}
// build marker 1779766535
