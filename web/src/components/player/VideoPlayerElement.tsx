import { useEffect } from 'react'
import { Volume2 } from 'lucide-react'
import { TorrentInfo, streamArtworkURL, isLocalHash, parseLocalHash, localAudioCoverURL } from '../../api/client'
import { clientLog } from '../../lib/diag'
import Hls from 'hls.js'
import { useAirPlay } from './playerHooks'
import { canPlayNativeHls } from './playerFormat'
import { recoverHlsFatal, tryAutoplayMutedFallback, kickPastStartGap } from './mediaUrls'
import { ResumePrompt, PlayerLoadingOverlay, TranscodingBadge, AirPlayButton } from './PlayerOverlays'

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
  if (!audioMode || !info) return null
  return (
    <div className="absolute inset-0 flex items-center justify-center bg-gradient-to-br from-gray-800 to-gray-900 pointer-events-none">
      <Volume2 className="absolute w-12 h-12 text-text-muted" />
      <img
        src={audioCoverURL(info, selectedFile, mediaToken)}
        alt=""
        className="relative max-h-full max-w-full object-contain"
        onError={(e) => { e.currentTarget.style.display = 'none' }}
      />
    </div>
  )
}

export function VideoPlayerElement({
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
  // HLS (.m3u8) toca nativo só no WebKit (Safari + qualquer browser iOS). Chrome/
  // Firefox/Edge desktop precisam do hls.js pra tocar o MESMO HLS-VOD — é o que
  // lhes dá seek e evita o caminho progressive frágil. Fontes diretas/progressive
  // vão direto no <video src>. A condição abaixo TEM que casar com o src= do
  // <video> pra nunca setar os dois ao mesmo tempo.
  const useHlsJs = !!streamURL && streamURL.includes('.m3u8') && !canPlayNativeHls() && Hls.isSupported()
  useEffect(() => {
    const v = videoRef.current
    if (!v || !useHlsJs || !streamURL) return
    // Buffer dianteiro modesto: como é transcode sob demanda atrás de um servidor
    // com seek-restart, pedir fragmentos muito à frente do que o transcoder já
    // produziu força um seek-restart caro (a cascata vista no Chrome/Firefox).
    const hls = new Hls({
      enableWorker: true,
      lowLatencyMode: false,
      startPosition: 0,
      testBandwidth: false,
      maxBufferLength: 20,
      maxMaxBufferLength: 40,
      backBufferLength: 30,
      fragLoadingTimeOut: 60000,
      manifestLoadingTimeOut: 30000,
    })
    // Recupera de erros transitórios (buracos enquanto o transcoder reinicia) em
    // vez de mostrar a UI de erro fatal.
    hls.on(Hls.Events.ERROR, (_evt, data) => recoverHlsFatal(hls, data))
    // Autoplay: o atributo autoPlay não dispara sozinho no hls.js (a fonte é
    // anexada via MSE de forma async, fora do gesto de abertura). Ao parsear o
    // manifest, tenta tocar; se o browser bloquear sem áudio mudo (NotAllowed),
    // tenta de novo mudado (autoplay mudo é sempre permitido) — aí o usuário só
    // dá unmute, em vez de ter que clicar em play.
    hls.on(Hls.Events.MANIFEST_PARSED, () => {
      tryAutoplayMutedFallback(v)
    })
    hls.loadSource(streamURL)
    hls.attachMedia(v)
    return () => hls.destroy()
  }, [videoRef, streamURL, useHlsJs])

  // AirPlay (Safari/iOS): native <video controls> already shows the route button,
  // but a custom one aids discovery and works while minimized (controls hidden).
  // Only rendered when a target is on the network.
  const airplay = useAirPlay(videoRef, streamURL)

  return (
    <div
      className={`bg-black relative w-full mx-auto flex items-center justify-center ${
        audioMode
          // Áudio: a capa é só um cabeçalho compacto — sem controles nativos
          // (controls={!audioMode}=false) e, em álbuns sem capa embutida, o
          // espaço grande era puro desperdício. Faixa baixa (~160px) devolve a
          // altura pro EQ/visualizer/letras e pra lista de faixas.
          ? 'h-28 sm:h-40'
          : 'max-h-[70dvh] sm:max-h-[58dvh]'
      }`}
      style={audioMode ? undefined : { aspectRatio: '16 / 9' }}
    >
      <AudioCoverArt audioMode={audioMode} info={info} selectedFile={selectedFile} mediaToken={mediaToken} />
      {showResumePrompt && resumePosition !== null && (
        <ResumePrompt
          resumePosition={resumePosition}
          formatTime={formatTime}
          onContinue={onResumeContinue}
          onRestart={onResumeRestart}
        />
      )}
      {!videoError && currentTime === 0 && bufferedEnd === 0 && (
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
      <TranscodingBadge attempted={transcodeFallbackAttempted} videoError={videoError} />
      <AirPlayButton airplay={airplay} videoError={videoError} />
      {videoError ? null : (
        <video
          ref={videoRef}
          src={useHlsJs ? undefined : (streamURL || undefined)}
          controls={!audioMode}
          autoPlay
          playsInline
          {...{ 'webkit-playsinline': 'true', 'x-webkit-airplay': 'allow' } as any}
          className={`max-h-full max-w-full${audioMode ? ' w-full h-full' : ''}`}
          onError={onVideoError}
          onLoadStart={() => clientLog('info', 'player', 'loadstart', { src: streamURL })}
          onStalled={() => {
            clientLog('warn', 'player', 'stalled', videoDiagnostic())
            const v = videoRef.current
            if (v && kickPastStartGap(v)) clientLog('info', 'player', 'start-gap nudge (stalled)', { currentTime: v.currentTime })
          }}
          onWaiting={() => {
            clientLog('info', 'player', 'waiting (buffering)', { readyState: videoRef.current?.readyState })
            const v = videoRef.current
            if (v) kickPastStartGap(v)
          }}
          onTimeUpdate={onTimeUpdate}
          onLoadedMetadata={(e) => {
            const v = e.currentTarget
            clientLog('info', 'player', 'loadedmetadata', { duration: v.duration, videoWidth: v.videoWidth, videoHeight: v.videoHeight, currentSrc: v.currentSrc })
            onTimeUpdate()
          }}
          onProgress={() => {
            const v = videoRef.current
            if (v) kickPastStartGap(v)
            onTimeUpdate()
          }}
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
