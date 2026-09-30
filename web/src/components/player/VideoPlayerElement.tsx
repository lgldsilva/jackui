import { useEffect, useState, useRef } from 'react'
import { Volume2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { TorrentInfo, streamArtworkURL, streamArtURL, resolveArt, isLocalHash, parseLocalHash, localAudioCoverURL, isIOS } from '../../api/client'
import { clientLog } from '../../lib/diag'
import Hls from 'hls.js'
import { useAirPlay } from './playerHooks'
import { canPlayNativeHls, audioElementKey } from './playerFormat'
import { shouldShowStartOverlay, shouldShowStartAudioOverlay } from './playerOverlay'
import { recoverHlsFatal, tryAutoplayMutedFallback, kickPastStartGap } from './mediaUrls'
import { wireHlsAudioSubs } from './hlsAudioTracks'
import { useSeamlessAudio } from './useSeamlessAudio'
import { usePersistedVolume } from './usePersistedVolume'
import { ResumePrompt, PlayerLoadingOverlay, TranscodingBadge, AirPlayButton, StartAudioOverlay } from './PlayerOverlays'
import { PlayerExperienceOverlays } from './PlayerExperienceOverlays'
import { HlsQualityMenu } from './HlsQualityMenu'
import type { MediaChapter } from '../../api/stream-types'

type VideoPlayerElementProps = {
  readonly videoRef: React.RefObject<HTMLVideoElement | null>
  readonly streamURL: string
  // engineActive: the gapless engine took over the audio (plays on its own <audio> elements). The
  // <video> then stays WITHOUT src and muted (the cover remains), so the audio isn't doubled.
  readonly engineActive?: boolean
  // disableNativeAutoplay: iOS-audio NOT yet started. Apple forbids play() of
  // audio-bearing media outside a gesture, so we do NOT fire non-gesture autoplay/nudge
  // (it would wedge the element at readyState 1, AbortError loop). We show the
  // "Play" overlay; the tap starts it. Flips false after the 1st play (blessed) → auto-advance.
  readonly disableNativeAutoplay?: boolean
  // onPlaybackStarted: fired on the element's 1st 'playing' event. On iOS it marks
  // "blessed" (user started via gesture) → unlocks auto-advance for the following tracks.
  readonly onPlaybackStarted?: () => void
  // suppressStartOverlay: there was already a track in this instance (music track
  // switch, not a cold open). Suppresses the "loading" spinner at the start of the
  // new track — the cover/seekbar stay; without this the spinner flashed on every switch.
  readonly suppressStartOverlay?: boolean
  readonly audioMode: boolean
  readonly subtitleVttURL: string
  // Phase 8 (multi-audio HLS master): ABSOLUTE index of the track chosen via the
  // SEAMLESS switch (null = default). Applied by hls.audioTrack / video.audioTracks without
  // recreating the player. Only takes effect when the master exposes >1 rendition; otherwise
  // the switch goes through the legacy ?audio=N path (streamURL) and this stays null.
  readonly seamlessAudioIndex?: number | null
  // probeAudioTracks: probe tracks (in order) to map absolute index →
  // position in the hls.js/WebKit rendition list. See hlsAudioTracks.ts.
  readonly probeAudioTracks?: readonly { index: number }[]
  // onHlsAudioCount reports how many audio tracks the engine exposed (hls.js or
  // native AudioTrackList) so the parent can decide seamless × reload. 0 when there's no HLS.
  readonly onHlsAudioCount?: (n: number) => void
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
  readonly duration?: number
  readonly chapters?: readonly MediaChapter[]
  readonly hasNext?: boolean
  readonly nextLabel?: string
  readonly onNext?: () => void
}

// Album cover shown behind the audio player (audio mode only). Extracted with
// its own audioMode/info guard so VideoPlayerElement's JSX loses the inline
// `audioMode && info &&` conditional → keeps its cognitive complexity under the
// gate. (The <track> elements stay inline in the <video> so the captions-track
// accessibility rule S4084 sees a literal child.)
// audioCoverURL picks the art source: a local file serves its EMBEDDED cover
// (the dedicated route, headerless via ?token=); a torrent uses the per-file
// extracted artwork. Both 204 when there's no picture (the <img> onError hides).
function audioCoverURL(info: TorrentInfo, selectedFile: number, mediaToken: string): string {
  if (isLocalHash(info.infoHash)) {
    const loc = parseLocalHash(info.infoHash)
    if (loc) return localAudioCoverURL(loc.mount, loc.path, mediaToken || undefined)
  }
  return streamArtworkURL(info.infoHash, selectedFile, mediaToken || undefined)
}

function AudioCoverArt({ audioMode, info, selectedFile, mediaToken }: {
  readonly audioMode: boolean
  readonly info: TorrentInfo | null
  readonly selectedFile: number
  readonly mediaToken: string
}) {
  // Fallback when the file has NO embedded picture: for torrents, kick the
  // server-side art chain (embedded → TMDB → WEB SEARCH, music-aware via the AI
  // MusicQuery) and show whatever it resolves — so an album with no cover tag
  // still gets art off the web instead of an empty box. Local files resolve the
  // web fallback server-side, so here they just hide on miss.
  const [fallbackSrc, setFallbackSrc] = useState('')
  const [hidden, setHidden] = useState(false)
  useEffect(() => { setFallbackSrc(''); setHidden(false) }, [info?.infoHash, selectedFile])
  if (!audioMode || !info) return null

  const handleError = async () => {
    if (fallbackSrc || isLocalHash(info.infoHash)) { setHidden(true); return }
    const src = await resolveArt(info.infoHash, -1, info.name).catch(() => null)
    if (src) setFallbackSrc(streamArtURL(info.infoHash))
    else setHidden(true)
  }

  const url = fallbackSrc || audioCoverURL(info, selectedFile, mediaToken)
  return (
    <div className="absolute inset-0 flex items-center justify-center bg-gradient-to-br from-gray-800 to-gray-900 pointer-events-none">
      <Volume2 className="absolute w-12 h-12 text-text-muted" />
      {!hidden && (
        <img
          key={url}
          src={url}
          alt=""
          className="relative max-h-full max-w-full object-contain rounded shadow-2xl"
          onError={handleError}
        />
      )}
    </div>
  )
}

// shouldAttachHlsJs: use hls.js (MSE) for this src? Only for HLS (.m3u8) in a browser
// that does NOT play HLS natively (Chrome/Firefox/Edge) and supports MSE. Safari/iOS
// play the .m3u8 natively; direct sources go straight into <video src>. Extracted out
// of the component to keep VideoPlayerElement's cognitive complexity
// well below the gate (the && chain weighed on the component body).
function shouldAttachHlsJs(streamURL: string): boolean {
  return !!streamURL && streamURL.includes('.m3u8') && !canPlayNativeHls() && Hls.isSupported()
}

// audioPreload: iOS/Safari (WebKit) doesn't fetch direct-play audio data without a gesture
// when preload is the mobile default ('metadata') → the 'canplay' event never fires
// and autoplay (tied to onCanPlay) wedges the element at readyState 2. 'auto' in the
// WebKit-audio case forces the fetch. Video and Chrome/Firefox keep the default. (Helper outside
// the component to keep VideoPlayerElement's cognitive complexity at the limit.)
function audioPreload(audioMode: boolean): 'auto' | undefined {
  return audioMode && canPlayNativeHls() ? 'auto' : undefined
}

// handleMetaLoaded: 'loadedmetadata' ALWAYS fires (iOS included). On WebKit
// (iOS/Safari) a stalled direct video parks at readyState 2 and 'canplay'
// (readyState ≥3) NEVER arrives → autoplay was never triggered and the video "loaded
// but didn't play" (confirmed in the logs: loadedmetadata → stalled rs2 → no 'autoplay
// try'). We call the kick here (= onVideoCanPlay, idempotent via autoplayTriedRef +
// seek/resume): the play()→muted-fallback unlocks rs2. Desktop/Chrome keep going on
// 'canplay' (which fires normally there, so the kick here is an idempotent no-op).
function handleMetaLoaded(v: HTMLVideoElement, onTimeUpdate: () => void, kickAutoplay: () => void, disableNativeAutoplay: boolean) {
  clientLog('info', 'player', 'loadedmetadata', { duration: v.duration, videoWidth: v.videoWidth, videoHeight: v.videoHeight, currentSrc: v.currentSrc })
  onTimeUpdate()
  if (canPlayNativeHls() && !disableNativeAutoplay) kickAutoplay()
}

function playerShellClass(audioMode: boolean): string {
  const base = 'bg-black relative w-full mx-auto flex items-center justify-center '
  // Audio: contained cover (max-w-xl). Video: 16:9 aspect via style.
  return base + (audioMode ? 'h-44 sm:h-56 lg:h-72 xl:h-80 max-w-xl' : 'max-h-[70dvh] sm:max-h-[58dvh]')
}

function videoSrcAttr(iosNative: boolean, engineActive: boolean, useHlsJs: boolean, streamURL: string): string | undefined {
  if (iosNative || engineActive || useHlsJs) return undefined
  return streamURL || undefined
}

function attachHlsJs(
  video: HTMLVideoElement,
  streamURL: string,
  onHlsAudioCount: ((n: number) => void) | undefined,
  hlsRef: React.MutableRefObject<Hls | null>,
  onHls: (hls: Hls | null) => void,
): () => void {
  // Modest forward buffer: on-demand transcode + seek-restart — requesting
  // fragments far from the transcoder forces an expensive restart.
  const hls = new Hls({
    enableWorker: true,
    lowLatencyMode: false,
    startPosition: 0,
    // Lowest rung first: torrent pieces are still arriving; 1080p/5 Mbps
    // cold-start is the stall users feel. hls.js sorts levels by bitrate.
    startLevel: 0,
    testBandwidth: false,
    maxBufferLength: 20,
    maxMaxBufferLength: 40,
    backBufferLength: 30,
    fragLoadingTimeOut: 60000,
    manifestLoadingTimeOut: 30000,
  })
  hls.on(Hls.Events.ERROR, (_evt, data) => recoverHlsFatal(hls, data))
  hls.on(Hls.Events.MANIFEST_PARSED, () => { tryAutoplayMutedFallback(video) })
  wireHlsAudioSubs(hls, onHlsAudioCount)
  hlsRef.current = hls
  onHls(hls)
  hls.loadSource(streamURL)
  hls.attachMedia(video)
  return () => {
    hlsRef.current = null
    onHls(null)
    onHlsAudioCount?.(0)
    hls.destroy()
  }
}

function nudgeOnGap(
  videoRef: React.RefObject<HTMLVideoElement | null>,
  suppressNudge: boolean,
  label: string,
): void {
  const v = videoRef.current
  if (!v || suppressNudge) return
  if (kickPastStartGap(v)) clientLog('info', 'player', label, { currentTime: v.currentTime })
}

export function VideoPlayerElement({
  videoRef,
  streamURL,
  engineActive = false,
  disableNativeAutoplay = false,
  onPlaybackStarted,
  suppressStartOverlay = false,
  audioMode,
  subtitleVttURL,
  seamlessAudioIndex = null,
  probeAudioTracks,
  onHlsAudioCount,
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
  duration = 0,
  chapters,
  hasNext = false,
  nextLabel,
  onNext,
}: VideoPlayerElementProps) {
  const { t } = useTranslation()
  // HLS (.m3u8) plays natively only on WebKit. Chrome/Firefox/Edge use hls.js.
  // With the gapless engine the <video> has no src → hls.js never attaches.
  const useHlsJs = !engineActive && shouldAttachHlsJs(streamURL)
  const hlsRef = useRef<Hls | null>(null)
  const [hlsInstance, setHlsInstance] = useState<Hls | null>(null)
  useEffect(() => {
    const v = videoRef.current
    if (!v || !useHlsJs || !streamURL) return
    return attachHlsJs(v, streamURL, onHlsAudioCount, hlsRef, setHlsInstance)
  }, [videoRef, streamURL, useHlsJs, onHlsAudioCount])

  useSeamlessAudio({ videoRef, hlsRef, engineActive, useHlsJs, streamURL, seamlessAudioIndex, probeAudioTracks, onHlsAudioCount })
  // Restore mute/volume onto this element — a new one is mounted on every
  // file/mode switch (see the `key` below), which used to reset both.
  usePersistedVolume({ mediaRef: videoRef, forceMuted: engineActive, elementKey: audioElementKey(audioMode, isTranscoded) })
  const airplay = useAirPlay(videoRef, streamURL)

  const suppressNudge = disableNativeAutoplay && !isTranscoded
  const [startOverlayDismissed, setStartOverlayDismissed] = useState(false)
  useEffect(() => { setStartOverlayDismissed(false) }, [streamURL])
  const showStartAudioOverlay = shouldShowStartAudioOverlay({
    disableNativeAutoplay, startOverlayDismissed, videoError, showResumePrompt, currentTime,
  })
  const iosNative = isIOS() && !engineActive && !useHlsJs
  const attachedSrcRef = useRef('')
  useEffect(() => {
    const v = videoRef.current
    if (!v || !iosNative || !streamURL || disableNativeAutoplay) return
    if (attachedSrcRef.current === streamURL) return
    attachedSrcRef.current = streamURL
    v.src = streamURL
  }, [videoRef, iosNative, streamURL, disableNativeAutoplay])

  const startAudioPlayback = () => {
    const v = videoRef.current
    if (!v) return
    setStartOverlayDismissed(true)
    clientLog('info', 'player', 'tap "Play" (gesture) → src+play()', { readyState: v.readyState })
    if (streamURL) { attachedSrcRef.current = streamURL; v.src = streamURL }
    v.play()
      .then(() => clientLog('info', 'player', 'tap-to-play ok (sound)', { readyState: v.readyState }))
      .catch((e) => {
        setStartOverlayDismissed(false)
        clientLog('warn', 'player', 'tap-to-play falhou', { name: (e as { name?: string })?.name, err: String(e) })
      })
  }

  const showLoading = shouldShowStartOverlay({
    videoError, engineActive, suppressStartOverlay, disableNativeAutoplay, currentTime, bufferedEnd,
  })
  const showResume = showResumePrompt && resumePosition !== null

  return (
    <div className={playerShellClass(audioMode)} style={audioMode ? undefined : { aspectRatio: '16 / 9' }}>
      <AudioCoverArt audioMode={audioMode} info={info} selectedFile={selectedFile} mediaToken={mediaToken} />
      {showResume && (
        <ResumePrompt
          resumePosition={resumePosition!}
          formatTime={formatTime}
          onContinue={onResumeContinue}
          onRestart={onResumeRestart}
        />
      )}
      {showLoading && (
        <PlayerLoadingOverlay
          serverReady={serverReady}
          resumePosition={resumePosition}
          info={info}
          selectedFile={selectedFile}
          isTranscoded={isTranscoded}
          transcodeFallbackAttempted={transcodeFallbackAttempted}
          formatTime={formatTime}
        />
      )}
      {showStartAudioOverlay && <StartAudioOverlay onPlay={startAudioPlayback} />}
      <TranscodingBadge attempted={transcodeFallbackAttempted} videoError={videoError} />
      <AirPlayButton airplay={airplay} videoError={videoError} />
      <HlsQualityMenu hls={hlsInstance} />
      <PlayerExperienceOverlays
        videoRef={videoRef}
        chapters={chapters}
        currentTime={currentTime}
        duration={duration}
        hasNext={hasNext}
        nextLabel={nextLabel}
        audioMode={audioMode}
        videoError={videoError}
        showResumePrompt={showResume}
        onNext={onNext}
      />
      {!videoError && (
        <video
          key={audioElementKey(audioMode, isTranscoded)}
          ref={videoRef}
          src={videoSrcAttr(iosNative, engineActive, useHlsJs, streamURL)}
          muted={engineActive}
          controls={!audioMode}
          autoPlay={!disableNativeAutoplay}
          preload={iosNative ? 'none' : audioPreload(audioMode)}
          playsInline
          {...{ 'webkit-playsinline': 'true', 'x-webkit-airplay': 'allow' } as any}
          className={`max-h-full max-w-full${audioMode ? ' w-full h-full' : ''}`}
          onError={onVideoError}
          onLoadStart={() => clientLog('info', 'player', 'loadstart', { src: streamURL })}
          onStalled={() => {
            clientLog('warn', 'player', 'stalled', videoDiagnostic())
            nudgeOnGap(videoRef, suppressNudge, 'start-gap nudge (stalled)')
          }}
          onWaiting={() => {
            clientLog('info', 'player', 'waiting (buffering)', { readyState: videoRef.current?.readyState })
            nudgeOnGap(videoRef, suppressNudge, 'start-gap nudge (waiting)')
          }}
          onTimeUpdate={onTimeUpdate}
          onLoadedMetadata={(e) => handleMetaLoaded(e.currentTarget, onTimeUpdate, onVideoCanPlay, disableNativeAutoplay)}
          onProgress={() => {
            nudgeOnGap(videoRef, suppressNudge, 'start-gap nudge (progress)')
            onTimeUpdate()
          }}
          onEnded={onVideoEnded}
          onCanPlay={onVideoCanPlay}
          onPlaying={onPlaybackStarted}
        >
          <track
            kind={subtitleVttURL ? 'subtitles' : 'metadata'}
            src={subtitleVttURL || ''}
            srcLang={subtitleVttURL ? 'pt' : ''}
            label={subtitleVttURL ? t('player.video.subtitleTrackLabel') : ''}
            default
          />
          <track kind="captions" srcLang="pt" label={t('player.video.captionsTrackLabel')} />
        </video>
      )}
      {videoError && renderVideoError()}
    </div>
  )
}
