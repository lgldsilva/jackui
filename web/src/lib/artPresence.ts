// Pure helpers for Perf #8: only mount art <img> when batch/single resolve
// says art exists — avoids a GET /stream/art/:hash (often 204) per card.

/** Subset of ArtResolveResult used for presence decisions. */
export type ArtResultLike = {
  source?: string
  resolved?: boolean
  status?: string
}

/**
 * True when a resolve response indicates art is available to GET.
 * - `source` set (torrent/tmdb/web/frame, incl. reused) → yes
 * - explicit `resolved: true` without source → yes (defensive)
 * - `resolved: false` / empty → no
 */
export function artResultHasArt(r: ArtResultLike | null | undefined): boolean {
  if (!r) return false
  if (r.source) return true
  return r.resolved === true
}

/** Per-hash presence map from a batch resolve payload. */
export function artPresenceFromBatch(
  results: Record<string, ArtResultLike>,
): Record<string, boolean> {
  const out: Record<string, boolean> = {}
  for (const [hash, r] of Object.entries(results)) {
    out[hash] = artResultHasArt(r)
  }
  return out
}

/**
 * Cache-buster timestamps for hashes that gained art (defeats a prior 204
 * cached by the browser for the same URL).
 */
export function artBustsFromBatch(
  results: Record<string, ArtResultLike>,
  now = Date.now(),
): Record<string, number> {
  const busts: Record<string, number> = {}
  for (const [hash, r] of Object.entries(results)) {
    if (artResultHasArt(r)) busts[hash] = now
  }
  return busts
}

export function mergeArtBustMaps(
  prev: Record<string, number>,
  bumps: Record<string, number>,
): Record<string, number> {
  return { ...prev, ...bumps }
}

export function mergeArtPresence(
  prev: Record<string, boolean>,
  next: Record<string, boolean>,
): Record<string, boolean> {
  return { ...prev, ...next }
}

/**
 * Whether to mount the per-torrent art <img>.
 *
 * - `hasArt === true` → mount (batch/single said yes)
 * - `hasArt === false` → skip (known miss; no GET)
 * - `hasArt === undefined` + `requireKnown` → skip until batch seeds presence
 * - `hasArt === undefined` + !requireKnown → legacy (Thumbnail without batch)
 */
export function shouldMountArtImg(opts: {
  infoHash?: string | null
  hasArt?: boolean
  artFailed?: boolean
  /** Library grid: only mount after batch marks presence. */
  requireKnown?: boolean
}): boolean {
  if (!opts.infoHash || opts.artFailed) return false
  if (opts.hasArt === true) return true
  if (opts.hasArt === false) return false
  return !opts.requireKnown
}

/** Append `?_=` / `&_=` bust query when bust > 0. */
export function withArtBust(url: string, bust?: number): string {
  if (bust == null || bust <= 0) return url
  const sep = url.includes('?') ? '&' : '?'
  return `${url}${sep}_=${bust}`
}
