import { useState, useEffect, useMemo, useRef, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { X, Play, Loader2, AlertCircle, FileVideo, Download, Upload, Users, Activity, Check, Maximize2, Minimize2, Cpu, Heart, ChevronLeft, ChevronRight, ListMusic, Shuffle, Repeat, EyeOff, Eye, Info, Hash, Server, Copy, Home } from 'lucide-react'
import {
  SearchResult,
  TorrentInfo,
  Subtitle,
  StreamProbe,
  TranscodeCapabilities,
  SidecarSubtitle,
  streamAdd,
  streamMetadata,
  pickTorrentSource,
  streamInfo,
  streamViewerOpen,
  streamViewerClose,
  streamFileURL,
  isSafariBrowser,
  isIOS,
  resolveArt,
  subtitlesEnabled,
  subtitlesSearch,
  subtitlesAuto,
  fetchMediaToken,
  transcodeCapabilities,
  favoriteAdd,
  favoriteRemove,
  favoritesList,
  libraryGet,
  libraryUpdateResume,
  LibraryEntry,
  downloadLocalFileDirect,
  classifyCategory,
  isLocalHash,
  localSubtrackBlobURL,
} from '../api/client'
import { formatRate } from '../lib/format'
import { parentDir, filesUnderDir } from '../lib/treeSelect'
import { usePersistedState } from '../lib/storage'
import { clientLog } from '../lib/diag'
import { useScrollLock } from '../lib/useScrollLock'
import { useSwipe } from '../lib/useSwipe'
import { useIncognito } from '../lib/incognito'
import { useAuth } from '../auth/AuthContext'
import FilePreviewModal from './FilePreviewModal'
import { detectViewerKind } from './viewer/viewerKind'
import { previewRawURL } from '../api/preview'
import { useHoverThumb } from './FileThumbHover'
import { Sheet } from './Sheet'
import { useKeyboardShortcuts, useMediaSession, useMediaQueue, useSubtitleOffset, useTrackProbe, useSubtitleChoicePersist, useHevcBackstop } from './player/playerHooks'
import { formatSize, getSubtitleLabel, filterAndSortFiles, parseEpisodeTag, type FileType } from './player/playerFormat'
import { useTrackOrder } from './player/useTrackOrder'
import { nextTrack, prevTrack } from '../lib/trackTransport'
import { computeMediaUrls } from './player/mediaUrls'
import { computeFilePickerState } from './player/filePickerVisibility'
import { buildErrorInfo, tryPrefetchNext, updateBufferedRanges, tryAutoFavorite, trySaveResume, trySyncUrlPlayhead, chooseInitialFile } from './player/playerEffects'
import { VideoPlayerElement } from './player/VideoPlayerElement'
import { FilePickerSidebar } from './player/FilePickerSidebar'
import DownloadModal from './DownloadModal'
import { PlaylistTracksSidebar } from './player/PlaylistTracksSidebar'
import { PlayerControlsPanel } from './player/PlayerControlsPanel'
import { SimpleAudioPlayer } from './player/SimpleAudioPlayer'
import { SimpleAudioControls } from './player/SimpleAudioControls'
import { AudioCoverArt, audioCoverURL } from './player/AudioCoverArt'
import { useAudioDirectUrl } from './player/useAudioDirectUrl'
import { usePlaylistTracks } from './player/usePlaylistTracks'
import { useToast } from './Toast'

// The translate fn type (react-i18next's TFunction), shared with the module-level
// render helpers below (they aren't components, so they receive `t` as a param).
type TFn = ReturnType<typeof useTranslation>['t']

type PlaylistMeta = {
  readonly name: string
  // Each item is a torrent (a pack with many files) or a single local file.
  // The aggregated track sidebar needs the source (infoHash/magnet) to resolve
  // every item's file list, not just the playing one.
  readonly items: readonly { title: string; infoHash: string; magnet: string; fileIndex: number }[]
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
  readonly onPlaylistJump?: (itemIndex: number, fileIndex?: number) => void
  readonly repeat?: 'none' | 'one' | 'all'
  readonly shuffle?: boolean
  readonly onCycleRepeat?: () => void
  readonly onToggleShuffle?: () => void
  readonly onPrefetchNextPlaylist?: () => void
  readonly onPrefetchNextNextPlaylist?: () => void
  readonly startMinimized?: boolean
  readonly audioMode?: boolean
  /** Render the player filling the whole browser viewport (not the centered
   *  modal) — used when the tab booted at a /?play= deep-link. Shows a Home
   *  button instead of minimize/close. */
  readonly fullViewport?: boolean
  /** Navigate back to Home (used by the full-viewport Home button). */
  readonly onHome?: () => void
  /** Reports the playhead (seconds) on every timeupdate. Lets the provider
   *  preserve position when it re-keys the modal on a Cinema/Música switch. */
  readonly onProgress?: (sec: number) => void
}

// minimizedOrFullClass returns the outer panel classes for the player shell:
// minimized (audio bar / video PiP), full-viewport (deep-link tab — fills the
// whole browser window), or the default centered modal.
function minimizedOrFullClass(minimized: boolean, audioMode: boolean, fullViewport: boolean): string {
  if (minimized) {
    return audioMode
      ? 'bg-surface-secondary rounded-t-xl border-t border-default shadow-2xl w-full flex flex-col overflow-hidden pb-[env(safe-area-inset-bottom,0px)]'
      : 'bg-surface-secondary rounded-xl border border-default shadow-2xl w-full flex flex-col overflow-hidden'
  }
  if (fullViewport) {
    return 'bg-surface-secondary w-full h-full max-w-none rounded-none border-0 min-h-0 flex flex-col'
  }
  return 'bg-surface-secondary rounded-none sm:rounded-2xl border-0 sm:border border-default w-full max-w-4xl lg:max-w-6xl 2xl:max-w-[min(90vw,1600px)] shadow-2xl sm:h-auto sm:max-h-[90vh] min-h-0 flex flex-col animate-[player-expand_320ms_cubic-bezier(0.16,1,0.3,1)]'
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
  onShowInfo: () => void
  headerRef: React.RefObject<HTMLDivElement>
  fullViewport?: boolean
  onHome?: () => void
  t: TFn
}) {
  const { minimized, info, result, isTranscoded, caps, encoderLabel, isFavorite, toggleFavorite, incognito, setIncognito, setMinimized, onClose, onShowInfo, headerRef, fullViewport, onHome, t } = props
  if (minimized) return null
  return (
    <div ref={headerRef} className="flex items-center justify-between px-4 pb-4 pt-statusbar sm:!pt-4 border-b border-default flex-shrink-0 touch-pan-y">
      <h2 className="text-base font-semibold text-text-primary flex items-center gap-2 min-w-0">
        <Play className="w-4 h-4 text-green-500 flex-shrink-0" />
        <span className="truncate">{info?.name || result.title}</span>
        {isTranscoded && caps?.preferred && <span className="text-[10px] bg-purple-500/20 text-purple-700 dark:text-purple-300 border border-purple-500/30 px-1.5 py-0.5 rounded flex items-center gap-1 flex-shrink-0" title={t('player.modal.encoderTitle', { encoder: caps.preferred })}><Cpu className="w-2.5 h-2.5" />{encoderLabel}</span>}
        {isTranscoded && !caps?.preferred && <span className="text-[10px] bg-purple-500/20 text-purple-700 dark:text-purple-300 border border-purple-500/30 px-1.5 py-0.5 rounded flex items-center gap-1 flex-shrink-0"><Cpu className="w-2.5 h-2.5" />GPU</span>}
      </h2>
      <div className="flex items-center gap-2 flex-shrink-0 ml-2">
        {info && <button onClick={onShowInfo} title={t('player.modal.torrentInfo')} className="text-text-secondary hover:text-text-primary transition-colors"><Info className="w-5 h-5" /></button>}
        {info && <button onClick={toggleFavorite} title={isFavorite ? t('player.modal.removeFavorite') : t('player.modal.addFavorite')} className={`transition-colors ${isFavorite ? 'text-pink-400 hover:text-pink-500 dark:hover:text-pink-300' : 'text-text-muted hover:text-pink-400'}`}><Heart className={`w-5 h-5 ${isFavorite ? 'fill-current' : ''}`} /></button>}
        <button onClick={() => setIncognito(!incognito)} title={incognito ? t('player.modal.incognitoActive') : t('player.modal.incognitoEnable')} className={`transition-colors ${incognito ? 'text-amber-400 hover:text-amber-500 dark:hover:text-amber-300' : 'text-text-secondary hover:text-text-primary'}`}>{incognito ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}</button>
        {fullViewport
          ? (
            // Deep-link tab dedicated to playback: the only navigation affordance
            // is a single Home button (no minimize/PiP — there's nothing behind).
            <button onClick={() => (onHome ?? onClose)()} title={t('player.modal.backHome')} className="flex items-center gap-1 text-sm text-text-secondary hover:text-text-primary transition-colors">
              <Home className="w-5 h-5" />
            </button>
          )
          : (
            <>
              <button onClick={() => setMinimized(m => !m)} title={minimized ? t('player.modal.expand') : t('player.modal.minimize')} className="text-text-secondary hover:text-text-primary transition-colors">{minimized ? <Maximize2 className="w-4 h-4" /> : <Minimize2 className="w-5 h-5" />}</button>
              <button onClick={onClose} className="text-text-secondary hover:text-text-primary transition-colors"><X className="w-5 h-5" /></button>
            </>
          )}
      </div>
    </div>
  )
}

