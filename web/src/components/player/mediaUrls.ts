import {
  TorrentInfo,
  StreamProbe,
  TranscodeCapabilities,
  streamFileURL,
  streamHLSMasterURL,
  streamSubtrackURL,
  streamSidecarURL,
  streamPlaylistM3UURL,
  subtitleDownloadURL,
  isLocalHash,
} from '../../api/client'
import Hls from 'hls.js'
import type { ErrorData } from 'hls.js'
import { hlsFatalAction, startGapNudgeTarget } from './playerHooks'
import { canPlayNativeHls, shouldUseHlsJs } from './playerFormat'

export type MediaUrlInput = {
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
  // localEmbeddedVttURL: blob URL of a LOCAL embedded sub fetched with retry (the
  // server extracts large rclone files in the background). '' while extracting —
  // the <track> stays empty until ready instead of 502ing. Local-only; torrent
  // embedded subs keep the direct streamSubtrackURL.
  localEmbeddedVttURL: string
  caps: TranscodeCapabilities | null
  authEnabled: boolean
  probe: StreamProbe | null
  // audioMode forces hls.js on WebKit (so the audio reaches the Web Audio graph);
  // it also flips the native_hls flag on the HLS URL to match the real transport.
  audioMode: boolean
}

// buildStreamURL: vazia se não der pra tocar; direct-play (streamFileURL) quando
// não precisa transcode; senão HLS-VOD pra TODOS os browsers (segmentado +
// seekável). Safari/iOS tocam nativo; os demais anexam via hls.js (ver o efeito
// em VideoPlayerElement). HLS substitui o antigo MP4 progressive, que não tinha
// seek e tinha o ffmpeg morto a cada byte-range (Chrome E iOS Edge). (HLS usa a
// faixa de áudio default → AAC; seleção de faixa não-default e burn de legenda
// image-based não passam por aqui — tradeoff do HLS-everywhere.)
function buildStreamURL(info: TorrentInfo | null, selectedFile: number, serverReady: boolean, tokenMissing: boolean, isTranscoded: boolean, mediaToken: string, audioMode: boolean): string {
  if (!info || selectedFile < 0 || !serverReady || tokenMissing) return ''
  if (!isTranscoded) return streamFileURL(info.infoHash, selectedFile, mediaToken)
  return appendNativeHLS(streamHLSMasterURL(info.infoHash, selectedFile, mediaToken), audioMode)
}

// appendNativeHLS marks the HLS master URL when the client plays HLS natively
// (Safari/iOS). The server uses it to apply the VOD policy per client class and
// to key the session (see HLSSessionManager.EffectiveKey); segment URLs in the
// playlist already carry the flag, so only the master needs it here. Omitted
// for hls.js clients (treated as not-native server-side).
function appendNativeHLS(url: string, audioMode: boolean): string {
  if (!url) return url
  // Marca native_hls SÓ quando o cliente vai tocar NATIVO de fato: WebKit E sem
  // forçar hls.js. Caso contrário (desktop, ou iOS-áudio que agora vai por hls.js)
  // o servidor tem que tratar como NÃO-nativo, pra master e segmentos concordarem
  // na policy de sessão/VOD (EffectiveKey) — senão divergem sob VODHLSJS.
  const native = canPlayNativeHls() && !shouldUseHlsJs({ isHls: true, audioMode, hlsSupported: Hls.isSupported() })
  if (!native) return url
  const sep = url.includes('?') ? '&' : '?'
  return `${url}${sep}native_hls=1`
}

function buildSubtitleVttURL(input: MediaUrlInput, tokenMissing: boolean): string {
  const { info, selectedFile, customSubURL, sidecarIdx, embeddedSub, subActive, mediaToken, localEmbeddedVttURL } = input
  if (customSubURL) return customSubURL
  if (tokenMissing) return ''
  if (info && sidecarIdx !== null) return streamSidecarURL(info.infoHash, sidecarIdx, mediaToken)
  if (info && embeddedSub !== null) {
    // Local embedded subs ride the retry-fetched blob ('' until extracted); the
    // torrent path serves the track URL directly.
    if (isLocalHash(info.infoHash)) return localEmbeddedVttURL
    return streamSubtrackURL(info.infoHash, selectedFile, embeddedSub, mediaToken)
  }
  if (subActive) return subtitleDownloadURL(subActive, mediaToken)
  return ''
}

function pickEncoderLabel(caps: TranscodeCapabilities | null): string {
  if (caps?.hasNvidia) return 'NVENC'
  if (caps?.hasVaapi) return 'VAAPI'
  if (caps?.hasQsv) return 'QSV'
  return 'CPU'
}

