// Base compartilhada dos módulos /api/local*: o "pseudo info-hash"
// (`local-<b64>`) que faz um arquivo de disco passar por torrent no PlayerModal,
// e o estado "view as user" (admin) que reescreve as queries mount/path.
// Fica isolado aqui pra que local.ts e os módulos irmãos (local-cache,
// local-audio, …) importem sem ciclos. Extraído de local.ts (#417 follow-up).

// ─── Local file source (pseudo-hash routing) ─────────────────────────────
//
// Arquivos locais usam um "pseudo info-hash" no formato `local-<base64url(json{mount,path})>`.
// PlayerModal e demais consumers continuam achando que estão lidando com um torrent
// normal — as funções abaixo (streamProbe, streamSidecars, subtitlesAuto, etc.)
// detectam o prefixo e roteiam pro `/api/local/*` em vez do `/api/stream/*`.
//
// Vantagem: PlayerModal não precisa mudar (zero risco no caminho torrent que já funciona).

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

// localQS monta a query mount/path (+?user= quando "view as user"). Exportada
// porque stream.ts/subtitles.ts reusam pra rotear o branch local.
export function localQS(mount: string, path: string): string {
  const base = `mount=${encodeURIComponent(mount)}&path=${encodeURIComponent(path)}`
  return localViewAsUser ? `${base}&user=${encodeURIComponent(localViewAsUser)}` : base
}
