import { useCallback, useEffect, useRef, useState } from 'react'
import { streamMetadata, streamAdd, isLocalHash, parseLocalHash } from '../../api/client'
import { detectKind } from '../../lib/playable'
import { extractTracks, orderPending, basename, type PlaylistGroup, type PlaylistItemLite } from './playlistTracks'
import type { TorrentInfo } from '../../api/client'

// skeletonGroup builds a playlist item's initial group.
// LOCAL item: the pseudo-hash already carries everything to DISPLAY the track
// (title + path), so it starts 'ready' with the derived single track — NO backend
// call. (The direct-vs-HLS/URL resolution only matters at PLAY, which the
// PlayerModal does on selection; localPlayBatch pre-warms those URLs.) This kills
// the N+1 of ~1 GET /api/local/play (ffprobe) per track just to build the LIST.
// TORRENT item: starts 'pending' (its file list resolves via streamMetadata).
function skeletonGroup(it: PlaylistItemLite, i: number): PlaylistGroup {
  if (isLocalHash(it.infoHash)) {
    const path = parseLocalHash(it.infoHash)?.path ?? it.title
    return {
      itemIndex: i, title: it.title, infoHash: it.infoHash, isLocal: true, status: 'ready',
      tracks: [{ fileIndex: 0, name: basename(path), path, size: 0, kind: detectKind(it.title) === 'video' ? 'video' : 'audio' }],
    }
  }
  return { itemIndex: i, title: it.title, infoHash: it.infoHash, isLocal: false, status: 'pending', tracks: [] }
}

// Resolving an item's file list may require ACTIVATING its torrent on the
// server (anacrolix fetches metadata from peers) — so a playlist of packs can
// add many torrents. Cap how many we activate at once to stay gentle on the
// home server; cached items + local files resolve instantly and don't count
// against this (streamMetadata never activates anything).
const ACTIVATE_CONCURRENCY = 2

export type PlaylistTracksAPI = {
  readonly groups: PlaylistGroup[]
  // Force-resolve one group now (bypasses the throttle) — used when the user
  // expands a still-pending group.
  readonly ensureLoaded: (itemIndex: number) => void
}

// usePlaylistTracks progressively resolves EVERY item of the active playlist
// into a flat, grouped track list. The currently-playing item is seeded from
// the already-loaded `currentInfo` (no refetch); the rest fill in in the
// background, current-first then ascending, cache-first, throttled.
//
// `enabled` controla a RESOLUÇÃO EM RAJADA (cada item faz streamMetadata/streamAdd
// — no caso local, ffprobe no servidor). O ESQUELETO da lista NÃO depende de
// `enabled` (persiste ao fechar a sidebar → não re-resolve ~47 faixas ao reabrir).
// O antigo `resolveEnabled`/blessed foi removido: com preload='none' no iOS não
// há byte-stream no cold-start pra a rajada sufocar, então o defer é desnecessário.
export function usePlaylistTracks(
  items: readonly PlaylistItemLite[],
  currentItemIndex: number,
  currentInfo: TorrentInfo | null,
  enabled: boolean,
): PlaylistTracksAPI {
  const [groups, setGroups] = useState<PlaylistGroup[]>([])
  const groupsRef = useRef<PlaylistGroup[]>(groups)
  groupsRef.current = groups
  const inFlight = useRef<Set<number>>(new Set())
  const cancelled = useRef(false)

  // Identity of the current playlist — when it changes we rebuild the skeleton.
  // Keyed by the items' hashes so a re-render with the same playlist is a no-op.
  const signature = items.map(i => i.infoHash || i.magnet).join('|')

  const setGroup = useCallback((idx: number, patch: Partial<PlaylistGroup>) => {
    setGroups(prev => prev.map(g => (g.itemIndex === idx ? { ...g, ...patch } : g)))
  }, [])

  // (1) Rebuild the skeleton whenever the playlist CHANGES (signature). Does NOT
  // depend on `enabled`: with the sidebar closed (`enabled=false`) the already-
  // resolved groups PERSIST so re-opening doesn't re-resolve ~47 items. The
  // BACKGROUND driver (effect 3) stays gated by `enabled` (no torrent activation
  // while closed), but 'ready' items survive the close→open cycle.
  useEffect(() => {
    cancelled.current = false
    inFlight.current = new Set()
    setGroups(items.map((it, i) => skeletonGroup(it, i)))
    return () => { cancelled.current = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [signature])

  // (2) Seed the current group from the metadata the player already loaded —
  // avoids a redundant fetch and shows the playing album's tracks instantly.
  //
  // CRITICAL: key this on the STRUCTURAL identity of the file list (hash + file
  // count), NOT on the `currentInfo` object. The player replaces `currentInfo`
  // on every poll tick (progress/rate fields change ~1×/s), so depending on the
  // object re-seeded the group every second → rewrote groups state → the whole
  // track list re-rendered and visibly "reloaded". Read the live value via ref.
  const currentInfoRef = useRef<TorrentInfo | null>(currentInfo)
  currentInfoRef.current = currentInfo
  const currentSig = `${currentInfo?.infoHash ?? ''}:${currentInfo?.files?.length ?? 0}`
  useEffect(() => {
    const ci = currentInfoRef.current
    if (!ci?.files?.length) return
    if (currentItemIndex < 0 || currentItemIndex >= items.length) return
    inFlight.current.delete(currentItemIndex)
    setGroup(currentItemIndex, { status: 'ready', tracks: extractTracks(ci) })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentItemIndex, currentSig])

  // resolveOne: cache/local peek first; only activate the torrent (streamAdd)
  // when the peek came back empty and it's a real (non-local) torrent.
  const resolveOne = useCallback(async (idx: number) => {
    const it = items[idx]
    if (!it || inFlight.current.has(idx)) return
    if (groupsRef.current.find(g => g.itemIndex === idx)?.status === 'ready') return
    inFlight.current.add(idx)
    setGroup(idx, { status: 'loading' })
    try {
      let info = await streamMetadata(it.infoHash)
      if (!info?.files?.length && !isLocalHash(it.infoHash)) {
        info = await streamAdd(it.magnet, detectKind(it.title))
      }
      if (cancelled.current) return
      setGroup(idx, info?.files?.length
        ? { status: 'ready', tracks: extractTracks(info) }
        : { status: 'error' })
    } catch {
      if (!cancelled.current) setGroup(idx, { status: 'error' })
    } finally {
      inFlight.current.delete(idx)
    }
  }, [items, setGroup])

  // (3) Background driver: keep up to ACTIVATE_CONCURRENCY resolves running,
  // current-first. Re-runs on every `groups` change (a finished resolve frees a
  // slot → fills the next). resolveOne flips status off 'pending' synchronously,
  // so the same item is never started twice. Gated by `enabled` so no torrent
  // activation/ffprobe while the playlist sidebar is closed.
  useEffect(() => {
    if (!enabled) return
    const free = ACTIVATE_CONCURRENCY - inFlight.current.size
    if (free <= 0) return
    const next = orderPending(groups, currentItemIndex, inFlight.current).slice(0, free)
    for (const idx of next) void resolveOne(idx)
  }, [groups, enabled, currentItemIndex, resolveOne])

  // ensureLoaded resolve UM grupo sob demanda (clique do usuário pra expandir um
  // grupo ainda 'pending'). É 1 requisição vinda de um gesto — não a rajada de N.
  const ensureLoaded = useCallback((itemIndex: number) => {
    void resolveOne(itemIndex)
  }, [resolveOne])

  return { groups, ensureLoaded }
}