// Torrent-info overlay opened from the player header. Rendered ABOVE the player
// (z-[60] > the modal's z-50) so it floats over the video. Reads the live `info`
// so swarm stats update while open.
function renderTorrentInfoModal(props: {
  info: TorrentInfo
  result: SearchResult
  isTranscoded: boolean
  encoderLabel: string
  onClose: () => void
  onCopyHash: () => void
  hashCopied: boolean
  effectiveCategory: string
  setOverrideCategory: (v: string | null) => void
  handleClassifyCategory: () => void
  classifyingCat: boolean
  t: TFn
}) {
  const { info, result, isTranscoded, encoderLabel, onClose, onCopyHash, hashCopied, effectiveCategory, setOverrideCategory, handleClassifyCategory, classifyingCat, t } = props
  const pct = info.progress === undefined ? null : `${(info.progress * 100).toFixed(1)}%`
  const Row = ({ icon, label, children }: { icon?: React.ReactNode; label: string; children: React.ReactNode }) => (
    <div className="flex items-start gap-2 py-1.5 border-b border-default/40 last:border-0">
      <span className="text-text-muted text-xs w-28 flex-shrink-0 flex items-center gap-1.5">{icon}{label}</span>
      <span className="text-text-primary text-sm min-w-0 break-words flex-1">{children}</span>
    </div>
  )
  return (
    <Sheet
      open
      onClose={onClose}
      zClass="z-[60]"
      lockScroll={false}
      size="md"
      title={t('player.modal.torrentInfo')}
      icon={<Info className="w-4 h-4 text-blue-400 flex-shrink-0" />}
    >
      <Row icon={<FileVideo className="w-3.5 h-3.5" />} label={t('player.modal.info.name')}>{info.name || result.title}</Row>
      {info.name && info.name !== result.title && <Row label={t('player.modal.info.release')}>{result.title}</Row>}
      <Row icon={<Download className="w-3.5 h-3.5" />} label={t('player.modal.info.size')}>{formatSize(info.totalSize)} · {info.files.length} {info.files.length === 1 ? t('player.files.file') : t('player.files.files')}</Row>
      <Row icon={<Users className="w-3.5 h-3.5" />} label={t('player.modal.info.seedsPeers')}>{info.seeders ?? 0} / {info.peers ?? 0}</Row>
      {(info.downRate ?? 0) > 0 && <Row icon={<Activity className="w-3.5 h-3.5" />} label={t('player.modal.info.speed')}>{formatRate(info.downRate)}{pct && ` · ${t('player.modal.info.pctDownloaded', { pct })}`}</Row>}
      {(info.bytesDownloaded ?? 0) > 0 && <Row icon={<Download className="w-3.5 h-3.5" />} label={t('player.modal.info.downloaded')}>{formatSize(info.bytesDownloaded ?? 0)}{pct && ` · ${pct}`}</Row>}
      {((info.bytesUploaded ?? 0) > 0 || (info.upRate ?? 0) > 0) && <Row icon={<Upload className="w-3.5 h-3.5" />} label={t('player.modal.info.uploaded')}>{formatSize(info.bytesUploaded ?? 0)}{(info.upRate ?? 0) > 0 ? ` · ${formatRate(info.upRate)}` : ''}</Row>}
      {info.stalled && (info.downRate ?? 0) === 0 && <Row icon={<Loader2 className="w-3.5 h-3.5 animate-spin" />} label={t('player.modal.info.transfer')}>{t('player.modal.info.awaitingData')}</Row>}
      {result.tracker && <Row icon={<Server className="w-3.5 h-3.5" />} label={t('player.modal.info.tracker')}>{result.tracker}</Row>}
      {globalThis.electronAPI ? (
        <Row label={t('player.modal.info.category')}>
          <div className="flex items-center gap-1.5">
            <select
              className="bg-surface-tertiary text-text-primary text-xs rounded px-2 py-1 border border-strong"
              value={effectiveCategory}
              onChange={(e) => setOverrideCategory(e.target.value === 'default' ? null : e.target.value)}
            >
              <option value="default">{result?.category || t('player.modal.category.auto')}</option>
              <option value="movies">{t('player.modal.category.movies')}</option>
              <option value="tv">{t('player.modal.category.tv')}</option>
              <option value="music">{t('player.modal.category.music')}</option>
              <option value="games">{t('player.modal.category.games')}</option>
              <option value="software">{t('player.modal.category.software')}</option>
              <option value="adult">{t('player.modal.category.adult')}</option>
              <option value="books">{t('player.modal.category.books')}</option>
              <option value="other">{t('player.modal.category.other')}</option>
            </select>
            <button
              onClick={handleClassifyCategory}
              disabled={classifyingCat}
              className="text-[10px] bg-indigo-500/20 hover:bg-indigo-500/30 text-indigo-700 dark:text-indigo-300 px-1.5 py-0.5 rounded"
              title={t('player.modal.category.detectAI')}
            >
              {classifyingCat ? '…' : t('player.modal.category.ai')}
            </button>
          </div>
        </Row>
      ) : (
        result.category && <Row label={t('player.modal.info.category')}>{result.category}</Row>
      )}
      {isTranscoded && <Row icon={<Cpu className="w-3.5 h-3.5" />} label={t('player.modal.info.encoder')}>{encoderLabel || 'GPU'}</Row>}
      {info.infoHash && (
        <Row icon={<Hash className="w-3.5 h-3.5" />} label={t('player.modal.info.infoHash')}>
          <span className="flex items-center gap-2 min-w-0">
            <span className="font-mono text-xs truncate min-w-0">{info.infoHash}</span>
            <button onClick={onCopyHash} title={t('player.modal.copy')} className="flex-shrink-0 text-text-muted hover:text-text-primary">
              {hashCopied ? <Check className="w-3.5 h-3.5 text-green-400" /> : <Copy className="w-3.5 h-3.5" />}
            </button>
          </span>
        </Row>
      )}
    </Sheet>
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
  t: TFn,
) {
  return (
    <div className="flex items-center justify-between gap-2 px-4 py-2 bg-blue-500/10 border-b border-blue-500/30 text-xs text-blue-700 dark:text-blue-200 flex-shrink-0">
      <div className="flex items-center gap-2 min-w-0">
        <ListMusic className="w-3.5 h-3.5 flex-shrink-0" />
        <span className="font-medium truncate">{playlist.name}</span>
        <span className="text-blue-400/80 flex-shrink-0">· {t('player.modal.ofCount', { current: playlist.currentIndex + 1, total: playlist.items.length })}</span>
      </div>
      <div className="flex items-center gap-1 flex-shrink-0">
        <button onClick={onPrev} className="flex items-center justify-center min-w-[36px] min-h-[36px] sm:min-w-0 sm:min-h-0 p-2 sm:p-1 rounded hover:bg-blue-500/20 text-blue-700 dark:text-blue-200 hover:text-blue-900 dark:hover:text-white" title={t('player.modal.prevItem')}><ChevronLeft className="w-4 h-4" /></button>
        <button onClick={onToggleShuffle} className={`flex items-center justify-center min-w-[36px] min-h-[36px] sm:min-w-0 sm:min-h-0 p-2 sm:p-1 rounded hover:bg-blue-500/20 ${shuffle ? 'text-green-700 dark:text-green-300' : 'text-blue-700/60 dark:text-blue-200/60'} hover:text-blue-900 dark:hover:text-white`} title={shuffle ? t('player.controls.shuffleOn') : t('player.controls.shuffleOff')}><Shuffle className="w-3.5 h-3.5" /></button>
        <button onClick={onCycleRepeat} className={`flex items-center justify-center min-w-[36px] min-h-[36px] sm:min-w-0 sm:min-h-0 p-2 sm:p-1 rounded hover:bg-blue-500/20 ${repeat === 'none' ? 'text-blue-700/60 dark:text-blue-200/60' : 'text-green-700 dark:text-green-300'} hover:text-blue-900 dark:hover:text-white relative`} title={t('player.controls.repeatMode', { mode: repeat })}>
          <Repeat className="w-3.5 h-3.5" />
          {repeat === 'one' && <span className="absolute bottom-0.5 right-0.5 text-[8px] font-bold text-green-700 dark:text-green-300">1</span>}
        </button>
        <button onClick={onNext} className="flex items-center justify-center min-w-[36px] min-h-[36px] sm:min-w-0 sm:min-h-0 p-2 sm:p-1 rounded hover:bg-blue-500/20 text-blue-700 dark:text-blue-200 hover:text-blue-900 dark:hover:text-white" title={t('player.modal.nextItem')}><ChevronRight className="w-4 h-4" /></button>
      </div>
    </div>
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
  const { notify, notifyError } = useToast()
  const [info, setInfo] = useState<TorrentInfo | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  // blessed (iOS): o usuário JÁ iniciou a reprodução nesta sessão via gesto (toque
  // no "Tocar" / play). A Apple então libera load()/play() programático pras
  // faixas seguintes ("once the user has started playing the first media element").
  // Detectado pelo 1º evento 'playing' do elemento — no iOS, a 1ª reprodução só
  // ocorre por gesto (autoplay frio é bloqueado), então 'playing' ⇒ blessed. Com
  // isso o auto-avanço entre faixas passa a tocar sozinho (player de verdade).
  const [blessed, setBlessed] = useState(false)
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
  // Blob URL of a LOCAL embedded sub fetched with retry — the server extracts
  // large rclone files in the background (503 until ready), so a <track src>
  // pointing straight at the endpoint would 502/hang. '' until extracted.
  const [localEmbeddedVttURL, setLocalEmbeddedVttURL] = useState('')

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
      notify(t('player.modal.subtitleReadError'), 'error')
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
    // Music mode is album-browsing first — open the track list by default so a
    // multi-track album (e.g. "2 de 73") shows its songs without hunting for the
    // collapsed strip. Video keeps the per-user stored preference.
    if (audioMode) return true
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
  // Auth ligado no servidor? Com auth off as rotas de mídia são públicas e não
  // precisam de ?token= (e /auth/media-token nem existe → 404).
  const { enabled: authEnabled } = useAuth()

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

  // Fetch (with retry) the selected LOCAL embedded subtitle as a VTT blob. Polls
  // while the server reports "extracting"; sets the blob when ready so the track
  // appears without the player hanging. Revokes the previous blob on change.
  // (Placed after mediaToken so its value is available when the effect runs.)
  useEffect(() => {
    setLocalEmbeddedVttURL(prev => {
      if (prev) URL.revokeObjectURL(prev)
      return ''
    })
    if (!info || !isLocalHash(info.infoHash) || embeddedSub === null) return
    let cancelled = false
    localSubtrackBlobURL(info.infoHash, selectedFile, embeddedSub, mediaToken, () => cancelled)
      .then(url => {
        if (cancelled) {
          if (url) URL.revokeObjectURL(url)
          return
        }
        if (url) setLocalEmbeddedVttURL(url)
      })
    return () => { cancelled = true }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [info?.infoHash, selectedFile, embeddedSub, mediaToken])

  // Frozen snapshot of the diagnostic at the moment onVideoError fired. Used by
  // the error UI which re-renders AFTER the <video> element unmounted, so by
  // then videoRef.current is null and a live diagnostic would come back empty.
  const [lastErrorDiag, setLastErrorDiag] = useState<Record<string, unknown> | null>(null)
  // Inline preview for non-playable files (txt/srt/nfo/pdf/jpg/etc).
  // Storing the file index lets us look up path + size from `info.files`
  // on render without duplicating state when the user reopens the player.
  const [previewFileIdx, setPreviewFileIdx] = useState<number | null>(null)

  // Server-side background download state. Loading/success ficam falsos agora
  // que o botão abre o modal unificado (em vez de baixar direto) — mantidos pra
  // não mexer na interface do PlayerControlsPanel.
  const [serverDownloadLoading] = useState(false)
  const [serverDownloadSuccess] = useState(false)
  // Alvo do modal de download aninhado (destino + seleção); indices pré-seleciona.
  const [playerDownload, setPlayerDownload] = useState<{ result: SearchResult; indices?: number[] } | null>(null)

  // Constrói o SearchResult pro modal a partir do info/result atuais. null pra
  // arquivos locais (sem magnet — o torrent client não os aceita; usam LocalCacheButton).
  const buildDownloadResult = useCallback((): SearchResult | null => {
    if (!info || isLocalHash(info.infoHash)) return null
    const magnet = result?.magnetUri || `magnet:?xt=urn:btih:${info.infoHash}`
    if (result) return { ...result, magnetUri: magnet, infoHash: info.infoHash, title: result.title || info.name }
    return {
      title: info.name, tracker: '', categoryId: 0, category: '', size: 0, seeders: 0,
      leechers: 0, age: '', magnetUri: magnet, link: '', infoHash: info.infoHash, publishDate: '',
    }
  }, [info, result])

  // "Cache no servidor": abre o modal pré-selecionando o arquivo em reprodução.
  const handleServerDownload = () => {
    const r = buildDownloadResult()
    if (!r) return
    setPlayerDownload({ result: r, indices: selectedFile >= 0 ? [selectedFile] : undefined })
  }

  // 📁↓ por arquivo: baixar a pasta inteira (recursiva) daquele arquivo. Abre o
  // modal com todos os arquivos da pasta pré-selecionados.
  const downloadFolderFromPlayer = useCallback((file: TorrentInfo['files'][number]) => {
    const r = buildDownloadResult()
    if (!r || !info) return
    const dir = parentDir(file.path)
    const indices = dir ? filesUnderDir(info.files, dir).map(f => f.index) : [file.index]
    setPlayerDownload({ result: r, indices })
  }, [buildDownloadResult, info])

  // 📁↓ na linha da pasta (árvore): baixa a pasta inteira, recursivamente. O
  // node.path é o caminho real (mesmo em folders single-child colapsados), então
  // filesUnderDir casa tudo sob ele. Abre o modal com esses arquivos pré-marcados.
  const downloadDirFromPlayer = useCallback((dirPath: string) => {
    const r = buildDownloadResult()
    if (!r || !info) return
    const indices = filesUnderDir(info.files, dirPath).map(f => f.index)
    if (indices.length === 0) return
    setPlayerDownload({ result: r, indices })
  }, [buildDownloadResult, info])
  // Local (Electron) download with automatic categorization
  const [localDownloadLoading, setLocalDownloadLoading] = useState(false)
  const [overrideCategory, setOverrideCategory] = useState<string | null>(null)
  const [classifyingCat, setClassifyingCat] = useState(false)

  // 'default' = não forçar categoria (deixa o backend categorizar). O <select>
  // tem uma <option value="default">; mapear o estado nela evita o value órfão
  // (a string crua do Jackett, ex. "Movies/HD", não casa com nenhuma option →
  // warning do React + categoria errada no download).
  const effectiveCategory = overrideCategory ?? 'default'

  const handleLocalDownload = async () => {
    if (!info || selectedFile < 0) return
    setLocalDownloadLoading(true)
    try {
      const file = info.files[selectedFile]
      const name = file.path.split('/').pop() || info.name
      const apiPath = streamFileURL(info.infoHash, selectedFile)
      const categoryArg = effectiveCategory === 'default' ? undefined : effectiveCategory
      await downloadLocalFileDirect(apiPath, name, categoryArg)
    } catch (err) {
      notifyError(err)
    } finally {
      setLocalDownloadLoading(false)
    }
  }

  const handleClassifyCategory = async () => {
    if (!info) return
    setClassifyingCat(true)
    try {
      const res = await classifyCategory(info.name, result?.category ? String(result.category) : undefined)
      if (res.category && res.category !== 'other') {
        setOverrideCategory(res.category)
      }
    } catch { /* silent */ }
    setClassifyingCat(false)
  }
  // Transcoding options — any non-null value triggers `/api/stream/transcode` instead of raw stream
  const [transcodeAudio, setTranscodeAudio] = useState<number | null>(null)
  // Dispara o auto-transcode do áudio incompatível no máximo uma vez por arquivo.
  const audioAutoRef = useRef(false)
  const [forceH264, setForceH264] = useState(false)
  const [burnSubTrack, setBurnSubTrack] = useState<number | null>(null)
  // Auto-transcode do áudio quando o codec da faixa DEFAULT não é decodável pelo
  // browser (AC3/E-AC3/DDP/DTS/TrueHD/Atmos/PCM/WMA) — senão o vídeo toca MUDO
  // (ex: MKV DDP5.1 Atmos). O Safari vai pelo caminho HLS, que já resolve isso →
  // só não-Safari. Dispara uma vez por arquivo; o seletor de faixa ainda permite
  // o usuário trocar.
  useEffect(() => {
    if (audioAutoRef.current || !probe || isSafariBrowser() || transcodeAudio !== null) return
    const INCOMPATIBLE = /^(ac-?3|e-?ac-?3|eac3|ddp?|dts|dca|truehd|mlp|pcm|wmav?)/i
    const def = probe.audio.find(a => a.default) ?? probe.audio[0]
    if (def && INCOMPATIBLE.test(def.codec)) {
      audioAutoRef.current = true
      setTranscodeAudio(def.index)
    }
  }, [probe, transcodeAudio])
  // HEVC auto-fallback: on first <video> error, if a GPU encoder is available, retry via transcode.
  // The "Attempted" flag prevents an infinite loop if the transcoded stream also errors.
  const [transcodeFallbackAttempted, setTranscodeFallbackAttempted] = useState(false)
  const [caps, setCaps] = useState<TranscodeCapabilities | null>(null)
  // Torrent-info overlay (opened from the header Info button).
  const [showInfo, setShowInfo] = useState(false)
  const [hashCopied, setHashCopied] = useState(false)
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
  // Size sort is shared (persisted) with TorrentContentsModal so the order the
  // user picked there carries into the player's file list — same localStorage key.
  const [fileSortBySize, setFileSortBySize] = usePersistedState('fileview.sortBySize', false)
  const [fileSizeDesc, setFileSizeDesc] = usePersistedState('fileview.sizeDesc', true)
  const hoverThumb = useHoverThumb()

  // Favorites — auto-mark after 5min of actual playback (currentTime accumulates)
  const [isFavorite, setIsFavorite] = useState(false)
  const watchedRef = useRef(0)            // accumulated playback time (seconds)
  const lastTickRef = useRef<number>(0)   // last currentTime sample (for delta)
  const AUTO_FAV_THRESHOLD = 5 * 60       // 5 minutes
  // everReadyRef: vira true assim que o player já mostrou conteúdo (info +
  // arquivo) ao menos uma vez nesta instância. Habilita o "warm hold" na troca
  // de faixa de música (ver o efeito [result]) e suprime o overlay de start.
  const everReadyRef = useRef(false)
  // streamAddDoneRef: o streamAdd (autoritativo) já resolveu? Evita que o
  // preview do cache de metadados sobrescreva o resultado autoritativo na corrida.
  const streamAddDoneRef = useRef(false)
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
  const audioRef = useRef<HTMLAudioElement | null>(null)
  // Swipe down on the header bar minimizes the player to its PiP card — the same
  // non-destructive dismiss as tapping the backdrop or pressing Escape (keeps
  // playback alive), the iOS idiom for "push this sheet away".
  const headerRef = useRef<HTMLDivElement>(null)
  useSwipe(headerRef, { onDown: () => setMinimized(true) }, { enabled: !minimized, threshold: 50 })
  // Minimized → arrastar pra CIMA (ou tocar) na barra expande de volta. Simétrico
  // ao swipe-down do header: o player tem dois estados claros, barra↔cheio, por gesto.
  const miniBarRef = useRef<HTMLDivElement>(null)
  useSwipe(miniBarRef, { onUp: () => setMinimized(false) }, { enabled: minimized, threshold: 40 })
  const pollRef = useRef<ReturnType<typeof globalThis.setInterval> | null>(null)
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

    // The PlayerProvider reuses this instance across videos (key='video'), so a
    // stale hover preview from the previous file would otherwise stay pinned
    // over the new file list. Dismiss it on every result change.
    hoverThumb.hide()

    // Guard against a slow streamAdd from the PREVIOUS result resolving after
    // we've switched to a new one — without it the old torrent's file list +
    // thumbnails clobber the new video. Flipped by the cleanup below.
    let cancelled = false

    // warmHold: numa troca de faixa de MÚSICA com o player já populado, NÃO
    // desmonta a UI (capa/seekbar/transport) nem corta o áudio atual — segura o
    // `info`/`selectedFile`/`serverReady` antigos (streamURL deriva de `info`, então
    // o <video> continua na faixa atual) até o streamMetadata/streamAdd da nova
    // resolver; aí a troca é atômica. Sem isso a tela "piscava" (overlay
    // "Conectando ao swarm") a cada faixa. Escopo só ÁUDIO (vídeo mantém o reset
    // completo, sem regressão); cold start (1ª faixa) também reseta normal.
    const warmHold = everReadyRef.current && audioMode
    streamAddDoneRef.current = false

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
    setTranscodeAudio(null)
    audioAutoRef.current = false
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
    // fileSortBySize/fileSizeDesc persist (shared with TorrentContentsModal) —
    // intentionally NOT reset here, so the chosen order carries into the player.
    origCuesRef.current = []

    // Try the cached metadata first — if the server has seen this hash before,
    // the file list + name appear instantly. streamAdd still kicks off in
    // parallel to actually load the torrent client (required for playback).
    if (result.infoHash) {
      streamMetadata(result.infoHash).then(cached => {
        // streamAddDoneRef (não `info`): no warm hold o `info` antigo ainda está
        // setado, então o preview do cache PRECISA poder sobrescrevê-lo; só não
        // pode passar por cima do streamAdd autoritativo se este já resolveu.
        if (cancelled || !cached || streamAddDoneRef.current) return
        setInfo(cached)
        setSelectedFile(chooseInitialFile(cached, initialFileIndex))
      })
    }

    streamAdd(pickTorrentSource(result), audioMode ? 'audio' : 'video')
      .then(t => {
        if (cancelled) return
        streamAddDoneRef.current = true
        setInfo(t)
        setSelectedFile(chooseInitialFile(t, initialFileIndex))
        // Streamer now has the torrent active — unblock <video src>.
        setServerReady(true)
      })
      .catch(err => { if (!cancelled) setError(err?.response?.data?.error || err.message || t('player.modal.streamStartFailed')) })
      .finally(() => { if (!cancelled) setLoading(false) })

    // Check whether subtitles backend is configured
    subtitlesEnabled().then(v => { if (!cancelled) setSubEnabled(v) }).catch(() => { if (!cancelled) setSubEnabled(false) })
    // Cache transcode capabilities once per modal — used by HEVC auto-fallback decision.
    if (!caps) {
      transcodeCapabilities().then(c => { if (!cancelled) setCaps(c) }).catch(() => { if (!cancelled) setCaps(null) })
    }

    return () => { cancelled = true }
    // Keyado no VALOR `infoHash`, não no objeto `result`: o PlayerProvider recria o
    // objeto `result` em vários pontos (playlistItemToResult, toggle Cinema/Música,
    // sync URL↔estado) com o MESMO infoHash. Keyar no objeto re-rodava toda esta
    // init (streamAdd/probe/library) ~16s após abrir, recarregando o <video> e
    // abortando o play() do gesto no iOS. A troca de arquivo real muda infoHash e
    // dispara normal; initialFileIndex tem efeito dedicado próprio.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [result?.infoHash])

  // Marca que o player já renderizou uma faixa nesta instância → habilita o warm
  // hold (troca de faixa sem desmontar a UI) nas próximas trocas.
  useEffect(() => {
    if (info && selectedFile >= 0) everReadyRef.current = true
  }, [info, selectedFile])

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
    setLastErrorDiag(diag)
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
      <div className="mt-3 text-[10px] text-text-muted font-mono space-y-0.5">
        <div>MediaError: <span className="text-yellow-400">{codeName}</span> {diag.errorMsg ? `· ${diag.errorMsg}` : ''}</div>
        <div>ready={diag.readyState ?? '—'} net={diag.networkState ?? '—'} {diag.isTranscoded ? '· transcode ON' : '· direct play'}{diag.transcodeFallbackAttempted ? ' · fallback tried' : ''}</div>
        <div className="text-text-muted">{t('player.modal.fullLogHint')}</div>
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
      <div className="absolute inset-0 flex flex-col items-center justify-center text-text-primary p-6 text-center">
        <AlertCircle className={`w-12 h-12 mb-3 ${kind === 'swarm' ? 'text-orange-400' : 'text-yellow-400'}`} />
        <p className="font-medium">{errorData.title}</p>
        <p className="text-sm text-text-muted mt-2 max-w-md">{errorData.detail}</p>
        {renderDiagnosticChip()}
        <button
          onClick={() => setVideoError(false)}
          className="mt-4 text-xs text-green-400 hover:underline"
        >
          {t('player.modal.tryAgain')}
        </button>
      </div>
    )
  }

  // onPlaying do <video>: marca o `blessed` (1ª reprodução iniciada por gesto no
  // iOS) UMA vez e loga. A partir daí o auto-avanço pode tocar programaticamente —
  // o grant per-element da Apple persiste no src-swap ("Auto-play restrictions are
  // granted on a per-element basis" + "change the source... instead of creating
  // multiple media elements"). Idempotente: onPlaying dispara a cada retomada.
  const handlePlaybackStarted = () => {
    if (blessed) return
    clientLog('info', 'player', 'blessed: 1ª reprodução iniciada (auto-avanço liberado)', {})
    setBlessed(true)
  }

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

  // Safari HEVC silent-failure backstop. Safari on macOS does NOT fire
  // <video onError> when it can't decode HEVC — it just stays at readyState=0
  // with no diagnostic. After 20 s, if we still haven't reached
  // HAVE_CURRENT_DATA AND playback hasn't moved, trigger the same fallback
  // that onError would. 20s (not 10s) because HEVC 10-bit transcode legitimately
  // takes longer to emit the first segment — a tighter window fired the
  // fallback while ffmpeg was still producing, causing a reload storm.
  useHevcBackstop({
    videoRef, info, selectedFile, audioMode, transcodeAudio, forceH264, burnSubTrack,
    transcodeFallbackAttempted, videoError, bufferedEnd, needsTranscode: probe?.needsTranscode, caps, videoDiagnostic,
    setTranscodeFallbackAttempted, setForceH264,
  })

  // Poll progress every 2s while modal is open
  useEffect(() => {
    if (!info?.infoHash) return
    const tick = () => {
      // Skip while the tab is hidden (áudio em background é o caso comum): cada
      // streamInfo reconstrói o buildInfo do torrent (dezenas de BytesCompleted
      // num pacote multi-arquivo). Volta a atualizar sozinho ao focar a aba.
      if (document.hidden) return
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

  // Persist final resume position — ONLY when the modal truly unmounts (user
  // closes/navigates), never on intra-playback state changes. Dropping the
  // torrent is handled by the viewer-lease effect below (keyed on the hash), not
  // here, so switching A→B in the same instance releases A as well.
  useEffect(() => {
    return () => {
      const { libraryEntryID: libID, fileIndex, incognito: wasIncognito } = cleanupRef.current
      const v = videoRef.current
      if (!wasIncognito && libID !== null && v && v.currentTime > 1) {
        // Persist which file was watched so reopening a season pack resumes the
        // same episode (not the torrent's primary file).
        libraryUpdateResume(libID, v.currentTime, v.duration || 0, fileIndex >= 0 ? fileIndex : undefined).catch(() => {})
      }
    }
  }, [])

  // Viewer lease: hold a lease on the active torrent while it's open and release
  // it when the hash changes (playlist/autoplay reuses this instance) or on
  // unmount. The backend keeps the torrent alive while ≥1 viewer holds a lease
  // (so a co-watcher closing one tab doesn't kill the others) and drops a
  // stream-only torrent shortly after the LAST viewer leaves — instead of
  // seeding idly until the reaper. local- hashes aren't streamer torrents.
  useEffect(() => {
    const hash = info?.infoHash
    if (!hash || hash.startsWith('local-')) return
    streamViewerOpen(hash).catch(() => {})
    return () => { streamViewerClose(hash).catch(() => {}) }
  }, [info?.infoHash])

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
        setSubError(error_?.response?.data?.error || error_.message || t('player.modal.subtitleSearchError'))
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

  // Desktop keyboard shortcuts (part of #63): wired AFTER the audio engine block
  // below, so it controls the active element (the engine's <audio> when active).
  // Touch gestures are intentionally NOT added — the native <video controls> owns
  // touch on iOS. Skipped while minimized / typing / when the <video> has focus.

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
  useSubtitleOffset({ videoRef, subActive, embeddedSub, sidecarIdx, localEmbeddedVttURL, subOffset, origCuesRef })

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
  // Autoplay nativo (iOS): dispara uma vez por fonte, no canplay sem-resume.
  const autoplayTriedRef = useRef(false)
  useEffect(() => {
    // Reset whenever a new file is selected so a future URL-driven re-play
    // (e.g., navigating to ?play=X&t=...) re-applies the seek instead of
    // remembering "already done" from the previous file.
    appliedInitialSeekRef.current = false
    appliedAutoResumeRef.current = false
    autoplayTriedRef.current = false
    setShowResumePrompt(false)
  }, [selectedFile, info?.infoHash])

  // Seek once the video can play. Priority:
  //   1. URL-supplied initialSeek (explicit, e.g. shared link with `t=120`)
  //   2. per-user library resumeSeconds (background-saved, silent)
  // iosAudio: caminho ÁUDIO no iPhone/iPad. Gate único do "tap-to-play": no iOS o
  // play() de mídia-com-áudio EXIGE um gesto (regra da Apple), então desligamos o
  // autoplay não-gesto e os nudges, mostramos o overlay "Tocar" e deixamos o tap do
  // usuário iniciar. isIOS() (não isSafariBrowser) pra NÃO regredir o macOS-Safari,
  // que toca com autoplay normal. Só depende de audioMode (prop) → válido aqui.
  const iosAudio = audioMode && isIOS()
  // disableNativeAutoplay: bloqueia o autoplay não-gesto SÓ até o usuário iniciar a
  // reprodução (blessed). Depois disso a Apple libera o play() programático, então
  // as faixas seguintes encadeiam sozinhas (auto-avanço) — vira false e o caminho
  // volta ao normal (autoplay no loadedmetadata/canplay).
  // Só alimenta o VideoPlayerElement (o áudio usa o SimpleAudioPlayer, que tem seu
  // próprio gesto). O VÍDEO no iOS sofre O MESMO erro do áudio: o <video> com src
  // declarativo pré-carrega e ESTACIONA em readyState 2 sem um gesto (logs: stalled
  // rs2 + sem 'autoplay try'). Então o vídeo também vira tap-to-play no iOS — daí
  // isIOS() (não só iosAudio). macOS-Safari/desktop seguem no autoplay (isIOS=false).
  const disableNativeAutoplay = isIOS() && !blessed
  // Autoplay no caminho NATIVO (<video> sem hls.js): o iOS ignora o atributo
  // autoPlay quando há áudio, então tentamos play() explicitamente (com fallback
  // mudo). Uma vez por fonte. Não chamado quando vamos exibir o prompt de resume
  // — aí o usuário escolhe continuar/recomeçar. (O caminho hls.js já trata o
  // autoplay no MANIFEST_PARSED; um play() extra aqui seria no-op idempotente.)
  const maybeAutoplayNative = (v: HTMLVideoElement) => {
    if (autoplayTriedRef.current) return
    autoplayTriedRef.current = true
    // iOS-áudio AINDA NÃO iniciado (não blessed): NÃO tentar autoplay. A Apple proíbe
    // play() de mídia-com-áudio fora de um gesto; um play() não-gesto trava o elemento
    // em readyState 1 e aborta em loop. Deixamos pausado e mostramos o overlay "Tocar"
    // — o tap do usuário (gesto) inicia. DEPOIS de iniciado (blessed), a Apple libera
    // o play() programático → caímos no caminho normal abaixo e a faixa seguinte do
    // álbum toca sozinha (auto-avanço).
    if (iosAudio && !blessed) {
      clientLog('info', 'player', 'iOS: autoplay pulado — aguardando gesto (tap-to-play)', { readyState: v.readyState })
      return
    }
    // DIAGNÓSTICO (temporário): registra qual caminho o autoplay tomou no device,
    // pra cravar a intermitência do iOS — tocou com SOM, caiu no MUDO (sem gesto),
    // ou falhou. Mesma lógica do tryAutoplayMutedFallback + logs.
    clientLog('info', 'player', 'autoplay try', { readyState: v.readyState, file: selectedFile })
    v.play()
      .then(() => clientLog('info', 'player', 'autoplay ok (som)', {}))
      .catch((e) => {
        // AbortError ≠ bloqueio de autoplay (NotAllowedError): o play() foi
        // INTERROMPIDO por um load()/troca de src/remontagem do elemento enquanto
        // ainda estava pendente (no iOS a janela de buffering inicial é longa).
        // NÃO encadear um play() mudo num elemento ainda carregando — isso só
        // agrava o abort e mata o som de vez. Em vez disso, libera o guard
        // one-shot pra o PRÓXIMO loadedmetadata/canplay re-tentar limpo no
        // elemento já estabilizado (com SOM). Era a causa do "tocou e parou /
        // sem som" no iPhone.
        if ((e as { name?: string })?.name === 'AbortError') {
          clientLog('warn', 'player', 'autoplay abortado (load interrompeu) — re-tentará', { err: String(e) })
          autoplayTriedRef.current = false
          return
        }
        clientLog('warn', 'player', 'autoplay bloqueado, tentando mudo', { err: String(e) })
        v.muted = true
        v.play()
          .then(() => clientLog('info', 'player', 'autoplay ok (mudo)', {}))
          .catch((error_) => clientLog('error', 'player', 'autoplay falhou (nem mudo)', { err: String(error_) }))
      })
  }
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
      maybeAutoplayNative(v)
      return
    }
    if (resumePosition !== null && v.currentTime < 1 && resumePosition > 30 && !appliedAutoResumeRef.current) {
      appliedAutoResumeRef.current = true
      // Ask instead of silently jumping: the user picks "continue" or "restart"
      // via the overlay (see resume prompt). Mark applied so it only asks once.
      // DIAGNÓSTICO (temporário): este caminho NÃO auto-toca (espera o gesto no
      // prompt) — se aparecer muito, é a causa do "não tocou" em faixas c/ posição.
      clientLog('info', 'player', 'resume prompt mostrado (autoplay pulado)', { resumePosition })
      setShowResumePrompt(true)
      return
    }
    // Sem seek explícito nem prompt de resume → começa a tocar sozinho.
    maybeAutoplayNative(v)
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
    onProgress?.(now)
    updateBufferedRanges(v, now, setBufferedRanges, setBufferedEnd)
    tryAutoFavorite(watchedRef.current, isFavorite, AUTO_FAV_THRESHOLD, info, setIsFavorite)
    trySaveResume(now, incognito, libraryEntryID, lastResumeSaveRef, v.duration || 0)
    trySyncUrlPlayhead(now, lastUrlSyncRef)
    tryPrefetchNext({ v, now, nextVideoIdx: mediaQueue.nextIdx, info, prefetchedNextEpRef, onPrefetchNextPlaylist, prefetchedPlaylistN1Ref, onPrefetchNextNextPlaylist, prefetchedPlaylistN2Ref })
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

  // ─── Áudio simplificado ───────────────────────────────────────────────────
  // Player de áudio "pelado": <audio controls> com src DIRECT, sem Web Audio,
  // sem gapless/crossfade, sem HLS.js, sem <track>. A única diferença entre
  // origem local (rclone/disco) e torrent é a URL.
  const inPlaylist = !!playlist && playlist.items.length > 1
  const audioDirectSrc = useAudioDirectUrl(info, selectedFile, mediaToken)
  const activeMediaRef = audioMode ? audioRef : videoRef

  // Sidebar agregada da playlist (lista de faixas de vários itens). Resolução
  // adiada até blessed no iOS, igual antes, para não sufocar o byte-stream.
  const resolveEnabled = !iosAudio || blessed
  const aggregate = usePlaylistTracks(playlist?.items ?? [], playlist?.currentIndex ?? -1, info, inPlaylist && sidebarOpen, resolveEnabled)

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

  // Parse S/E pattern from filename for nicer episode labels — shared with the
  // sidebar's sort so labels and ordering agree.
  const parseEpisode = parseEpisodeTag

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

  // "Nenhum arquivo de vídeo": é só um aviso informativo — some sozinho após uns
  // segundos pra não ocupar a tela permanentemente. Reaparece a cada torrent novo.
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
  const currentEp = currentFile ? parseEpisode(currentFile.path) : null

  // In-torrent queue of the current file's kind (computed by useMediaQueue above).
  const mediaFileIndices = mediaQueue.indices
  const mediaCursor = mediaQueue.cursor

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
  const videoUrls = computeMediaUrls({ info, selectedFile, serverReady, mediaToken, transcodeAudio, forceH264, burnSubTrack, subActive, sidecarIdx, embeddedSub, customSubURL, localEmbeddedVttURL, caps, authEnabled, probe })
  const { streamURL, subtitleVttURL, vlcURL, iinaURL, infuseURL, directURL, encoderLabel, isTranscoded } = videoUrls

  const subtitleLabel = getSubtitleLabel(embeddedSub, subActive, autoSource, subLoading)

  // Container/overlay attributes for the modal shell. Extracted to a nested
  // helper so the minimized-vs-fullscreen ternaries live in their own scope
  // (keeps the component body's cognitive complexity down) — behavior unchanged.
  const shellProps = (): React.HTMLAttributes<HTMLDivElement> => {
    if (minimized) {
      // Áudio: barra fina full-width colada no footer (acima da nav inferior, se houver)
      // — o "mini-player" de música de verdade. Vídeo: card PiP no canto (um vídeo numa
      // barra fina não faz sentido).
      if (audioMode) {
        return { className: 'fixed inset-x-0 z-50', style: { bottom: 'var(--bottom-bar-h, 0px)' } }
      }
      return { className: 'fixed right-3 z-50 w-[360px] max-w-[calc(100vw-1.5rem)]', style: { bottom: 'calc(0.75rem + var(--bottom-bar-h, 0px) + env(safe-area-inset-bottom, 0px))' } }
    }
    // Full-viewport (deep-link tab): fill the whole browser window — solid black,
    // no padding, no backdrop-click-to-minimize (the tab is dedicated to playback;
    // exit is the Home button). Escape goes Home.
    if (fullViewport) {
      return {
        className: 'fixed inset-0 bg-black flex items-stretch justify-center z-50',
        onKeyDown: (e) => { if (e.key === 'Escape') (onHome ?? onClose)() },
        role: 'dialog',
        'aria-modal': 'true',
        tabIndex: -1,
      }
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

  // Corpo do player de ÁUDIO (capa + <audio> nativo + transporte). Extraído como
  // render fn aninhada (igual renderActiveStream) para não inflar a complexidade
  // cognitiva de renderActiveStream — todos os ternários de layout vivem aqui.
  const renderAudioBody = () => (
    <>
      {/* Capa do álbum preenche a caixa; a barra <audio controls> nativa fica
          LOGO ABAIXO (não esticada por cima da capa). */}
      <div className={minimized
        ? 'relative w-12 h-12 lg:w-14 lg:h-14 flex-shrink-0 bg-gradient-to-br from-gray-800 to-gray-900 rounded overflow-hidden'
        : 'relative w-full max-w-xl mx-auto h-44 sm:h-56 lg:h-72 xl:h-80 bg-gradient-to-br from-gray-800 to-gray-900 rounded-lg overflow-hidden'}>
        <AudioCoverArt info={info} selectedFile={selectedFile} mediaToken={mediaToken} />
      </div>
      <SimpleAudioPlayer
        src={audioDirectSrc}
        onEnded={handleVideoEnded}
        onTimeUpdate={handleAudioTimeUpdate}
        onPlaying={handlePlaybackStarted}
        onError={() => setVideoError(true)}
        elementRef={(el) => { audioRef.current = el }}
        className={minimized ? 'flex-1 min-w-0 basis-[55%] lg:basis-0' : 'max-w-xl mx-auto mt-2'}
      />
      {/* Controles ⏮⏭ + shuffle/repeat: a AudioTransportBar foi removida na
          simplificação e os controls nativos do <audio> não têm prev/next.
          Só botões que trocam a FAIXA (handlePrev/handleNext) — sem Web Audio. */}
      <SimpleAudioControls
        onPrev={handlePrev}
        onNext={handleNext}
        hasPrev={hasPrev}
        hasNext={hasNext}
        shuffle={shuffle}
        repeat={repeat}
        onToggleShuffle={onToggleShuffle}
        onCycleRepeat={onCycleRepeat}
        position={trackOrder.order.length > 1 ? `${trackOrder.cursor + 1} / ${trackOrder.order.length}` : undefined}
        className={minimized ? 'w-full !py-1 lg:w-auto lg:ml-auto' : ''}
      />
    </>
  )

  // The active-stream layout (video + controls + file picker). Extracted to a
  // nested render fn so its conditional blocks don't inflate the component's
  // cognitive complexity. Closes over all the local state — behavior identical.
  const renderActiveStream = () => {
    if (!info || selectedFile < 0) return null
    // A playlist with >1 item shows the aggregated track list (all items'
    // files); a single item (or no playlist) shows the per-torrent picker.
    const aggregateMode = !!playlist && playlist.items.length > 1
    const pickerState = computeFilePickerState({ info, minimized, sidebarOpen, aggregateMode })
    return (
      <div className="flex flex-col lg:flex-row flex-1 min-h-0">
        {/* Main column: video + transport + status + panels. On lg+ the
            file picker moves to a sidebar on the right — frees this
            column to grow without forcing the page into outer scroll.
            Audio mode centers its content vertically (lg:justify-center) so the
            cover + transport fill the modal height instead of hugging the top
            with a big empty gap below (the track sidebar makes the modal tall).
            It still scrolls when EQ/lyrics expand past the height. */}
        <div className={audioMode && minimized
          ? 'flex flex-row flex-wrap items-center gap-x-2 gap-y-1 px-2 py-1.5 min-w-0 lg:flex-nowrap lg:gap-x-4 lg:px-4'
          : ['flex flex-col min-w-0 lg:flex-1 lg:overflow-y-auto lg:overflow-x-hidden', audioMode ? 'lg:justify-center' : ''].join(' ')}>
        {/* Player de áudio simplificado ou vídeo completo. Áudio usa <audio>
            controls> com src DIRECT, espelhando o audiotest.html que toca no iOS.
            Vídeo mantém o player existente com HLS/transcode. */}
        {audioMode ? renderAudioBody() : (
          <VideoPlayerElement
            videoRef={videoRef}
            streamURL={streamURL}
            disableNativeAutoplay={disableNativeAutoplay}
            onPlaybackStarted={handlePlaybackStarted}
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
              if (v) {
                if (v.currentTime > 1.5) v.currentTime = 0
                v.play().catch(() => {})
              }
              setShowResumePrompt(false)
              setResumePosition(null)
            }}
          />
        )}

        {/* Everything below the video (transport, status, subtitle panel)
            is hidden in minimized mode — the native <video> controls cover
            play/pause/seek in the compact card. The <video> element itself
            stays mounted above, so all the HEVC/HLS/buffer logic is intact. */}
        {!minimized && (
          <PlayerControlsPanel
            info={info}
            audioMode={audioMode}
            currentFile={currentFile}
            mediaFileIndices={mediaFileIndices}
            mediaCursor={mediaCursor}
            onPrevMedia={handlePrev}
            onNextMedia={handleNext}
            hasPrevMedia={hasPrev}
            hasNextMedia={hasNext}
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
            onSeek={(sec) => { const el = activeMediaRef.current; if (el && Number.isFinite(sec)) el.currentTime = sec }}
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
            iinaURL={iinaURL}
            infuseURL={infuseURL}
            directURL={directURL}
            streamURL={streamURL}
            serverDownloadLoading={serverDownloadLoading}
            serverDownloadSuccess={serverDownloadSuccess}
            subOpen={subOpen}
            customSubName={customSubName}
            subError={subError}
            subResults={subResults}
            formatTime={formatTime}
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
            handleLocalDownload={handleLocalDownload}
            localDownloadLoading={localDownloadLoading}
            setSubOpen={setSubOpen}
            handleCustomSubtitleUpload={handleCustomSubtitleUpload}
            pickSubtitle={pickSubtitle}
          />
        )}
        </div>{/* end main column */}

        {/* Playlist mode: the sidebar AGGREGATES every item's files (a playlist
            is a collection of torrents/local files), not just the current
            torrent's. Single playback keeps the rich FilePickerSidebar. */}
        {!minimized && sidebarOpen && aggregateMode && (
          <PlaylistTracksSidebar
            groups={aggregate.groups}
            ensureLoaded={aggregate.ensureLoaded}
            currentItemIndex={playlist.currentIndex}
            selectedFile={selectedFile}
            playFile={playFile}
            onJump={(ii, fi) => onPlaylistJump?.(ii, fi)}
            onClose={() => setSidebarOpen(false)}
          />
        )}
        {info && pickerState.showFilePicker && (
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
            onDownloadFolder={isLocalHash(info.infoHash) ? undefined : downloadFolderFromPlayer}
            onDownloadDir={isLocalHash(info.infoHash) ? undefined : downloadDirFromPlayer}
          />
        )}

        {/* Collapsed-sidebar reopen tab — two variants:
            • lg+: slim vertical strip on the right edge of the modal.
            • mobile: horizontal bar below the video. Without this, iOS
              users who tap "Esconder lista" had no way to bring it back —
              the list literally vanished. (See issue #50.) */}
        {pickerState.showReopenTab && (
          <>
            {/* Mobile (and tablet up to lg): full-width bar */}
            <button
              onClick={() => setSidebarOpen(true)}
              title={t('player.modal.showFileList')}
              className="lg:hidden flex items-center justify-center gap-2 w-full px-4 py-2 border-t border-default bg-surface-elevated hover:bg-surface-tertiary text-text-secondary hover:text-text-primary text-xs flex-shrink-0"
            >
              <ChevronLeft className="w-4 h-4 rotate-90" />
              {t('player.modal.showFileListCount', { count: aggregateMode ? playlist.items.length : pickerState.fileCount })}
            </button>
            {/* lg+: vertical strip on the right edge */}
            <button
              onClick={() => setSidebarOpen(true)}
              title={t('player.modal.showFileList')}
              className="hidden lg:flex flex-col items-center justify-center w-8 border-l border-default bg-surface-elevated hover:bg-surface-tertiary text-text-secondary hover:text-text-primary flex-shrink-0"
            >
              <ChevronLeft className="w-4 h-4" />
              <span className="text-[10px] [writing-mode:vertical-rl] rotate-180 mt-2">
                {t('player.modal.filesCount', { count: aggregateMode ? playlist.items.length : pickerState.fileCount })}
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
      <div className={minimizedOrFullClass(minimized, audioMode, fullViewport)}>
        {/* Minimized (PiP) control strip — renderPlayerHeader returns null when
            minimized, which previously left the little card with NO way back to the
            full player. This bar restores the expand + close affordances. */}
        {minimized && (
          <div ref={miniBarRef} className="flex flex-col flex-shrink-0 bg-surface/80 border-b border-default touch-pan-y" title={t('player.modal.dragUpToExpand')}>
            {/* Grabber — sinaliza arrastar-pra-expandir (igual ao bottom-sheet). Sem
                onClick na faixa: no iOS um toque/arrasto disparava expand espúrio
                ("vai pra local nenhum"); expandir é só pelo gesto swipe-up ou o botão. */}
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
        {/* Top playlist bar is hidden in audio mode: the AudioTransportBar below
            already carries prev/next/shuffle/repeat + position, so showing both
            duplicated the controls above AND below the play button. */}
        {playlist && !audioMode && renderPlaylistBar(playlist, onPlaylistPrevious, onToggleShuffle, shuffle, onCycleRepeat, repeat, onPlaylistAdvance, t)}

        {/* Content. min-h-0 + flex-1 lets the inner active-stream block manage
            its own scroll regions (main column + sidebar) without the parent
            forcing an outer scrollbar. */}
        <div className="flex flex-col flex-1 min-h-0 overflow-hidden">
          {/* Big loading: only when we have NOTHING — no metadata cache hit AND
              streamAdd hasn't returned yet. If the cache primed `info`, we skip
              this and show the populated UI immediately with a slim inline
              "waiting on swarm" indicator instead (rendered further below). */}
          {loading && !info && (
            <div className="flex flex-col items-center justify-center py-16 text-text-secondary">
              <Loader2 className="w-10 h-10 animate-spin mb-4 text-green-500" />
              <p className="font-medium">{t('player.overlays.connectingSwarm')}</p>
              <p className="text-xs text-text-muted mt-2">{t('player.modal.firstTimePeers')}</p>
            </div>
          )}
          {/* Slim inline indicator: cached file list visible, swarm still warming up.
              The big buffering overlay over the video area covers the actual playback
              start; this strip is just to acknowledge that something is happening. */}
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
          {renderActiveStream()}

          {/* No video files in torrent — auto-dismisses after a few seconds.
              NÃO no modo áudio: um álbum não tem vídeo por definição, e numa playlist
              o aviso reabria a cada troca de faixa (info.infoHash muda). */}
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
      {/* Inline preview overlay for non-playable companion files (NFO, log,
          subtitles, PDFs shipped inside the torrent). Rendered outside the
          main modal box so its z-index can sit ABOVE the player without
          fighting flex layout. */}
      {previewFileIdx !== null && info?.files[previewFileIdx] && (() => {
        // Sibling images of the same torrent become prev/next navigation in
        // the image viewer (cover scans, artwork folders).
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
          barreira de propagação evita que o Escape/clique-fora do Sheet borbulhe
          pro shell do player (que minimizaria). nested → lockScroll=false + z-[70]. */}
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
