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
} from '../../api/client'
import Hls from 'hls.js'
import type { ErrorData } from 'hls.js'
import { hlsFatalAction } from './playerHooks'
import { canPlayNativeHls } from './playerFormat'

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
  caps: TranscodeCapabilities | null
  authEnabled: boolean
  probe: StreamProbe | null
}

// buildStreamURL: vazia se não der pra tocar; direct-play (streamFileURL) quando
// não precisa transcode; senão HLS-VOD pra TODOS os browsers (segmentado +
// seekável). Safari/iOS tocam nativo; os demais anexam via hls.js (ver o efeito
// em VideoPlayerElement). HLS substitui o antigo MP4 progressive, que não tinha
// seek e tinha o ffmpeg morto a cada byte-range (Chrome E iOS Edge). (HLS usa a
// faixa de áudio default → AAC; seleção de faixa não-default e burn de legenda
// image-based não passam por aqui — tradeoff do HLS-everywhere.)
function buildStreamURL(info: TorrentInfo | null, selectedFile: number, serverReady: boolean, tokenMissing: boolean, isTranscoded: boolean, mediaToken: string): string {
  if (!info || selectedFile < 0 || !serverReady || tokenMissing) return ''
  if (!isTranscoded) return streamFileURL(info.infoHash, selectedFile, mediaToken)
  return appendNativeHLS(streamHLSMasterURL(info.infoHash, selectedFile, mediaToken))
}

// appendNativeHLS marks the HLS master URL when the client plays HLS natively
// (Safari/iOS). The server uses it to apply the VOD policy per client class and
// to key the session (see HLSSessionManager.EffectiveKey); segment URLs in the
// playlist already carry the flag, so only the master needs it here. Omitted
// for hls.js clients (treated as not-native server-side).
function appendNativeHLS(url: string): string {
  if (!url || !canPlayNativeHls()) return url
  const sep = url.includes('?') ? '&' : '?'
  return `${url}${sep}native_hls=1`
}

function buildSubtitleVttURL(input: MediaUrlInput, tokenMissing: boolean): string {
  const { info, selectedFile, customSubURL, sidecarIdx, embeddedSub, subActive, mediaToken } = input
  if (customSubURL) return customSubURL
  if (tokenMissing) return ''
  if (info && sidecarIdx !== null) return streamSidecarURL(info.infoHash, sidecarIdx, mediaToken)
  if (info && embeddedSub !== null) return streamSubtrackURL(info.infoHash, selectedFile, embeddedSub, mediaToken)
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
  const { info, selectedFile, serverReady, mediaToken, transcodeAudio, forceH264, burnSubTrack, caps, authEnabled, probe } = input
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

  const streamURL = buildStreamURL(info, selectedFile, serverReady, tokenMissing, isTranscoded, mediaToken)
  const subtitleVttURL = buildSubtitleVttURL(input, tokenMissing)

  let vlcURL = ''
  if (info && selectedFile >= 0) {
    const transcodeParam = forceH264 ? 'h264' : undefined
    vlcURL = streamPlaylistM3UURL(info.infoHash, selectedFile, transcodeParam)
  }

  const encoderLabel = pickEncoderLabel(caps)

  return { streamURL, subtitleVttURL, vlcURL, encoderLabel, isTranscoded }
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
