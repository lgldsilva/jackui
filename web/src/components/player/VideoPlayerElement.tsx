import { useEffect, useState, useRef } from 'react'
import { Volume2 } from 'lucide-react'
import { TorrentInfo, streamArtworkURL, streamArtURL, resolveArt, isLocalHash, parseLocalHash, localAudioCoverURL, isIOS } from '../../api/client'
import { clientLog } from '../../lib/diag'
import Hls from 'hls.js'
import { useAirPlay } from './playerHooks'
import { canPlayNativeHls, audioElementKey } from './playerFormat'
import { shouldShowStartOverlay, shouldShowStartAudioOverlay } from './playerOverlay'
import { recoverHlsFatal, tryAutoplayMutedFallback, kickPastStartGap } from './mediaUrls'
import { ResumePrompt, PlayerLoadingOverlay, TranscodingBadge, AirPlayButton, StartAudioOverlay } from './PlayerOverlays'

type VideoPlayerElementProps = {
  readonly videoRef: React.RefObject<HTMLVideoElement | null>
  readonly streamURL: string
  // engineActive: o motor gapless assumiu o áudio (toca em <audio> próprios). O
  // <video> então fica SEM src e mudo (a capa continua), pra não dobrar o áudio.
  readonly engineActive?: boolean
  // disableNativeAutoplay: iOS-áudio AINDA não iniciado. A Apple proíbe play() de
  // mídia-com-áudio fora de um gesto, então NÃO disparamos autoplay/nudge não-gesto
  // (travariam o elemento em readyState 1, loop de AbortError). Mostramos o overlay
  // "Tocar"; o tap inicia. Vira false após o 1º play (blessed) → auto-avanço.
  readonly disableNativeAutoplay?: boolean
  // onPlaybackStarted: disparado no 1º evento 'playing' do elemento. No iOS marca o
  // "blessed" (usuário iniciou via gesto) → libera o auto-avanço das próximas faixas.
  readonly onPlaybackStarted?: () => void
  // suppressStartOverlay: já houve uma faixa nesta instância (troca de faixa de
  // música, não abertura fria). Suprime o spinner de "carregando" no início da
  // nova faixa — a capa/seekbar continuam; sem isso o spinner piscava a cada troca.
  readonly suppressStartOverlay?: boolean
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

// shouldAttachHlsJs: usar hls.js (MSE) pra este src? Só pra HLS (.m3u8) em browser
// que NÃO toca HLS nativo (Chrome/Firefox/Edge) e que suporta MSE. Safari/iOS
// tocam o .m3u8 nativo; fontes diretas vão direto no <video src>. Extraído pra
// fora do componente pra manter a complexidade cognitiva do VideoPlayerElement
// bem abaixo do gate (a cadeia && pesava no corpo do componente).
function shouldAttachHlsJs(streamURL: string): boolean {
  return !!streamURL && streamURL.includes('.m3u8') && !canPlayNativeHls() && Hls.isSupported()
}

// audioPreload: iOS/Safari (WebKit) não busca dados de áudio direct-play sem gesto
// quando preload é o default mobile ('metadata') → o evento 'canplay' nunca dispara
// e o autoplay (preso a onCanPlay) trava o elemento em readyState 2. 'auto' no caso
// WebKit-áudio força o fetch. Vídeo e Chrome/Firefox mantêm o default. (Helper fora
// do componente p/ manter a complexidade cognitiva do VideoPlayerElement no limite.)
function audioPreload(audioMode: boolean): 'auto' | undefined {
  return audioMode && canPlayNativeHls() ? 'auto' : undefined
}

// handleMetaLoaded: 'loadedmetadata' SEMPRE dispara (iOS incluso). No WebKit
// (iOS/Safari) o vídeo direct PARADO estaciona em readyState 2 e o 'canplay'
// (readyState ≥3) NUNCA chega → o autoplay nunca era acionado e o vídeo "carregava
// mas não tocava" (confirmado nos logs: loadedmetadata → stalled rs2 → sem 'autoplay
// try'). Chamamos o kick aqui (= onVideoCanPlay, idempotente via autoplayTriedRef +
// seek/resume): o play()→fallback-mudo destrava o rs2. Desktop/Chrome seguem no
// 'canplay' (que lá dispara normal, então o kick aqui é no-op idempotente).
function handleMetaLoaded(v: HTMLVideoElement, onTimeUpdate: () => void, kickAutoplay: () => void, disableNativeAutoplay: boolean) {
  clientLog('info', 'player', 'loadedmetadata', { duration: v.duration, videoWidth: v.videoWidth, videoHeight: v.videoHeight, currentSrc: v.currentSrc })
  onTimeUpdate()
  if (canPlayNativeHls() && !disableNativeAutoplay) kickAutoplay()
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
  // Com o motor gapless ativo o <video> não carrega nada (o áudio sai dos <audio>
  // do motor) → nunca anexa hls.js nem seta src.
  const useHlsJs = !engineActive && shouldAttachHlsJs(streamURL)
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

  // iOS-áudio (tap-to-play): suprime os nudges não-gesto (start-gap) SÓ no
  // direct-play — HLS/transcode no iOS ainda precisa do nudge de warmup. O overlay
  // "Tocar" some assim que o usuário toca (startOverlayDismissed) e reseta a cada
  // troca de faixa (streamURL). startAudioPlayback roda DENTRO do onClick (gesto)
  // → o iOS baixa os dados e toca com som a partir de readyState 1.
  const suppressNudge = disableNativeAutoplay && !isTranscoded
  const [startOverlayDismissed, setStartOverlayDismissed] = useState(false)
  useEffect(() => { setStartOverlayDismissed(false) }, [streamURL])
  const showStartAudioOverlay = shouldShowStartAudioOverlay({
    disableNativeAutoplay, startOverlayDismissed, videoError, showResumePrompt, currentTime,
  })
  // iOS: src IMPERATIVO (igual ao SimpleAudioPlayer que TOCA). O <video> é montado SEM
  // src no iOS (não pré-carrega → não estaciona em readyState 2 antes do gesto — esse
  // era o bug: o tap chamava v.load() num elemento pré-carregado, resetava rs2→0 e
  // ABORTAVA o play, AbortError). Aqui o src é setado: pré-gesto (disableNativeAutoplay)
  // ESPERA o tap; pós-blessed (auto-avanço) seta o src e o handleMetaLoaded toca no
  // loadedmetadata. attachedSrcRef evita reanexar (el.src é absoluto; comparar com a
  // streamURL relativa sempre diferiria → reload/abort).
  const iosNative = isIOS() && !engineActive && !useHlsJs
  const attachedSrcRef = useRef('')
  useEffect(() => {
    const v = videoRef.current
    if (!v || !iosNative || !streamURL) return
    if (disableNativeAutoplay) return
    if (attachedSrcRef.current === streamURL) return
    attachedSrcRef.current = streamURL
    v.src = streamURL
  }, [videoRef, iosNative, streamURL, disableNativeAutoplay])
  // O tap no "Tocar" é o gesto que destrava o vídeo no iOS. Espelha o SimpleAudioPlayer:
  // seta o src DENTRO do gesto (o elemento estava SEM src) e play() no mesmo tick —
  // SEM v.load(). Re-exibe o overlay se o play() falhar e LOGA o desfecho.
  const startAudioPlayback = () => {
    const v = videoRef.current
    if (!v) return
    setStartOverlayDismissed(true)
    clientLog('info', 'player', 'tap "Tocar" (gesto) → src+play()', { readyState: v.readyState })
    if (streamURL) { attachedSrcRef.current = streamURL; v.src = streamURL }
    v.play()
      .then(() => clientLog('info', 'player', 'tap-to-play ok (som)', { readyState: v.readyState }))
      .catch((e) => {
        setStartOverlayDismissed(false)
        clientLog('warn', 'player', 'tap-to-play falhou', { name: (e as { name?: string })?.name, err: String(e) })
      })
  }

  return (
    <div
      className={`bg-black relative w-full mx-auto flex items-center justify-center ${
        audioMode
          // Áudio: a capa é o foco visual. Cresce com a tela, mas continua contida
          // (object-contain) e limitada a max-w-xl + mx-auto pra alinhar com o resto
          // das seções centralizadas (transport, painel) — bloco coeso. No mobile
          // fica compacta pra lista de faixas respirar.
          ? 'h-44 sm:h-56 lg:h-72 xl:h-80 max-w-xl'
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
      {/* No modo-motor o <video> está mudo/sem-src (bufferedEnd fica sempre 0) e o
          motor é quem toca — então NÃO mostra o overlay de "carregando" (senão ele
          piscaria a cada faixa, o "refresh" indevido). Ver shouldShowStartOverlay. */}
      {shouldShowStartOverlay({ videoError, engineActive, suppressStartOverlay, disableNativeAutoplay, currentTime, bufferedEnd }) && (
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
      {videoError ? null : (
        <video
          // Fresh element when an audio track crosses the direct-play↔HLS line on
          // WebKit, so a graph-tapped element never inherits an HLS src (→ mute).
          // See audioElementKey.
          key={audioElementKey(audioMode, isTranscoded)}
          ref={videoRef}
          src={iosNative || engineActive || useHlsJs ? undefined : (streamURL || undefined)}
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
            const v = videoRef.current
            if (v && !suppressNudge && kickPastStartGap(v)) clientLog('info', 'player', 'start-gap nudge (stalled)', { currentTime: v.currentTime })
          }}
          onWaiting={() => {
            clientLog('info', 'player', 'waiting (buffering)', { readyState: videoRef.current?.readyState })
            const v = videoRef.current
            if (v && !suppressNudge) kickPastStartGap(v)
          }}
          onTimeUpdate={onTimeUpdate}
          onLoadedMetadata={(e) => handleMetaLoaded(e.currentTarget, onTimeUpdate, onVideoCanPlay, disableNativeAutoplay)}
          onProgress={() => {
            const v = videoRef.current
            if (v && !suppressNudge) kickPastStartGap(v)
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
