import { useState, useEffect, useRef } from 'react'
import { X, Play, Loader2, AlertCircle, FileVideo, Download, ExternalLink, Users, Activity, Subtitles, Check, Maximize2, Minimize2, Minus, Plus, RotateCcw, FastForward, Cpu, Volume2, Flame, Heart, ChevronLeft, ChevronRight, ChevronDown, ListMusic, Shuffle, Repeat, EyeOff, Eye, ArrowDownWideNarrow, ArrowUpWideNarrow, Upload } from 'lucide-react'
import {
  SearchResult,
  TorrentInfo,
  Subtitle,
  StreamProbe,
  TranscodeOpts,
  TranscodeCapabilities,
  MediaTrack,
  SidecarSubtitle,
  streamAdd,
  streamMetadata,
  pickTorrentSource,
  streamInfo,
  streamDrop,
  streamFileURL,
  streamHLSMasterURL,
  streamArtworkURL,
  isSafariBrowser,
  streamSubtrackURL,
  streamTranscodeURL,
  streamSidecarURL,
  streamPlaylistM3UURL,
  streamPrefetch,
  resolveArt,
  subtitlesEnabled,
  subtitlesSearch,
  subtitlesAuto,
  subtitleDownloadURL,
  fetchMediaToken,
  transcodeCapabilities,
  favoriteAdd,
  favoriteRemove,
  favoritesList,
  libraryGet,
  libraryUpdateResume,
  LibraryEntry,
  downloadCreate,
  streamThumbnailURL,
} from '../api/client'
import { formatRate } from '../lib/format'
import { clientLog } from '../lib/diag'
import { useScrollLock } from '../lib/useScrollLock'
import { useIncognito } from '../lib/incognito'
import FilePreviewModal, { detectPreviewKind } from './FilePreviewModal'
import { useHoverThumb } from './FileThumbHover'
import { useKeyboardShortcuts, useMediaSession, useSubtitleOffset, useTrackProbe, useSubtitleChoicePersist, useHevcBackstop } from './player/playerHooks'

type PlaylistMeta = {
  readonly name: string
  readonly items: readonly { title: string }[]
  readonly currentIndex: number
}

type PlayerModalProps = {
  readonly result: SearchResult | null
  readonly onClose: () => void
  readonly initialFileIndex?: number
  readonly initialSeek?: number
  readonly playlist?: PlaylistMeta | null
  readonly onPlaylistAdvance?: () => void
  readonly onPlaylistPrevious?: () => void
  readonly repeat?: 'none' | 'one' | 'all'
  readonly shuffle?: boolean
  readonly onCycleRepeat?: () => void
  readonly onToggleShuffle?: () => void
  readonly onPrefetchNextPlaylist?: () => void
  readonly onPrefetchNextNextPlaylist?: () => void
  readonly startMinimized?: boolean
  readonly audioMode?: boolean
}

