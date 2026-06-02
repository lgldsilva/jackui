import { SearchResult } from '../api/client'

/**
 * Returns a canonical grouping key for a result: a lowercase 40-hex infoHash.
 * Prefers the dedicated field, falling back to the magnet's `xt=urn:btih:` when
 * the field is absent or non-canonical. The backend already canonicalizes this,
 * but deep links, history rows and synthetic results can still reach the grouper
 * with a raw/uppercase hash or none at all — without this, the same torrent
 * scatters across buckets and renders as duplicate cards. Returns '' when no
 * valid hash can be derived (caller then falls back to the name|size bucket).
 *
 * Base32 btih (32 chars) is intentionally not decoded here — the backend coerces
 * it to hex before results reach the UI.
 */
function canonicalHash(infoHash: string | undefined, magnetUri: string | undefined): string {
  const norm = (s: string): string => {
    const t = s.trim().toLowerCase()
    return /^[0-9a-f]{40}$/.test(t) ? t : ''
  }
  if (infoHash) {
    const h = norm(infoHash)
    if (h) return h
  }
  if (magnetUri) {
    const m = /[?&]xt=urn:btih:([^&]+)/i.exec(magnetUri)
    if (m) {
      const h = norm(decodeURIComponent(m[1]))
      if (h) return h
    }
  }
  return ''
}

/**
 * Pulls every `tr=` (announce URL) out of a magnet URI. Returns the URLs
 * URL-decoded so the merge step can dedupe on the canonical form.
 */
function extractTrackers(magnet: string | undefined): string[] {
  if (!magnet) return []
  const q = magnet.split('?')[1]
  if (!q) return []
  try {
    return new URLSearchParams(q).getAll('tr').filter(Boolean)
  } catch {
    return []
  }
}

/**
 * Returns `magnet` with `extraTrackers` appended as additional `tr=` params,
 * skipping any that are already present. anacrolix's AddMagnet honors the full
 * announce list, so a "fattened" magnet from one tracker's listing can pull
 * peers from every other tracker that indexed the same infoHash.
 */
function mergeTrackersIntoMagnet(magnet: string, extraTrackers: string[]): string {
  if (!magnet || extraTrackers.length === 0) return magnet
  const qIdx = magnet.indexOf('?')
  if (qIdx < 0) return magnet
  try {
    const params = new URLSearchParams(magnet.slice(qIdx + 1))
    const existing = new Set(params.getAll('tr'))
    let appended = 0
    for (const t of extraTrackers) {
      if (!t || existing.has(t)) continue
      params.append('tr', t)
      existing.add(t)
      appended++
    }
    if (appended === 0) return magnet
    return `${magnet.slice(0, qIdx + 1)}${params.toString()}`
  } catch {
    return magnet
  }
}

/**
 * Groups results by infoHash. When the same infoHash appears on multiple trackers,
 * keeps the entry with the most seeders (and prefers entries with magnetUri) as
 * primary and lists the other trackers in `alsoIn`. Also folds every secondary
 * magnet's `tr=` announce URLs into the primary's magnet so Play/Download can
 * reach peers indexed only by the runners-up.
 *
 * Secondary dedup: only entries WITHOUT a usable infoHash get a `name|size`
 * fallback bucket, and they collapse only among themselves. Many trackers don't
 * expose the info_hash via Jackett, so two hash-less listings of the same release
 * (same title, same size) would otherwise show as visually-duplicate cards — and
 * favoriting one would visibly favorite the other (favorites cache keys on title).
 * A hash-BEARING result is a confirmed-distinct torrent and is never collapsed by
 * this fuzzy proxy, so a hash-less private listing (e.g. amigos-share) is never
 * silently absorbed into a same-title public card and hidden.
 */
