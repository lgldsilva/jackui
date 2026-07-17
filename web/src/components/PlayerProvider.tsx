import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, lazy, Suspense, ReactNode } from 'react'
import { useSearchParams } from 'react-router-dom'
import { SearchResult, PlaylistItem, streamAdd, isLocalHash, parseLocalHash } from '../api/client'
import { detectKind } from '../lib/playable'
import { clientLog } from '../lib/diag'
import { useMediaMode, getMediaMode } from '../lib/mediaMode'
import { shuffledOrder } from '../lib/shuffle'
import { savePlaylistSnapshot, loadPlaylistSnapshot, clearPlaylistSnapshot } from './player/playlistSnapshot'
import {
  RepeatMode,
  PlaylistState,
  playlistItemToResult,
  nextRepeatMode,
  tryBootRestorePlaylist,
  handleClearedPlayUrl,
  applyPlayHash,
  type PlayUrlDeps,
} from './player/providerHelpers'

// Re-exported so the provider module keeps its original public surface.
export { parsePositiveInt, parsePositiveFloat } from './player/providerHelpers'
// Lazy so hls.js (~150KB gz) + the whole player bundle load only on first play,
// not in the initial bundle of every page (this provider lives above the router).
const PlayerModal = lazy(() => import('./PlayerModal'))

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

export type PlaylistContext = {
  readonly name: string
  readonly items: readonly PlaylistItem[]
  readonly currentIndex: number
}

type PlayerAPI = {
  /** Plays a single item with no auto-advance logic. `expand` opens the player
   *  maximised even for audio (default: audio opens as the minimized dock). */
  readonly playSingle: (result: SearchResult, initialFileIndex?: number, initialSeek?: number, expand?: boolean) => void
  /** Plays an entire playlist starting at `startIndex`. Replaces any current playback. */
  readonly playPlaylist: (name: string, items: PlaylistItem[], startIndex?: number, expand?: boolean) => void
  /**
   * Jump straight to a specific item (and optionally a file within it) of the
   * ACTIVE playlist — powers the aggregated track list, where the user clicks
   * any file of any item. No-op when there's no active playlist.
   */
  readonly playPlaylistAt: (itemIndex: number, fileIndex?: number) => void
  /** Close the player. Clears playlist state too. */
  readonly close: () => void
  /** Move to the previous item respecting shuffle/repeat. */
  readonly previous: () => void
  /** Move to the next item respecting shuffle/repeat. */
  readonly next: () => void
  /** Cycle 'none' → 'all' → 'one' → 'none'. */
  readonly cycleRepeat: () => void
  /** Toggle shuffle. When turning on, regenerates the shuffle order. */
  readonly toggleShuffle: () => void
  /** Begin warming up the next playlist item (called from PlayerModal at ~50% progress). */
  readonly prefetchNext: () => void
  /** Begin warming up the playlist item after the next (called at ~85% progress). */
  readonly prefetchNextNext: () => void

  // Read-only state for UI
  readonly playlist: PlaylistContext | null
  readonly repeat: RepeatMode
  readonly shuffle: boolean
}

const Ctx = createContext<PlayerAPI | null>(null)

export function usePlayer(): PlayerAPI {
  const v = useContext(Ctx)
  if (!v) throw new Error('usePlayer must be used inside <PlayerProvider>')
  return v
}

