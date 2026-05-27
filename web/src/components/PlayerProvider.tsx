import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, ReactNode } from 'react'
import { useSearchParams } from 'react-router-dom'
import { SearchResult, PlaylistItem, streamAdd, libraryList } from '../api/client'
import { detectKind, syntheticResult } from '../lib/playable'
import PlayerModal from './PlayerModal'
import AudioBar from './AudioBar'

/**
 * PlayerProvider — central authority for "what's currently playing" and "what's next".
 *
 * Why a provider:
 *   - Before this, each page held its own `playTarget` state and mounted its own
 *     PlayerModal. That works for single-item playback but cannot express "auto-advance
 *     between unrelated torrents" (playlist), because closing the modal on one page
 *     and opening it on another would be needed.
 *   - Centralising state means one modal, mounted at the app root, fed sequentially
 *     by the provider.
 *
 * The PlayerModal itself stays as-is — it still knows nothing about playlists.
 * The provider listens for `onPlaylistAdvance` (a new prop) and decides what to feed
 * the modal next.
 */

export interface PlaylistContext {
  name: string
  items: PlaylistItem[]
  currentIndex: number
}

type RepeatMode = 'none' | 'one' | 'all'

interface PlayerAPI {
  /** Plays a single item with no auto-advance logic. */
  playSingle: (result: SearchResult, initialFileIndex?: number, initialSeek?: number) => void
  /** Plays an entire playlist starting at `startIndex`. Replaces any current playback. */
  playPlaylist: (name: string, items: PlaylistItem[], startIndex?: number) => void
  /** Close the player. Clears playlist state too. */
  close: () => void
  /** Move to the previous item respecting shuffle/repeat. */
  previous: () => void
  /** Move to the next item respecting shuffle/repeat. */
  next: () => void
  /** Cycle 'none' → 'all' → 'one' → 'none'. */
  cycleRepeat: () => void
  /** Toggle shuffle. When turning on, regenerates the shuffle order. */
  toggleShuffle: () => void
  /** Begin warming up the next playlist item (called from PlayerModal at ~50% progress). */
  prefetchNext: () => void
  /** Begin warming up the playlist item after the next (called at ~85% progress). */
  prefetchNextNext: () => void

  // Read-only state for UI
  playlist: PlaylistContext | null
  repeat: RepeatMode
  shuffle: boolean
}

const Ctx = createContext<PlayerAPI | null>(null)

export function usePlayer(): PlayerAPI {
  const v = useContext(Ctx)
  if (!v) throw new Error('usePlayer must be used inside <PlayerProvider>')
  return v
}

interface PlaylistState {
  name: string
  items: PlaylistItem[]
  // The "order" — when shuffle is on, this is a permutation of [0..items.length-1].
  // When off, it's the identity sequence. The "position" cursor walks this array.
  order: number[]
  position: number
}

function playlistItemToResult(item: PlaylistItem): { result: SearchResult; fileIdx?: number } {
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

function shuffledOrder(n: number, startIndex: number): number[] {
  // Fisher-Yates on [0..n-1] excluding startIndex, then put startIndex at position 0.
  const rest = Array.from({ length: n }, (_, i) => i).filter(i => i !== startIndex)
  for (let i = rest.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1))
    ;[rest[i], rest[j]] = [rest[j], rest[i]]
  }
  return [startIndex, ...rest]
}

