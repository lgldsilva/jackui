import { SearchResult } from '../api/client'

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
 * Secondary dedup: entries without infoHash AND entries whose infoHash doesn't
 * collide get a `name|size` fallback bucket. Many trackers don't expose the
 * info_hash via Jackett, so two listings of the same release (same title, same
 * size) would otherwise show as visually-duplicate cards — and favoriting one
 * would visibly favorite the other (favorites cache keys on title). Bucketing
 * by `name|size` collapses those into one card with `alsoIn` filled in.
 */
export function groupByInfoHash<T extends SearchResult>(results: T[]): T[] {
  const hashGroups = new Map<string, T[]>()
  const noHash: T[] = []

  for (const r of results) {
    if (!r.infoHash) {
      noHash.push(r)
      continue
    }
    const arr = hashGroups.get(r.infoHash) || []
    arr.push(r)
    hashGroups.set(r.infoHash, arr)
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

  // Secondary pass: dedup hash-grouped + noHash entries by a NORMALIZED
  // title plus a size-with-tolerance bucket. Two reasons each:
  //
  //   1) Title normalization: trackers publish the same release with cosmetic
  //      differences in punctuation/whitespace — "Disturbed - Discography
  //      2000-2019" vs "Disturbed Discography 2000 2019" are visibly the same.
  //      Strip non-alphanumerics down to ASCII letters/digits + single spaces.
  //
  //   2) Size bucket of 10 MiB: trackers can report slightly different byte
  //      counts for the same release (extra padding files, info dictionary
  //      variations, BEP-47 padding rules). 3.86 GB on tracker A might be
  //      4,144,987,136 bytes and on tracker B 4,144,985,088 — both display
  //      "3.86 GB" but exact-match misses. Bucketing to 10 MiB groups them.
  //
  // Risk of false positives: two genuinely-different releases with similar
  // title/size collapse. Unlikely in practice since size bucket is tight
  // enough that re-rips with different bitrate (typical 50-200 MB delta)
  // land in distinct buckets.
  const normalizeTitle = (s: string) =>
    s.toLowerCase()
      .normalize('NFD').replaceAll(/[\u0300-\u036f]/g, '') // strip diacritics
      .replaceAll(/[^a-z0-9]+/g, ' ')
      .trim()
  const sizeBucket = (bytes: number) => Math.floor(bytes / (10 * 1024 * 1024)) // 10 MiB granularity
  const finalBuckets = new Map<string, T[]>()
  const seenKey = (r: T) => `${normalizeTitle(r.title)}|${sizeBucket(r.size)}`
  for (const r of [...hashOut, ...noHash]) {
    const k = seenKey(r)
    const arr = finalBuckets.get(k) || []
    arr.push(r)
    finalBuckets.set(k, arr)
  }

  const out: T[] = []
  for (const [, arr] of finalBuckets) {
    if (arr.length === 1) { out.push(arr[0]); continue }
    arr.sort((a, b) => {
      // Prefer entries with magnetUri (Play button works), then seeders.
      const am = a.magnetUri ? 1 : 0
      const bm = b.magnetUri ? 1 : 0
      if (am !== bm) return bm - am
      return b.seeders - a.seeders
    })
    const primary = arr[0]
    const mergedAlsoIn = new Set<string>(primary.alsoIn || [])
    const extraTrackers: string[] = []
    // Aggregate seeders/leechers across the bucket. The trackers don't
    // necessarily expose the same swarm size — taking MAX is conservative
    // (avoids double-counting peers seen by both) while still surfacing the
    // best signal so the card surfaces the strongest source.
    let bestSeeders = primary.seeders
    let bestLeechers = primary.leechers
    for (const r of arr.slice(1)) {
      if (r.tracker) mergedAlsoIn.add(r.tracker)
      ;(r.alsoIn || []).forEach(t => mergedAlsoIn.add(t))
      for (const t of extractTrackers(r.magnetUri)) extraTrackers.push(t)
      if (r.seeders > bestSeeders) bestSeeders = r.seeders
      if (r.leechers > bestLeechers) bestLeechers = r.leechers
    }
    if (primary.tracker) mergedAlsoIn.delete(primary.tracker) // primary tracker is shown separately
    out.push({
      ...primary,
      magnetUri: mergeTrackersIntoMagnet(primary.magnetUri, extraTrackers),
      seeders: bestSeeders,
      leechers: bestLeechers,
      alsoIn: mergedAlsoIn.size > 0 ? Array.from(mergedAlsoIn) : undefined,
    })
  }
  return out
}
