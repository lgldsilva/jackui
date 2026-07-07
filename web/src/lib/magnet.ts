/**
 * Canonical info-hash extraction shared by grouping, favorites, downloads and API.
 * Base32 btih (32 chars) is intentionally not decoded here — the backend coerces
 * it to hex before results reach the UI.
 */

const HEX40 = /^[0-9a-f]{40}$/

function normHex(s: string): string {
  const t = s.trim().toLowerCase()
  return HEX40.test(t) ? t : ''
}

/** Extract a lowercase 40-hex btih from a magnet URI, or '' when absent. */
export function extractInfoHashFromMagnet(magnet: string): string {
  const m = /[?&]xt=urn:btih:([^&]+)/i.exec(magnet)
  if (!m) return ''
  return normHex(decodeURIComponent(m[1]))
}

/** Resolve the best canonical hash from explicit field and/or magnet. */
export function canonicalInfoHash(infoHash?: string, magnetUri?: string): string {
  if (infoHash) {
    const h = normHex(infoHash)
    if (h) return h
  }
  if (magnetUri) return extractInfoHashFromMagnet(magnetUri)
  return ''
}
