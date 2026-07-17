import { useState, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { X, Loader2, AlertCircle, Maximize2 } from 'lucide-react'
import {
  TorrentInfo,
  StreamProbe,
  TranscodeCapabilities,
  isIOS,
} from '../api/client'
import { usePersistedState } from '../lib/storage'
import { useScrollLock } from '../lib/useScrollLock'
import { useSwipe } from '../lib/useSwipe'
import { useIncognito } from '../lib/incognito'
import { useAuth } from '../auth/AuthContext'
import FilePreviewModal from './FilePreviewModal'
import { detectViewerKind } from './viewer/viewerKind'
import { previewRawURL } from '../api/preview'
import { useHoverThumb } from './FileThumbHover'
import { parseEpisodeTag, type FileType } from './player/playerFormat'
import { useSubtitles } from './player/useSubtitles'
import { computeMediaUrls } from './player/mediaUrls'
import { seamlessAudioAvailable } from './player/hlsAudioTracks'
import DownloadModal from './DownloadModal'
import { useToast } from './Toast'
import type { PlayerModalProps } from './player/playerTypes'
import { minimizedOrFullClass, shellProps, renderPlayerHeader } from './player/PlayerHeader'
import { renderTorrentInfoModal } from './player/TorrentInfoSheet'
import { renderPlaylistBar } from './player/PlaylistBar'
import { VideoErrorOverlay } from './player/VideoErrorOverlay'
import { ActiveStreamView } from './player/ActiveStreamView'
import { usePlayerDownloads } from './player/usePlayerDownloads'
import { useStreamSession } from './player/useStreamSession'
import { useResumePlayback } from './player/useResumePlayback'
import { useVideoFallback } from './player/useVideoFallback'
import { useFullscreen } from './player/useFullscreen'
import { useFavorites } from './player/useFavorites'
import { usePlayerTransport } from './player/usePlayerTransport'
import { usePlaybackProgress } from './player/usePlaybackProgress'

export type { PlaylistMeta } from './player/playerTypes'

export default function PlayerModal({
  result,
  onClose,
  initialFileIndex,
  initialSeek,
  playlist = null,
  onPlaylistAdvance,
  onPlaylistPrevious,
  onPlaylistJump,
  repeat = 'none',
  shuffle = false,
  onCycleRepeat,
  onToggleShuffle,
  onPrefetchNextPlaylist,
  onPrefetchNextNextPlaylist,
  startMinimized = false,
  audioMode = false,
  fullViewport = false,
  onHome,
  onProgress,
}: PlayerModalProps) {
  const { t } = useTranslation()
  const { notifyError } = useToast()
  const playbackIDRef = useRef('')
  if (!playbackIDRef.current) {
    playbackIDRef.current = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`
  }
  const [info, setInfo] = useState<TorrentInfo | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  // blessed (iOS): o usuário JÁ iniciou a reprodução nesta sessão via gesto (toque
  // no "Tocar" / play). A Apple então libera load()/play() programático pras
  // faixas seguintes ("once the user has started playing the first media element").
  const [blessed, setBlessed] = useState(false)
  const [selectedFile, setSelectedFile] = useState<number>(-1)
  // Scrolls the file list to the currently-playing file when the picker opens —
  // a season pack reopened at episode 20 lands on it instead of at the top.
  const selectedFileRef = useRef<HTMLButtonElement>(null)
  const [videoError, setVideoError] = useState(false)
  // Minimized (picture-in-picture-style) mode. The <video> element stays
  // mounted in the same DOM position — only the surrounding container shrinks.
  const [minimized, setMinimized] = useState(startMinimized)
  // Lock background scroll while the player is full-screen; allow it when
  // minimized (PiP) so the user can still browse the page behind the card.
  useScrollLock(!minimized)
  // Mobile: secondary controls collapsed behind an "Opções" toggle.
  const [showMobileOpts, setShowMobileOpts] = useState(false)
  // Subtitles backend configured? (feature flag; the rest of the subtitle
  // cluster lives in useSubtitles.)
  const [subEnabled, setSubEnabled] = useState(false)
  // Embedded audio + subtitle tracks discovered via ffprobe. Shared: drives the
  // subtitle auto-pick (in useSubtitles) AND the audio auto-transcode + HEVC
  // backstop, so it stays owned here and is fed to the subtitle hook.
  const [probe, setProbe] = useState<StreamProbe | null>(null)

  // Library entry for this torrent — used for resume seek + saving position
  const [libraryEntryID, setLibraryEntryID] = useState<number | null>(null)
  const [resumePosition, setResumePosition] = useState<number | null>(null)
  // When a saved resume point exists, ask on play whether to continue or restart.
  const [showResumePrompt, setShowResumePrompt] = useState(false)
  const lastResumeSaveRef = useRef(0)
  const bufferRetryRef = useRef(0)

  // Sidebar (file list) open/closed state. Persisted to localStorage.
  const [sidebarOpen, setSidebarOpen] = useState<boolean>(() => {
    // Music mode is album-browsing first — open the track list by default.
    if (audioMode) return true
    const stored = localStorage.getItem('jackui.playerSidebar')
    return stored === null ? true : stored === '1'
  })
  useEffect(() => {
    localStorage.setItem('jackui.playerSidebar', sidebarOpen ? '1' : '0')
  }, [sidebarOpen])
  // Incognito mode: synced with the global toggle in the navbar (localStorage).
  const [incognito, setIncognito] = useIncognito()
  // Auth ligado no servidor? Com auth off as rotas de mídia são públicas.
  const { enabled: authEnabled } = useAuth()

  // serverReady — flips true the moment streamAdd resolves and the streamer has
  // actually loaded the torrent.
  const [serverReady, setServerReady] = useState(false)
  // Media token: JWT scope="media" com TTL longo (6h backend). Buscado uma vez
  // ao abrir o player e usado em TODAS as URLs de mídia.
  const [mediaToken, setMediaToken] = useState('')

  // Frozen snapshot of the diagnostic at the moment onVideoError fired.
  const [lastErrorDiag, setLastErrorDiag] = useState<Record<string, unknown> | null>(null)
  // Inline preview for non-playable files (txt/srt/nfo/pdf/jpg/etc).
  const [previewFileIdx, setPreviewFileIdx] = useState<number | null>(null)

  // Transcoding options — any non-null value triggers `/api/stream/transcode` instead of raw stream
  const [transcodeAudio, setTranscodeAudio] = useState<number | null>(null)
  // Fase 8 (HLS master multi-áudio): quando o master expõe >1 rendition de áudio,
  // a troca é SEAMLESS (hls.audioTrack, sem reload) — seamlessAudio guarda o
  // índice escolhido e transcodeAudio (o gatilho de reload ?audio=N) fica intacto.
  // hlsAudioCount é reportado pelo VideoPlayerElement (hls.js/WebKit). Com o toggle
  // do backend OFF o master traz ≤1 faixa → seamless nunca ativa → troca legada.
  const [hlsAudioCount, setHlsAudioCount] = useState(0)
  const [seamlessAudio, setSeamlessAudio] = useState<number | null>(null)
  // Dispara o auto-transcode do áudio incompatível no máximo uma vez por arquivo.
  const audioAutoRef = useRef(false)
  const [forceH264, setForceH264] = useState(false)
  const [burnSubTrack, setBurnSubTrack] = useState<number | null>(null)
  // HEVC auto-fallback: on first <video> error, if a GPU encoder is available, retry via transcode.
  const [transcodeFallbackAttempted, setTranscodeFallbackAttempted] = useState(false)
  const [caps, setCaps] = useState<TranscodeCapabilities | null>(null)
  // Torrent-info overlay (opened from the header Info button).
  const [showInfo, setShowInfo] = useState(false)
  const [hashCopied, setHashCopied] = useState(false)
  // Variable playback speed for audiobooks / lectures. Persisted in localStorage.
  const [playbackSpeed, setPlaybackSpeed] = useState<number>(() => {
    const stored = Number.parseFloat(localStorage.getItem('jackui.playbackSpeed') || '1')
    return Number.isFinite(stored) && stored > 0 ? stored : 1
  })
  // File list filter.
  const [fileFilter, setFileFilter] = useState('')
  const [fileTypeFilter, setFileTypeFilter] = useState<FileType>('all')
  // Size sort is shared (persisted) with TorrentContentsModal.
  const [fileSortBySize, setFileSortBySize] = usePersistedState('fileview.sortBySize', false)
  const [fileSizeDesc, setFileSizeDesc] = usePersistedState('fileview.sizeDesc', true)
  const hoverThumb = useHoverThumb()

  // Favorites — auto-mark after 5min of actual playback (currentTime accumulates)
  const watchedRef = useRef(0)            // accumulated playback time (seconds)
  const lastTickRef = useRef<number>(0)   // last currentTime sample (for delta)
  const AUTO_FAV_THRESHOLD = 5 * 60       // 5 minutes
  // Playback state
  const [currentTime, setCurrentTime] = useState(0)
  const [duration, setDuration] = useState(0)
  const [bufferedEnd, setBufferedEnd] = useState(0)
  // All buffered TimeRanges as [start, end] pairs (seconds).
  const [bufferedRanges, setBufferedRanges] = useState<Array<[number, number]>>([])
  const videoRef = useRef<HTMLVideoElement>(null)
  const audioRef = useRef<HTMLAudioElement | null>(null)
  // Swipe down on the header bar minimizes the player to its PiP card.
  const headerRef = useRef<HTMLDivElement>(null)
  useSwipe(headerRef, { onDown: () => setMinimized(true) }, { enabled: !minimized, threshold: 50 })
  // Minimized → arrastar pra CIMA (ou tocar) na barra expande de volta.
  const miniBarRef = useRef<HTMLDivElement>(null)
  useSwipe(miniBarRef, { onUp: () => setMinimized(false) }, { enabled: minimized, threshold: 40 })
  // Prefetch fire-once flags. Reset whenever the underlying selected file changes.
  const prefetchedNextEpRef = useRef(false)
  const prefetchedPlaylistN1Ref = useRef(false)
  const prefetchedPlaylistN2Ref = useRef(false)
  // Tracks which file indices have already been auto-enqueued for background download.
  const autoDownloadDoneRef = useRef<Set<number>>(new Set())

  // Subtitle cluster (external/embedded/sidecar/custom tracks, OpenSubtitles
  // panel, sync offset). probe stays here (shared) and is fed in via setProbe.
  const subs = useSubtitles({ videoRef, info, selectedFile, serverReady, result, mediaToken, setProbe })
  const {
    subActive, embeddedSub, customSubURL, localEmbeddedVttURL, sidecarIdx,
    resetSubtitles, resetSubtitlesForFile,
  } = subs

  // Favorites (isFavorite + toggle + initial load).
  const { isFavorite, setIsFavorite, toggleFavorite } = useFavorites(info, result)

  // Download entry points (📁↓ folder, cache-on-server, local, auto-enqueue).
  const downloads = usePlayerDownloads({ info, result, selectedFile, notifyError })
  const {
    playerDownload, setPlayerDownload, setOverrideCategory,
    classifyingCat, effectiveCategory, enqueueNextDownloadRef, handleClassifyCategory,
  } = downloads

  // Fullscreen affordances (button + iPhone-landscape auto-fullscreen).
  const handleRequestFullscreen = useFullscreen(videoRef)

  // Cross-cutting reset when a new result opens. Kept here (all setters in scope)
  // so useStreamSession's main effect just calls it with the warm-hold flag.
  const resetForNewResult = (warmHold: boolean) => {
    // The PlayerProvider reuses this instance across videos, so a stale hover
    // preview from the previous file would otherwise stay pinned. Dismiss it.
    hoverThumb.hide()
    setLoading(true)
    setError('')
    if (!warmHold) {
      setInfo(null)
      setSelectedFile(-1)
      setServerReady(false)
      setCurrentTime(0)
      setDuration(0)
      setBufferedEnd(0)
      setBufferedRanges([])
    }
    setVideoError(false)
    // Subtitle-cluster reset lives in the hook; probe stays owned here.
    resetSubtitles()
    setProbe(null)
    setLibraryEntryID(null)
    setResumePosition(null)
    lastResumeSaveRef.current = 0
    setTranscodeAudio(null)
    setSeamlessAudio(null)
    setHlsAudioCount(0)
    audioAutoRef.current = false
    setForceH264(false)
    setBurnSubTrack(null)
    setTranscodeFallbackAttempted(false)
    prefetchedNextEpRef.current = false
    prefetchedPlaylistN1Ref.current = false
    prefetchedPlaylistN2Ref.current = false
    autoDownloadDoneRef.current.clear()
    bufferRetryRef.current = 0
    setFileFilter('')
    setFileTypeFilter('all')
    // fileSortBySize/fileSizeDesc persist (shared with TorrentContentsModal) —
    // intentionally NOT reset here, so the chosen order carries into the player.
  }

  // Per-file reset when the user switches track/episode within the torrent.
  const resetForFile = (idx: number) => {
    setSelectedFile(idx)
    setVideoError(false)
    setLastErrorDiag(null)
    // Per-file subtitle reset (subset); probe stays owned here.
    resetSubtitlesForFile()
    setProbe(null)
    watchedRef.current = 0
    lastTickRef.current = 0
    setCurrentTime(0)
    setBufferedEnd(0)
    setBufferedRanges([])
  }

  // Session lifecycle: media-token, streamAdd (+cache preview), Cinema↔Música
  // re-send, 2s poll, viewer lease. handlePlaybackStarted marks iOS "blessed".
  const { handlePlaybackStarted } = useStreamSession({
    result, audioMode, initialFileIndex, t, info, selectedFile, caps, blessed,
    setLoading, setError, setInfo, setSelectedFile, setServerReady, setMediaToken,
    setSubEnabled, setCaps, setBlessed, resetForNewResult,
  })

  // Resume-position plumbing (library fetch, final persist, seek/autoplay on canplay).
  const { handleVideoCanPlay } = useResumePlayback({
    info, incognito, selectedFile, initialSeek, audioMode, blessed, videoRef,
    libraryEntryID, resumePosition, setLibraryEntryID, setResumePosition, setShowResumePrompt,
  })

  // In-torrent queue + transport + audio engine + keyboard/media-session.
  const transport = usePlayerTransport({
    info, selectedFile, shuffle, repeat, audioMode, minimized, mediaToken, playlist, sidebarOpen,
    onPlaylistAdvance, onPlaylistPrevious, onProgress, videoRef, audioRef, handleRequestFullscreen,
    fileFilter, fileTypeFilter, fileSortBySize, fileSizeDesc, resetForFile, setCurrentTime, setDuration,
  })
  const {
    mediaQueue, trackOrder,
    playFile, handleNext, handlePrev, hasNext, hasPrev, handleVideoEnded,
    audioDirectSrc, activeMediaRef, aggregate, handleAudioTimeUpdate,
  } = transport

  // Per-timeupdate work + playback-speed / auto-download / art / scroll effects.
  const { handleTimeUpdate } = usePlaybackProgress({
    videoRef, info, selectedFile, incognito, serverReady, sidebarOpen, playbackSpeed,
    mediaQueueNextIdx: mediaQueue.nextIdx, isFavorite, autoFavThreshold: AUTO_FAV_THRESHOLD,
    libraryEntryID, selectedFileRef, watchedRef, lastTickRef, lastResumeSaveRef,
    prefetchedNextEpRef, prefetchedPlaylistN1Ref, prefetchedPlaylistN2Ref, autoDownloadDoneRef,
    enqueueNextDownloadRef, onProgress, onPrefetchNextPlaylist, onPrefetchNextNextPlaylist,
    setCurrentTime, setDuration, setBufferedEnd, setBufferedRanges, setIsFavorite,
  })

  // URL builder: raw direct play unless any transcoding option is active. Safari
  // + HEVC/x265/AV1/4K short-circuits to HLS before the first <video> attempt.
  const videoUrls = computeMediaUrls({ info, selectedFile, serverReady, mediaToken, transcodeAudio, forceH264, burnSubTrack, subActive, sidecarIdx, embeddedSub, customSubURL, localEmbeddedVttURL, caps, authEnabled, probe, playbackID: playbackIDRef.current })
  const { streamURL, encoderLabel, isTranscoded } = videoUrls

  // Fase 8: seleção de áudio unificada. Com o master expondo >1 rendition, a troca
  // é seamless (seamlessAudio, via hls.audioTrack — sem tocar em transcodeAudio,
  // logo sem reload da streamURL); senão cai no legado (setTranscodeAudio → ?audio=N).
  const seamlessAudioOn = seamlessAudioAvailable(hlsAudioCount)
  const activeAudioIndex = seamlessAudioOn ? seamlessAudio : transcodeAudio
  const selectAudio = (idx: number | null) => {
    if (seamlessAudioOn) setSeamlessAudio(idx)
    else setTranscodeAudio(idx)
  }

  // HEVC/unsupported-codec handling (audio auto-transcode, onError fallback
  // chain, Safari silent-failure backstop). Called AFTER computeMediaUrls so it
  // sees the resolved streamURL/isTranscoded.
  const { videoDiagnostic, handleVideoError } = useVideoFallback({
    videoRef, info, probe, selectedFile, audioMode, bufferedEnd, streamURL, isTranscoded,
    transcodeAudio, forceH264, burnSubTrack, transcodeFallbackAttempted, videoError, caps,
    audioAutoRef, bufferRetryRef,
    setTranscodeAudio, setForceH264, setTranscodeFallbackAttempted,
    setVideoError, setLastErrorDiag,
  })

  // Apply a changed initialFileIndex when the player is ALREADY open for the
  // same torrent (e.g. the user picks a different file in the contents modal).
  useEffect(() => {
    if (initialFileIndex === undefined || initialFileIndex < 0) return
    if (!info || initialFileIndex >= info.files.length) return
    if (initialFileIndex === selectedFile) return
    playFile(initialFileIndex)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialFileIndex])

  // "Nenhum arquivo de vídeo": aviso informativo — some sozinho após uns segundos.
  const [showNoVideoBanner, setShowNoVideoBanner] = useState(true)
  useEffect(() => {
    if (!info || info.files.some(f => f.isVideo)) return // tem vídeo → nem aparece
    setShowNoVideoBanner(true)
    const t = setTimeout(() => setShowNoVideoBanner(false), 8000)
    return () => clearTimeout(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [info?.infoHash])

  if (!result) return null

  const videoFiles = info?.files.filter(f => f.isVideo) || []
  const currentFile = selectedFile >= 0 ? info?.files[selectedFile] : null
  const currentEp = currentFile ? parseEpisodeTag(currentFile.path) : null
  // disableNativeAutoplay: bloqueia o autoplay não-gesto SÓ até o usuário iniciar
  // a reprodução (blessed). O <video> no iOS sofre o mesmo bloqueio do áudio.
  const disableNativeAutoplay = isIOS() && !blessed

  const renderVideoError = () => (
    <VideoErrorOverlay
      info={info}
      selectedFile={selectedFile}
      lastErrorDiag={lastErrorDiag}
      videoDiagnostic={videoDiagnostic}
      onRetry={() => setVideoError(false)}
    />
  )

  return (
    <div {...shellProps({ minimized, audioMode, fullViewport, onHome, onClose, setMinimized })}>
      <div className={minimizedOrFullClass(minimized, audioMode, fullViewport)}>
        {/* Minimized (PiP) control strip — renderPlayerHeader returns null when
            minimized. This bar restores the expand + close affordances. */}
        {minimized && (
          <div ref={miniBarRef} className="flex flex-col flex-shrink-0 bg-surface/80 border-b border-default touch-pan-y" title={t('player.modal.dragUpToExpand')}>
            <div className="mx-auto mt-1 h-1 w-8 rounded-full bg-gray-600" aria-hidden />
            <div className="flex items-center justify-between gap-2 px-2 py-1">
              <span className="text-[11px] text-text-primary truncate min-w-0 px-1" title={info?.name || result.title}>{info?.name || result.title}</span>
              <div className="flex items-center gap-0.5 flex-shrink-0">
                <button onClick={(e) => { e.stopPropagation(); setMinimized(false) }} title={t('player.modal.expand')} className="p-1 rounded text-text-secondary hover:text-text-primary hover:bg-surface-tertiary/60"><Maximize2 className="w-4 h-4" /></button>
                <button onClick={(e) => { e.stopPropagation(); onClose() }} title={t('player.modal.close')} className="p-1 rounded text-text-secondary hover:text-text-primary hover:bg-surface-tertiary/60"><X className="w-4 h-4" /></button>
              </div>
            </div>
          </div>
        )}
        {renderPlayerHeader({ minimized, info, result, isTranscoded, caps, encoderLabel, isFavorite, toggleFavorite, incognito, setIncognito, setMinimized, onClose, onShowInfo: () => setShowInfo(true), headerRef, fullViewport, onHome, t })}
        {!minimized && showInfo && info && renderTorrentInfoModal({
          info, result, isTranscoded, encoderLabel,
          onClose: () => setShowInfo(false),
          onCopyHash: () => { if (info.infoHash) { navigator.clipboard?.writeText(info.infoHash); setHashCopied(true); globalThis.setTimeout(() => setHashCopied(false), 2000) } },
          hashCopied,
          effectiveCategory,
          setOverrideCategory,
          handleClassifyCategory,
          classifyingCat,
          t,
        })}
        {/* Top playlist bar is hidden in audio mode (controls duplicated below). */}
        {playlist && !audioMode && renderPlaylistBar(playlist, { onPrev: onPlaylistPrevious, onToggleShuffle, shuffle, onCycleRepeat, repeat, onNext: onPlaylistAdvance }, t)}

        {/* Content. min-h-0 + flex-1 lets the active-stream block manage its own scroll. */}
        <div className="flex flex-col flex-1 min-h-0 overflow-hidden">
          {/* Big loading: only when we have NOTHING — no cache hit AND streamAdd pending. */}
          {loading && !info && (
            <div className="flex flex-col items-center justify-center py-16 text-text-secondary">
              <Loader2 className="w-10 h-10 animate-spin mb-4 text-green-500" />
              <p className="font-medium">{t('player.overlays.connectingSwarm')}</p>
              <p className="text-xs text-text-muted mt-2">{t('player.modal.firstTimePeers')}</p>
            </div>
          )}
          {/* Slim inline indicator: cached file list visible, swarm still warming up. */}
          {loading && info && !serverReady && (
            <div className="px-4 py-1.5 text-xs text-blue-700 dark:text-blue-300 bg-blue-500/10 border-b border-blue-500/30 flex items-center gap-2 flex-shrink-0">
              <Loader2 className="w-3 h-3 animate-spin" />
              {t('player.modal.cachedMetaConnecting')}
            </div>
          )}

          {/* Error state */}
          {error && (
            <div className="m-5 p-4 bg-red-500/10 border border-red-500/30 rounded-xl">
              <p className="flex items-center gap-2 text-red-400 font-medium">
                <AlertCircle className="w-4 h-4" />
                {t('player.modal.streamError')}
              </p>
              <p className="text-sm text-red-700 dark:text-red-300 mt-1">{error}</p>
              <p className="text-xs text-text-muted mt-3">
                {t('player.modal.streamErrorHint')}
              </p>
            </div>
          )}

          {/* Active stream */}
          {info && (
            <ActiveStreamView
              subs={subs}
              videoUrls={videoUrls}
              downloads={downloads}
              trackOrder={trackOrder}
              aggregate={aggregate}
              hoverThumb={hoverThumb}
              info={info}
              selectedFile={selectedFile}
              playlist={playlist}
              minimized={minimized}
              sidebarOpen={sidebarOpen}
              audioMode={audioMode}
              videoRef={videoRef}
              audioRef={audioRef}
              selectedFileRef={selectedFileRef}
              activeMediaRef={activeMediaRef}
              mediaToken={mediaToken}
              serverReady={serverReady}
              videoError={videoError}
              currentTime={currentTime}
              duration={duration}
              bufferedEnd={bufferedEnd}
              bufferedRanges={bufferedRanges}
              disableNativeAutoplay={disableNativeAutoplay}
              showResumePrompt={showResumePrompt}
              resumePosition={resumePosition}
              transcodeFallbackAttempted={transcodeFallbackAttempted}
              probe={probe}
              subEnabled={subEnabled}
              showMobileOpts={showMobileOpts}
              playbackSpeed={playbackSpeed}
              currentFile={currentFile}
              currentEp={currentEp}
              videoFiles={videoFiles}
              mediaFileIndices={mediaQueue.indices}
              mediaCursor={mediaQueue.cursor}
              fileFilter={fileFilter}
              fileTypeFilter={fileTypeFilter}
              fileSortBySize={fileSortBySize}
              fileSizeDesc={fileSizeDesc}
              activeAudioIndex={activeAudioIndex}
              selectAudio={selectAudio}
              seamlessAudioOn={seamlessAudioOn}
              seamlessAudioIndex={seamlessAudio}
              onHlsAudioCount={setHlsAudioCount}
              forceH264={forceH264}
              burnSubTrack={burnSubTrack}
              shuffle={shuffle}
              repeat={repeat}
              audioDirectSrc={audioDirectSrc}
              setShowResumePrompt={setShowResumePrompt}
              setResumePosition={setResumePosition}
              setVideoError={setVideoError}
              setShowMobileOpts={setShowMobileOpts}
              setPlaybackSpeed={setPlaybackSpeed}
              setForceH264={setForceH264}
              setBurnSubTrack={setBurnSubTrack}
              setFileFilter={setFileFilter}
              setFileTypeFilter={setFileTypeFilter}
              setFileSortBySize={setFileSortBySize}
              setFileSizeDesc={setFileSizeDesc}
              setSidebarOpen={setSidebarOpen}
              setPreviewFileIdx={setPreviewFileIdx}
              renderVideoError={renderVideoError}
              videoDiagnostic={videoDiagnostic}
              onVideoError={handleVideoError}
              onTimeUpdate={handleTimeUpdate}
              onVideoEnded={handleVideoEnded}
              onVideoCanPlay={handleVideoCanPlay}
              onPlaybackStarted={handlePlaybackStarted}
              onAudioTimeUpdate={handleAudioTimeUpdate}
              handlePrev={handlePrev}
              handleNext={handleNext}
              hasPrev={hasPrev}
              hasNext={hasNext}
              handleRequestFullscreen={handleRequestFullscreen}
              playFile={playFile}
              onToggleShuffle={onToggleShuffle}
              onCycleRepeat={onCycleRepeat}
              onPlaylistJump={onPlaylistJump}
            />
          )}

          {/* No video files in torrent — auto-dismisses after a few seconds. */}
          {!audioMode && info && videoFiles.length === 0 && showNoVideoBanner && (
            <div className="m-5 p-4 bg-yellow-500/10 border border-yellow-500/30 rounded-xl transition-opacity">
              <p className="flex items-center gap-2 text-yellow-400 font-medium">
                <AlertCircle className="w-4 h-4" />
                {t('player.modal.noVideoFile')}
              </p>
              <p className="text-xs text-text-muted mt-2">
                {t('player.modal.noVideoDetail', { count: info.files.length })}
              </p>
            </div>
          )}
        </div>
      </div>
      {/* Inline preview overlay for non-playable companion files. Rendered
          outside the main modal box so its z-index can sit ABOVE the player. */}
      {previewFileIdx !== null && info?.files[previewFileIdx] && (() => {
        // Sibling images of the same torrent become prev/next navigation.
        const imageFiles = info.files.filter(f => detectViewerKind(f.path) === 'image')
        const imageStart = Math.max(0, imageFiles.findIndex(f => f.index === previewFileIdx))
        return (
          <FilePreviewModal
            infoHash={info.infoHash}
            fileIdx={previewFileIdx}
            filePath={info.files[previewFileIdx].path}
            fileSize={info.files[previewFileIdx].size}
            imageItems={imageFiles.map(f => ({ label: f.path, url: previewRawURL(info.infoHash, f.index) }))}
            imageStart={imageStart}
            onClose={() => setPreviewFileIdx(null)}
          />
        )
      })()}
      {/* Download modal aninhado (📁↓ por arquivo / "cache no servidor"). A
          barreira de propagação evita que o Escape/clique-fora borbulhe pro shell. */}
      {playerDownload && (
        <div
          onClick={e => e.stopPropagation()}
          onKeyDown={e => e.stopPropagation()}
          role="presentation"
        >
          <DownloadModal
            result={playerDownload.result}
            initialFileIndices={playerDownload.indices}
            nested
            onClose={() => setPlayerDownload(null)}
          />
        </div>
      )}
      {hoverThumb.popover}
    </div>
  )
}
