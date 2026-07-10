import { useTranslation } from 'react-i18next'
import { ChevronLeft } from 'lucide-react'
import { TorrentInfo, StreamProbe, isLocalHash } from '../../api/client'
import { useHoverThumb } from '../FileThumbHover'
import { formatTime, parseEpisodeTag, type FileType } from './playerFormat'
import { computeMediaUrls } from './mediaUrls'
import { computeFilePickerState } from './filePickerVisibility'
import { VideoPlayerElement } from './VideoPlayerElement'
import { FilePickerSidebar } from './FilePickerSidebar'
import { PlaylistTracksSidebar } from './PlaylistTracksSidebar'
import { PlayerControlsPanel } from './PlayerControlsPanel'
import { SimpleAudioPlayer } from './SimpleAudioPlayer'
import { SimpleAudioControls } from './SimpleAudioControls'
import { AudioCoverArt } from './AudioCoverArt'
import { useSubtitles } from './useSubtitles'
import { useTrackOrder } from './useTrackOrder'
import { usePlaylistTracks } from './usePlaylistTracks'
import { usePlayerDownloads } from './usePlayerDownloads'
import type { PlaylistMeta } from './playerTypes'

type Setter<T> = React.Dispatch<React.SetStateAction<T>>

// The active-stream layout (video/audio + controls + file picker) extracted from
// PlayerModal. It closes over nothing — everything it needs (state, setters,
// handlers and the cohesive hook returns) is passed explicitly, so behaviour is
// identical to the old nested render fns (renderActiveStream + renderAudioBody).
export function ActiveStreamView(props: Readonly<{
  subs: ReturnType<typeof useSubtitles>
  videoUrls: ReturnType<typeof computeMediaUrls>
  downloads: ReturnType<typeof usePlayerDownloads>
  trackOrder: ReturnType<typeof useTrackOrder>
  aggregate: ReturnType<typeof usePlaylistTracks>
  hoverThumb: ReturnType<typeof useHoverThumb>
  info: TorrentInfo
  selectedFile: number
  playlist: PlaylistMeta | null | undefined
  minimized: boolean
  sidebarOpen: boolean
  audioMode: boolean
  videoRef: React.RefObject<HTMLVideoElement | null>
  audioRef: React.MutableRefObject<HTMLAudioElement | null>
  selectedFileRef: React.RefObject<HTMLButtonElement>
  activeMediaRef: React.RefObject<HTMLMediaElement | null>
  mediaToken: string
  serverReady: boolean
  videoError: boolean
  currentTime: number
  duration: number
  bufferedEnd: number
  bufferedRanges: Array<[number, number]>
  disableNativeAutoplay: boolean
  showResumePrompt: boolean
  resumePosition: number | null
  transcodeFallbackAttempted: boolean
  probe: StreamProbe | null
  subEnabled: boolean
  showMobileOpts: boolean
  playbackSpeed: number
  currentFile: TorrentInfo['files'][number] | null | undefined
  currentEp: string | null
  videoFiles: TorrentInfo['files']
  mediaFileIndices: number[]
  mediaCursor: number
  fileFilter: string
  fileTypeFilter: FileType
  fileSortBySize: boolean
  fileSizeDesc: boolean
  transcodeAudio: number | null
  forceH264: boolean
  burnSubTrack: number | null
  shuffle: boolean
  repeat: 'none' | 'one' | 'all'
  audioDirectSrc: string
  setShowResumePrompt: Setter<boolean>
  setResumePosition: Setter<number | null>
  setVideoError: Setter<boolean>
  setShowMobileOpts: Setter<boolean>
  setPlaybackSpeed: Setter<number>
  setTranscodeAudio: Setter<number | null>
  setForceH264: Setter<boolean>
  setBurnSubTrack: Setter<number | null>
  setFileFilter: Setter<string>
  setFileTypeFilter: Setter<FileType>
  setFileSortBySize: (v: boolean) => void
  setFileSizeDesc: (v: boolean) => void
  setSidebarOpen: Setter<boolean>
  setPreviewFileIdx: Setter<number | null>
  renderVideoError: () => React.ReactNode
  videoDiagnostic: () => Record<string, unknown>
  onVideoError: () => void
  onTimeUpdate: () => void
  onVideoEnded: () => void
  onVideoCanPlay: () => void
  onPlaybackStarted: () => void
  onAudioTimeUpdate: (currentTime: number, duration: number) => void
  handlePrev: () => void
  handleNext: () => void
  hasPrev: boolean
  hasNext: boolean
  handleRequestFullscreen: () => void
  playFile: (idx: number) => void
  onToggleShuffle?: () => void
  onCycleRepeat?: () => void
  onPlaylistJump?: (itemIndex: number, fileIndex?: number) => void
}>) {
  const {
    subs, videoUrls, downloads, trackOrder, aggregate, hoverThumb,
    info, selectedFile, playlist, minimized, sidebarOpen, audioMode,
    videoRef, audioRef, selectedFileRef, activeMediaRef,
    mediaToken, serverReady, videoError, currentTime, duration, bufferedEnd, bufferedRanges,
    disableNativeAutoplay, showResumePrompt, resumePosition, transcodeFallbackAttempted,
    probe, subEnabled, showMobileOpts, playbackSpeed,
    currentFile, currentEp, videoFiles, mediaFileIndices, mediaCursor,
    fileFilter, fileTypeFilter, fileSortBySize, fileSizeDesc,
    transcodeAudio, forceH264, burnSubTrack, shuffle, repeat, audioDirectSrc,
    setShowResumePrompt, setResumePosition, setVideoError, setShowMobileOpts, setPlaybackSpeed,
    setTranscodeAudio, setForceH264, setBurnSubTrack, setFileFilter, setFileTypeFilter,
    setFileSortBySize, setFileSizeDesc, setSidebarOpen, setPreviewFileIdx,
    renderVideoError, videoDiagnostic, onVideoError, onTimeUpdate, onVideoEnded, onVideoCanPlay,
    onPlaybackStarted, onAudioTimeUpdate, handlePrev, handleNext, hasPrev, hasNext, handleRequestFullscreen,
    playFile, onToggleShuffle, onCycleRepeat, onPlaylistJump,
  } = props
  const { t } = useTranslation()
  const {
    subOpen, subResults, subLoading, subError, subActive, subOffset, autoSource,
    embeddedSub, customSubName, sidecars, sidecarIdx, subtitleLabel,
    setSubOpen, setSubActive, setEmbeddedSub, setSidecarIdx, setAutoSource,
    handleCustomSubtitleUpload, clearCustomSub, openSubtitlePanel, pickSubtitle,
    adjustSubOffset, resetSubOffset,
  } = subs
  const { streamURL, subtitleVttURL, vlcURL, iinaURL, infuseURL, directURL, isTranscoded } = videoUrls
  const { serverDownloadLoading, serverDownloadSuccess, localDownloadLoading, handleServerDownload, handleLocalDownload, downloadFolderFromPlayer, downloadDirFromPlayer } = downloads
  const parseEpisode = parseEpisodeTag

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
        onEnded={onVideoEnded}
        onTimeUpdate={onAudioTimeUpdate}
        onPlaying={onPlaybackStarted}
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

  if (selectedFile < 0) return null
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
          onPlaybackStarted={onPlaybackStarted}
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
          onVideoError={onVideoError}
          onTimeUpdate={onTimeUpdate}
          onVideoEnded={onVideoEnded}
          onVideoCanPlay={onVideoCanPlay}
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
      {!minimized && sidebarOpen && aggregateMode && playlist && (
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
            {t('player.modal.showFileListCount', { count: aggregateMode && playlist ? playlist.items.length : pickerState.fileCount })}
          </button>
          {/* lg+: vertical strip on the right edge */}
          <button
            onClick={() => setSidebarOpen(true)}
            title={t('player.modal.showFileList')}
            className="hidden lg:flex flex-col items-center justify-center w-8 border-l border-default bg-surface-elevated hover:bg-surface-tertiary text-text-secondary hover:text-text-primary flex-shrink-0"
          >
            <ChevronLeft className="w-4 h-4" />
            <span className="text-[10px] [writing-mode:vertical-rl] rotate-180 mt-2">
              {t('player.modal.filesCount', { count: aggregateMode && playlist ? playlist.items.length : pickerState.fileCount })}
            </span>
          </button>
        </>
      )}
    </div>
  )
}
