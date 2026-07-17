// Pure helpers + types for PlayerProvider, extracted verbatim so the provider
// file stays lean. NO React here — only pure functions and type aliases used by
// the provider (playlist item mapping, URL query parsing, deep-link resolution,
// repeat-mode cycling). Keeping them out of the provider avoids fattening the
// component that lives above the router.
import { SearchResult, PlaylistItem, libraryList } from '../../api/client'
import { syntheticResult } from '../../lib/playable'
import { isRevealHidden } from '../../lib/reveal'
import { shouldBlockHiddenDeepLink } from '../../lib/deepLinkGate'

export type RepeatMode = 'none' | 'one' | 'all'

export type PlaylistState = {
  readonly name: string
  readonly items: readonly PlaylistItem[]
  // The "order" — when shuffle is on, this is a permutation of [0..items.length-1].
  // When off, it's the identity sequence. The "position" cursor walks this array.
  readonly order: readonly number[]
  readonly position: number
}

export function playlistItemToResult(item: PlaylistItem): { result: SearchResult; fileIdx?: number } {
  const result: SearchResult = {
    title: item.title,
    tracker: '',
    categoryId: 0,
    category: '',
    size: 0,
    seeders: 0,
    leechers: 0,
    age: '',
    magnetUri: item.magnet,
    link: '',
    infoHash: item.infoHash,
    publishDate: '',
  }
  // Treat fileIndex === 0 as "unset" (column default in playlist_items is 0) so
  // the player falls back to the server's pickPrimaryFile. Side effect: legitimate
  // file-0 picks from the contents picker also go through pickPrimaryFile — for
  // most torrents that's still correct (file 0 is rarely the actual primary).
  return { result, fileIdx: item.fileIndex > 0 ? item.fileIndex : undefined }
}

// parsePositiveInt/Float read a URL query value as a positive number, returning
// undefined for missing/zero/NaN. Extracted so the URL→state effect doesn't carry
// the ternary+&& parsing inline (keeps its cognitive complexity under the gate).
export function parsePositiveInt(s: string | null): number | undefined {
  if (!s) return undefined
  const n = Number.parseInt(s, 10)
  return Number.isFinite(n) && n > 0 ? n : undefined
}
export function parsePositiveFloat(s: string | null): number | undefined {
  if (!s) return undefined
  const n = Number.parseFloat(s)
  return Number.isFinite(n) && n > 0 ? n : undefined
}

// playResolvedFromLibrary picks the nicest metadata for `hash` from a library
// list (title/magnet + persisted kind) and plays it, falling back to a synthetic
// magnet when the hash isn't in the list.
export function playResolvedFromLibrary(
  list: { infoHash: string; name?: string; magnet?: string; kind?: string }[],
  hash: string,
  fIdx: number | undefined,
  initialSeek: number | undefined,
  play: (result: SearchResult, initialFileIndex?: number, initialSeek?: number, expand?: boolean) => void,
): void {
  const entry = list.find(e => e.infoHash === hash)
  const magnet = entry?.magnet || `magnet:?xt=urn:btih:${hash}`
  const name = entry?.name || hash
  // Carry the library entry's kind so a refresh of an audio deep-link opens the
  // audio UI (the title heuristic alone misjudged albums → opened video).
  const mk = entry?.kind === 'audio' || entry?.kind === 'video' ? entry.kind : undefined
  play(syntheticResult(hash, name, magnet, mk), fIdx, initialSeek)
}

// resolveDeepLinkPlay resolves a 40-hex info_hash from a ?play deep link and plays
// it. With the hidden curtain (easter egg) CLOSED it refuses to auto-play an item
// that lives ONLY behind the curtain — otherwise a ?play=<hidden-hash> URL (e.g.
// the one the player mirrored while the curtain was open, re-opened after a reload
// that reset the in-memory curtain) would silently reveal hidden content. Items
// visible without the curtain, and genuine non-library magnets (shared links),
// still play.
export function resolveDeepLinkPlay(
  hash: string,
  fIdx: number | undefined,
  initialSeek: number | undefined,
  play: (result: SearchResult, initialFileIndex?: number, initialSeek?: number, expand?: boolean) => void,
): void {
  if (isRevealHidden()) {
    libraryList({ limit: 200 })
      .then(list => playResolvedFromLibrary(list, hash, fIdx, initialSeek, play))
      .catch(() => play(syntheticResult(hash, hash, `magnet:?xt=urn:btih:${hash}`), fIdx, initialSeek))
    return
  }
  Promise.all([
    libraryList({ limit: 200 }).catch(() => []),
    libraryList({ limit: 200, revealHidden: true }).catch(() => []),
  ]).then(([visible, revealed]) => {
    if (shouldBlockHiddenDeepLink(hash, visible, revealed)) return
    playResolvedFromLibrary(visible, hash, fIdx, initialSeek, play)
  })
}