function formatSize(bytes: number): string {
  if (bytes === 0 || !bytes) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${Number.parseFloat((bytes / Math.pow(k, i)).toFixed(2))} ${sizes[i]}`
}

type FileType = 'all' | 'video' | 'audio' | 'other'
const PLAYER_AUDIO_RE = /\.(mp3|flac|m4a|aac|ogg|wav|opus|alac|wma)$/i
const PLAYER_VIDEO_RE = /\.(mp4|mkv|avi|mov|webm|m4v|wmv|flv|ts|m2ts|vob)$/i
// Variable playback speed for audiobooks / lectures.
const SPEED_OPTIONS = [0.75, 1, 1.25, 1.5, 1.75, 2, 2.5, 3] as const

// fileType buckets a file for the sidebar type filter: video (backend flag or
// extension) → audio (extension) → everything else.
function fileType(f: { isVideo?: boolean; path: string }): Exclude<FileType, 'all'> {
  if (f.isVideo || PLAYER_VIDEO_RE.test(f.path)) return 'video'
  if (PLAYER_AUDIO_RE.test(f.path)) return 'audio'
  return 'other'
}

function buildErrorInfo(peers: number, starving: boolean, info: TorrentInfo | null): { title: string; detail: string } {
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

type MediaUrlInput = {
  info: TorrentInfo | null
  selectedFile: number
  serverReady: boolean
  mediaToken: string
  transcodeAudio: number | null
  forceH264: boolean
  burnSubTrack: number | null
  subActive: string | null
  sidecarIdx: number | null
  embeddedSub: number | null
  customSubURL: string | null
  caps: TranscodeCapabilities | null
}

function computeMediaUrls(input: MediaUrlInput) {
  const { info, selectedFile, serverReady, mediaToken, transcodeAudio, forceH264, burnSubTrack, subActive, sidecarIdx, embeddedSub, customSubURL, caps } = input
  const selectedFilename = info?.files?.[selectedFile]?.path ?? ''
  const safariNeedsTranscode = isSafariBrowser() &&
    /\b(x265|h\.?265|hevc|av1|2160p?|4k|uhd)\b/i.test(selectedFilename)
  const isTranscoded = transcodeAudio !== null || forceH264 || burnSubTrack !== null || safariNeedsTranscode

  const transcodeOpts: TranscodeOpts = {}
  if (transcodeAudio !== null) transcodeOpts.audio = transcodeAudio
  if (forceH264) transcodeOpts.video = 'h264'
  if (burnSubTrack !== null) {
    transcodeOpts.burn = burnSubTrack
    transcodeOpts.video = 'h264'
  }
  if (transcodeAudio !== null) transcodeOpts.acodec = 'aac'

  const streamURL = (() => {
    if (!info || selectedFile < 0 || !serverReady || !mediaToken) return ''
    if (!isTranscoded) return streamFileURL(info.infoHash, selectedFile, mediaToken)
    if (isSafariBrowser()) return streamHLSMasterURL(info.infoHash, selectedFile, mediaToken)
    return streamTranscodeURL(info.infoHash, selectedFile, transcodeOpts, mediaToken)
  })()

  const subtitleVttURL = (() => {
    if (customSubURL) return customSubURL
    if (!mediaToken) return ''
    if (info && sidecarIdx !== null) return streamSidecarURL(info.infoHash, sidecarIdx, mediaToken)
    if (info && embeddedSub !== null) return streamSubtrackURL(info.infoHash, selectedFile, embeddedSub, mediaToken)
    if (subActive) return subtitleDownloadURL(subActive, mediaToken)
    return ''
  })()

  let vlcURL = ''
  if (info && selectedFile >= 0) {
    const transcodeParam = forceH264 ? 'h264' : undefined
    vlcURL = streamPlaylistM3UURL(info.infoHash, selectedFile, transcodeParam)
  }

  let encoderLabel = 'CPU'
  if (caps?.hasNvidia) {
    encoderLabel = 'NVENC'
  } else if (caps?.hasVaapi) {
    encoderLabel = 'VAAPI'
  } else if (caps?.hasQsv) {
    encoderLabel = 'QSV'
  }

  return { streamURL, subtitleVttURL, vlcURL, encoderLabel, isTranscoded }
}

function renderPlayerHeader(props: {
  minimized: boolean
  info: TorrentInfo | null
  result: SearchResult
  isTranscoded: boolean
  caps: TranscodeCapabilities | null
  encoderLabel: string
  isFavorite: boolean
  toggleFavorite: () => void
  incognito: boolean
  setIncognito: (v: boolean) => void
  setMinimized: (v: boolean | ((prev: boolean) => boolean)) => void
  onClose: () => void
}) {
  const { minimized, info, result, isTranscoded, caps, encoderLabel, isFavorite, toggleFavorite, incognito, setIncognito, setMinimized, onClose } = props
  if (minimized) return null
  return (
    <div className="flex items-center justify-between px-4 pb-4 pt-statusbar sm:!pt-4 border-b border-gray-700 flex-shrink-0">
      <h2 className="text-base font-semibold text-gray-100 flex items-center gap-2 min-w-0">
        <Play className="w-4 h-4 text-green-500 flex-shrink-0" />
        <span className="truncate">{info?.name || result.title}</span>
        {isTranscoded && caps?.preferred && <span className="text-[10px] bg-purple-500/20 text-purple-300 border border-purple-500/30 px-1.5 py-0.5 rounded flex items-center gap-1 flex-shrink-0" title={`Encoder: ${caps.preferred}`}><Cpu className="w-2.5 h-2.5" />{encoderLabel}</span>}
        {isTranscoded && !caps?.preferred && <span className="text-[10px] bg-purple-500/20 text-purple-300 border border-purple-500/30 px-1.5 py-0.5 rounded flex items-center gap-1 flex-shrink-0"><Cpu className="w-2.5 h-2.5" />GPU</span>}
      </h2>
      <div className="flex items-center gap-2 flex-shrink-0 ml-2">
        {info && <button onClick={toggleFavorite} title={isFavorite ? 'Remover dos favoritos' : 'Marcar como favorito'} className={`transition-colors ${isFavorite ? 'text-pink-400 hover:text-pink-300' : 'text-gray-500 hover:text-pink-400'}`}><Heart className={`w-5 h-5 ${isFavorite ? 'fill-current' : ''}`} /></button>}
        <button onClick={() => setIncognito(!incognito)} title={incognito ? 'Modo incógnito ativo' : 'Ativar modo incógnito'} className={`transition-colors ${incognito ? 'text-amber-400 hover:text-amber-300' : 'text-gray-400 hover:text-gray-200'}`}>{incognito ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}</button>
        <button onClick={() => setMinimized(m => !m)} title={minimized ? 'Expandir player' : 'Minimizar'} className="text-gray-400 hover:text-gray-200 transition-colors">{minimized ? <Maximize2 className="w-4 h-4" /> : <Minimize2 className="w-5 h-5" />}</button>
        <button onClick={onClose} className="text-gray-400 hover:text-gray-200 transition-colors"><X className="w-5 h-5" /></button>
      </div>
    </div>
  )
}

function renderPlaylistBar(
  playlist: PlaylistMeta,
  onPrev: (() => void) | undefined,
  onToggleShuffle: (() => void) | undefined,
  shuffle: boolean,
  onCycleRepeat: (() => void) | undefined,
  repeat: 'none' | 'one' | 'all',
  onNext: (() => void) | undefined,
) {
  return (
    <div className="flex items-center justify-between gap-2 px-4 py-2 bg-blue-500/10 border-b border-blue-500/30 text-xs text-blue-200 flex-shrink-0">
      <div className="flex items-center gap-2 min-w-0">
        <ListMusic className="w-3.5 h-3.5 flex-shrink-0" />
        <span className="font-medium truncate">{playlist.name}</span>
        <span className="text-blue-400/80 flex-shrink-0">· {playlist.currentIndex + 1} de {playlist.items.length}</span>
      </div>
      <div className="flex items-center gap-1 flex-shrink-0">
        <button onClick={onPrev} className="p-1 rounded hover:bg-blue-500/20 text-blue-200 hover:text-white" title="Item anterior da playlist"><ChevronLeft className="w-4 h-4" /></button>
        <button onClick={onToggleShuffle} className={`p-1 rounded hover:bg-blue-500/20 ${shuffle ? 'text-green-300' : 'text-blue-200/60'} hover:text-white`} title={shuffle ? 'Shuffle: ON' : 'Shuffle: OFF'}><Shuffle className="w-3.5 h-3.5" /></button>
        <button onClick={onCycleRepeat} className={`p-1 rounded hover:bg-blue-500/20 ${repeat === 'none' ? 'text-blue-200/60' : 'text-green-300'} hover:text-white relative`} title={`Repeat: ${repeat}`}>
          <Repeat className="w-3.5 h-3.5" />
          {repeat === 'one' && <span className="absolute -bottom-0.5 -right-0.5 text-[8px] font-bold text-green-300">1</span>}
        </button>
        <button onClick={onNext} className="p-1 rounded hover:bg-blue-500/20 text-blue-200 hover:text-white" title="Próximo item da playlist"><ChevronRight className="w-4 h-4" /></button>
      </div>
    </div>
  )
}

function tryPrefetchNext(props: {
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

function updateBufferedRanges(
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

function tryAutoFavorite(
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

function trySaveResume(
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

function trySyncUrlPlayhead(
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

function getSubtitleLabel(embeddedSub: number | null, subActive: string | null, autoSource: string | null, subLoading: boolean): string {
  if (embeddedSub !== null) return 'Legenda embutida'
  if (subActive) return autoSource === 'hash' ? 'Legenda ✓ hash' : 'Legenda ativa'
  if (subLoading) return 'Buscando...'
  return 'Legendas'
}

type VideoPlayerElementProps = {
  readonly videoRef: React.RefObject<HTMLVideoElement | null>
  readonly streamURL: string
  readonly audioMode: boolean
  readonly subtitleVttURL: string
  readonly videoError: boolean
  readonly serverReady: boolean
  readonly currentTime: number
  readonly bufferedEnd: number
  readonly info: TorrentInfo | null
  readonly selectedFile: number
  readonly showResumePrompt: boolean
  readonly resumePosition: number | null
  readonly isTranscoded: boolean
  readonly transcodeFallbackAttempted: boolean
  readonly mediaToken: string
  readonly renderVideoError: () => React.ReactNode
  readonly formatTime: (s: number) => string
  readonly onVideoError: () => void
  readonly onTimeUpdate: () => void
  readonly onVideoEnded: () => void
  readonly onVideoCanPlay: () => void
  readonly videoDiagnostic: () => Record<string, unknown>
  readonly onResumeContinue: (pos: number) => void
  readonly onResumeRestart: () => void
}

function VideoPlayerElement({
  videoRef,
  streamURL,
  audioMode,
  subtitleVttURL,
  videoError,
  serverReady,
  currentTime,
  bufferedEnd,
  info,
  selectedFile,
  showResumePrompt,
  resumePosition,
  isTranscoded,
  transcodeFallbackAttempted,
  mediaToken,
  renderVideoError,
  formatTime,
  onVideoError,
  onTimeUpdate,
  onVideoEnded,
  onVideoCanPlay,
  videoDiagnostic,
  onResumeContinue,
  onResumeRestart,
}: VideoPlayerElementProps) {
  return (
    <div className="bg-black relative w-full mx-auto flex items-center justify-center max-h-[70vh] sm:max-h-[58vh]" style={{ aspectRatio: '16 / 9' }}>
      {audioMode && info && (
        <div className="absolute inset-x-0 top-0 bottom-12 flex items-center justify-center bg-gradient-to-br from-gray-800 to-gray-900 pointer-events-none">
          <Volume2 className="absolute w-12 h-12 text-gray-600" />
          <img
            src={streamArtworkURL(info.infoHash, selectedFile, mediaToken || undefined)}
            alt=""
            className="relative max-h-full max-w-full object-contain"
            onError={(e) => { (e.currentTarget as HTMLImageElement).style.display = 'none' }}
          />
        </div>
      )}
      {showResumePrompt && resumePosition !== null && (
        <div className="absolute inset-0 z-30 flex items-center justify-center bg-black/70 backdrop-blur-sm p-4">
          <div className="bg-gray-800 border border-gray-700 rounded-2xl p-5 flex flex-col gap-3 w-full max-w-xs">
            <p className="text-gray-300 text-sm text-center">Você parou em</p>
            <p className="text-blue-300 text-center font-mono text-2xl">{formatTime(resumePosition)}</p>
            <button
              onClick={() => onResumeContinue(resumePosition)}
              className="btn-primary w-full justify-center"
            >
              Continuar
            </button>
            <button
              onClick={onResumeRestart}
              className="btn-secondary w-full justify-center"
            >
              Começar do início
            </button>
          </div>
        </div>
      )}
      {!videoError && currentTime === 0 && bufferedEnd === 0 && (
        <div className="absolute inset-0 flex flex-col items-center justify-center pointer-events-none z-10 bg-black/40">
          <Loader2 className="w-12 h-12 animate-spin text-green-500 mb-3" />
          <p className="text-gray-200 font-medium">
            {serverReady ? 'Baixando primeiras peças do torrent...' : 'Conectando ao swarm...'}
          </p>
          {resumePosition !== null && (
            <p className="text-xs text-blue-300 mt-2">
              Continuando de {formatTime(resumePosition)}
            </p>
          )}
          <p className="text-xs text-gray-400 mt-1">
            {info && info.peers > 0
              ? `${info.seeders} seeders / ${info.peers} peers conectados`
              : 'Aguardando peers...'}
          </p>
          {info && info.downRate > 0 && (
            <p className="text-[11px] text-gray-400 mt-1 tabular-nums">
              <span className="text-green-400">↓ {formatRate(info.downRate)}</span>
              {info.files?.[selectedFile] && (
                <span className="text-gray-500"> · {formatSize(info.files[selectedFile].downloaded)} em buffer</span>
              )}
            </p>
          )}
          {isTranscoded && (
            <p className="text-[11px] text-purple-300 mt-2 flex items-center gap-1">
              <Cpu className="w-3 h-3" />
              {transcodeFallbackAttempted
                ? 'Convertendo via GPU — codec original incompatível (HEVC/AV1)'
                : 'Transcoding ativo — primeiros frames demoram mais'}
            </p>
          )}
        </div>
      )}
      {transcodeFallbackAttempted && !videoError && (
        <div className="absolute top-2 right-2 bg-purple-600/85 text-white text-[10px] px-2 py-1 rounded-md flex items-center gap-1 backdrop-blur-sm pointer-events-none z-20">
          <Cpu className="w-3 h-3" />
          Convertendo via GPU
        </div>
      )}
      {videoError ? null : (
        <video
          ref={videoRef}
          src={streamURL || undefined}
          controls
          autoPlay
          playsInline
          {...{ 'webkit-playsinline': 'true' } as any}
          className={`max-h-full max-w-full${audioMode ? ' w-full h-full' : ''}`}
          onError={onVideoError}
          onLoadStart={() => clientLog('info', 'player', 'loadstart', { src: streamURL })}
          onStalled={() => clientLog('warn', 'player', 'stalled', videoDiagnostic())}
          onWaiting={() => clientLog('info', 'player', 'waiting (buffering)', { readyState: videoRef.current?.readyState })}
          onTimeUpdate={onTimeUpdate}
          onLoadedMetadata={(e) => {
            const v = e.currentTarget
            clientLog('info', 'player', 'loadedmetadata', { duration: v.duration, videoWidth: v.videoWidth, videoHeight: v.videoHeight, currentSrc: v.currentSrc })
            onTimeUpdate()
          }}
          onProgress={onTimeUpdate}
          onEnded={onVideoEnded}
          onCanPlay={onVideoCanPlay}
        >
          <track
            kind={subtitleVttURL ? 'subtitles' : 'metadata'}
            src={subtitleVttURL || ''}
            srcLang={subtitleVttURL ? 'pt' : ''}
            label={subtitleVttURL ? 'Português (BR)' : ''}
            default
          />
          <track kind="captions" srcLang="pt" label="Português (BR) [CC]" />
        </video>
      )}
      {videoError && renderVideoError()}
    </div>
  )
}

// Resolve the file to auto-select when (re)opening a torrent: an explicit
// override wins, then the backend-suggested primary, else the first file.
function chooseInitialFile(t: TorrentInfo, initialFileIndex: number | undefined): number {
  if (initialFileIndex !== undefined && initialFileIndex >= 0 && initialFileIndex < t.files.length) {
    return initialFileIndex
  }
  return Math.max(0, t.primaryFile)
}

const FILE_EXTRA_RE = (() => {
  const SPACE_OR_DASH = String.raw`[\s-]?`
  return new RegExp(String.raw`\b(featurettes?|extras?|bonus|behind${SPACE_OR_DASH}the${SPACE_OR_DASH}scenes|deleted${SPACE_OR_DASH}scenes|making${SPACE_OR_DASH}of|samples?|trailers?|interviews?|gag${SPACE_OR_DASH}reel|outtakes?)\b`, 'i')
})()
const FILE_AUDIO_RE = /\.(mp3|flac|m4a|aac|ogg|wav|opus|alac|wma)$/i

type FilePickerSidebarProps = {
  readonly info: TorrentInfo
  readonly videoFiles: TorrentInfo['files']
  readonly selectedFile: number
  readonly selectedFileRef: React.RefObject<HTMLButtonElement>
  readonly fileFilter: string
  readonly fileTypeFilter: FileType
  readonly fileSortBySize: boolean
  readonly fileSizeDesc: boolean
  readonly hoverThumb: ReturnType<typeof useHoverThumb>
  readonly parseEpisode: (path: string) => string | null
  readonly playFile: (idx: number) => void
  readonly setFileFilter: (v: string) => void
  readonly setFileTypeFilter: (v: FileType) => void
  readonly setFileSortBySize: (v: boolean) => void
  readonly setFileSizeDesc: (v: boolean) => void
  readonly setSidebarOpen: (v: boolean) => void
  readonly setPreviewFileIdx: (v: number | null) => void
}

// File picker — right sidebar on lg+, stacked panel below on mobile.
// Series-aware: detects S/E in filenames and labels them. Filter matches both
// the path AND the parsed S/E tag so "s04e03" finds the episode without typing
// the show name. Extras (featurettes, bonus, behind-the-scenes) sort to the
// bottom with an EXTRA badge.
function FilePickerSidebar({
  info,
  videoFiles,
  selectedFile,
  selectedFileRef,
  fileFilter,
  fileTypeFilter,
  fileSortBySize,
  fileSizeDesc,
  hoverThumb,
  parseEpisode,
  playFile,
  setFileFilter,
  setFileTypeFilter,
  setFileSortBySize,
  setFileSizeDesc,
  setSidebarOpen,
  setPreviewFileIdx,
}: FilePickerSidebarProps) {
  const filterLower = fileFilter.trim().toLowerCase()
  const matchesFile = (path: string, ep: string | null) =>
    !filterLower ||
    path.toLowerCase().includes(filterLower) ||
    (ep || '').toLowerCase().includes(filterLower)
  const isExtra = (path: string) => FILE_EXTRA_RE.test(path)
  const typeCounts = { video: 0, audio: 0, other: 0 }
  for (const f of info.files) typeCounts[fileType(f)]++
  const filteredFiles = info.files
    .filter(f => matchesFile(f.path, parseEpisode(f.path)))
    .filter(f => fileTypeFilter === 'all' || fileType(f) === fileTypeFilter)
    .slice()
    .sort((a, b) => {
      if (fileSortBySize) {
        if (a.size !== b.size) return fileSizeDesc ? b.size - a.size : a.size - b.size
        return a.index - b.index
      }
      const ax = isExtra(a.path), bx = isExtra(b.path)
      if (ax !== bx) return ax ? 1 : -1
      const ae = parseEpisode(a.path), be = parseEpisode(b.path)
      if (ae && be) return ae.localeCompare(be)
      if (ae) return -1
      if (be) return 1
      return a.index - b.index
    })
  const fileBtnClass = (fIdx: number, isPlayable: boolean, canPreview: boolean, ext: boolean): string => {
    if (selectedFile === fIdx) return 'bg-green-500/20 text-green-400 border border-green-500/30'
    if (isPlayable) {
      if (ext) return 'bg-gray-800/40 text-gray-500 hover:bg-gray-700/80 border border-transparent'
      return 'bg-gray-700/50 text-gray-300 hover:bg-gray-700 border border-transparent'
    }
    if (canPreview) return 'bg-blue-500/5 text-blue-200/80 hover:bg-blue-500/15 border border-blue-500/20'
    return 'bg-gray-800/50 text-gray-500 hover:bg-gray-700 border border-transparent'
  }
  const cycleSizeSort = () => {
    // Cicla: Padrão → Tamanho (maior) → Tamanho (menor) → Padrão
    if (!fileSortBySize) setFileSortBySize(true)
    else if (fileSizeDesc) setFileSizeDesc(false)
    else { setFileSortBySize(false); setFileSizeDesc(true) }
  }
  return (
    <aside className="flex flex-col flex-1 lg:flex-initial lg:flex-shrink-0 lg:w-80 xl:w-96 border-t lg:border-t-0 lg:border-l border-gray-700 bg-gray-850/50 min-h-0 lg:overflow-hidden">
      <div className="flex items-center justify-between gap-2 px-3 py-2 border-b border-gray-700 flex-shrink-0">
        <p className="text-xs text-gray-400 flex items-center gap-2 min-w-0">
          <FileVideo className="w-3.5 h-3.5 text-gray-500 flex-shrink-0" />
          <span className="truncate">
            {filteredFiles.length}{filterLower ? ` / ${info.files.length}` : ''} arquivo{filteredFiles.length === 1 ? '' : 's'}
            {videoFiles.length > 0 && <span className="text-blue-400"> · {videoFiles.length} vídeo{videoFiles.length === 1 ? '' : 's'}</span>}
          </span>
        </p>
        <button
          onClick={() => setSidebarOpen(false)}
          title="Esconder lista de arquivos"
          className="text-gray-500 hover:text-gray-200 p-1 rounded hover:bg-gray-700 flex-shrink-0"
        >
          <ChevronRight className="w-4 h-4" />
        </button>
      </div>
      {info.files.length > 6 && (
        <div className="px-3 py-2 border-b border-gray-700 flex-shrink-0">
          <input
            type="text"
            value={fileFilter}
            onChange={e => setFileFilter(e.target.value)}
            placeholder="Filtrar (ex: s04e03)"
            className="w-full bg-gray-900 border border-gray-700 rounded px-3 py-2 sm:py-1 text-sm sm:text-xs text-gray-200 placeholder-gray-500 focus:outline-none focus:border-green-500"
          />
        </div>
      )}
      <div className="px-3 py-2 border-b border-gray-700 flex-shrink-0 flex items-center gap-1.5 flex-wrap">
        {([
          { key: 'all' as const, label: 'Todos', count: info.files.length },
          { key: 'video' as const, label: 'Vídeo', count: typeCounts.video },
          { key: 'audio' as const, label: 'Áudio', count: typeCounts.audio },
          { key: 'other' as const, label: 'Outros', count: typeCounts.other },
        ])
          .filter(o => o.key === 'all' || o.count > 0)
          .map(o => (
            <button
              key={o.key}
              onClick={() => setFileTypeFilter(o.key)}
              className={`px-2 py-1 rounded text-[11px] border transition-colors ${
                fileTypeFilter === o.key
                  ? 'bg-green-500/20 text-green-300 border-green-500/40'
                  : 'bg-gray-900 text-gray-400 border-gray-700 hover:bg-gray-700/60'
              }`}
            >
              {o.label} <span className="tabular-nums opacity-70">{o.count}</span>
            </button>
          ))}
        <div className="flex-1" />
        <button
          onClick={cycleSizeSort}
          title="Ordenar por tamanho"
          className={`flex items-center gap-1 px-2 py-1 rounded text-[11px] border transition-colors ${
            fileSortBySize
              ? 'bg-green-500/20 text-green-300 border-green-500/40'
              : 'bg-gray-900 text-gray-400 border-gray-700 hover:bg-gray-700/60'
          }`}
        >
          {fileSortBySize && !fileSizeDesc
            ? <ArrowUpWideNarrow className="w-3.5 h-3.5" />
            : <ArrowDownWideNarrow className={`w-3.5 h-3.5 ${fileSortBySize ? '' : 'opacity-50'}`} />}
          Tamanho
        </button>
      </div>
      <div className="flex flex-col gap-1.5 px-2 py-2 overflow-y-auto min-h-0 flex-1 lg:flex-none lg:max-h-[60vh]">
        {filteredFiles.length === 0 && (
          <p className="text-xs text-gray-500 text-center py-3">
            {fileFilter ? `Nenhum arquivo bate com "${fileFilter}"` : 'Nenhum arquivo com esse filtro'}
          </p>
        )}
        {filteredFiles.slice(0, 100).map(f => {
          const ep = parseEpisode(f.path)
          const extra = isExtra(f.path)
          // Compact name for sidebar: drop the long shared prefix
          // (everything before the last "/") so paths fit in 320px.
          const shortName = f.path.split('/').slice(-2).join('/')
          const isPlayable = f.isVideo || FILE_AUDIO_RE.test(f.path)
          const previewKind = isPlayable ? 'unknown' : detectPreviewKind(f.path)
          const canPreview = previewKind !== 'unknown'
          const previewBadge = canPreview ? previewKind.toUpperCase() : null
          // Hover frame-preview only for video files.
          const thumbUrl = fileType(f) === 'video' && info.infoHash
            ? streamThumbnailURL(info.infoHash, f.index, 10)
            : null
          return (
            <button
              key={f.index}
              ref={selectedFile === f.index ? selectedFileRef : null}
              onClick={() => {
                if (isPlayable) playFile(f.index)
                else if (canPreview) setPreviewFileIdx(f.index)
                // else: dead row, click does nothing (download via long-press / context menu still available)
              }}
              onMouseEnter={e => hoverThumb.show(thumbUrl, e)}
              onMouseMove={hoverThumb.move}
              onMouseLeave={hoverThumb.hide}
              title={f.path}
              className={`flex flex-col flex-shrink-0 gap-1 px-3 py-2.5 sm:py-2 min-h-[48px] sm:min-h-0 rounded-lg text-sm sm:text-xs transition-colors text-left ${fileBtnClass(f.index, isPlayable, canPreview, extra)}`}
            >
              <span className="flex items-center gap-1.5 min-w-0">
                {ep && (
                  <span className="text-[10px] font-mono bg-blue-500/15 text-blue-300 border border-blue-500/30 px-1.5 py-0.5 rounded flex-shrink-0">
                    {ep}
                  </span>
                )}
                {extra && (
                  <span className="text-[10px] font-mono bg-gray-700/60 text-gray-400 border border-gray-600/40 px-1.5 py-0.5 rounded flex-shrink-0">
                    EXTRA
                  </span>
                )}
                {previewBadge && (
                  <span className="text-[10px] font-mono bg-blue-500/15 text-blue-300 border border-blue-500/30 px-1.5 py-0.5 rounded flex-shrink-0" title="Visualizar inline">
                    {previewBadge}
                  </span>
                )}
                {selectedFile === f.index && <Play className="w-3 h-3 flex-shrink-0" />}
              </span>
              <span className="flex items-center justify-between gap-2 min-w-0">
                <span className="truncate">{shortName}</span>
                <span className="text-gray-500 flex-shrink-0 text-[10px] tabular-nums">{formatSize(f.size)}</span>
              </span>
            </button>
          )
        })}
        {filteredFiles.length > 100 && (
          <p className="text-[11px] text-gray-500 text-center py-3 px-2 leading-snug">
            Mostrando 100 de {filteredFiles.length}. Use o filtro acima
            (ex: <span className="font-mono text-gray-400">s04e03</span> ou
            parte do nome) pra achar o resto.
          </p>
        )}
      </div>
    </aside>
  )
}

function audioTrackTitle(a: MediaTrack): string {
  let t = a.title || a.codec
  if (a.channels) t += ` (${a.channels}ch)`
  return `${t} — clicar transcoda via FFmpeg, perde seek`
}

function subBtnClass(active: boolean, image: boolean | undefined): string {
  if (active) {
    return image
      ? 'bg-orange-500/20 text-orange-300 border-orange-500/30'
      : 'bg-emerald-500/20 text-emerald-300 border-emerald-500/30'
  }
  return 'bg-gray-700/40 text-gray-400 border-gray-700 hover:text-gray-200'
}

type EmbeddedTracksPanelProps = {
  readonly probe: StreamProbe
  readonly sidecars: SidecarSubtitle[]
  readonly transcodeAudio: number | null
  readonly forceH264: boolean
  readonly burnSubTrack: number | null
  readonly isTranscoded: boolean
  readonly sidecarIdx: number | null
  readonly embeddedSub: number | null
  readonly clearCustomSub: () => void
  readonly setTranscodeAudio: (v: number | null) => void
  readonly setForceH264: (fn: (prev: boolean) => boolean) => void
  readonly setBurnSubTrack: (v: number | null) => void
  readonly setSidecarIdx: (v: number | null) => void
  readonly setEmbeddedSub: (v: number | null) => void
  readonly setSubActive: (v: string | null) => void
  readonly setAutoSource: (v: 'hash' | 'title' | 'embedded' | null) => void
}

// Embedded tracks panel (audio + subtitles inside the file, plus sidecar .srt
// files shipped alongside the video in the torrent).
function EmbeddedTracksPanel({
  probe,
  sidecars,
  transcodeAudio,
  forceH264,
  burnSubTrack,
  isTranscoded,
  sidecarIdx,
  embeddedSub,
  clearCustomSub,
  setTranscodeAudio,
  setForceH264,
  setBurnSubTrack,
  setSidecarIdx,
  setEmbeddedSub,
  setSubActive,
  setAutoSource,
}: EmbeddedTracksPanelProps) {
  return (
    <div className="px-3 sm:px-4 py-3 border-b border-gray-700 flex flex-col gap-3">
      {/* Audio tracks — clicking a non-default triggers transcoded remux */}
      {probe.audio.length > 1 && (
        <div>
          <p className="text-xs text-gray-500 mb-1.5 flex items-center gap-2">
            <Volume2 className="w-3 h-3" />
            Faixas de áudio ({probe.audio.length})
            {transcodeAudio !== null && (
              <span className="text-[10px] text-purple-300 bg-purple-500/15 border border-purple-500/30 px-1.5 py-0.5 rounded">
                <Cpu className="w-2.5 h-2.5 inline mr-0.5" />GPU encoding
              </span>
            )}
          </p>
          <div className="flex flex-wrap gap-1">
            <button
              onClick={() => setTranscodeAudio(null)}
              className={`text-[11px] px-2 py-1 rounded border transition-colors ${
                transcodeAudio === null
                  ? 'bg-blue-500/20 text-blue-300 border-blue-500/30'
                  : 'bg-gray-800 text-gray-500 border-gray-700 hover:text-gray-300'
              }`}
              title="Faixa padrão do arquivo (direct play, com seek completo)"
            >
              Padrão
            </button>
            {probe.audio.map(a => (
              <button
                key={a.index}
                onClick={() => setTranscodeAudio(a.index)}
                title={audioTrackTitle(a)}
                className={`text-[11px] px-2 py-1 rounded border transition-colors ${(() => {
                  if (transcodeAudio === a.index) return 'bg-purple-500/20 text-purple-300 border-purple-500/30'
                  if (a.default) return 'bg-blue-500/10 text-blue-400 border-blue-500/20 hover:bg-blue-500/20'
                  return 'bg-gray-700/40 text-gray-400 border-gray-700 hover:text-gray-200'
                })()}`}
              >
                {a.language ? a.language.toUpperCase() : '??'}
                <span className="text-gray-500 ml-1">{a.codec}{a.channels ? `·${a.channels}ch` : ''}</span>
                {a.default && <span className="ml-1 text-[9px]">★</span>}
              </button>
            ))}
          </div>
        </div>
      )}

      {/* Force H.264 toggle — useful for HEVC files in Chrome */}
      <div className="flex items-center justify-between gap-2 flex-wrap">
        <button
          onClick={() => setForceH264(v => !v)}
          title="Re-encoda vídeo para H.264 — útil quando o codec original é HEVC e o browser não decodifica"
          className={`flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-lg border transition-colors ${
            forceH264
              ? 'bg-purple-500/20 text-purple-300 border-purple-500/30'
              : 'bg-gray-700/50 text-gray-400 border-gray-700 hover:text-gray-200'
          }`}
        >
          <Cpu className="w-3.5 h-3.5" />
          Forçar H.264
          {forceH264 && <Check className="w-3 h-3" />}
        </button>

        {/* Stream mode indicator */}
        {isTranscoded && (
          <span className="text-[11px] text-yellow-400 flex items-center gap-1">
            <AlertCircle className="w-3 h-3" />
            Stream transcoded — seek limitado
          </span>
        )}
      </div>

      {/* Sidecar subtitles (.srt files alongside the video in the torrent) */}
      {sidecars.length > 0 && (
        <div>
          <p className="text-xs text-gray-500 mb-1.5 flex items-center gap-2">
            <Subtitles className="w-3 h-3" />
            Legendas no torrent ({sidecars.length}) <span className="text-[10px] text-gray-600 italic">— arquivos .srt/.vtt</span>
          </p>
          <div className="flex flex-wrap gap-1">
            <button
              onClick={() => {
                setSidecarIdx(null)
                clearCustomSub()
              }}
              className={`text-[11px] px-2 py-1 rounded border transition-colors ${
                sidecarIdx === null
                  ? 'bg-gray-700 text-gray-200 border-gray-600'
                  : 'bg-gray-800 text-gray-500 border-gray-700 hover:text-gray-300'
              }`}
            >
              Nenhuma
            </button>
            {sidecars.map(s => (
              <button
                key={s.index}
                onClick={() => {
                  setSidecarIdx(s.index)
                  setEmbeddedSub(null)
                  setSubActive(null)
                  setAutoSource('embedded')
                  clearCustomSub()
                }}
                title={s.path}
                className={`text-[11px] px-2 py-1 rounded border transition-colors ${
                  sidecarIdx === s.index
                    ? 'bg-emerald-500/20 text-emerald-300 border-emerald-500/30'
                    : 'bg-gray-700/40 text-gray-400 border-gray-700 hover:text-gray-200'
                }`}
              >
                {(s.language || '??').toUpperCase()}
                <span className="text-gray-500 ml-1">.{s.format}</span>
              </button>
            ))}
          </div>
        </div>
      )}

      {/* Embedded subtitles — pickable (text subs as track, image subs as burn-in) */}
      {probe.subtitles.length > 0 && (
        <div>
          <p className="text-xs text-gray-500 mb-1.5 flex items-center gap-2">
            <Subtitles className="w-3 h-3" />
            Legendas embutidas ({probe.subtitles.length})
            {burnSubTrack !== null && (
              <span className="text-[10px] text-orange-300 bg-orange-500/15 border border-orange-500/30 px-1.5 py-0.5 rounded">
                <Flame className="w-2.5 h-2.5 inline mr-0.5" />Burn-in
              </span>
            )}
          </p>
          <div className="flex flex-wrap gap-1">
            <button
              onClick={() => {
                setEmbeddedSub(null)
                setBurnSubTrack(null)
                clearCustomSub()
              }}
              className={`text-[11px] px-2 py-1 rounded border transition-colors ${
                embeddedSub === null && burnSubTrack === null
                  ? 'bg-gray-700 text-gray-200 border-gray-600'
                  : 'bg-gray-850 text-gray-500 border-gray-700 hover:text-gray-300'
              }`}
            >
              Nenhuma
            </button>
            {probe.subtitles.map(s => {
              const isActive = embeddedSub === s.index || burnSubTrack === s.index
              return (
                <button
                  key={s.index}
                  onClick={() => {
                    clearCustomSub()
                    if (s.image) {
                      // Image sub → burn-in (forces video re-encode)
                      setBurnSubTrack(s.index)
                      setEmbeddedSub(null)
                    } else {
                      // Text sub → extract as VTT
                      setEmbeddedSub(s.index)
                      setBurnSubTrack(null)
                      setSubActive(null)
                      setAutoSource('embedded')
                    }
                  }}
                  title={
                    s.image
                      ? `${s.codec} (imagem) — burn-in via FFmpeg, vai forçar transcode do vídeo`
                      : s.title || s.codec
                  }
                  className={`text-[11px] px-2 py-1 rounded border transition-colors ${subBtnClass(isActive, s.image)}`}
                >
                  {s.language ? s.language.toUpperCase() : '??'}
                  <span className="text-gray-500 ml-1">{s.codec}</span>
                  {s.forced && <span className="ml-1 text-[9px] text-yellow-400">FORCED</span>}
                  {s.image && <span className="ml-1 text-[9px] text-orange-400">IMG</span>}
                </button>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}

// Slim time readout shown below the cover art when an audio track plays in the
// minimized (PiP) card, so the user knows where they are without expanding.
function MinimizedAudioProgress({ currentTime, duration, formatTime }: {
  readonly currentTime: number
  readonly duration: number
  readonly formatTime: (s: number) => string
}) {
  const pct = duration > 0 ? `${(currentTime / duration) * 100}%` : '0%'
  return (
    <div className="px-3 py-1.5 bg-gray-900 border-t border-gray-700 flex items-center gap-2 text-xs text-gray-400">
      <span className="font-mono tabular-nums">{formatTime(currentTime)}</span>
      <div className="flex-1 h-1 bg-gray-700 rounded-full overflow-hidden">
        <div className="h-full bg-purple-500 rounded-full transition-all" style={{ width: pct }} />
      </div>
      <span className="font-mono tabular-nums">{formatTime(duration)}</span>
    </div>
  )
}

function subtitleButtonTitle(enabled: boolean, source: string | null): string {
  if (!enabled) return 'Configure OpenSubtitles API key em Settings'
  if (source === 'embedded') return 'Legenda embutida no arquivo (sync perfeito)'
  if (source === 'hash') return 'Legenda casada por hash do arquivo (frame-exato)'
  if (source === 'title') return 'Legenda encontrada pelo título'
  return 'Buscar legendas em português'
}

function subtitleBtnClass(active: string | null, embedded: number | null, source: string | null, enabled: boolean): string {
  if (active || embedded !== null) {
    if (source === 'embedded' || source === 'hash') return 'bg-emerald-500/20 text-emerald-400 border-emerald-500/30'
    return 'bg-green-500/20 text-green-400 border-green-500/30'
  }
  if (enabled) return 'bg-blue-500/20 hover:bg-blue-500/30 text-blue-300 border-blue-500/30'
  return 'bg-gray-700/50 text-gray-500 border-gray-700 cursor-not-allowed opacity-50'
}

function serverDownloadIcon(loading: boolean, success: boolean): React.ReactNode {
  if (loading) return <Loader2 className="w-3.5 h-3.5 animate-spin" />
  if (success) return <Check className="w-3.5 h-3.5" />
  return <Download className="w-3.5 h-3.5 text-green-400" />
}

type PlayerControlsPanelProps = {
  readonly info: TorrentInfo
  readonly currentFile: TorrentInfo['files'][number] | null | undefined
  readonly videoFileIndices: number[]
  readonly videoCursor: number
  readonly prevVideoIdx: number
  readonly nextVideoIdx: number
  readonly currentEp: string | null
  readonly currentTime: number
  readonly duration: number
  readonly bufferedEnd: number
  readonly bufferedRanges: Array<[number, number]>
  readonly subActive: string | null
  readonly subOffset: number
  readonly showMobileOpts: boolean
  readonly playbackSpeed: number
  readonly probe: StreamProbe | null
  readonly sidecars: SidecarSubtitle[]
  readonly transcodeAudio: number | null
  readonly forceH264: boolean
  readonly burnSubTrack: number | null
  readonly isTranscoded: boolean
  readonly sidecarIdx: number | null
  readonly embeddedSub: number | null
  readonly subEnabled: boolean
  readonly autoSource: 'hash' | 'title' | 'embedded' | null
  readonly subLoading: boolean
  readonly subtitleLabel: string
  readonly vlcURL: string
  readonly streamURL: string
  readonly serverDownloadLoading: boolean
  readonly serverDownloadSuccess: boolean
  readonly subOpen: boolean
  readonly customSubName: string | null
  readonly subError: string
  readonly subResults: Subtitle[]
  readonly formatTime: (s: number) => string
  readonly playFile: (idx: number) => void
  readonly adjustSubOffset: (delta: number) => void
  readonly resetSubOffset: () => void
  readonly setShowMobileOpts: (fn: (prev: boolean) => boolean) => void
  readonly setPlaybackSpeed: (v: number) => void
  readonly clearCustomSub: () => void
  readonly setTranscodeAudio: (v: number | null) => void
  readonly setForceH264: (fn: (prev: boolean) => boolean) => void
  readonly setBurnSubTrack: (v: number | null) => void
  readonly setSidecarIdx: (v: number | null) => void
  readonly setEmbeddedSub: (v: number | null) => void
  readonly setSubActive: (v: string | null) => void
  readonly setAutoSource: (v: 'hash' | 'title' | 'embedded' | null) => void
  readonly openSubtitlePanel: () => void
  readonly handleRequestFullscreen: () => void
  readonly handleServerDownload: () => void
  readonly setSubOpen: (v: boolean) => void
  readonly handleCustomSubtitleUpload: (e: React.ChangeEvent<HTMLInputElement>) => void
  readonly pickSubtitle: (s: Subtitle) => void
}

// Everything below the <video> when expanded: transport row (series nav + time
// + subtitle offset), the mobile "Opções" collapse, the status/buffer bar, the
// embedded-tracks panel, the action bar (subtitle/VLC/download), and the
// OpenSubtitles picker. Hidden entirely while minimized.
function PlayerControlsPanel({
  info,
  currentFile,
  videoFileIndices,
  videoCursor,
  prevVideoIdx,
  nextVideoIdx,
  currentEp,
  currentTime,
  duration,
  bufferedEnd,
  bufferedRanges,
  subActive,
  subOffset,
  showMobileOpts,
  playbackSpeed,
  probe,
  sidecars,
  transcodeAudio,
  forceH264,
  burnSubTrack,
  isTranscoded,
  sidecarIdx,
  embeddedSub,
  subEnabled,
  autoSource,
  subLoading,
  subtitleLabel,
  vlcURL,
  streamURL,
  serverDownloadLoading,
  serverDownloadSuccess,
  subOpen,
  customSubName,
  subError,
  subResults,
  formatTime,
  playFile,
  adjustSubOffset,
  resetSubOffset,
  setShowMobileOpts,
  setPlaybackSpeed,
  clearCustomSub,
  setTranscodeAudio,
  setForceH264,
  setBurnSubTrack,
  setSidecarIdx,
  setEmbeddedSub,
  setSubActive,
  setAutoSource,
  openSubtitlePanel,
  handleRequestFullscreen,
  handleServerDownload,
  setSubOpen,
  handleCustomSubtitleUpload,
  pickSubtitle,
}: PlayerControlsPanelProps) {
  return (
    <>
      {/* Transport row — ONE line. The native <video controls> already
          provides the seek bar, play/pause and ±skip, so we keep only
          what it lacks: series navigation (prev/next episode) and a time
          readout. "Back to start" / "resume" are now offered as a prompt
          on play (see resume overlay); ±10s removed (native bar seeks). */}
      <div className="px-3 sm:px-4 py-2 bg-gray-900 border-b border-gray-700 flex items-center gap-2 min-w-0">
        {videoFileIndices.length > 1 && (
          <>
            <button
              onClick={() => playFile(prevVideoIdx)}
              disabled={prevVideoIdx < 0}
              title="Episódio anterior"
              className="flex items-center gap-1 text-sm sm:text-xs bg-blue-500/20 hover:bg-blue-500/30 text-blue-300 border border-blue-500/30 px-3 sm:px-2 py-2 sm:py-1.5 min-h-[44px] sm:min-h-0 rounded-lg transition-colors disabled:opacity-30 flex-shrink-0"
            >
              <ChevronLeft className="w-4 h-4 sm:w-3.5 sm:h-3.5" />
              <span className="hidden xs:inline">Ep ant.</span>
            </button>
            <button
              onClick={() => playFile(nextVideoIdx)}
              disabled={nextVideoIdx < 0}
              title="Próximo episódio"
              className="flex items-center gap-1 text-sm sm:text-xs bg-blue-500/20 hover:bg-blue-500/30 text-blue-300 border border-blue-500/30 px-3 sm:px-2 py-2 sm:py-1.5 min-h-[44px] sm:min-h-0 rounded-lg transition-colors disabled:opacity-30 flex-shrink-0"
            >
              <span className="hidden xs:inline">Próx.</span>
              <ChevronRight className="w-4 h-4 sm:w-3.5 sm:h-3.5" />
            </button>
            {currentEp && (
              <span className="text-xs text-blue-300 px-2 py-1 bg-blue-500/10 rounded border border-blue-500/20 font-mono flex-shrink-0">
                {currentEp}
              </span>
            )}
            <span className="text-xs text-gray-500 flex-shrink-0">
              {videoCursor + 1}/{videoFileIndices.length}
            </span>
          </>
        )}
        <span className="text-xs text-gray-400 ml-auto font-mono tabular-nums flex-shrink-0">
          {formatTime(currentTime)} <span className="text-gray-600">/</span> {formatTime(duration)}
        </span>

        {/* Subtitle offset controls — only visible when sub active */}
        {subActive && (
          <div className="flex items-center gap-1 ml-auto bg-gray-800 border border-gray-700 rounded-lg px-2 py-0.5">
            <span className="text-[10px] text-gray-500 uppercase tracking-wide mr-1">Legenda</span>
            <button
              onClick={() => adjustSubOffset(-0.1)}
              title="Atrasar legenda em 0.1s"
              className="text-gray-400 hover:text-blue-400 p-1 transition-colors"
            >
              <Minus className="w-3 h-3" />
            </button>
            <span className="text-xs text-gray-200 font-mono tabular-nums min-w-[40px] text-center">
              {subOffset >= 0 ? '+' : ''}{subOffset.toFixed(1)}s
            </span>
            <button
              onClick={() => adjustSubOffset(0.1)}
              title="Adiantar legenda em 0.1s"
              className="text-gray-400 hover:text-blue-400 p-1 transition-colors"
            >
              <Plus className="w-3 h-3" />
            </button>
            {subOffset !== 0 && (
              <button
                onClick={resetSubOffset}
                title="Resetar offset"
                className="text-gray-500 hover:text-gray-200 p-1 transition-colors"
              >
                <RotateCcw className="w-3 h-3" />
              </button>
            )}
          </div>
        )}
      </div>

      {/* Mobile-only toggle that collapses everything below (status,
          transcode controls, subtitle picker, VLC/download) so the file
          list sits right under the video. Desktop shows it all inline. */}
      <button
        onClick={() => setShowMobileOpts(v => !v)}
        className="sm:hidden flex items-center justify-center gap-1.5 w-full px-4 py-2.5 border-b border-gray-700 bg-gray-900/40 text-gray-300 text-sm active:bg-gray-800"
      >
        {showMobileOpts ? <ChevronDown className="w-4 h-4" /> : <ChevronRight className="w-4 h-4" />}
        {showMobileOpts ? 'Ocultar opções' : 'Opções (legendas · status · baixar)'}
      </button>

      {/* Secondary controls — collapsed on mobile unless toggled, always
          shown on desktop. */}
      <div className={showMobileOpts ? 'flex flex-col' : 'hidden sm:flex sm:flex-col'}>
        {/* Status bar with buffer + torrent progress. `relative` lets the
            hover preview bubble (absolute) anchor inside this container. */}
        <div className="relative px-3 sm:px-4 py-3 bg-gray-900/50 border-b border-gray-700 flex flex-col gap-2 text-xs">
          <div className="flex items-center gap-3 flex-wrap">
            <span className="flex items-center gap-1.5 text-gray-300">
              <Users className="w-3.5 h-3.5 text-green-400" />
              {info.seeders} <span className="text-gray-500 hidden sm:inline">seeders</span>
              <span className="text-gray-500">/</span> {info.peers} <span className="text-gray-500 hidden sm:inline">peers</span>
            </span>
            <span className="flex items-center gap-1.5 text-gray-300">
              <Activity className="w-3.5 h-3.5 text-blue-400" />
              {(info.progress * 100).toFixed(1)}%<span className="text-gray-500 hidden sm:inline ml-1">torrent</span>
            </span>
            <span className="flex items-center gap-1.5 text-gray-300 tabular-nums">
              <span className="text-green-400">↓</span> {formatRate(info.downRate)}
              <span className="text-yellow-400 ml-1">↑</span> {formatRate(info.upRate)}
            </span>
            <label className="flex items-center gap-1 text-gray-400" title="Velocidade de reprodução (pitch preservado — voz não fica robotizada)">
              <FastForward className="w-3.5 h-3.5 text-gray-500" />
              <select
                value={playbackSpeed}
                onChange={e => setPlaybackSpeed(Number.parseFloat(e.target.value))}
                className="bg-gray-800 border border-gray-700 rounded px-1 py-0.5 text-xs text-gray-200 tabular-nums focus:outline-none focus:border-green-500"
              >
                {SPEED_OPTIONS.map(s => (
                  <option key={s} value={s}>{s}x</option>
                ))}
              </select>
            </label>
            {currentFile && (
              <span className="text-gray-400">
                {formatSize(currentFile.downloaded)} / {formatSize(currentFile.size)}
              </span>
            )}
            {bufferedEnd > 0 && duration > 0 && (
              <span className="text-gray-400 ml-auto">
                Buffer: <span className="text-blue-400">{formatTime(bufferedEnd - currentTime)}</span> à frente
              </span>
            )}
          </div>
          {/* Load/buffer indicator — PRESENTATION ONLY (not clickable).
              The native <video controls> bar owns seeking; this strip just
              visualises state so it doesn't compete with it: gray = torrent
              downloaded, blue islands = buffered/ready (disjoint after a #61
              seek-restart, gaps = not loaded yet), green = play progress. */}
          <div className="relative bg-gray-700 rounded-full h-1.5">
            <div
              className="absolute inset-y-0 left-0 bg-gray-500 rounded-full"
              style={{ width: `${(currentFile?.progress || 0) * 100}%` }}
            />
            {duration > 0 && (
              <>
                {bufferedRanges.map(([start, end]) => (
                  <div
                    key={start}
                    className="absolute inset-y-0 bg-blue-500/50 rounded-full"
                    style={{
                      left: `${(start / duration) * 100}%`,
                      width: `${(Math.max(0, end - start) / duration) * 100}%`,
                    }}
                  />
                ))}
                <div
                  className="absolute inset-y-0 left-0 bg-green-500 rounded-full"
                  style={{ width: `${(currentTime / duration) * 100}%` }}
                />
              </>
            )}
          </div>
        </div>

        {/* Embedded tracks (audio + subtitles inside the file) */}
        {probe && (probe.audio.length > 0 || probe.subtitles.length > 0) && (
          <EmbeddedTracksPanel
            probe={probe}
            sidecars={sidecars}
            transcodeAudio={transcodeAudio}
            forceH264={forceH264}
            burnSubTrack={burnSubTrack}
            isTranscoded={isTranscoded}
            sidecarIdx={sidecarIdx}
            embeddedSub={embeddedSub}
            clearCustomSub={clearCustomSub}
            setTranscodeAudio={setTranscodeAudio}
            setForceH264={setForceH264}
            setBurnSubTrack={setBurnSubTrack}
            setSidecarIdx={setSidecarIdx}
            setEmbeddedSub={setEmbeddedSub}
            setSubActive={setSubActive}
            setAutoSource={setAutoSource}
          />
        )}

        {/* Action bar */}
        <div className="px-3 sm:px-4 py-3 flex items-center gap-2 flex-wrap">
          <button
            onClick={openSubtitlePanel}
            disabled={!subEnabled}
            title={subtitleButtonTitle(subEnabled, autoSource)}
            className={`flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-lg transition-colors border ${subtitleBtnClass(subActive, embeddedSub, autoSource, subEnabled)}`}
          >
            {subLoading ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Subtitles className="w-3.5 h-3.5" />}
            {subtitleLabel}
          </button>
          <button
            onClick={handleRequestFullscreen}
            title="Tela cheia"
            className="flex items-center gap-1.5 text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 px-3 py-1.5 rounded-lg transition-colors sm:hidden"
          >
            <Maximize2 className="w-3.5 h-3.5" />
            Fullscreen
          </button>
          <a
            href={vlcURL}
            className="flex items-center gap-1.5 text-xs bg-orange-500/20 hover:bg-orange-500/30 text-orange-300 border border-orange-500/30 px-3 py-1.5 rounded-lg transition-colors"
            title="Abrir o stream no app VLC local — funciona com qualquer codec"
          >
            <ExternalLink className="w-3.5 h-3.5" />
            VLC
          </a>
          <button
            onClick={handleServerDownload}
            disabled={serverDownloadLoading || serverDownloadSuccess}
            className={`flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-lg transition-colors border ${
              serverDownloadSuccess
                ? 'bg-emerald-500/20 text-emerald-400 border-emerald-500/30'
                : 'bg-green-500/20 hover:bg-green-500/30 text-green-300 border-green-500/30'
            }`}
            title="Salvar download completo no servidor (Background Download)"
          >
            {serverDownloadIcon(serverDownloadLoading, serverDownloadSuccess)}
            <span>
              {serverDownloadSuccess ? 'Adicionado!' : 'Baixar no Servidor'}
            </span>
          </button>
          <a
            href={streamURL}
            download
            className="flex items-center gap-1.5 text-xs bg-gray-700 hover:bg-gray-600 text-gray-300 px-3 py-1.5 rounded-lg transition-colors"
          >
            <Download className="w-3.5 h-3.5" />
            <span className="hidden sm:inline">Baixar direto</span>
            <span className="sm:hidden">Baixar</span>
          </a>
          <span className="text-xs text-gray-600 ml-auto hidden sm:block">
            {info.files.length} arquivo{info.files.length === 1 ? '' : 's'} • {formatSize(info.totalSize)}
          </span>
        </div>

        {/* Subtitle picker panel */}
        {subOpen && (
          <div className="px-3 sm:px-4 pb-4 border-t border-gray-700 pt-3">
            <div className="flex items-center justify-between mb-2">
              <h3 className="text-sm font-medium text-gray-200 flex items-center gap-2">
                <Subtitles className="w-4 h-4 text-blue-400" />
                Legendas (pt-BR / pt)
              </h3>
              <button onClick={() => setSubOpen(false)} className="text-gray-500 hover:text-gray-300">
                <X className="w-4 h-4" />
              </button>
            </div>

            {/* Carregar Legenda Local */}
            <div className="mb-3 pb-3 border-b border-gray-700/50 flex flex-col gap-2">
              <div>
                <label className="inline-flex items-center gap-1.5 text-xs bg-gray-700 hover:bg-gray-600 text-gray-200 px-3 py-1.5 rounded-lg cursor-pointer transition-colors border border-gray-600">
                  <Upload className="w-3.5 h-3.5" />
                  <span>Carregar Legenda Local (.srt/.vtt)</span>
                  <input
                    type="file"
                    accept=".srt,.vtt"
                    onChange={handleCustomSubtitleUpload}
                    className="hidden"
                  />
                </label>
              </div>
              {customSubName && (
                <div className="flex items-center gap-1.5 text-xs text-green-400 bg-green-500/10 border border-green-500/20 px-2.5 py-1.5 rounded-lg">
                  <Check className="w-3.5 h-3.5 flex-shrink-0" />
                  <span className="truncate flex-1">Ativa: {customSubName}</span>
                  <button
                    onClick={clearCustomSub}
                    className="text-gray-400 hover:text-red-400 font-bold ml-1 p-0.5"
                    title="Remover legenda"
                  >
                    <X className="w-3.5 h-3.5" />
                  </button>
                </div>
              )}
            </div>
            {subLoading && (
              <div className="flex items-center gap-2 text-sm text-gray-400 py-2">
                <Loader2 className="w-4 h-4 animate-spin" />
                Buscando no OpenSubtitles...
              </div>
            )}
            {subError && (
              <p className="text-xs text-red-400 py-2">{subError}</p>
            )}
            {!subLoading && !subError && subResults.length === 0 && (
              <p className="text-xs text-gray-500 py-2">Nenhuma legenda encontrada</p>
            )}
            {subResults.length > 0 && (
              <div className="flex flex-col gap-1 max-h-48 overflow-y-auto">
                {subResults.map(s => (
                  <button
                    key={s.id}
                    onClick={() => pickSubtitle(s)}
                    className={`flex items-center justify-between gap-2 px-3 py-2 rounded-lg text-xs text-left transition-colors ${
                      subActive === s.id
                        ? 'bg-green-500/20 text-green-400 border border-green-500/30'
                        : 'bg-gray-900/50 hover:bg-gray-900 text-gray-300 border border-transparent'
                    }`}
                  >
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="font-mono uppercase text-[10px] bg-gray-700 px-1.5 py-0.5 rounded">
                          {s.language}
                        </span>
                        <span className="truncate">{s.release || '(sem release name)'}</span>
                        {s.trusted && <span className="text-green-400 text-[10px]">✓ trusted</span>}
                        {s.hearingImpaired && <span className="text-yellow-400 text-[10px]">[HI]</span>}
                      </div>
                      <div className="text-[10px] text-gray-500 mt-0.5">
                        {s.uploaderName} • {s.downloads.toLocaleString()} downloads
                      </div>
                    </div>
                    {subActive === s.id && <Check className="w-4 h-4 flex-shrink-0" />}
                  </button>
                ))}
              </div>
            )}
            {subActive && (
              <button
                onClick={() => setSubActive(null)}
                className="mt-2 text-xs text-gray-500 hover:text-red-400 transition-colors flex items-center gap-1"
              >
                <X className="w-3 h-3" />
                Remover legenda
              </button>
            )}
          </div>
        )}
      </div>
    </>
  )
}

export default function PlayerModal({
  result,
  onClose,
  initialFileIndex,
  initialSeek,
  playlist = null,
  onPlaylistAdvance,
  onPlaylistPrevious,
  repeat = 'none',
  shuffle = false,
  onCycleRepeat,
  onToggleShuffle,
  onPrefetchNextPlaylist,
  onPrefetchNextNextPlaylist,
  startMinimized = false,
  audioMode = false,
}: PlayerModalProps) {
  const [info, setInfo] = useState<TorrentInfo | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [selectedFile, setSelectedFile] = useState<number>(-1)
  // Scrolls the file list to the currently-playing file when the picker opens —
  // a season pack reopened at episode 20 lands on it instead of at the top.
  const selectedFileRef = useRef<HTMLButtonElement>(null)
  const [videoError, setVideoError] = useState(false)
  // Minimized (picture-in-picture-style) mode. The <video> element stays
  // mounted in the same DOM position — only the surrounding container shrinks
  // to a floating card and the heavy panels hide. This unifies the modal and
  // the old AudioBar into one player: audio opens minimized, video opens full,
  // and either can toggle. Since PlayerProvider lives above the router, the
  // player keeps playing across page navigation in both modes.
  const [minimized, setMinimized] = useState(startMinimized)
  // Lock background scroll while the player is full-screen; allow it when
  // minimized (PiP) so the user can still browse the page behind the card.
  useScrollLock(!minimized)
  // Mobile: secondary controls (status, transcode, subtitle, VLC/download) are
  // collapsed behind an "Opções" toggle so the video + file list get the space
  // — the Plex/Stremio pattern. Desktop (sm+) always shows them inline.
  const [showMobileOpts, setShowMobileOpts] = useState(false)
  // Subtitles
  const [subEnabled, setSubEnabled] = useState(false)
  const [subOpen, setSubOpen] = useState(false)
  const [subResults, setSubResults] = useState<Subtitle[]>([])
  const [subLoading, setSubLoading] = useState(false)
  const [subError, setSubError] = useState('')
  const [subActive, setSubActive] = useState<string | null>(null)
  const [subOffset, setSubOffset] = useState(0) // seconds; +/-0.1s steps
  // True once we've restored (or decided there's nothing to restore) the saved
  // subtitle choice for the current file. Gates the save effect so the reset on
  // file-switch doesn't persist an empty choice before restore runs.
  const [subRestored, setSubRestored] = useState(false)
  const [autoSource, setAutoSource] = useState<'hash' | 'title' | 'embedded' | null>(null)
  // Embedded tracks discovered via ffprobe
  const [probe, setProbe] = useState<StreamProbe | null>(null)
  const [embeddedSub, setEmbeddedSub] = useState<number | null>(null) // selected embedded sub track index
  const [customSubURL, setCustomSubURL] = useState<string | null>(null)
  const [customSubName, setCustomSubName] = useState<string | null>(null)

  useEffect(() => {
    return () => {
      setCustomSubURL(prev => {
        if (prev) URL.revokeObjectURL(prev)
        return null
      })
    }
  }, [])

  const handleCustomSubtitleUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return

    let text: string
    try {
      text = await file.text()
    } catch {
      alert('Erro ao ler o arquivo de legenda.')
      return
    }

    const vttContent = file.name.endsWith('.srt')
      ? 'WEBVTT\n\n' + text.replaceAll(/(\d{2}:\d{2}:\d{2}),(\d{3})/g, '$1.$2')
      : text

    setCustomSubURL(prev => {
      if (prev) URL.revokeObjectURL(prev)
      return null
    })

    const blob = new Blob([vttContent], { type: 'text/vtt' })
    const url = URL.createObjectURL(blob)
    setCustomSubURL(url)
    setCustomSubName(file.name)

    setSubActive(null)
    setSidecarIdx(null)
    setEmbeddedSub(null)
    setAutoSource(null)
  }

  // Drop any uploaded custom subtitle (revoking its blob URL) when the user
  // switches to an embedded/sidecar/external track instead.
  const clearCustomSub = () => {
    setCustomSubURL(prev => { if (prev) { URL.revokeObjectURL(prev) } return null })
    setCustomSubName(null)
  }

  // Sidecar subtitle files (separate .srt/.vtt inside the torrent)
  const [sidecars, setSidecars] = useState<SidecarSubtitle[]>([])
  const [sidecarIdx, setSidecarIdx] = useState<number | null>(null) // selected sidecar file index

  // Library entry for this torrent — used for resume seek + saving position
  const [libraryEntryID, setLibraryEntryID] = useState<number | null>(null)
  const [resumePosition, setResumePosition] = useState<number | null>(null)
  // When a saved resume point exists, ask on play whether to continue or
  // restart (instead of auto-seeking silently / a permanent "back to start"
  // button). Shown as an overlay over the video.
  const [showResumePrompt, setShowResumePrompt] = useState(false)
  const lastResumeSaveRef = useRef(0)
  const lastUrlSyncRef = useRef(0)
  const bufferRetryRef = useRef(0)

  const loadLibraryEntry = (list: LibraryEntry[], infoHash: string) => {
    const entry = list.find(e => e.infoHash === infoHash)
    if (entry) {
      setLibraryEntryID(entry.id)
      if (entry.resumeSeconds > 30 && entry.durationSeconds > 0 && entry.resumeSeconds < entry.durationSeconds - 30) {
        setResumePosition(entry.resumeSeconds)
      }
    }
  }

  // Sidebar (file list) open/closed state. On lg+ screens the file picker
  // renders as a right column instead of a stacked panel below the video.
  // Persisted to localStorage so the user's choice survives between modals.
  const [sidebarOpen, setSidebarOpen] = useState<boolean>(() => {
    const stored = localStorage.getItem('jackui.playerSidebar')
    return stored === null ? true : stored === '1'
  })
  useEffect(() => {
    localStorage.setItem('jackui.playerSidebar', sidebarOpen ? '1' : '0')
  }, [sidebarOpen])
  // Incognito mode: synced with the global toggle in the navbar (localStorage).
  // When incognito is ON, the backend still records history/library but marks
  // entries as incognito=1; they are cleaned up when incognito is disabled or
  // the user logs out.
  const [incognito, setIncognito] = useIncognito()

  // serverReady — flips true the moment streamAdd resolves and the streamer has
  // actually loaded the torrent. The metadata cache lets us populate `info`
  // (file list, primaryFile) instantly from disk, but the <video src> can't
  // start fetching pieces until the streamer has the torrent in its `active`
  // map — otherwise /api/stream/HASH/IDX returns 404 and the browser fires a
  // misleading "format not supported" error before swarm bootstrap completes.
  const [serverReady, setServerReady] = useState(false)
  // Media token: JWT scope="media" com TTL longo (6h backend). Buscado uma vez
  // ao abrir o player e usado em TODAS as URLs de mídia (<video src>,
  // <track src>, <img cover>). Se usássemos o access token regular (15min),
  // o refresh em background trocaria a query string e o <video> resetaria
  // playback pra 0. URLs ficam vazias até o token chegar — gate equivalente
  // ao serverReady abaixo, garantindo que o <src> só é setado uma vez.
  const [mediaToken, setMediaToken] = useState('')
  // Frozen snapshot of the diagnostic at the moment onVideoError fired. Used by
  // the error UI which re-renders AFTER the <video> element unmounted, so by
  // then videoRef.current is null and a live diagnostic would come back empty.
  const [lastErrorDiag, setLastErrorDiag] = useState<Record<string, unknown> | null>(null)
  // Inline preview for non-playable files (txt/srt/nfo/pdf/jpg/etc).
  // Storing the file index lets us look up path + size from `info.files`
  // on render without duplicating state when the user reopens the player.
  const [previewFileIdx, setPreviewFileIdx] = useState<number | null>(null)

  // Server-side background download state
  const [serverDownloadLoading, setServerDownloadLoading] = useState(false)
  const [serverDownloadSuccess, setServerDownloadSuccess] = useState(false)

  const handleServerDownload = async () => {
    if (!info || selectedFile < 0) return
    setServerDownloadLoading(true)
    try {
      const magnet = result?.magnetUri || `magnet:?xt=urn:btih:${info.infoHash}`
      const file = info.files[selectedFile]
      await downloadCreate({
        infoHash: info.infoHash,
        fileIndex: selectedFile,
        magnet,
        name: file.path.split('/').pop() || info.name,
        filePath: file.path,
        fileSize: file.size,
      })
      setServerDownloadSuccess(true)
      setTimeout(() => setServerDownloadSuccess(false), 3000)
    } catch (err: any) {
      alert(err.message || 'Erro ao iniciar download no servidor')
    } finally {
      setServerDownloadLoading(false)
    }
  }
  // Transcoding options — any non-null value triggers `/api/stream/transcode` instead of raw stream
  const [transcodeAudio, setTranscodeAudio] = useState<number | null>(null)
  const [forceH264, setForceH264] = useState(false)
  const [burnSubTrack, setBurnSubTrack] = useState<number | null>(null)
  // HEVC auto-fallback: on first <video> error, if a GPU encoder is available, retry via transcode.
  // The "Attempted" flag prevents an infinite loop if the transcoded stream also errors.
  const [transcodeFallbackAttempted, setTranscodeFallbackAttempted] = useState(false)
  const [caps, setCaps] = useState<TranscodeCapabilities | null>(null)
  // Variable playback speed for audiobooks / lectures. We persist this in
  // localStorage so it survives modal close and across sessions. We rely on
  // the browser's built-in pitch-preservation (preservesPitch / webkitPreservesPitch)
  // so 1.5x/2x doesn't sound chipmunked.
  const [playbackSpeed, setPlaybackSpeed] = useState<number>(() => {
    const stored = Number.parseFloat(localStorage.getItem('jackui.playbackSpeed') || '1')
    return Number.isFinite(stored) && stored > 0 ? stored : 1
  })
  // File list filter — for series packs with 30+ episodes the list pushes the
  // settings off-screen. The filter keeps the list short so settings stay reachable.
  const [fileFilter, setFileFilter] = useState('')
  const [fileTypeFilter, setFileTypeFilter] = useState<FileType>('all')
  const [fileSortBySize, setFileSortBySize] = useState(false)
  const [fileSizeDesc, setFileSizeDesc] = useState(true)
  const hoverThumb = useHoverThumb()

  // Favorites — auto-mark after 5min of actual playback (currentTime accumulates)
  const [isFavorite, setIsFavorite] = useState(false)
  const watchedRef = useRef(0)            // accumulated playback time (seconds)
  const lastTickRef = useRef<number>(0)   // last currentTime sample (for delta)
  const AUTO_FAV_THRESHOLD = 5 * 60       // 5 minutes
  // Playback state
  const [currentTime, setCurrentTime] = useState(0)
  const [duration, setDuration] = useState(0)
  const [bufferedEnd, setBufferedEnd] = useState(0)
  // All buffered TimeRanges as [start, end] pairs (seconds). With #61 seek-
  // restart the player holds several disjoint islands (one around each visited
  // position), so the bar renders each as its own "loaded" segment rather than
  // a single left-anchored fill.
  const [bufferedRanges, setBufferedRanges] = useState<Array<[number, number]>>([])
  const videoRef = useRef<HTMLVideoElement>(null)
  const pollRef = useRef<number | null>(null)
  // Prefetch fire-once flags. Reset whenever the underlying selected file
  // changes so re-watching a playlist item or switching files starts fresh.
  const prefetchedNextEpRef = useRef(false)
  const prefetchedPlaylistN1Ref = useRef(false)
  const prefetchedPlaylistN2Ref = useRef(false)
  // Store the original (un-offset) cue timings the first time we see them
  const origCuesRef = useRef<{ start: number; end: number }[]>([])

  // Pede um media token (JWT TTL longo, scope="media") ao abrir o player.
  // Necessário ANTES de montar o <video src> pra que a URL não troque depois
  // (o que faria o browser interpretar como mídia nova e resetar pra 0).
  // Refresh do access token regular em background não afeta este — só vai
  // expirar depois da sessão de playback inteira (6h default).
  useEffect(() => {
    if (!result) return
    let cancelled = false
    fetchMediaToken()
      .then(t => { if (!cancelled) setMediaToken(t) })
      .catch(() => {}) // fallback: streamURL fica vazio, UI mostra "carregando"
    return () => { cancelled = true }
  }, [result?.infoHash])

  // Add the torrent when modal opens
  useEffect(() => {
    if (!result || !pickTorrentSource(result)) return

    setLoading(true)
    setError('')
    setInfo(null)
    setSelectedFile(-1)
    setVideoError(false)
    setSubActive(null)
    setSubResults([])
    setSubError('')
    setSubOpen(false)
    setSubOffset(0)
    setAutoSource(null)
    setProbe(null)
    setEmbeddedSub(null)
    setSidecars([])
    setSubRestored(false)
    setSidecarIdx(null)
    setLibraryEntryID(null)
    setResumePosition(null)
    lastResumeSaveRef.current = 0
    setServerReady(false)
    setTranscodeAudio(null)
    setForceH264(false)
    setBurnSubTrack(null)
    setCustomSubURL(prev => {
      if (prev) URL.revokeObjectURL(prev)
      return null
    })
    setCustomSubName(null)
    setTranscodeFallbackAttempted(false)
    prefetchedNextEpRef.current = false
    prefetchedPlaylistN1Ref.current = false
    prefetchedPlaylistN2Ref.current = false
    bufferRetryRef.current = 0
    setFileFilter('')
    setFileTypeFilter('all')
    setFileSortBySize(false)
    origCuesRef.current = []
    setCurrentTime(0)
    setDuration(0)
    setBufferedEnd(0)
    setBufferedRanges([])

    // Try the cached metadata first — if the server has seen this hash before,
    // the file list + name appear instantly. streamAdd still kicks off in
    // parallel to actually load the torrent client (required for playback).
    if (result.infoHash) {
      streamMetadata(result.infoHash).then(cached => {
        if (cached && !info) {
          setInfo(cached)
          setSelectedFile(chooseInitialFile(cached, initialFileIndex))
        }
      })
    }

    streamAdd(pickTorrentSource(result))
      .then(t => {
        setInfo(t)
        setSelectedFile(chooseInitialFile(t, initialFileIndex))
        // Streamer now has the torrent active — unblock <video src>.
        setServerReady(true)
      })
      .catch(err => setError(err?.response?.data?.error || err.message || 'Falha ao iniciar stream'))
      .finally(() => setLoading(false))

    // Check whether subtitles backend is configured
    subtitlesEnabled().then(setSubEnabled).catch(() => setSubEnabled(false))
    // Cache transcode capabilities once per modal — used by HEVC auto-fallback decision.
    if (!caps) {
      transcodeCapabilities().then(setCaps).catch(() => setCaps(null))
    }
  }, [result])

  // Diagnostic snapshot helper. Returns a plain object with the MediaError code,
  // network state, ready state, current src and user-agent details — everything
  // needed to debug "format not supported" reports without back-and-forth.
  // Codes per HTML spec:
  //   MediaError.code: 1=ABORTED, 2=NETWORK, 3=DECODE, 4=SRC_NOT_SUPPORTED
  //   networkState:    0=EMPTY, 1=IDLE,  2=LOADING, 3=NO_SOURCE
  //   readyState:      0=NOTHING, 1=METADATA, 2=CURRENT, 3=FUTURE, 4=ENOUGH
  const videoDiagnostic = () => {
    const v = videoRef.current
    if (!v) return { reason: 'no video element' }
    return {
      errorCode: v.error?.code,
      errorMsg: v.error?.message,
      networkState: v.networkState,
      readyState: v.readyState,
      currentSrc: v.currentSrc,
      duration: v.duration,
      currentTime: v.currentTime,
      buffered: v.buffered.length > 0 ? `${v.buffered.start(0)}-${v.buffered.end(0)}` : 'empty',
      forceH264,
      transcodeFallbackAttempted,
      isTranscoded,
      caps: caps ? { nvidia: caps.hasNvidia, vaapi: caps.hasVaapi, qsv: caps.hasQsv, preferred: caps.preferred } : null,
      ua: navigator.userAgent,
    }
  }

  // HEVC / unsupported codec auto-fallback. The native <video onError> fires for HEVC, AV1,
  // VP9-in-MKV, etc. — anything Safari/Chrome can't decode. If a GPU encoder is available we
  // retry through /api/stream/transcode (force h264) silently. The "Attempted" flag prevents
  // looping when the transcoded stream itself errors (then the real error UI shows).
  //
  // Diagnostic logs are intentionally verbose — Safari HEVC silent failures are
  // notoriously hard to reproduce locally and we want enough context to debug
  // from a single user report (paste the console output).
  const handleNoFallback = (diag: ReturnType<typeof videoDiagnostic>) => {
    const downloadingNow = (info?.downRate ?? 0) > 30 * 1024
    if (downloadingNow && bufferRetryRef.current < 6) {
      bufferRetryRef.current++
      clientLog('info', 'player', 'buffer retry — swarm still delivering, reloading playlist',
        { retry: bufferRetryRef.current, downRate: info?.downRate, ...diag })
      setVideoError(false)
      globalThis.setTimeout(() => { videoRef.current?.load() }, 6000)
      return
    }
    let reason: string
    if (transcodeFallbackAttempted) {
      reason = 'already-attempted'
    } else if (forceH264) {
      reason = 'h264-already-forced'
    } else {
      reason = 'no-caps'
    }
    clientLog('warn', 'player', 'surfacing error UI — no more fallbacks available',
      { reason, retries: bufferRetryRef.current, ...diag })
    setVideoError(true)
  }

  const handleNoGPU = () => {
    clientLog('warn', 'player', 'no GPU encoder — surfacing manual UI', { caps })
    setVideoError(true)
  }

  const handleAutoFallback = (diag: ReturnType<typeof videoDiagnostic>) => {
    clientLog('info', 'player', 'auto-fallback engaging via onError', { willRetryVia: caps?.preferred, ...diag })
    setTranscodeFallbackAttempted(true)
    setForceH264(true)
    setVideoError(false)
  }

  const handleVideoError = () => {
    const vEl = videoRef.current
    if (!streamURL || !vEl?.currentSrc) {
      clientLog('info', 'player', 'ignoring onError — no resolved source yet', { hasStreamURL: !!streamURL })
      return
    }
    const diag = videoDiagnostic()
    clientLog('warn', 'player', 'video onError fired', diag)
    setLastErrorDiag(diag as Record<string, unknown>)
    if (transcodeFallbackAttempted || forceH264 || !caps) {
      handleNoFallback(diag)
      return
    }
    const hasGPU = caps.hasNvidia || caps.hasVaapi || caps.hasQsv
    if (!hasGPU) {
      handleNoGPU()
      return
    }
    handleAutoFallback(diag)
  }

  const renderDiagnosticChip = () => {
    const diag = (lastErrorDiag ?? videoDiagnostic()) as Record<string, any>
    const codeNames: Record<number, string> = { 1: 'ABORTED', 2: 'NETWORK', 3: 'DECODE', 4: 'SRC_NOT_SUPPORTED' }
    const codeName = diag.errorCode ? codeNames[diag.errorCode] || `code ${diag.errorCode}` : '—'
    return (
      <div className="mt-3 text-[10px] text-gray-500 font-mono space-y-0.5">
        <div>MediaError: <span className="text-yellow-400">{codeName}</span> {diag.errorMsg ? `· ${diag.errorMsg}` : ''}</div>
        <div>ready={diag.readyState ?? '—'} net={diag.networkState ?? '—'} {diag.isTranscoded ? '· transcode ON' : '· direct play'}{diag.transcodeFallbackAttempted ? ' · fallback tried' : ''}</div>
        <div className="text-gray-600">Full log: filtre por "[player]" no console</div>
      </div>
    )
  }

  const renderVideoError = () => {
    const cf = info?.files?.[selectedFile]
    const peers = info?.peers ?? 0
    const fileDownloaded = cf?.downloaded ?? 0
    const starving = fileDownloaded < 30 * 1024 * 1024
    const kind: 'swarm' | 'codec' = (peers === 0 || starving) ? 'swarm' : 'codec'
    const errorData = buildErrorInfo(peers, starving, info)
    return (
      <div className="absolute inset-0 flex flex-col items-center justify-center text-gray-300 p-6 text-center">
        <AlertCircle className={`w-12 h-12 mb-3 ${kind === 'swarm' ? 'text-orange-400' : 'text-yellow-400'}`} />
        <p className="font-medium">{errorData.title}</p>
        <p className="text-sm text-gray-500 mt-2 max-w-md">{errorData.detail}</p>
        {renderDiagnosticChip()}
        <button
          onClick={() => setVideoError(false)}
          className="mt-4 text-xs text-green-400 hover:underline"
        >
          Tentar de novo
        </button>
      </div>
    )
  }

  const handleVideoEnded = () => {
    console.debug('[player] video onEnded', {
      repeat,
      nextVideoIdx,
      hasPlaylistAdvance: !!onPlaylistAdvance,
      playlistName: playlist?.name,
      audioMode,
    })
    if (repeat === 'one') {
      const v = videoRef.current
      if (v) { v.currentTime = 0; v.play().catch(() => {}) }
      return
    }
    if (nextVideoIdx >= 0) {
      playFile(nextVideoIdx)
      return
    }
    if (onPlaylistAdvance) {
      onPlaylistAdvance()
    }
  }

  // Safari HEVC silent-failure backstop. Safari on macOS does NOT fire
  // <video onError> when it can't decode HEVC — it just stays at readyState=0
  // with no diagnostic. After 20 s, if we still haven't reached
  // HAVE_CURRENT_DATA AND playback hasn't moved, trigger the same fallback
  // that onError would. 20s (not 10s) because HEVC 10-bit transcode legitimately
  // takes longer to emit the first segment — a tighter window fired the
  // fallback while ffmpeg was still producing, causing a reload storm.
  useHevcBackstop({
    videoRef, info, selectedFile, audioMode, transcodeAudio, forceH264, burnSubTrack,
    transcodeFallbackAttempted, videoError, bufferedEnd, caps, videoDiagnostic,
    setTranscodeFallbackAttempted, setForceH264,
  })

  // Poll progress every 2s while modal is open
  useEffect(() => {
    if (!info?.infoHash) return
    const tick = () => {
      streamInfo(info.infoHash).then(setInfo).catch(() => {})
    }
    pollRef.current = globalThis.setInterval(tick, 2000)
    return () => {
      if (pollRef.current) globalThis.clearInterval(pollRef.current)
    }
  }, [info?.infoHash])

  // Mirror the values the unmount cleanup needs into a ref, refreshed every
  // render. This lets the cleanup run ONLY on real unmount (deps: []) while
  // still seeing current values — without it, depending on [libraryEntryID]
  // re-ran the cleanup the moment the library entry loaded mid-playback,
  // calling streamDrop() and KILLING the torrent we were actively streaming
  // (ffmpeg then died with "torrent closed" → "Sem seeds").
  const cleanupRef = useRef<{ readonly infoHash: string; readonly libraryEntryID: number | null; readonly fileIndex: number; readonly incognito: boolean }>({ infoHash: '', libraryEntryID: null, fileIndex: -1, incognito: false })
  useEffect(() => {
    cleanupRef.current = { infoHash: info?.infoHash ?? '', libraryEntryID, fileIndex: selectedFile, incognito }
  })

  // Drop the torrent + persist final resume position — ONLY when the modal
  // truly unmounts (user closes/navigates), never on intra-playback state changes.
  useEffect(() => {
    return () => {
      const { infoHash, libraryEntryID: libID, fileIndex, incognito: wasIncognito } = cleanupRef.current
      const v = videoRef.current
      if (!wasIncognito && libID !== null && v && v.currentTime > 1) {
        // Persist which file was watched so reopening a season pack resumes the
        // same episode (not the torrent's primary file).
        libraryUpdateResume(libID, v.currentTime, v.duration || 0, fileIndex >= 0 ? fileIndex : undefined).catch(() => {})
      }
      if (infoHash) {
        streamDrop(infoHash).catch(() => {})
      }
    }
  }, [])

  // Detect season/episode from title for better subtitle matches
  const parseSeasonEpisode = (title: string): { season?: number; episode?: number; cleanQuery: string } => {
    const match = /[Ss](\d{1,2})[Ee](\d{1,3})/.exec(title)
    if (!match) return { cleanQuery: title }
    return {
      season: Number.parseInt(match[1]),
      episode: Number.parseInt(match[2]),
      cleanQuery: title.slice(0, match.index).trim().replaceAll(/[._]/g, ' '),
    }
  }

  const openSubtitlePanel = async () => {
    setSubOpen(true)
    if (subResults.length > 0 || !result || !info) return
    setSubLoading(true)
    setSubError('')
    try {
      // Prefer hash-based auto search (frame-exact) — single API call, results ranked by relevance
      const resp = await subtitlesAuto(info.infoHash, selectedFile, 'pt-BR,pt')
      setSubResults(resp.results || [])
      if (resp.osHash && !resp.hashErr) setAutoSource('hash')
      else setAutoSource('title')
    } catch {
      // Fall back to plain title search if auto endpoint fails
      try {
        const baseTitle = info.name || result.title
        const { season, episode, cleanQuery } = parseSeasonEpisode(baseTitle)
        const data = await subtitlesSearch(cleanQuery || baseTitle, { season, episode, langs: 'pt-BR,pt' })
        setSubResults(data || [])
        setAutoSource('title')
      } catch (error_: any) {
        setSubError(error_?.response?.data?.error || error_.message || 'Erro ao buscar legendas')
      }
    } finally {
      setSubLoading(false)
    }
  }

  const pickSubtitle = (s: Subtitle) => {
    // Apply but keep the panel open so the active subtitle shows its ✓/highlight
    // and the user can switch or remove it without reopening. They close it via
    // the ✕ (or the "Legendas" toggle) when done.
    setSubActive(s.id)
    clearCustomSub()
  }

  const handleRequestFullscreen = () => {
    const v = videoRef.current as any
    if (!v) return
    // iOS Safari uses webkitEnterFullscreen on the <video> element
    if (typeof v.webkitEnterFullscreen === 'function') {
      v.webkitEnterFullscreen()
    } else if (v.requestFullscreen) {
      v.requestFullscreen()
    } else if (v.webkitRequestFullscreen) {
      v.webkitRequestFullscreen()
    }
  }

  // Desktop keyboard shortcuts (part of #63). Touch gestures are intentionally
  // NOT added — the native <video controls> owns touch on iOS and custom
  // overlays fought its gestures. Skipped while minimized, while typing in an
  // input/select, and when the <video> itself has focus (let the browser's
  // native handler act, so we don't double-seek).
  useKeyboardShortcuts({ videoRef, minimized, requestFullscreen: handleRequestFullscreen })

  // iPhone landscape → native iOS fullscreen. The custom modal layout isn't
  // built to reflow for a short, wide phone viewport (it got cramped/garbled),
  // and the native player is the chosen behaviour there. We trigger on
  // orientation change for phone-sized viewports only — `max-height: 600px` in
  // landscape rules out tablets (iPad landscape is ~768px+ tall). iOS only
  // honours fullscreen on the <video> via webkitEnterFullscreen, and may refuse
  // it outside a user gesture, so this is best-effort (rotate again if it
  // didn't catch, or tap the fullscreen button).
  useEffect(() => {
    const mq = globalThis.matchMedia('(orientation: landscape) and (max-height: 600px)')
    const handleOrient = () => {
      const v = videoRef.current as any
      if (!v || !mq.matches || v.readyState < 1) return
      try {
        if (typeof v.webkitEnterFullscreen === 'function') v.webkitEnterFullscreen()
        else if (v.requestFullscreen) v.requestFullscreen()
        else if (v.webkitRequestFullscreen) v.webkitRequestFullscreen()
      } catch {
        /* iOS may block fullscreen outside a user gesture — ignore */
      }
    }
    mq.addEventListener?.('change', handleOrient)
    globalThis.addEventListener('orientationchange', handleOrient)
    return () => {
      mq.removeEventListener?.('change', handleOrient)
      globalThis.removeEventListener('orientationchange', handleOrient)
    }
  }, [])


  // Update offset and reapply to all cues
  const adjustSubOffset = (delta: number) => {
    setSubOffset((prev) => Math.round((prev + delta) * 10) / 10)
  }

  const resetSubOffset = () => setSubOffset(0)

  // Apply subtitle offset whenever active sub or offset changes (and reset the
  // cue snapshot when the subtitle changes). Extracted to a hook.
  useSubtitleOffset({ videoRef, subActive, subOffset, origCuesRef })

  // After torrent metadata loads, fetch the library entry to know if we have a saved resume position
  useEffect(() => {
    if (!info?.infoHash || incognito) return
    libraryGet(0).catch(() => {})
    const hash = info.infoHash
    import('../api/client').then(({ libraryList }) => {
      libraryList({ limit: 100 }).then(list => loadLibraryEntry(list, hash)).catch(() => {})
    })
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [info?.infoHash])

  // One-shot guard for the URL-supplied seek. Without it we'd re-apply the
  // initial seek every time `canplay` fires (which happens on each format
  // negotiation, transcode fallback, etc.), making it impossible to scrub away.
  const appliedInitialSeekRef = useRef(false)
  // Same idea for the library-driven auto-resume — fire once per file selection
  // and then keep `resumePosition` populated so the "Continuar" button can use
  // it after the user goes back to the start.
  const appliedAutoResumeRef = useRef(false)
  useEffect(() => {
    // Reset whenever a new file is selected so a future URL-driven re-play
    // (e.g., navigating to ?play=X&t=...) re-applies the seek instead of
    // remembering "already done" from the previous file.
    appliedInitialSeekRef.current = false
    appliedAutoResumeRef.current = false
    setShowResumePrompt(false)
  }, [selectedFile, info?.infoHash])

  // Seek once the video can play. Priority:
  //   1. URL-supplied initialSeek (explicit, e.g. shared link with `t=120`)
  //   2. per-user library resumeSeconds (background-saved, silent)
  const handleVideoCanPlay = () => {
    const v = videoRef.current
    if (!v) return
    if (initialSeek !== undefined && initialSeek > 0 && !appliedInitialSeekRef.current) {
      if (v.currentTime < 1) {
        v.currentTime = initialSeek
      }
      appliedInitialSeekRef.current = true
      // Clear DB resume to avoid the second branch firing on the same canplay
      setResumePosition(null)
      return
    }
    if (resumePosition === null) return
    if (v.currentTime < 1 && resumePosition > 30 && !appliedAutoResumeRef.current) {
      appliedAutoResumeRef.current = true
      // Ask instead of silently jumping: the user picks "continue" or "restart"
      // via the overlay (see resume prompt). Mark applied so it only asks once.
      setShowResumePrompt(true)
    }
  }

  // Probe container for embedded audio + subtitle tracks (uses ffprobe on first ~16MB).
  // Gated by serverReady so we don't fire while the torrent is still warming up —
  // ffprobe needs a live Reader from the streamer's active map.
  useTrackProbe({
    info, selectedFile, serverReady, subActive, embeddedSub,
    setProbe, setEmbeddedSub, setAutoSource, setSidecars, setSidecarIdx,
  })

  // Resolve + persist a per-torrent thumbnail once playback is live. Gated by
  // serverReady so the torrent is active (embedded image / frame capture need a
  // live Reader). Idempotent server-side: skips re-processing if good art was
  // already persisted. Best-effort — never touches playback.
  useEffect(() => {
    if (!info?.infoHash || selectedFile < 0 || !serverReady) return
    resolveArt(info.infoHash, selectedFile)
  }, [info?.infoHash, selectedFile, serverReady])

  // Scroll the file picker to the selected file (after it renders). Runs on
  // open and whenever the selection changes — a tiny delay lets the list mount.
  useEffect(() => {
    if (!sidebarOpen || selectedFile < 0) return
    const t = setTimeout(() => {
      selectedFileRef.current?.scrollIntoView({ block: 'center', behavior: 'auto' })
    }, 60)
    return () => clearTimeout(t)
  }, [sidebarOpen, selectedFile, info?.infoHash])

  // Note: auto-search of OpenSubtitles intentionally NOT triggered here — it would burn quota.
  // Embedded subtitles auto-load (free), external ones require explicit click via "Legendas" button.
  // The hash-based search runs only on first open of the panel.

  // Restore the saved subtitle choice for this file (external/embedded/sidecar
  // + offset). Runs before the pt auto-load gets a chance (which is gated by
  // hasSavedChoice in the probe effect), so the user's pick wins. subRestored
  // gates the save effect below so the file-switch reset can't persist an empty
  // choice before this runs.
  useSubtitleChoicePersist({
    info, selectedFile, subRestored, subActive, embeddedSub, sidecarIdx, subOffset,
    setSubActive, setEmbeddedSub, setSidecarIdx, setSubOffset, setAutoSource, setSubRestored,
  })

  // Track playback state + accumulate watch time for auto-favorite
  const handleTimeUpdate = () => {
    const v = videoRef.current
    if (!v) return
    const now = v.currentTime
    const delta = now - lastTickRef.current
    if (delta > 0 && delta < 2) watchedRef.current += delta
    lastTickRef.current = now
    setCurrentTime(now)
    setDuration(v.duration || 0)
    updateBufferedRanges(v, now, setBufferedRanges, setBufferedEnd)
    tryAutoFavorite(watchedRef.current, isFavorite, AUTO_FAV_THRESHOLD, info, setIsFavorite)
    trySaveResume(now, incognito, libraryEntryID, lastResumeSaveRef, v.duration || 0)
    trySyncUrlPlayhead(now, lastUrlSyncRef)
    tryPrefetchNext({ v, now, nextVideoIdx, info, prefetchedNextEpRef, onPrefetchNextPlaylist, prefetchedPlaylistN1Ref, onPrefetchNextNextPlaylist, prefetchedPlaylistN2Ref })
  }

  // Apply playback speed + pitch preservation whenever the user changes it or
  // a new <video>/<audio> element mounts (i.e., when selectedFile changes).
  // preservesPitch is the modern spec; webkitPreservesPitch is the Safari/iOS
  // legacy attribute that's still required on some devices.
  useEffect(() => {
    const v = videoRef.current
    if (!v) return
    v.playbackRate = playbackSpeed
    v.preservesPitch = true
    // Safari/iOS legacy attribute — not in lib.dom but still needed on older WebKit.
    ;(v as unknown as { webkitPreservesPitch?: boolean }).webkitPreservesPitch = true
    localStorage.setItem('jackui.playbackSpeed', String(playbackSpeed))
  }, [playbackSpeed, selectedFile, info?.infoHash])

  // Media Session API — exposes "what's playing" + media keys / lock-screen
  // controls to the OS. Without this, iOS shows "JackUI" with no metadata and
  // AirPods/bluetooth controls don't fire next/previous on the playlist.
  useMediaSession({ videoRef, info, selectedFile, playlistName: playlist?.name, onNext: onPlaylistAdvance, onPrev: onPlaylistPrevious })

  // Load initial favorite state when torrent info arrives. Match by infoHash
  // first (precise — same content always returns same hash) and fall back to
  // name only when needed. The old version matched ONLY by name, which broke
  // when `info.name` (anacrolix torrent.Name()) differed from the favorite's
  // stored name (which was the search result title at favorite-time) — common
  // for torrents whose name has trailing periods, encoded characters, etc.
  useEffect(() => {
    if (!info) return
    favoritesList()
      .then(list => setIsFavorite(list.some(f =>
        (f.infoHash?.toLowerCase() === info.infoHash?.toLowerCase())
        || f.name === info.name
      )))
      .catch(() => {})
  }, [info?.name, info?.infoHash])

  const toggleFavorite = async () => {
    if (!info) return
    const next = !isFavorite
    setIsFavorite(next)
    try {
      if (next) {
        // We have the real source URL via `result.magnetUri || result.link` (pickTorrentSource).
        // If the result came from a search, magnetUri is set; if from /library, the magnet was already saved.
        // Fallback to inferred magnet (no trackers) ONLY if nothing else available.
        const magnet = (result ? pickTorrentSource(result) : '')
          || (info.infoHash ? `magnet:?xt=urn:btih:${info.infoHash}` : '')
        await favoriteAdd(info.name, info.infoHash, magnet, 'manual')
      } else {
        await favoriteRemove(info.name)
      }
    } catch {
      setIsFavorite(!next) // revert on error
    }
  }

  const formatTime = (s: number): string => {
    if (!Number.isFinite(s) || s < 0) return '0:00'
    const h = Math.floor(s / 3600)
    const m = Math.floor((s % 3600) / 60)
    const sec = Math.floor(s % 60)
    if (h > 0) return `${h}:${m.toString().padStart(2, '0')}:${sec.toString().padStart(2, '0')}`
    return `${m}:${sec.toString().padStart(2, '0')}`
  }

  // Parse S/E pattern from filename for nicer episode labels (defined before
  // conditional return so the hook below is not behind a branch)
  const parseEpisode = (path: string): string | null => {
    const m = /[Ss](\d{1,2})[ ._-]?[Ee](\d{1,3})/.exec(path)
    if (m) return `S${m[1].padStart(2, '0')}E${m[2].padStart(2, '0')}`
    return null
  }

  const playFile = (idx: number) => {
    if (idx < 0) return
    setSelectedFile(idx)
    setVideoError(false)
    setLastErrorDiag(null)
    setSidecarIdx(null)
    setEmbeddedSub(null)
    setSubActive(null)
    setProbe(null)
    setSidecars([])
    setSubRestored(false)
    watchedRef.current = 0
    lastTickRef.current = 0
    setCurrentTime(0)
    setBufferedEnd(0)
    setBufferedRanges([])
  }

  // Apply a changed initialFileIndex when the player is ALREADY open for the
  // same torrent (e.g. the user picks a different file in the contents modal).
  // `result` keeps its reference across that pick, so the main [result] effect
  // doesn't re-run — without this the user had to click play twice (first click
  // changed the prop, second finally switched). Guards: only when metadata is
  // loaded and the index actually differs from what's selected.
  useEffect(() => {
    if (initialFileIndex === undefined || initialFileIndex < 0) return
    if (!info || initialFileIndex >= info.files.length) return
    if (initialFileIndex === selectedFile) return
    playFile(initialFileIndex)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialFileIndex])

  if (!result) return null

  const videoFiles = info?.files.filter(f => f.isVideo) || []
  const currentFile = selectedFile >= 0 ? info?.files[selectedFile] : null
  const currentEp = currentFile ? parseEpisode(currentFile.path) : null

  // Series-in-torrent navigation: detect prev/next video file (by index order, restricted to video files)
  const videoFileIndices = (info?.files || []).filter(f => f.isVideo).map(f => f.index)
  const videoCursor = videoFileIndices.indexOf(selectedFile)
  const prevVideoIdx = videoCursor > 0 ? videoFileIndices[videoCursor - 1] : -1
  const nextVideoIdx = videoCursor >= 0 && videoCursor < videoFileIndices.length - 1 ? videoFileIndices[videoCursor + 1] : -1

  // URL builder: raw direct play unless any transcoding option is active.
  // Safari + HEVC/x265/AV1 short-circuits to transcode (which is HLS for Safari)
  // BEFORE the first <video> attempt — otherwise the user sees the direct-play
  // attempt fail, the auto-fallback overlay flash, then a retry loop ("tente
  // novamente até funcionar"). Detection by filename is best-effort; if it
  // misses, the auto-fallback flow on onError still rescues it like before.
  // Route to HLS up-front on Safari for anything it likely can't direct-play:
  //   - HEVC/x265/AV1 by name (codec markers)
  //   - 2160p/4K/UHD: even "MP4" containers at 4K are usually HEVC or H264 at
  //     a level Safari's <video> rejects; trying direct-play first just burns
  //     ~18s before the fallback. The whole point is to NOT attempt the path
  //     we know fails. Misses still get rescued by onError/backstop fallback.
  const videoUrls = computeMediaUrls({ info, selectedFile, serverReady, mediaToken, transcodeAudio, forceH264, burnSubTrack, subActive, sidecarIdx, embeddedSub, customSubURL, caps })
  const { streamURL, subtitleVttURL, vlcURL, encoderLabel, isTranscoded } = videoUrls

  const subtitleLabel = getSubtitleLabel(embeddedSub, subActive, autoSource, subLoading)

  // Container/overlay attributes for the modal shell. Extracted to a nested
  // helper so the minimized-vs-fullscreen ternaries live in their own scope
  // (keeps the component body's cognitive complexity down) — behavior unchanged.
  const shellProps = (): React.HTMLAttributes<HTMLDivElement> => {
    if (minimized) {
      return { className: 'fixed bottom-3 right-3 z-50 w-[360px] max-w-[calc(100vw-1.5rem)]' }
    }
    return {
      className: 'fixed inset-0 bg-black/80 backdrop-blur-sm flex items-stretch sm:items-center justify-center z-50 sm:p-4',
      onClick: (e) => { if (e.target === e.currentTarget) setMinimized(true) },
      onKeyDown: (e) => { if (e.key === 'Escape') setMinimized(true) },
      role: 'dialog',
      'aria-modal': 'true',
      tabIndex: -1,
    }
  }

  // The active-stream layout (video + controls + file picker). Extracted to a
  // nested render fn so its conditional blocks don't inflate the component's
  // cognitive complexity. Closes over all the local state — behavior identical.
  const renderActiveStream = () => {
    if (!info || selectedFile < 0) return null
    return (
      <div className="flex flex-col lg:flex-row flex-1 min-h-0">
        {/* Main column: video + transport + status + panels. On lg+ the
            file picker moves to a sidebar on the right — frees this
            column to grow without forcing the page into outer scroll. */}
        <div className="flex flex-col min-w-0 lg:flex-1 lg:overflow-y-auto lg:overflow-x-hidden">
        {/* Video player. Vertical-aware sizing: we cap at ~58vh so the controls,
            status bar, file picker, and panels below all fit inside the modal's
            90vh budget on standard 1080p/ultrawide-1080 monitors. The flex
            centering + `mx-auto` keeps the <video> centered with letterbox
            bars when the source aspect doesn't match the available area. */}
        <VideoPlayerElement
          videoRef={videoRef}
          streamURL={streamURL}
          audioMode={audioMode}
          subtitleVttURL={subtitleVttURL}
          videoError={videoError}
          serverReady={serverReady}
          currentTime={currentTime}
          bufferedEnd={bufferedEnd}
          info={info}
          selectedFile={selectedFile}
          showResumePrompt={showResumePrompt}
          resumePosition={resumePosition}
          isTranscoded={isTranscoded}
          transcodeFallbackAttempted={transcodeFallbackAttempted}
          mediaToken={mediaToken}
          renderVideoError={renderVideoError}
          formatTime={formatTime}
          onVideoError={handleVideoError}
          onTimeUpdate={handleTimeUpdate}
          onVideoEnded={handleVideoEnded}
          onVideoCanPlay={handleVideoCanPlay}
          videoDiagnostic={videoDiagnostic}
          onResumeContinue={(pos) => {
            const v = videoRef.current
            if (v) { v.currentTime = pos; v.play().catch(() => {}) }
            setShowResumePrompt(false)
          }}
          onResumeRestart={() => {
            const v = videoRef.current
            if (v) { v.currentTime = 0; v.play().catch(() => {}) }
            setShowResumePrompt(false)
            setResumePosition(null)
          }}
        />

        {/* Minimized audio: show a slim time readout below the cover-art box
            so the user knows where they are in the track without expanding.
            Native <video controls> handle play/pause/seek (visible once the
            video element is sized — see audioMode w-full h-full above). */}
        {minimized && audioMode && duration > 0 && (
          <MinimizedAudioProgress currentTime={currentTime} duration={duration} formatTime={formatTime} />
        )}

        {/* Everything below the video (transport, status, subtitle panel)
            is hidden in minimized mode — the native <video> controls cover
            play/pause/seek in the compact card. The <video> element itself
            stays mounted above, so all the HEVC/HLS/buffer logic is intact. */}
        {!minimized && (
          <PlayerControlsPanel
            info={info}
            currentFile={currentFile}
            videoFileIndices={videoFileIndices}
            videoCursor={videoCursor}
            prevVideoIdx={prevVideoIdx}
            nextVideoIdx={nextVideoIdx}
            currentEp={currentEp}
            currentTime={currentTime}
            duration={duration}
            bufferedEnd={bufferedEnd}
            bufferedRanges={bufferedRanges}
            subActive={subActive}
            subOffset={subOffset}
            showMobileOpts={showMobileOpts}
            playbackSpeed={playbackSpeed}
            probe={probe}
            sidecars={sidecars}
            transcodeAudio={transcodeAudio}
            forceH264={forceH264}
            burnSubTrack={burnSubTrack}
            isTranscoded={isTranscoded}
            sidecarIdx={sidecarIdx}
            embeddedSub={embeddedSub}
            subEnabled={subEnabled}
            autoSource={autoSource}
            subLoading={subLoading}
            subtitleLabel={subtitleLabel}
            vlcURL={vlcURL}
            streamURL={streamURL}
            serverDownloadLoading={serverDownloadLoading}
            serverDownloadSuccess={serverDownloadSuccess}
            subOpen={subOpen}
            customSubName={customSubName}
            subError={subError}
            subResults={subResults}
            formatTime={formatTime}
            playFile={playFile}
            adjustSubOffset={adjustSubOffset}
            resetSubOffset={resetSubOffset}
            setShowMobileOpts={setShowMobileOpts}
            setPlaybackSpeed={setPlaybackSpeed}
            clearCustomSub={clearCustomSub}
            setTranscodeAudio={setTranscodeAudio}
            setForceH264={setForceH264}
            setBurnSubTrack={setBurnSubTrack}
            setSidecarIdx={setSidecarIdx}
            setEmbeddedSub={setEmbeddedSub}
            setSubActive={setSubActive}
            setAutoSource={setAutoSource}
            openSubtitlePanel={openSubtitlePanel}
            handleRequestFullscreen={handleRequestFullscreen}
            handleServerDownload={handleServerDownload}
            setSubOpen={setSubOpen}
            handleCustomSubtitleUpload={handleCustomSubtitleUpload}
            pickSubtitle={pickSubtitle}
          />
        )}
        </div>{/* end main column */}

        {!minimized && info.files.length > 1 && sidebarOpen && (
          <FilePickerSidebar
            info={info}
            videoFiles={videoFiles}
            selectedFile={selectedFile}
            selectedFileRef={selectedFileRef}
            fileFilter={fileFilter}
            fileTypeFilter={fileTypeFilter}
            fileSortBySize={fileSortBySize}
            fileSizeDesc={fileSizeDesc}
            hoverThumb={hoverThumb}
            parseEpisode={parseEpisode}
            playFile={playFile}
            setFileFilter={setFileFilter}
            setFileTypeFilter={setFileTypeFilter}
            setFileSortBySize={setFileSortBySize}
            setFileSizeDesc={setFileSizeDesc}
            setSidebarOpen={setSidebarOpen}
            setPreviewFileIdx={setPreviewFileIdx}
          />
        )}

        {/* Collapsed-sidebar reopen tab — two variants:
            • lg+: slim vertical strip on the right edge of the modal.
            • mobile: horizontal bar below the video. Without this, iOS
              users who tap "Esconder lista" had no way to bring it back —
              the list literally vanished. (See issue #50.) */}
        {info.files.length > 1 && !sidebarOpen && (
          <>
            {/* Mobile (and tablet up to lg): full-width bar */}
            <button
              onClick={() => setSidebarOpen(true)}
              title="Mostrar lista de arquivos"
              className="lg:hidden flex items-center justify-center gap-2 w-full px-4 py-2 border-t border-gray-700 bg-gray-850 hover:bg-gray-700 text-gray-400 hover:text-gray-200 text-xs flex-shrink-0"
            >
              <ChevronLeft className="w-4 h-4 rotate-90" />
              Mostrar lista de arquivos ({info.files.length})
            </button>
            {/* lg+: vertical strip on the right edge */}
            <button
              onClick={() => setSidebarOpen(true)}
              title="Mostrar lista de arquivos"
              className="hidden lg:flex flex-col items-center justify-center w-8 border-l border-gray-700 bg-gray-850 hover:bg-gray-700 text-gray-400 hover:text-gray-200 flex-shrink-0"
            >
              <ChevronLeft className="w-4 h-4" />
              <span className="text-[10px] [writing-mode:vertical-rl] rotate-180 mt-2">
                Arquivos ({info.files.length})
              </span>
            </button>
          </>
        )}
      </div>
    )
  }

  return (
    <div {...shellProps()}>
      {/* Responsive width: phones/tablets keep ~896px (max-w-4xl) for a tight focused
          modal. Laptops bump to ~1280px so the file list + side panels stop fighting
          for vertical space. Ultra-wide desktops (≥1536px) use 90vw — fills usable
          area without going edge-to-edge.

          Mobile-fullscreen: `h-[100dvh]` on phones makes the modal occupy the full
          dynamic viewport (handles iOS URL-bar collapse). Border/rounding stripped
          on phones so the modal becomes edge-to-edge. Returns to bounded card on sm+. */}
      <div className={minimized
        ? 'bg-gray-800 rounded-xl border border-gray-700 shadow-2xl w-full flex flex-col overflow-hidden'
        : 'bg-gray-800 rounded-none sm:rounded-2xl border-0 sm:border border-gray-700 w-full max-w-4xl lg:max-w-6xl 2xl:max-w-[min(90vw,1600px)] shadow-2xl sm:h-auto sm:max-h-[90vh] min-h-0 flex flex-col'}>
        {renderPlayerHeader({ minimized, info, result, isTranscoded, caps, encoderLabel, isFavorite, toggleFavorite, incognito, setIncognito, setMinimized, onClose })}
        {playlist && renderPlaylistBar(playlist, onPlaylistPrevious, onToggleShuffle, shuffle, onCycleRepeat, repeat, onPlaylistAdvance)}

        {/* Content. min-h-0 + flex-1 lets the inner active-stream block manage
            its own scroll regions (main column + sidebar) without the parent
            forcing an outer scrollbar. */}
        <div className="flex flex-col flex-1 min-h-0 overflow-hidden">
          {/* Big loading: only when we have NOTHING — no metadata cache hit AND
              streamAdd hasn't returned yet. If the cache primed `info`, we skip
              this and show the populated UI immediately with a slim inline
              "waiting on swarm" indicator instead (rendered further below). */}
          {loading && !info && (
            <div className="flex flex-col items-center justify-center py-16 text-gray-400">
              <Loader2 className="w-10 h-10 animate-spin mb-4 text-green-500" />
              <p className="font-medium">Conectando ao swarm...</p>
              <p className="text-xs text-gray-500 mt-2">Primeira vez nesse torrent — buscando peers</p>
            </div>
          )}
          {/* Slim inline indicator: cached file list visible, swarm still warming up.
              The big buffering overlay over the video area covers the actual playback
              start; this strip is just to acknowledge that something is happening. */}
          {loading && info && !serverReady && (
            <div className="px-4 py-1.5 text-xs text-blue-300 bg-blue-500/10 border-b border-blue-500/30 flex items-center gap-2 flex-shrink-0">
              <Loader2 className="w-3 h-3 animate-spin" />
              Metadados em cache — conectando ao swarm em segundo plano...
            </div>
          )}

          {/* Error state */}
          {error && (
            <div className="m-5 p-4 bg-red-500/10 border border-red-500/30 rounded-xl">
              <p className="flex items-center gap-2 text-red-400 font-medium">
                <AlertCircle className="w-4 h-4" />
                Erro ao iniciar stream
              </p>
              <p className="text-sm text-red-300 mt-1">{error}</p>
              <p className="text-xs text-gray-500 mt-3">
                Causas comuns: torrent sem seeders, metadados não obtidos a tempo, ou magnet inválido.
              </p>
            </div>
          )}

          {/* Active stream */}
          {renderActiveStream()}

          {/* No video files in torrent */}
          {info && videoFiles.length === 0 && (
            <div className="m-5 p-4 bg-yellow-500/10 border border-yellow-500/30 rounded-xl">
              <p className="flex items-center gap-2 text-yellow-400 font-medium">
                <AlertCircle className="w-4 h-4" />
                Nenhum arquivo de vídeo encontrado
              </p>
              <p className="text-xs text-gray-500 mt-2">
                Este torrent contém {info.files.length} arquivo(s) mas nenhum é de vídeo reconhecido (.mp4, .mkv, .avi, etc.)
              </p>
            </div>
          )}
        </div>
      </div>
      {/* Inline preview overlay for non-playable companion files (NFO, log,
          subtitles, PDFs shipped inside the torrent). Rendered outside the
          main modal box so its z-index can sit ABOVE the player without
          fighting flex layout. */}
      {previewFileIdx !== null && info?.files[previewFileIdx] && (
        <FilePreviewModal
          infoHash={info.infoHash}
          fileIdx={previewFileIdx}
          filePath={info.files[previewFileIdx].path}
          fileSize={info.files[previewFileIdx].size}
          onClose={() => setPreviewFileIdx(null)}
        />
      )}
      {hoverThumb.popover}
    </div>
  )
}
