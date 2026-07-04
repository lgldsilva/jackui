import { Loader2, Users, Activity, Subtitles, Maximize2, Minus, Plus, RotateCcw, FastForward, ChevronDown, ChevronRight, Laptop, Download } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { TorrentInfo, Subtitle, StreamProbe, SidecarSubtitle, isLocalHash } from '../../api/client'
import { LocalCacheButton } from './LocalCacheButton'
import { ExternalPlayerMenu } from './ExternalPlayerMenu'
import { formatRate } from '../../lib/format'
import { formatSize, subtitleButtonTitle, subtitleBtnClass, serverDownloadIcon, SPEED_OPTIONS } from './playerFormat'
import { MediaNavButtons } from './PlayerOverlays'
import { EmbeddedTracksPanel } from './EmbeddedTracksPanel'
import { ChaptersPanel, ChapterNavButtons } from './ChaptersPanel'
import { Sheet } from '../Sheet'
import { SubtitlePicker } from './SubtitlePicker'

type PlayerControlsPanelProps = {
  readonly info: TorrentInfo
  readonly audioMode: boolean
  readonly currentFile: TorrentInfo['files'][number] | null | undefined
  readonly mediaFileIndices: number[]
  readonly mediaCursor: number
  readonly onPrevMedia: () => void
  readonly onNextMedia: () => void
  readonly hasPrevMedia: boolean
  readonly hasNextMedia: boolean
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
  readonly onSeek: (sec: number) => void
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
  readonly iinaURL?: string
  readonly infuseURL?: string
  // Absolute direct-play HTTP stream URL (?token=, no transcode) — the payload
  // copied by the "Copy URL" entry in the external-player menu.
  readonly directURL: string
  readonly streamURL: string
  readonly serverDownloadLoading: boolean
  readonly serverDownloadSuccess: boolean
  readonly subOpen: boolean
  readonly customSubName: string | null
  readonly subError: string
  readonly subResults: Subtitle[]
  readonly formatTime: (s: number) => string
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
  readonly handleLocalDownload: () => void
  readonly localDownloadLoading: boolean
  readonly setSubOpen: (v: boolean) => void
  readonly handleCustomSubtitleUpload: (e: React.ChangeEvent<HTMLInputElement>) => void
  readonly pickSubtitle: (s: Subtitle) => void
}