export function nextRepeatMode(r: 'none' | 'all' | 'one'): 'none' | 'all' | 'one' {
  if (r === 'none') return 'all'
  if (r === 'all') return 'one'
  return 'none'
}

/** Minimal snapshot shape for URL→state restore (avoids circular imports). */
export type PlaylistSnapshotLike = {
  readonly name: string
  readonly items: readonly PlaylistItem[]
  readonly currentItemIndex: number
}

export type PlayUrlDeps = {
  readonly playSingle: (result: SearchResult, initialFileIndex?: number, initialSeek?: number, expand?: boolean) => void
  readonly playPlaylist: (name: string, items: PlaylistItem[], startIndex?: number, expand?: boolean) => void
  readonly close: () => void
  readonly hasCurrent: boolean
  readonly loadSnapshot: () => PlaylistSnapshotLike | null
  readonly isLocalHash: (h: string) => boolean
  readonly parseLocalHash: (h: string) => { path: string } | null
  readonly setLastSynced: (h: string | null) => void
}

/** Cold-boot restore when the URL has no ?play (PWA start_url). Returns true if it started playback. */
export function tryBootRestorePlaylist(
  playHash: string | null,
  realHash: string | null,
  deps: Pick<PlayUrlDeps, 'hasCurrent' | 'loadSnapshot' | 'playPlaylist'>,
): boolean {
  if (playHash || realHash || deps.hasCurrent) return false
  const boot = deps.loadSnapshot()
  if (!boot || boot.items.length === 0) return false
  const idx = boot.currentItemIndex >= 0 && boot.currentItemIndex < boot.items.length
    ? boot.currentItemIndex
    : 0
  deps.playPlaylist(boot.name, [...boot.items], idx)
  return true
}

/** Empty ?play after router lag check: close active playback if the real URL also lacks play. */
export function handleClearedPlayUrl(
  realHash: string | null,
  deps: Pick<PlayUrlDeps, 'hasCurrent' | 'close' | 'setLastSynced'>,
): void {
  if (realHash) return // router lag — real location still has ?play
  if (deps.hasCurrent) deps.close()
  deps.setLastSynced(null)
}

/** Apply a non-empty ?play hash: snapshot playlist → local mount → torrent deep link. */
export function applyPlayHash(
  hash: string,
  fileUrlParam: string | null,
  timeUrlParam: string | null,
  deps: PlayUrlDeps,
): void {
  const fIdx = parsePositiveInt(fileUrlParam)
  const initialSeek = parsePositiveFloat(timeUrlParam)

  const snap = deps.loadSnapshot()
  const snapIdx = snap ? snap.items.findIndex(it => it.infoHash === hash) : -1
  // Prefer full playlist restore over single-item so prev/next survive reload.
  if (snap && snapIdx >= 0) {
    deps.setLastSynced(hash)
    deps.playPlaylist(snap.name, [...snap.items], snapIdx)
    return
  }

  if (deps.isLocalHash(hash)) {
    deps.setLastSynced(hash)
    const loc = deps.parseLocalHash(hash)
    const name = loc ? (loc.path.split('/').pop() || loc.path) : hash
    deps.playSingle(syntheticResult(hash, name, `magnet:?xt=urn:btih:${hash}`), fIdx, initialSeek, true)
    return
  }

  if (!/^[a-fA-F0-9]{40}$/.test(hash)) {
    deps.setLastSynced(null)
    return
  }
  deps.setLastSynced(hash)
  resolveDeepLinkPlay(hash, fIdx, initialSeek, deps.playSingle)
}