export default function PlayerProvider({ children }: { readonly children: ReactNode }) {
  const [current, setCurrent] = useState<{ result: SearchResult; fileIdx?: number; initialSeek?: number } | null>(null)
  const [playlist, setPlaylist] = useState<PlaylistState | null>(null)
  const [repeat, setRepeat] = useState<RepeatMode>('none')
  const [shuffle, setShuffle] = useState(false)
  // Cinema/Música preference, reactive (shared store). Tie-breaker for ambiguous
  // titles AND — via `forcedKind` — switches the ACTIVE player the instant the
  // user toggles it.
  const [mediaMode] = useMediaMode()
  const [forcedKind, setForcedKind] = useState<'audio' | 'video' | null>(null)
  // Open the player maximised (not the minimized audio dock) when a caller asks
  // — e.g. the local-files page wants the full music experience straight away.
  const [startExpanded, setStartExpanded] = useState(false)
  // deepLinkMode: this tab BOOTED at a /?play= deep-link (a new tab opened from a
  // search card). The whole tab is dedicated to playback → render the player
  // full-viewport (browser-wide, not the centered modal) with just a Home button.
  // Cleared on close/Home so any later in-app playback uses the normal modal.
  const [deepLinkMode, setDeepLinkMode] = useState(() =>
    new URLSearchParams(globalThis.location.search).has('play'),
  )
  const lastTimeRef = useRef(0)        // latest playhead (sec), fed by PlayerModal onProgress
  const prevModeRef = useRef(mediaMode) // detects an actual toggle vs. a re-render
  // Ref mirror so callbacks invoked from <PlayerModal onPlaylistAdvance> see the latest state
  // even when React hasn't committed yet (avoids stale-closure bug in auto-advance chains).
  const playlistRef = useRef<PlaylistState | null>(null)
  const repeatRef = useRef<RepeatMode>('none')
  playlistRef.current = playlist
  repeatRef.current = repeat

  // Persiste a playlist ativa pra reabrir o app restaurando prev/next + posição
  // (a URL só carrega o item atual). Salva enquanto há playlist; NÃO limpa ao
  // fechar — reabrir DEVE ressuscitar a última lista (o TTL de 7d corta antigas;
  // a restauração só dispara se o ?play=hash bater com um item da lista salva).
  useEffect(() => {
    if (!playlist) return
    savePlaylistSnapshot(playlist.name, playlist.items, playlist.order[playlist.position])
  }, [playlist])

  const playSingle = useCallback((result: SearchResult, initialFileIndex?: number, initialSeek?: number, expand = false) => {
    setStartExpanded(expand)
    setPlaylist(null)
    // Item único substitui o contexto de playlist — limpa o snapshot pra que o
    // boot-frio não ressuscite uma lista velha em que o usuário não está mais.
    clearPlaylistSnapshot()
    setCurrent({ result, fileIdx: initialFileIndex, initialSeek })
  }, [])

  const playPlaylist = useCallback((name: string, items: PlaylistItem[], startIndex = 0, expand = false) => {
    if (items.length === 0) return
    setStartExpanded(expand)
    const safeStart = Math.max(0, Math.min(items.length - 1, startIndex))
    const order = shuffle
      ? shuffledOrder(items.length, safeStart)
      : Array.from({ length: items.length }, (_, i) => i)
    const position = shuffle ? 0 : safeStart
    setPlaylist({ name, items, order, position })
    const first = playlistItemToResult(items[order[position]])
    setCurrent(first)
  }, [shuffle])

  // Jump to an arbitrary item (and file) of the active playlist. Used by the
  // aggregated track list: clicking a file in a NOT-currently-playing item
  // switches the playlist cursor to it and starts that file. Within shuffle
  // we move the cursor to wherever that item sits in `order` so subsequent
  // next/prev keep following the shuffled sequence.
  const playPlaylistAt = useCallback((itemIndex: number, fileIndex?: number) => {
    const pl = playlistRef.current
    if (!pl) return
    if (itemIndex < 0 || itemIndex >= pl.items.length) return
    const pos = pl.order.indexOf(itemIndex)
    if (pos < 0) return
    const updated = { ...pl, position: pos }
    setPlaylist(updated)
    playlistRef.current = updated
    // DIAGNÓSTICO: salto explícito de item (clique na lista agregada ou motor).
    clientLog('info', 'player', 'playlist jump', { itemIndex, pos, fileIndex })
    const base = playlistItemToResult(pl.items[itemIndex])
    setCurrent({ result: base.result, fileIdx: fileIndex ?? base.fileIdx })
  }, [])

  const close = useCallback(() => {
    // The torrent is dropped by PlayerModal's viewer-lease effect when it
    // unmounts (released here via setCurrent(null)) — not from this handler.
    // That keeps a single acquire/release pair per stream and protects
    // co-watchers (the backend only drops once the LAST viewer leaves).
    setCurrent(null)
    setPlaylist(null)
    setDeepLinkMode(false) // leaving the player exits full-viewport; later plays are modal
    // Fechar (X) = dispensar: não restaurar no próximo boot. Matar o app NÃO passa
    // por aqui (a playlist persiste no snapshot → é restaurada ao reabrir).
    clearPlaylistSnapshot()
  }, [])

  const goTo = useCallback((delta: number) => {
    const pl = playlistRef.current
    // Diagnostic: helps debug "player fechou mid-playlist" reports — captures
    // the exact state at the advance decision point. Inspect via DevTools when
    // reproducing.
    console.debug('[player] goTo', {
      delta,
      hasPlaylist: !!pl,
      position: pl?.position,
      total: pl?.order.length,
      repeat: repeatRef.current,
    })
    if (!pl) {
      // Single-item playback — stop without closing. Closing the modal here
      // was hostile for audio (the minimized dock would disappear right when
      // the user might want to replay). Caller (X button / explicit close)
      // still works.
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
        // End of playlist. Don't close — keep the modal visible so the user
        // can replay, switch to repeat-all, or pick another song. The X
        // button is the only path to close.
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
    // DIAGNÓSTICO: toda mudança de item por avanço (gesto OU onEnded) fica no log
    // do servidor — pra cravar trocas de faixa "fantasma" no iPhone.
    clientLog('info', 'player', 'goTo muda item', { delta, from: pl.position, to: next, item: pl.order[next], repeat: repeatRef.current })
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
    if (!item?.magnet) return
    const key = `${item.infoHash || item.magnet}:${item.fileIndex}`
    if (prefetchedHashes.current.has(key)) return
    prefetchedHashes.current.add(key)
    streamAdd(item.magnet, detectKind(item.title, 0, getMediaMode())).catch(() => {
      // Soft fail — main playback is unaffected. Remove the key so a retry
      // could happen on a future loop, but in practice we won't reach that
      // unless the user manually replays the playlist.
      prefetchedHashes.current.delete(key)
    })
  }, [])
  const prefetchNext = useCallback(() => prefetchUpcoming(1), [prefetchUpcoming])
  const prefetchNextNext = useCallback(() => prefetchUpcoming(2), [prefetchUpcoming])

  const cycleRepeat = useCallback(() => {
    setRepeat(r => nextRepeatMode(r))
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

  // Apply the Cinema/Música toggle to whatever is playing RIGHT NOW: when the
  // preference flips while a player is active, switch its mode immediately and
  // resume from the current playhead (the modal re-keys by kind, so we feed the
  // last reported time as initialSeek instead of restarting from zero). When
  // `current` changes for any other reason the guard short-circuits.
  useEffect(() => {
    if (prevModeRef.current === mediaMode) return
    prevModeRef.current = mediaMode
    if (!current) return
    setForcedKind(mediaMode)
    // lastTimeRef is re-seeded with the item's start position on every item
    // change (below), so it's always a valid playhead — even before the first
    // onProgress tick. No `> 0` guard: that would drop a legit resume position
    // (e.g. 0 vs. a Continue-Watching seek) when toggling in the first moments.
    setCurrent(c => (c ? { ...c, initialSeek: lastTimeRef.current } : c))
  }, [mediaMode, current])

  // A new item (or file) clears the explicit override so the detection /
  // tie-breaker decides the mode again for the next thing that plays. We also
  // re-seed lastTimeRef from the item's start position, so a Cinema/Música
  // toggle in the first moments (before the first onProgress tick) resumes from
  // the real start (e.g. a Continue-Watching resume) instead of snapping to 0.
  useEffect(() => {
    setForcedKind(null)
    lastTimeRef.current = current?.initialSeek ?? 0
  }, [current?.result.infoHash, current?.fileIdx])

  // Memoized so it keeps a stable identity while `playlist` doesn't change —
  // otherwise a fresh object every render invalidated the `api` value memo
  // during playlists, re-rendering every consumer of the player context.
  const playlistView = useMemo<PlaylistContext | null>(
    () => (playlist
      ? { name: playlist.name, items: playlist.items, currentIndex: playlist.order[playlist.position] }
      : null),
    [playlist],
  )

  const api = useMemo<PlayerAPI>(() => ({
    playSingle,
    playPlaylist,
    playPlaylistAt,
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
  }), [playSingle, playPlaylist, playPlaylistAt, close, next, previous, cycleRepeat, toggleShuffle, prefetchNext, prefetchNextNext, playlistView, repeat, shuffle])

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
  // Garante que a restauração de playlist no boot frio (URL sem ?play) rode UMA vez.
  // Depois disso, URL sem ?play = o usuário fechou o player → não re-abrir.
  const bootRestoredRef = useRef(false)
  const [searchParams, setSearchParams] = useSearchParams()
  const playUrlParam = searchParams.get('play')
  const fileUrlParam = searchParams.get('f')
  const timeUrlParam = searchParams.get('t')

  // URL → state (branching lives in providerHelpers to keep cognitive complexity down)
  useEffect(() => {
    const hash = playUrlParam
    const realHash = () => new URLSearchParams(globalThis.location.search).get('play')
    const deps: PlayUrlDeps = {
      playSingle,
      playPlaylist,
      close,
      hasCurrent: !!current,
      loadSnapshot: loadPlaylistSnapshot,
      isLocalHash,
      parseLocalHash,
      setLastSynced: (h) => { lastSyncedHashRef.current = h },
    }
    // Boot frio (1ª execução): restaura a última playlist ANTES do short-circuit
    // abaixo. No mount, hash e lastSynced são ambos null, então `hash === lastSynced`
    // pularia tudo — e o PWA standalone reabre no start_url SEM ?play, nunca
    // restaurando. Só com nada tocando e sem ?play (nem na URL real, contra lag do
    // router); roda 1x (fechar o player limpa o ?play e não deve re-abrir).
    if (!bootRestoredRef.current) {
      bootRestoredRef.current = true
      if (tryBootRestorePlaylist(hash, realHash(), deps)) return
    }
    if (hash === lastSyncedHashRef.current) return
    if (!hash) {
      handleClearedPlayUrl(realHash(), deps)
      return
    }
    applyPlayHash(hash, fileUrlParam, timeUrlParam, deps)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [playUrlParam, fileUrlParam])

  // state → URL
  useEffect(() => {
    const newHash = current?.result?.infoHash || null
    if (newHash === lastSyncedHashRef.current) return
    lastSyncedHashRef.current = newHash
    const params = new URLSearchParams(globalThis.location.search)
    if (newHash) {
      params.set('play', newHash)
      if (current?.fileIdx === undefined) params.delete('f')
      else params.set('f', String(current.fileIdx))
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
    // Explicit Cinema/Música toggle on the active item wins over everything.
    if (forcedKind) return forcedKind
    if (playlist && playlist.items.length > 0) {
      // Aggregate over playlist — any video → video mode.
      const anyVideo = playlist.items.some(it => detectKind(it.title, 0, mediaMode) === 'video')
      return anyVideo ? 'video' : 'audio'
    }
    // Prefer backend-resolved mediaKind quando presente; cai na heurística
    // local pra syntheticResult/deep-links que constroem SearchResult sem
    // o campo. 'other' do backend coalesce no fallback (Cinema/Música).
    if (current.result.mediaKind === 'audio') return 'audio'
    if (current.result.mediaKind === 'video') return 'video'
    return detectKind(current.result.title, current.result.categoryId, mediaMode)
  })()

  return (
    <Ctx.Provider value={api}>
      {children}
      {/* Unified player: PlayerModal serves BOTH video and audio. Audio opens
          minimized (a compact floating card with cover art) — this replaces the
          old separate AudioBar. Video opens full-screen. Either can toggle
          between full and minimized via the header button, and playback
          survives navigation since this provider lives above the router. */}
      {current && (
        <Suspense fallback={null}>
        <PlayerModal
          key={currentKind === 'audio' ? 'audio' : 'video'}
          result={current.result}
          initialFileIndex={current.fileIdx}
          initialSeek={current.initialSeek}
          onClose={close}
          playlist={playlistView}
          onPlaylistAdvance={next}
          onPlaylistPrevious={previous}
          onPlaylistJump={playPlaylistAt}
          repeat={repeat}
          shuffle={shuffle}
          onCycleRepeat={cycleRepeat}
          onToggleShuffle={toggleShuffle}
          onPrefetchNextPlaylist={prefetchNext}
          onPrefetchNextNextPlaylist={prefetchNextNext}
          startMinimized={currentKind === 'audio' && !startExpanded}
          audioMode={currentKind === 'audio'}
          fullViewport={deepLinkMode && currentKind !== 'audio'}
          onHome={close}
          onProgress={(s) => { lastTimeRef.current = s }}
        />
        </Suspense>
      )}
    </Ctx.Provider>
  )
}