export function computeMediaUrls(input: MediaUrlInput) {
  const { info, selectedFile, serverReady, mediaToken, transcodeAudio, forceH264, burnSubTrack, caps, authEnabled, probe, audioMode } = input
  // O media token só é OBRIGATÓRIO com auth ligado (<video>/<track> não mandam
  // header → carregam ?token=). Com auth off as rotas de mídia são públicas e
  // /auth/media-token responde 404 — gatear no token aqui deixaria a streamURL
  // vazia pra sempre e o player giraria sem nunca carregar.
  const tokenMissing = authEnabled && !mediaToken
  const selectedFilename = info?.files?.[selectedFile]?.path ?? ''
  // Decide transcode pelo CODEC REAL (probe do backend, navegador-agnóstico:
  // MKV/HEVC/AV1/AC3/DTS não tocam direto em browser nenhum). Antes era por NOME,
  // o que mandava incompatível pro direct-play → errorCode 4 no Safari. O probe
  // (useTrackProbe) chega logo; enquanto não chega, cai numa heurística de nome
  // só pra reduzir a janela — o probe sobrescreve assim que disponível.
  const nameSuggestsTranscode =
    /(x265|h\.?265|hevc|av1|vp9|2160p?|4k|uhd)/i.test(selectedFilename) ||
    /\.(mkv|avi|ts|m2ts|wmv|flv|mpg|mpeg|ogv)$/i.test(selectedFilename)
  const needsTranscode = probe?.needsTranscode ?? nameSuggestsTranscode
  const isTranscoded = transcodeAudio !== null || forceH264 || burnSubTrack !== null || needsTranscode

  const streamURL = buildStreamURL(info, selectedFile, serverReady, tokenMissing, isTranscoded, mediaToken, audioMode)
  const subtitleVttURL = buildSubtitleVttURL(input, tokenMissing)

  let vlcURL = ''
  if (info && selectedFile >= 0) {
    const transcodeParam = forceH264 ? 'h264' : undefined
    vlcURL = streamPlaylistM3UURL(info.infoHash, selectedFile, transcodeParam)
  }

  let iinaURL = ''
  let infuseURL = ''
  // absoluteDirectURL: direct-play HTTP stream (with ?token=, no transcode) used
  // as the payload of the scheme links AND exposed below as directURL for the
  // "Copiar URL" item — any external app can ingest it.
  let absoluteDirectURL = ''
  if (info && selectedFile >= 0) {
    const directPath = streamFileURL(info.infoHash, selectedFile, mediaToken)
    absoluteDirectURL = `${globalThis.location?.origin ?? ''}${directPath}`
    iinaURL = `iina://weblink?url=${encodeURIComponent(absoluteDirectURL)}`
    infuseURL = `infuse://x-callback-url/play?url=${encodeURIComponent(absoluteDirectURL)}`
  }

  const encoderLabel = pickEncoderLabel(caps)

  return { streamURL, subtitleVttURL, vlcURL, iinaURL, infuseURL, directURL: absoluteDirectURL, encoderLabel, isTranscoded }
}

// recoverHlsFatal trata erro FATAL do hls.js fora do componente (mantém a
// complexidade cognitiva de VideoPlayerElement baixa). A DECISÃO é pura
// (hlsFatalAction, testável); aqui só aplica o efeito no objeto Hls.
export function recoverHlsFatal(hls: Hls, data: ErrorData) {
  if (!data.fatal) return
  switch (hlsFatalAction(data.type, Hls.ErrorTypes)) {
    case 'startLoad': hls.startLoad(); break
    case 'recoverMedia': hls.recoverMediaError(); break
    default: hls.destroy()
  }
}

// tryAutoplayMutedFallback: o iOS/Safari ignora o atributo autoPlay quando há
// faixa de áudio (política de auto-play da Apple — só toca sozinho mudo, sem som
// ou após gesto). Tenta play() com som; se a política bloquear (NotAllowed sem
// gesto), cai pra MUDO (sempre permitido inline) e o usuário só dá unmute. No
// desktop, onde autoplay com som é permitido, o primeiro play() já passa e o
// vídeo NÃO fica mudo. Usado tanto no hls.js (desktop) quanto no <video> nativo.
export function tryAutoplayMutedFallback(v: HTMLVideoElement) {
  v.play().catch(() => {
    v.muted = true
    v.play().catch(() => {})
  })
}

// kickPastStartGap: aplica o nudge calculado por startGapNudgeTarget. Se o vídeo
// estiver travado no buraco inicial do t=0 (ver startGapNudgeTarget), pula o
// currentTime pra dentro do buffer e (re)tenta o autoplay — destrava o Safari
// que não inicia quando buffered.start(0) é um fio > 0. No-op quando não há esse
// buraco. Idempotente: depois do nudge o currentTime passa de buffered.start(0),
// então a próxima chamada já devolve null e nada acontece (sem reseek em loop).
export function kickPastStartGap(v: HTMLVideoElement): boolean {
  const start = v.buffered.length > 0 ? v.buffered.start(0) : null
  const target = startGapNudgeTarget(v.currentTime, start)
  if (target === null) return false
  v.currentTime = target
  tryAutoplayMutedFallback(v)
  return true
}
