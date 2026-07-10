import { useEffect } from 'react'
import { TorrentInfo, StreamProbe, TranscodeCapabilities, isSafariBrowser } from '../../api/client'
import { clientLog } from '../../lib/diag'
import { useHevcBackstop } from './playerHooks'

type Setter<T> = React.Dispatch<React.SetStateAction<T>>

// HEVC/unsupported-codec handling: the up-front auto-transcode of incompatible
// audio, the <video onError> fallback chain (buffer-retry → force h264 → surface
// error), and the Safari silent-failure backstop. Needs the resolved streamURL/
// isTranscoded, so it's called AFTER computeMediaUrls; state stays in the modal.
export function useVideoFallback(deps: {
  videoRef: React.RefObject<HTMLVideoElement>
  info: TorrentInfo | null
  probe: StreamProbe | null
  selectedFile: number
  audioMode: boolean
  bufferedEnd: number
  streamURL: string
  isTranscoded: boolean
  transcodeAudio: number | null
  forceH264: boolean
  burnSubTrack: number | null
  transcodeFallbackAttempted: boolean
  videoError: boolean
  caps: TranscodeCapabilities | null
  audioAutoRef: React.MutableRefObject<boolean>
  bufferRetryRef: React.MutableRefObject<number>
  setTranscodeAudio: Setter<number | null>
  setForceH264: Setter<boolean>
  setTranscodeFallbackAttempted: Setter<boolean>
  setVideoError: Setter<boolean>
  setLastErrorDiag: Setter<Record<string, unknown> | null>
}) {
  const {
    videoRef, info, probe, selectedFile, audioMode, bufferedEnd, streamURL, isTranscoded,
    transcodeAudio, forceH264, burnSubTrack, transcodeFallbackAttempted, videoError, caps,
    audioAutoRef, bufferRetryRef,
    setTranscodeAudio, setForceH264, setTranscodeFallbackAttempted,
    setVideoError, setLastErrorDiag,
  } = deps

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

  return { videoDiagnostic, handleVideoError }
}