function mergeIntoBucket<T extends SearchResult>(
  arr: T[],
  extractTrackers: (magnet: string | undefined) => string[],
  mergeTrackersIntoMagnet: (magnet: string, extraTrackers: string[]) => string,
): T {
  arr.sort((a, b) => {
    const am = a.magnetUri ? 1 : 0
    const bm = b.magnetUri ? 1 : 0
    if (am !== bm) return bm - am
    return b.seeders - a.seeders
  })
  const primary = arr[0]
  const mergedAlsoIn = new Set<string>(primary.alsoIn || [])
  const extraTrackers: string[] = []
  let bestSeeders = primary.seeders
  let bestLeechers = primary.leechers
  for (const r of arr.slice(1)) {
    if (r.tracker) { mergedAlsoIn.add(r.tracker) }
    (r.alsoIn || []).forEach(t => mergedAlsoIn.add(t))
    for (const t of extractTrackers(r.magnetUri)) extraTrackers.push(t)
    if (r.seeders > bestSeeders) bestSeeders = r.seeders
    if (r.leechers > bestLeechers) bestLeechers = r.leechers
  }
  if (primary.tracker) mergedAlsoIn.delete(primary.tracker)
  return {
    ...primary,
    magnetUri: mergeTrackersIntoMagnet(primary.magnetUri, extraTrackers),
    seeders: bestSeeders,
    leechers: bestLeechers,
    alsoIn: mergedAlsoIn.size > 0 ? Array.from(mergedAlsoIn) : undefined,
  }
}

function dedupNameSizeBuckets<T extends SearchResult>(
  hashOut: T[], noHash: T[],
  extractTrackers: (magnet: string | undefined) => string[],
  mergeTrackersIntoMagnet: (magnet: string, extraTrackers: string[]) => string,
): T[] {
  const normalizeTitle = (s: string) =>
    s.toLowerCase()
      .normalize('NFD').replaceAll(/[\u0300-\u036f]/g, '')
      .replaceAll(/[^a-z0-9]+/g, ' ')
      .trim()
  const sizeBucket = (bytes: number) => Math.floor(bytes / (10 * 1024 * 1024))
  // Hash-bearing entries are confirmed-distinct torrents (grouped by infoHash
  // above), so they pass through untouched. Only hash-LESS listings — private
  // trackers like amigos-share that don't expose info_hash via Jackett — get the
  // name|size fallback bucket, and only among THEMSELVES. This stops a private
  // listing from being absorbed into a same-title/size public (magnet) result and
  // silently hidden, while still collapsing duplicate hash-less listings.
  const out: T[] = [...hashOut]
  const finalBuckets = new Map<string, T[]>()
  const seenKey = (r: T) => `${normalizeTitle(r.title)}|${sizeBucket(r.size)}`
  for (const r of noHash) {
    const k = seenKey(r)
    const arr = finalBuckets.get(k) || []
    arr.push(r)
    finalBuckets.set(k, arr)
  }
  for (const [, arr] of finalBuckets) {
    out.push(arr.length === 1 ? arr[0] : mergeIntoBucket(arr, extractTrackers, mergeTrackersIntoMagnet))
  }
  return out
}

export function groupByInfoHash<T extends SearchResult>(results: T[]): T[] {
  const hashGroups = new Map<string, T[]>()
  const noHash: T[] = []

  for (const r of results) {
    const key = canonicalHash(r.infoHash, r.magnetUri)
    if (!key) {
      noHash.push(r)
      continue
    }
    const arr = hashGroups.get(key) || []
    arr.push(r)
    hashGroups.set(key, arr)
  }

  // Sort within each hash group: highest seeders first, magnet-bearing wins ties.
  const hashOut: T[] = []
  for (const [, arr] of hashGroups) {
    arr.sort((a, b) => {
      if (b.seeders !== a.seeders) return b.seeders - a.seeders
      const am = a.magnetUri ? 1 : 0
      const bm = b.magnetUri ? 1 : 0
      return bm - am
    })
    const primary = arr[0]
    const others = arr.slice(1).map(r => r.tracker).filter(Boolean)
    // Fold every secondary magnet's tr= URLs into the primary so Play/Download
    // pulls peers from every tracker that indexed this infoHash.
    const extraTrackers: string[] = []
    for (const r of arr.slice(1)) {
      for (const t of extractTrackers(r.magnetUri)) extraTrackers.push(t)
    }
    hashOut.push({
      ...primary,
      magnetUri: mergeTrackersIntoMagnet(primary.magnetUri, extraTrackers),
      alsoIn: others.length > 0 ? others : undefined,
    })
  }

  const out = dedupNameSizeBuckets(hashOut, noHash, extractTrackers, mergeTrackersIntoMagnet)
  return out
}
