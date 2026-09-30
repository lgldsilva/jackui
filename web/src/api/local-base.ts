// Shared base for the /api/local* modules: the "pseudo info-hash"
// (`local-<b64>`) that lets a disk file pass as a torrent in PlayerModal,
// and the "view as user" (admin) state that rewrites mount/path queries.
// Isolated here so local.ts and the sibling modules (local-cache,
// local-audio, …) import without cycles. Extracted from local.ts (#417 follow-up).

// ─── Local file source (pseudo-hash routing) ─────────────────────────────
//
// Local files use a "pseudo info-hash" in the format `local-<base64url(json{mount,path})>`.
// PlayerModal and the other consumers keep thinking they're dealing with a normal
// torrent — the functions below (streamProbe, streamSidecars, subtitlesAuto, etc.)
// detect the prefix and route to `/api/local/*` instead of `/api/stream/*`.
//
// Benefit: PlayerModal needs no changes (zero risk on the torrent path that already works).

const LOCAL_PREFIX = 'local-'

export function isLocalHash(hash: string): boolean {
  return typeof hash === 'string' && hash.startsWith(LOCAL_PREFIX)
}

export function buildLocalHash(mount: string, path: string): string {
  const json = JSON.stringify({ mount, path })
  // base64url, no padding (URL-safe)
  const bytes = new TextEncoder().encode(json)
  let bin = ''
  for (const byte of bytes) bin += String.fromCodePoint(byte)
  const b64 = btoa(bin)
    .replaceAll('+', '-')
    .replaceAll('/', '_')
    .replaceAll('=', '')
  return LOCAL_PREFIX + b64
}

export function parseLocalHash(hash: string): { mount: string; path: string } | null {
  if (!isLocalHash(hash)) return null
  try {
    let b64 = hash.slice(LOCAL_PREFIX.length).replaceAll('-', '+').replaceAll('_', '/')
    while (b64.length % 4) b64 += '='
    const raw = atob(b64)
    const rawBytes = new Uint8Array(raw.length)
    for (let i = 0; i < raw.length; i++) rawBytes[i] = raw.codePointAt(i) ?? 0
    const json = new TextDecoder().decode(rawBytes)
    const parsed = JSON.parse(json)
    if (typeof parsed.mount === 'string' && typeof parsed.path === 'string') return parsed
    return null
  } catch {
    return null
  }
}

// localViewAsUser holds the admin "view as user" selection. When set (admin
// only — the backend re-validates the role before honoring it), every
// /api/local/* call carries ?user=<username> so the server scopes to that
// user's subdir instead of the admin's own. Empty = operate on own space.
let localViewAsUser = ''
export function setLocalViewAsUser(username: string): void {
  localViewAsUser = username || ''
}
export function getLocalViewAsUser(): string {
  return localViewAsUser
}

// appendViewAs adds the ?user= override to a URLSearchParams when an admin has
// selected another user to view.
export function appendViewAs(p: URLSearchParams): URLSearchParams {
  if (localViewAsUser) p.set('user', localViewAsUser)
  return p
}

// withViewAs appends ?user= to an already-built URL (media URLs returned by the
// backend like localPlay's url, and the POST endpoints that take no params).
export function withViewAs(url: string): string {
  if (!localViewAsUser) return url
  const sep = url.includes('?') ? '&' : '?'
  return `${url}${sep}user=${encodeURIComponent(localViewAsUser)}`
}

// localQS builds the mount/path query (+?user= when "view as user"). Exported
// because stream.ts/subtitles.ts reuse it to route the local branch.
export function localQS(mount: string, path: string): string {
  const base = `mount=${encodeURIComponent(mount)}&path=${encodeURIComponent(path)}`
  return localViewAsUser ? `${base}&user=${encodeURIComponent(localViewAsUser)}` : base
}
