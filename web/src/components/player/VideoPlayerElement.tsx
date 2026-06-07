import { useEffect } from 'react'
import { Volume2 } from 'lucide-react'
import { TorrentInfo, streamArtworkURL } from '../../api/client'
import { clientLog } from '../../lib/diag'
import Hls from 'hls.js'
import { useAirPlay } from './playerHooks'
import { canPlayNativeHls } from './playerFormat'
import { recoverHlsFatal, tryAutoplayMutedFallback } from './mediaUrls'
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

// <track> elements for the <video>. Extracted so the chain of subtitleVttURL
// ternaries doesn't inflate VideoPlayerElement's cognitive complexity (gate).
function SubtitleTracks({ subtitleVttURL }: { readonly subtitleVttURL: string }) {
  return (
    <>
      <track
        kind={subtitleVttURL ? 'subtitles' : 'metadata'}
        src={subtitleVttURL || ''}
        srcLang={subtitleVttURL ? 'pt' : ''}
        label={subtitleVttURL ? 'Português (BR)' : ''}
        default
      />
      <track kind="captions" srcLang="pt" label="Português (BR) [CC]" />
    </>
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
          // Áudio: a capa não precisa de tela cheia. Encolhe a faixa (mantendo a
          // largura total p/ a barra de controles nativa respirar) e o espaço
          // sobra vai pra lista de faixas abaixo — estilo "tela de álbum".
          // min-h garante espaço pros controles nativos (play/seek) não cortarem
          // em telas pequenas; ainda bem menor que o 16:9 original → lista respira.
          ? 'h-[20dvh] min-h-[152px] sm:h-[38dvh] sm:min-h-0'
          : 'max-h-[70dvh] sm:max-h-[58dvh]'
      }`}
      style={audioMode ? undefined : { aspectRatio: '16 / 9' }}
    >
      {audioMode && info && (
        <div className="absolute inset-x-0 top-0 bottom-12 flex items-center justify-center bg-gradient-to-br from-gray-800 to-gray-900 pointer-events-none">
          <Volume2 className="absolute w-12 h-12 text-text-muted" />
          <img
            src={streamArtworkURL(info.infoHash, selectedFile, mediaToken || undefined)}
            alt=""
            className="relative max-h-full max-w-full object-contain"
            onError={(e) => { (e.currentTarget as HTMLImageElement).style.display = 'none' }}
          />
        </div>
      )}
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
          <SubtitleTracks subtitleVttURL={subtitleVttURL} />
        </video>
      )}
      {videoError && renderVideoError()}
    </div>
  )
}
