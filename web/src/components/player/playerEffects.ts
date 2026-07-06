import {
  TorrentInfo,
  streamPrefetch,
  favoriteAdd,
  libraryUpdateResume,
} from '../../api/client'
import { isLocalHash } from '../../api/local'
import { formatRate } from '../../lib/format'

export function buildErrorInfo(peers: number, starving: boolean, info: TorrentInfo | null): { title: string; detail: string } {
  if (peers === 0) {
    return {
      title: 'Sem seeds disponíveis',
      detail: 'Ninguém está compartilhando este torrent agora. Não há de onde baixar os dados para reproduzir.',
    }
  }
  if (starving) {
    const suffix = peers === 1 ? '' : 's'
    return {
      title: 'Download muito lento para streaming',
      detail: `Baixando a ${formatRate(info?.downRate ?? 0)} de ${peers} peer${suffix} — lento demais para assistir em tempo real (4K precisa de ~3,7 MB/s). Baixe o arquivo completo antes de assistir.`,
    }
  }
  return {
    title: 'Formato não suportado pelo browser',
    detail: 'Codec ou container não compatível (provavelmente HEVC/x265 ou MKV). Use o link "Abrir no VLC" abaixo para reproduzir local.',
  }
}

export function tryPrefetchNext(props: {
  v: HTMLVideoElement
  now: number
  nextVideoIdx: number
  info: TorrentInfo | null
  prefetchedNextEpRef: { current: boolean }
  onPrefetchNextPlaylist: (() => void) | undefined
  prefetchedPlaylistN1Ref: { current: boolean }
  onPrefetchNextNextPlaylist: (() => void) | undefined
  prefetchedPlaylistN2Ref: { current: boolean }
}) {
  const { v, now, nextVideoIdx, info, prefetchedNextEpRef, onPrefetchNextPlaylist, prefetchedPlaylistN1Ref, onPrefetchNextNextPlaylist, prefetchedPlaylistN2Ref } = props
  if (!v.duration || v.duration <= 0) return
  const ratio = now / v.duration
  if (ratio > 0.5) {
    if (!prefetchedNextEpRef.current && nextVideoIdx >= 0 && info) {
      prefetchedNextEpRef.current = true
      streamPrefetch(info.infoHash, nextVideoIdx)
    }
    if (!prefetchedPlaylistN1Ref.current && onPrefetchNextPlaylist) {
      prefetchedPlaylistN1Ref.current = true
      onPrefetchNextPlaylist()
    }
  }
  if (ratio > 0.85 && !prefetchedPlaylistN2Ref.current && onPrefetchNextNextPlaylist) {
    prefetchedPlaylistN2Ref.current = true
    onPrefetchNextNextPlaylist()
  }
}

export function updateBufferedRanges(
  v: HTMLVideoElement,
  now: number,
  setRanges: (r: Array<[number, number]>) => void,
  setEnd: (n: number) => void,
) {
  if (v.buffered.length === 0) return
  const ranges: Array<[number, number]> = []
  for (let i = 0; i < v.buffered.length; i++) ranges.push([v.buffered.start(i), v.buffered.end(i)])
  setRanges(ranges)
  let be = ranges[ranges.length - 1][1]
  for (const [s, e] of ranges) { if (now >= s && now <= e) { be = e; break } }
  setEnd(be)
}

export function tryAutoFavorite(
  watched: number,
  isFavorite: boolean,
  threshold: number,
  info: TorrentInfo | null,
  setIsFavorite: (v: boolean) => void,
) {
  if (!isFavorite && watched >= threshold && info) {
    setIsFavorite(true)
    favoriteAdd(info.name, info.infoHash, info.infoHash ? `magnet:?xt=urn:btih:${info.infoHash}` : '', 'auto-5min').catch(() => setIsFavorite(false))
  }
}

export function trySaveResume(
  now: number,
  incognito: boolean,
  libraryEntryID: number | null,
  lastSave: { current: number },
  duration: number,
) {
  if (incognito || libraryEntryID === null || now <= 1) return
  const elapsed = now - lastSave.current
  if (elapsed > 15 || elapsed < -1) {
    lastSave.current = now
    libraryUpdateResume(libraryEntryID, now, duration).catch(() => {})
  }
}

export function trySyncUrlPlayhead(
  now: number,
  lastSync: { current: number },
) {
  if (now <= 3) return
  const since = now - lastSync.current
  if (since > 5 || since < -1) {
    lastSync.current = now
    const params = new URLSearchParams(globalThis.location.search)
    params.set('t', String(Math.floor(now)))
    globalThis.history.replaceState(null, '', `${globalThis.location.pathname}?${params.toString()}`)
  }
}

// Resolve the file to auto-select when (re)opening a torrent: an explicit
// override wins, then the backend-suggested primary, else the first file.
export function chooseInitialFile(t: TorrentInfo, initialFileIndex: number | undefined): number {
  if (initialFileIndex !== undefined && initialFileIndex >= 0 && initialFileIndex < t.files.length) {
    return initialFileIndex
  }
  return Math.max(0, t.primaryFile)
}

// autoDownloadNextFile detects when the current streaming file has finished
// downloading and enqueues the next file in the in-torrent queue as a background
// download. Respects the same ordering as the player (filterAndSortFiles +
// same-kind constraint via mediaQueue.nextIdx).
//
// Call from the handleTimeUpdate or 2s-poll path; the function is pure (no
// React dependency) so it's testable without a component.
export function autoDownloadNextFile(props: {
  info: TorrentInfo | null
  selectedFile: number
  nextIdx: number
  doneRef: { current: Set<number> }
  incognito: boolean
  onEnqueue: (fileIndex: number) => void
}): void {
  const { info, selectedFile, nextIdx, doneRef, incognito, onEnqueue } = props
  if (!info || selectedFile < 0 || nextIdx < 0) return
  if (incognito) return
  if (isLocalHash(info.infoHash)) return

  const current = info.files.find(f => f.index === selectedFile)
  if (!current) return
  // File must be fully downloaded (the streaming client has received every byte).
  if (current.downloaded < current.size) return

  if (doneRef.current.has(nextIdx)) return

  // Only enqueue if the target file isn't already on disk.
  const next = info.files.find(f => f.index === nextIdx)
  if (next && next.downloaded >= next.size) return

  doneRef.current.add(nextIdx)
  onEnqueue(nextIdx)
}
