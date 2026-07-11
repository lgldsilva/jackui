import { useMemo, useCallback } from 'react'
import { TorrentInfo } from '../../api/client'
import { clientLog } from '../../lib/diag'
import { nextTrack, prevTrack } from '../../lib/trackTransport'
import { useKeyboardShortcuts, useMediaSession, useMediaQueue } from './playerHooks'
import { filterAndSortFiles, type FileType } from './playerFormat'
import { useTrackOrder } from './useTrackOrder'
import { useAudioDirectUrl } from './useAudioDirectUrl'
import { usePlaylistTracks } from './usePlaylistTracks'
import { audioCoverURL } from './AudioCoverArt'
import type { PlaylistMeta } from './playerTypes'

type Setter<T> = React.Dispatch<React.SetStateAction<T>>

// In-torrent queue + continuous transport (album tracks / series episodes) plus
// the shuffle/repeat-aware next/prev spill into the playlist, the simplified
// audio engine wiring, keyboard shortcuts and the Media Session (lock-screen)
// controls. Per-file resets on a track switch run via `resetForFile`.
export function usePlayerTransport(deps: {
  info: TorrentInfo | null
  selectedFile: number
  shuffle: boolean
  repeat: 'none' | 'one' | 'all'
  audioMode: boolean
  minimized: boolean
  mediaToken: string
  playlist: PlaylistMeta | null | undefined
  sidebarOpen: boolean
  onPlaylistAdvance?: () => void
  onPlaylistPrevious?: () => void
  onProgress?: (sec: number) => void
  videoRef: React.RefObject<HTMLVideoElement | null>
  audioRef: React.MutableRefObject<HTMLAudioElement | null>
  handleRequestFullscreen: () => void
  fileFilter: string
  fileTypeFilter: FileType
  fileSortBySize: boolean
  fileSizeDesc: boolean
  resetForFile: (idx: number) => void
  setCurrentTime: Setter<number>
  setDuration: Setter<number>
}) {
  const {
    info, selectedFile, shuffle, repeat, audioMode, minimized, mediaToken, playlist, sidebarOpen,
    onPlaylistAdvance, onPlaylistPrevious, onProgress, videoRef, audioRef, handleRequestFullscreen,
    fileFilter, fileTypeFilter, fileSortBySize, fileSizeDesc, resetForFile, setCurrentTime, setDuration,
  } = deps

  // Display order of the file list — the SAME order the sidebar renders
  // (episodes sorted, extras last, user's size-sort respected). The queue
  // below follows it so next/prev never disagree with the visible list.
  const displayFiles = useMemo(
    () => filterAndSortFiles(info?.files ?? [], {
      filter: fileFilter, typeFilter: fileTypeFilter,
      sortBySize: fileSortBySize, sizeDesc: fileSizeDesc,
    }),
    [info, fileFilter, fileTypeFilter, fileSortBySize, fileSizeDesc],
  )

  // Unified in-torrent queue (album tracks / series episodes) of the same kind
  // as the current file. Generalises the old video-only navigation so audio
  // albums get ⏮⏭ too. Hook keeps the logic out of this god-file (gate).
  const mediaQueue = useMediaQueue(info, selectedFile, displayFiles)
  // Ordem de reprodução das faixas do MESMO torrent, respeitando shuffle (bag) e
  // servindo de base pro repeat. O picker/sidebar segue usando mediaQueue (ordem
  // de exibição); o transporte (prev/next/onEnded) segue trackOrder.
  const trackOrder = useTrackOrder(mediaQueue.indices, selectedFile, shuffle, info?.infoHash)

  const playFile = (idx: number) => {
    if (idx < 0) return
    resetForFile(idx)
  }

  // Continuous transport: stay within the current torrent's queue, then spill
  // over into the user's playlist (next/prev torrent) at the boundary — one
  // logical timeline (Spotify/VLC style). Reused by the buttons, MediaSession
  // (lock-screen/headphones) and onEnded auto-advance. nextTrack/prevTrack
  // decidem faixa vs. spill vs. wrap (repeat-all sem playlist) — shuffle e
  // repeat passam a valer DENTRO do álbum, não só entre torrents.
  const handleNext = () => {
    const step = nextTrack(trackOrder.order, selectedFile, repeat, !!onPlaylistAdvance)
    if (step.kind === 'track') { playFile(step.fileIndex); return }
    if (step.kind === 'wrap-rebuild') {
      const first = trackOrder.rebuildAndFirst()
      if (first != null) playFile(first)
      return
    }
    onPlaylistAdvance?.()
  }
  const handlePrev = () => {
    const step = prevTrack(trackOrder.order, selectedFile, repeat, !!onPlaylistPrevious)
    if (step.kind === 'track') { playFile(step.fileIndex); return }
    onPlaylistPrevious?.()
  }
  const hasNext = trackOrder.hasNext || !!onPlaylistAdvance || repeat === 'all'
  const hasPrev = trackOrder.hasPrev || !!onPlaylistPrevious || repeat === 'all'

  const handleVideoEnded = () => {
    // Elemento ATIVO: em áudio o <audio> do SimpleAudioPlayer (espelhado em
    // audioRef via elementRef), em vídeo o <video>. Antes lia só videoRef →
    // em áudio era null e o repeat-one nunca religava a faixa.
    const v = audioMode ? audioRef.current : videoRef.current
    // iOS/WebKit dispara 'ended' ESPÚRIO quando o <video> direct-play TRAVA no
    // início (stall em readyState 2, playhead ~0) em vez de realmente terminar.
    // Tratar isso como fim auto-avançaria pro próximo item (na ordem/shuffle) e
    // trocaria o src no meio do start, abortando o play() pendente — era o
    // "trocou de faixa sozinho + sem som" no iPhone. Só é fim de verdade quando o
    // playhead chegou perto da duração; com duração desconhecida (0/NaN) avança
    // normal (não há como distinguir).
    // Fim de verdade ⇒ o playhead chegou perto da duração. Dois padrões de espúrio:
    //  (a) duração conhecida e o playhead longe do fim;
    //  (b) duração 0/NaN (elemento recém-trocado, ainda não estabilizou) com o
    //      playhead ainda no começo — o stall cross-item (mp3↔m4a) que ANTES
    //      escapava do guard e fazia a lista "pular" faixas sozinha (churn). Sem
    //      isto, ao destravar o auto-avanço, a 2ª faixa estalava e avançava em loop.
    const knownFarFromEnd = !!v && Number.isFinite(v.duration) && v.duration > 0 && v.currentTime < v.duration - 2
    const unknownDurAtStart = !!v && !(Number.isFinite(v.duration) && v.duration > 0) && v.currentTime < 1
    if (knownFarFromEnd || unknownDurAtStart) {
      clientLog('warn', 'player', 'ended espúrio ignorado', { currentTime: v?.currentTime, duration: v?.duration, readyState: v?.readyState })
      return
    }
    clientLog('info', 'player', 'video ended → avança', { repeat, nextIdx: mediaQueue.nextIdx, hasPlaylistAdvance: !!onPlaylistAdvance, audioMode })
    if (repeat === 'one') {
      if (v) { v.currentTime = 0; v.play().catch(() => {}) }
      return
    }
    // Continuous advance: next track/episode, else next playlist item.
    handleNext()
  }

  // ─── Áudio simplificado ───────────────────────────────────────────────────
  // Player de áudio "pelado": <audio controls> com src DIRECT, sem Web Audio,
  // sem gapless/crossfade, sem HLS.js, sem <track>. A única diferença entre
  // origem local (rclone/disco) e torrent é a URL.
  const inPlaylist = !!playlist && playlist.items.length > 1
  const audioDirectSrc = useAudioDirectUrl(info, selectedFile, mediaToken)
  const activeMediaRef = audioMode ? audioRef : videoRef

  // Sidebar agregada da playlist (lista de faixas de vários itens). O esqueleto
  // persiste ao fechar a sidebar (não re-resolve ~47 faixas ao reabrir); a rajada
  // de resolução é gateada por `sidebarOpen`. O antigo `resolveEnabled`/blessed foi
  // removido: com preload='none' no iOS não há byte-stream pra sufocar.
  const aggregate = usePlaylistTracks(playlist?.items ?? [], playlist?.currentIndex ?? -1, info, inPlaylist && sidebarOpen)

  // Espelha currentTime/duration/onProgress do <audio> no estado do player.
  const handleAudioTimeUpdate = useCallback((currentTime: number, duration: number) => {
    setCurrentTime(currentTime)
    setDuration(duration)
    onProgress?.(currentTime)
  }, [onProgress])

  // Atalhos de teclado controlam o elemento ativo (<audio> ou <video>).
  useKeyboardShortcuts({ videoRef: activeMediaRef, minimized, requestFullscreen: handleRequestFullscreen })

  // Media Session API — expõe metadata + controles de lock-screen/AirPods.
  // Capa pra tela de bloqueio (Now Playing) — URL ABSOLUTA porque o iOS busca a
  // imagem fora do contexto da página. Cobre local e torrent (audioCoverURL).
  const mediaArtworkURL = info ? `${globalThis.location?.origin ?? ''}${audioCoverURL(info, selectedFile, mediaToken)}` : ''
  useMediaSession({ videoRef: activeMediaRef, info, selectedFile, playlistName: playlist?.name, onNext: handleNext, onPrev: handlePrev, artworkURL: mediaArtworkURL })

  return {
    displayFiles, mediaQueue, trackOrder,
    playFile, handleNext, handlePrev, hasNext, hasPrev, handleVideoEnded,
    inPlaylist, audioDirectSrc, activeMediaRef, aggregate, handleAudioTimeUpdate,
  }
}