export default function PlayerProvider({ children }: { children: ReactNode }) {
  const [current, setCurrent] = useState<{ result: SearchResult; fileIdx?: number; initialSeek?: number } | null>(null)
  const [playlist, setPlaylist] = useState<PlaylistState | null>(null)
  const [repeat, setRepeat] = useState<RepeatMode>('none')
  const [shuffle, setShuffle] = useState(false)
  // Ref mirror so callbacks invoked from <PlayerModal onPlaylistAdvance> see the latest state
  // even when React hasn't committed yet (avoids stale-closure bug in auto-advance chains).
  const playlistRef = useRef<PlaylistState | null>(null)
  const repeatRef = useRef<RepeatMode>('none')
  playlistRef.current = playlist
  repeatRef.current = repeat

  const playSingle = useCallback((result: SearchResult, initialFileIndex?: number, initialSeek?: number) => {
    setPlaylist(null)
    setCurrent({ result, fileIdx: initialFileIndex, initialSeek })
  }, [])

  const playPlaylist = useCallback((name: string, items: PlaylistItem[], startIndex = 0) => {
    if (items.length === 0) return
    const safeStart = Math.max(0, Math.min(items.length - 1, startIndex))
    const order = shuffle
      ? shuffledOrder(items.length, safeStart)
      : Array.from({ length: items.length }, (_, i) => i)
    const position = shuffle ? 0 : safeStart
    setPlaylist({ name, items, order, position })
    const first = playlistItemToResult(items[order[position]])
    setCurrent(first)
  }, [shuffle])

  const close = useCallback(() => {
    setCurrent(null)
    setPlaylist(null)
  }, [])

  const goTo = useCallback((delta: number) => {
    const pl = playlistRef.current
    if (!pl) {
      // Single-item playback — just close
      if (delta > 0) {
        setCurrent(null)
      }
      return
    }
    let next = pl.position + delta
    const len = pl.order.length

    // Repeat-one short-circuits with the same item (caller may use this to replay)
    if (repeatRef.current === 'one' && delta > 0) {
      const same = playlistItemToResult(pl.items[pl.order[pl.position]])
      setCurrent(same)
      return
    }
    if (next >= len) {
      if (repeatRef.current === 'all') next = 0
      else {
        // Past the end — stop playback
        setCurrent(null)
        return
      }
    }
    if (next < 0) {
      if (repeatRef.current === 'all') next = len - 1
      else next = 0
    }
    const updated = { ...pl, position: next }
    setPlaylist(updated)
    playlistRef.current = updated
    setCurrent(playlistItemToResult(pl.items[pl.order[next]]))
  }, [])

  const next = useCallback(() => goTo(1), [goTo])
  const previous = useCallback(() => goTo(-1), [goTo])

  // Prefetch handlers: the PlayerModal calls these from onTimeUpdate when
  // the current item passes 50% / 85% progress. We dispatch a streamAdd against
  // the upcoming items' magnets so anacrolix parses metadata + queues head
  // pieces in the background. By the time auto-advance kicks in the next
  // torrent is already buffering.
  const prefetchedHashes = useRef(new Set<string>())
  const prefetchUpcoming = useCallback((offset: number) => {
    const pl = playlistRef.current
    if (!pl) return
    const target = pl.position + offset
    if (target < 0 || target >= pl.order.length) return
    const item = pl.items[pl.order[target]]
    if (!item || !item.magnet) return
    const key = `${item.infoHash || item.magnet}:${item.fileIndex}`
    if (prefetchedHashes.current.has(key)) return
    prefetchedHashes.current.add(key)
    streamAdd(item.magnet).catch(() => {
      // Soft fail — main playback is unaffected. Remove the key so a retry
      // could happen on a future loop, but in practice we won't reach that
      // unless the user manually replays the playlist.
      prefetchedHashes.current.delete(key)
    })
  }, [])
  const prefetchNext = useCallback(() => prefetchUpcoming(1), [prefetchUpcoming])
  const prefetchNextNext = useCallback(() => prefetchUpcoming(2), [prefetchUpcoming])

  const cycleRepeat = useCallback(() => {
    setRepeat(r => (r === 'none' ? 'all' : r === 'all' ? 'one' : 'none'))
  }, [])

  const toggleShuffle = useCallback(() => {
    setShuffle(s => {
      const nextShuffle = !s
      // Regenerate playlist order when toggling mid-playback
      const pl = playlistRef.current
      if (pl) {
        const order = nextShuffle
          ? shuffledOrder(pl.items.length, pl.order[pl.position])
          : Array.from({ length: pl.items.length }, (_, i) => i)
        const position = nextShuffle ? 0 : pl.order[pl.position]
        const updated = { ...pl, order, position }
        setPlaylist(updated)
        playlistRef.current = updated
      }
      return nextShuffle
    })
  }, [])

  const playlistView: PlaylistContext | null = playlist
    ? { name: playlist.name, items: playlist.items, currentIndex: playlist.order[playlist.position] }
    : null

  const api = useMemo<PlayerAPI>(() => ({
    playSingle,
    playPlaylist,
    close,
    next,
    previous,
    cycleRepeat,
    toggleShuffle,
    prefetchNext,
    prefetchNextNext,
    playlist: playlistView,
    repeat,
    shuffle,
  }), [playSingle, playPlaylist, close, next, previous, cycleRepeat, toggleShuffle, prefetchNext, prefetchNextNext, playlistView, repeat, shuffle])

  // ─── URL deep-linking ────────────────────────────────────────────────────
  //
  // The player's state is mirrored into the page's query string so a URL like
  //   /library?play=HASH&f=3&t=120
  // is a sharable, reload-safe pointer to "what was playing". Two unidirectional
  // effects keep state ↔ URL in sync without looping:
  //
  //   URL → state:  fires on mount and whenever ?play / ?f changes externally
  //                 (back/forward, paste, link share). Calls playSingle().
  //   state → URL:  fires whenever `current` changes (any playSingle/close).
  //                 Writes ?play / ?f back, suppressing the next URL→state.
  //
  // `lastSyncedHashRef` is the loop breaker: state→URL stamps it BEFORE writing,
  // and URL→state bails when the incoming hash matches it. This way a setter
  // (e.g. user clicks Play) triggers exactly one URL write — not a ping-pong.
  //
  // Note: `current` is intentionally excluded from URL→state's deps. Including it
  // would re-run the effect every time `current` mutates (e.g. fileIdx changes
  // via playlist advance), and our lastSynced gate doesn't yet account for those
  // intra-session updates. The dep on `searchParams.get('play')` is enough — it
  // only fires when the URL itself changes, which is the trigger we actually want.
  const lastSyncedHashRef = useRef<string | null>(null)
  const [searchParams, setSearchParams] = useSearchParams()
  const playUrlParam = searchParams.get('play')
  const fileUrlParam = searchParams.get('f')
  const timeUrlParam = searchParams.get('t')

  // URL → state
  useEffect(() => {
    const hash = playUrlParam
    if (hash === lastSyncedHashRef.current) return
    if (!hash) {
      // URL cleared externally (user removed ?play) — close any active playback
      if (current) close()
      lastSyncedHashRef.current = null
      return
    }
    // Validate: info_hash is 40 hex chars. Reject malformed values silently —
    // a stray ?play=foo in the URL shouldn't blow up the app.
    if (!/^[a-fA-F0-9]{40}$/.test(hash)) {
      lastSyncedHashRef.current = null
      return
    }
    lastSyncedHashRef.current = hash
    const fIdxParsed = fileUrlParam ? parseInt(fileUrlParam, 10) : NaN
    const fIdx = Number.isFinite(fIdxParsed) && fIdxParsed > 0 ? fIdxParsed : undefined
    const tParsed = timeUrlParam ? parseFloat(timeUrlParam) : NaN
    const initialSeek = Number.isFinite(tParsed) && tParsed > 0 ? tParsed : undefined

    // Best effort: lookup the library entry to get a proper title + magnet
    // (some trackers' magnets carry display_name + trackers, which is nice to
    // have over a bare xt-only magnet). If the user has never played this hash
    // before, fall back to the synthetic magnet — anacrolix will resolve trackers
    // via DHT.
    libraryList({ limit: 200 }).then(list => {
      const entry = list.find(e => e.infoHash === hash)
      const magnet = entry?.magnet || `magnet:?xt=urn:btih:${hash}`
      const name = entry?.name || hash
      playSingle(syntheticResult(hash, name, magnet), fIdx, initialSeek)
    }).catch(() => {
      playSingle(syntheticResult(hash, hash, `magnet:?xt=urn:btih:${hash}`), fIdx, initialSeek)
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [playUrlParam, fileUrlParam])

  // state → URL
  useEffect(() => {
    const newHash = current?.result?.infoHash || null
    if (newHash === lastSyncedHashRef.current) return
    lastSyncedHashRef.current = newHash
    const params = new URLSearchParams(window.location.search)
    if (newHash) {
      params.set('play', newHash)
      if (current?.fileIdx !== undefined) params.set('f', String(current.fileIdx))
      else params.delete('f')
      // We intentionally don't write `t` here — resume position comes from the
      // server's per-user library and updates every ~15s. Persisting it in the
      // URL on every tick would spam history and is the user's job (paste from
      // a "share at current timestamp" action, not yet implemented).
    } else {
      params.delete('play')
      params.delete('f')
      params.delete('t')
    }
    // `replace: true` avoids polluting browser history — back/forward should
    // navigate between pages, not cycle through every play/close state change.
    setSearchParams(params, { replace: true })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [current])

  // Choose between AudioBar (persistent bottom bar) and PlayerModal (full-screen)
  // based on the current playback context.
  //
  // For PLAYLISTS we use the *aggregate* kind: if even one item looks like a
  // video, the whole session uses PlayerModal — switching UIs mid-playlist
  // (modal closing and bar appearing) is jarring and breaks transitions.
  // The trade-off: a playlist of mixed audio + video shows the video modal
  // even during the audio tracks, which is OK because <video> plays audio fine.
  //
  // For SINGLE-ITEM playback we use the item's own kind detection.
  const currentKind = (() => {
    if (!current) return null
    if (playlist && playlist.items.length > 0) {
      // Aggregate over playlist — any video → video mode.
      const anyVideo = playlist.items.some(it => detectKind(it.title) === 'video')
      return anyVideo ? 'video' : 'audio'
    }
    return detectKind(current.result.title, current.result.categoryId)
  })()

  return (
    <Ctx.Provider value={api}>
      {children}
      {current && currentKind === 'video' && (
        <PlayerModal
          result={current.result}
          initialFileIndex={current.fileIdx}
          initialSeek={current.initialSeek}
          onClose={close}
          playlist={playlistView}
          onPlaylistAdvance={next}
          onPlaylistPrevious={previous}
          repeat={repeat}
          shuffle={shuffle}
          onCycleRepeat={cycleRepeat}
          onToggleShuffle={toggleShuffle}
          onPrefetchNextPlaylist={prefetchNext}
          onPrefetchNextNextPlaylist={prefetchNextNext}
        />
      )}
      {current && currentKind === 'audio' && (
        <AudioBar
          result={current.result}
          initialFileIndex={current.fileIdx}
          onClose={close}
          playlist={playlistView}
          onPlaylistAdvance={next}
          onPlaylistPrevious={previous}
          repeat={repeat}
          shuffle={shuffle}
          onCycleRepeat={cycleRepeat}
          onToggleShuffle={toggleShuffle}
          onPrefetchNextPlaylist={prefetchNext}
          onPrefetchNextNextPlaylist={prefetchNextNext}
        />
      )}
    </Ctx.Provider>
  )
}
