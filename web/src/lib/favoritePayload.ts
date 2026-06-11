// Pure logic for the "quick favorite" payload (the heart on ResultCard).
//
// Favoriting straight from a card — without ever opening it — must still link
// the favorite to a magnet/infoHash, otherwise FavoritesPage renders it inert
// (Play/Download are disabled when `fav.magnet` is empty). History rows from
// private trackers (amigosshare & cia) often carry only a `.torrent` link, so
// the payload may need a backend conversion before the favorite is written.

/** The slice of SearchResult that favorite linkage cares about. */
export type FavoriteLinkSource = {
  readonly magnetUri?: string
  readonly link?: string
  readonly infoHash?: string
}

export type FavoritePayload = {
  infoHash: string
  magnet: string
  /**
   * Which field produced the linkage:
   *  - 'magnet':   result already had a magnet URI
   *  - 'link':     backend converted the .torrent link (caller may backfill the result)
   *  - 'infoHash': tracker-less magnet synthesized from the bare infoHash
   *  - 'none':     nothing to link — favorite will be name-only (legacy behavior)
   */
  source: 'magnet' | 'link' | 'infoHash' | 'none'
}

/** Backend .torrent→magnet conversion (GET /api/convert/torrent-to-magnet). */
export type TorrentLinkResolver = (url: string) => Promise<{ magnet: string; infoHash: string }>

/**
 * Extracts a canonical lowercase 40-hex infoHash from a magnet's
 * `xt=urn:btih:` param. Returns '' when absent or non-canonical (base32 is
 * left alone — the backend coerces it to hex before results reach the UI).
 */
export function extractInfoHashFromMagnet(magnet: string): string {
  const m = /[?&]xt=urn:btih:([^&]+)/i.exec(magnet)
  if (!m) return ''
  const h = decodeURIComponent(m[1]).trim().toLowerCase()
  return /^[0-9a-f]{40}$/.test(h) ? h : ''
}

/**
 * Tracker-less magnet from a bare infoHash. anacrolix resolves peers via DHT,
 * and it's the same shape the backend repair migration writes for favorites.
 */
export function magnetFromInfoHash(infoHash: string): string {
  return `magnet:?xt=urn:btih:${infoHash}`
}

/**
 * Resolves the {infoHash, magnet} pair to persist with a favorite, mirroring
 * what the full open-card flow links. Order of preference:
 *  1. the result's own magnet;
 *  2. backend conversion of the `.torrent` link (resolver injected for tests);
 *  3. magnet synthesized from a bare infoHash;
 *  4. empty (name-only favorite — unchanged legacy behavior).
 * A resolver failure (dead link, offline indexer) falls through to 3/4 instead
 * of throwing: a partially-linked favorite beats an aborted action.
 */
export async function buildFavoritePayload(
  result: FavoriteLinkSource,
  resolveTorrentLink: TorrentLinkResolver,
): Promise<FavoritePayload> {
  if (result.magnetUri) {
    return {
      infoHash: result.infoHash || extractInfoHashFromMagnet(result.magnetUri),
      magnet: result.magnetUri,
      source: 'magnet',
    }
  }
  if (result.link) {
    try {
      const conv = await resolveTorrentLink(result.link)
      const infoHash = conv.infoHash || extractInfoHashFromMagnet(conv.magnet || '')
      const magnet = conv.magnet || (infoHash ? magnetFromInfoHash(infoHash) : '')
      if (magnet || infoHash) return { infoHash, magnet, source: 'link' }
    } catch {
      // fall through to the infoHash fallback below
    }
  }
  if (result.infoHash) {
    return { infoHash: result.infoHash, magnet: magnetFromInfoHash(result.infoHash), source: 'infoHash' }
  }
  return { infoHash: '', magnet: '', source: 'none' }
}