// Everything below the <video> when expanded: transport row (series nav + time
// + subtitle offset), the mobile "Opções" collapse, the status/buffer bar, the
// embedded-tracks panel, the action bar (subtitle/VLC/download), and the
// OpenSubtitles picker. Hidden entirely while minimized.
export function PlayerControlsPanel({
  info,
  audioMode,
  currentFile,
  mediaFileIndices,
  mediaCursor,
  onPrevMedia,
  onNextMedia,
  hasPrevMedia,
  hasNextMedia,
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
  onSeek,
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
  iinaURL,
  infuseURL,
  directURL,
  streamURL,
  serverDownloadLoading,
  serverDownloadSuccess,
  subOpen,
  customSubName,
  subError,
  subResults,
  formatTime,
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
  handleLocalDownload,
  localDownloadLoading,
  setSubOpen,
  handleCustomSubtitleUpload,
  pickSubtitle,
}: PlayerControlsPanelProps) {
  const { t } = useTranslation()
  return (
    <>
      {/* Transport row — ONE line. The native <video controls> already
          provides the seek bar, play/pause and ±skip, so we keep only
          what it lacks: series navigation (prev/next episode) and a time
          readout. "Back to start" / "resume" are now offered as a prompt
          on play (see resume overlay); ±10s removed (native bar seeks).
          Hidden in audio mode (no episode nav, the native controls already
          show the time) unless a subtitle offset control needs to show —
          frees a whole row for the track list. */}
      {(!audioMode || subActive) && (
      <div className="px-3 sm:px-4 py-2 bg-surface border-b border-default flex items-center gap-2 min-w-0">
        <MediaNavButtons
          mediaFileIndices={mediaFileIndices}
          mediaCursor={mediaCursor}
          currentEp={currentEp}
          onPrevMedia={onPrevMedia}
          onNextMedia={onNextMedia}
          hasPrevMedia={hasPrevMedia}
          hasNextMedia={hasNextMedia}
        />
        {/* Chapter prev/next — only when the probe found real chapters */}
        {!audioMode && (probe?.chapters?.length ?? 0) > 1 && (
          <ChapterNavButtons chapters={probe!.chapters!} currentTime={currentTime} onSeek={onSeek} />
        )}
        <span className="text-xs text-text-secondary ml-auto font-mono tabular-nums flex-shrink-0">
          {formatTime(currentTime)} <span className="text-text-muted">/</span> {formatTime(duration)}
        </span>

        {/* Subtitle offset controls — only visible when sub active */}
        {subActive && (
          <div className="flex items-center gap-1 ml-auto bg-surface-secondary border border-default rounded-lg px-2 py-0.5">
            <span className="text-[10px] text-text-muted uppercase tracking-wide mr-1">{t('player.controls.subtitle')}</span>
            <button
              onClick={() => adjustSubOffset(-0.1)}
              title={t('player.controls.delaySub')}
              className="text-text-secondary hover:text-blue-400 p-1 transition-colors"
            >
              <Minus className="w-3 h-3" />
            </button>
            <span className="text-xs text-text-primary font-mono tabular-nums min-w-[40px] text-center">
              {subOffset >= 0 ? '+' : ''}{subOffset.toFixed(1)}s
            </span>
            <button
              onClick={() => adjustSubOffset(0.1)}
              title={t('player.controls.advanceSub')}
              className="text-text-secondary hover:text-blue-400 p-1 transition-colors"
            >
              <Plus className="w-3 h-3" />
            </button>
            {subOffset !== 0 && (
              <button
                onClick={resetSubOffset}
                title={t('player.controls.resetOffset')}
                className="text-text-muted hover:text-text-primary p-1 transition-colors"
              >
                <RotateCcw className="w-3 h-3" />
              </button>
            )}
          </div>
        )}
      </div>
      )}

      {/* Mobile-only toggle that collapses everything below (status,
          transcode controls, subtitle picker, VLC/download) so the file
          list sits right under the video. Desktop shows it all inline. */}
      <button
        onClick={() => setShowMobileOpts(v => !v)}
        className="sm:hidden flex items-center justify-center gap-1.5 w-full px-4 py-2.5 border-b border-default bg-surface/40 text-text-primary text-sm active:bg-surface-secondary"
      >
        {showMobileOpts ? <ChevronDown className="w-4 h-4" /> : <ChevronRight className="w-4 h-4" />}
        {showMobileOpts ? t('player.controls.hideOptions') : t('player.controls.showOptions')}
      </button>

      {/* Secondary controls — collapsed on mobile unless toggled, always
          shown on desktop. */}
      <div className={showMobileOpts ? 'flex flex-col' : 'hidden sm:flex sm:flex-col'}>
        {/* Status bar with buffer + torrent progress. `relative` lets the
            hover preview bubble (absolute) anchor inside this container. */}
        <div className="relative px-3 sm:px-4 py-3 bg-surface/50 border-b border-default flex flex-col gap-2 text-xs">
          <div className="flex items-center gap-3 flex-wrap">
            <span className="flex items-center gap-1.5 text-text-primary">
              <Users className="w-3.5 h-3.5 text-green-400" />
              {info.seeders} <span className="text-text-muted hidden sm:inline">seeders</span>
              <span className="text-text-muted">/</span> {info.peers} <span className="text-text-muted hidden sm:inline">peers</span>
            </span>
            <span className="flex items-center gap-1.5 text-text-primary">
              <Activity className="w-3.5 h-3.5 text-blue-400" />
              {(info.progress * 100).toFixed(1)}%<span className="text-text-muted hidden sm:inline ml-1">torrent</span>
            </span>
            <span className="flex items-center gap-1.5 text-text-primary tabular-nums">
              <span className="text-green-400">↓</span> {formatRate(info.downRate)}
              <span className="text-yellow-400 ml-1">↑</span> {formatRate(info.upRate)}
            </span>
            <label className="flex items-center gap-1 text-text-secondary" title={t('player.controls.speedHint')}>
              <FastForward className="w-3.5 h-3.5 text-text-muted" />
              <select
                value={playbackSpeed}
                onChange={e => setPlaybackSpeed(Number.parseFloat(e.target.value))}
                className="bg-surface-secondary border border-default rounded px-1 py-0.5 text-xs text-text-primary tabular-nums focus:outline-none focus:border-green-500"
              >
                {SPEED_OPTIONS.map(s => (
                  <option key={s} value={s}>{s}x</option>
                ))}
              </select>
            </label>
            {currentFile && (
              <span className="text-text-secondary">
                {formatSize(currentFile.downloaded)} / {formatSize(currentFile.size)}
              </span>
            )}
            {bufferedEnd > 0 && duration > 0 && (
              <span className="text-text-secondary ml-auto">
                {t('player.controls.buffer')}: <span className="text-blue-400">{formatTime(bufferedEnd - currentTime)}</span> {t('player.controls.ahead')}
              </span>
            )}
          </div>
          {/* Load/buffer indicator — PRESENTATION ONLY (not clickable).
              The native <video controls> bar owns seeking; this strip just
              visualises state so it doesn't compete with it: gray = torrent
              downloaded, blue islands = buffered/ready (disjoint after a #61
              seek-restart, gaps = not loaded yet), green = play progress. */}
          <div className="relative bg-surface-tertiary rounded-full h-1.5">
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

        {/* Chapter markers — only worth showing when there's more than one */}
        {probe?.chapters && probe.chapters.length > 1 && (
          <ChaptersPanel
            chapters={probe.chapters}
            currentTime={currentTime}
            onSeek={onSeek}
            formatTime={formatTime}
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
            title={t('player.controls.fullscreenTitle')}
            className="flex items-center gap-1.5 text-xs bg-surface-tertiary hover:bg-surface-tertiary text-text-primary px-3 py-1.5 rounded-lg transition-colors"
          >
            <Maximize2 className="w-3.5 h-3.5" />
            {t('player.controls.fullscreen')}
          </button>
          {/* External players consolidated into a single "Open in ▾" split
              button (VLC/IINA/Infuse + Copy URL). It remembers the last choice
              so the primary click reopens it; the caret switches. Each entry
              builds the SAME scheme/playlist URL as the old per-app buttons. */}
          <ExternalPlayerMenu urls={{ vlcURL, iinaURL: iinaURL ?? '', infuseURL: infuseURL ?? '', directURL }} />
          {isLocalHash(info.infoHash) ? (
            // Local/rclone file: there's no torrent to "baixar no servidor".
            // Instead, cache the whole file to local disk (instant + seekable).
            <LocalCacheButton hash={info.infoHash} />
          ) : (
            <button
              onClick={handleServerDownload}
              disabled={serverDownloadLoading || serverDownloadSuccess}
              className={`flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-lg transition-colors border ${
                serverDownloadSuccess
                  ? 'bg-emerald-500/20 text-emerald-400 border-emerald-500/30'
                  : 'bg-green-500/20 hover:bg-green-500/30 text-green-700 dark:text-green-300 border-green-500/30'
              }`}
              title={t('player.controls.serverDownloadTitle')}
            >
              {serverDownloadIcon(serverDownloadLoading, serverDownloadSuccess)}
              <span>
                {serverDownloadSuccess ? t('player.controls.added') : t('player.controls.serverDownload')}
              </span>
            </button>
          )}
          {globalThis.electronAPI && (
            <button
              onClick={handleLocalDownload}
              disabled={localDownloadLoading}
              className="flex items-center gap-1.5 text-xs bg-indigo-500/20 hover:bg-indigo-500/30 text-indigo-700 dark:text-indigo-300 border border-indigo-500/30 px-3 py-1.5 rounded-lg transition-colors"
              title={t('player.controls.localDownloadTitle')}
            >
              <Laptop className="w-3.5 h-3.5" />
              {localDownloadLoading ? t('player.controls.downloading') : t('player.controls.localDownload')}
            </button>
          )}
          <a
            href={streamURL}
            download
            className="flex items-center gap-1.5 text-xs bg-surface-tertiary hover:bg-surface-tertiary text-text-primary px-3 py-1.5 rounded-lg transition-colors"
          >
            <Download className="w-3.5 h-3.5" />
            <span className="hidden sm:inline">{t('player.controls.downloadDirect')}</span>
            <span className="sm:hidden">{t('player.controls.download')}</span>
          </a>
          <span className="text-xs text-text-muted ml-auto hidden sm:block">
            {info.files.length} {info.files.length === 1 ? t('player.files.file') : t('player.files.files')} • {formatSize(info.totalSize)}
          </span>
        </div>

      </div>

      {/* Seletor de legendas — bottom-sheet no mobile (lista E filtro acessíveis,
          que o painel embutido abaixo da dobra não permitia) e card centralizado
          no desktop. z-[60] pra ficar acima do modal do player. */}
      <Sheet
        open={subOpen}
        onClose={() => setSubOpen(false)}
        title={t('player.subtitles.sheetTitle')}
        icon={<Subtitles className="w-4 h-4 text-blue-400" />}
        zClass="z-[60]"
      >
        <SubtitlePicker
          handleCustomSubtitleUpload={handleCustomSubtitleUpload}
          customSubName={customSubName}
          clearCustomSub={clearCustomSub}
          subLoading={subLoading}
          subError={subError}
          subResults={subResults}
          subActive={subActive}
          pickSubtitle={pickSubtitle}
          setSubActive={setSubActive}
        />
      </Sheet>
    </>
  )
}
