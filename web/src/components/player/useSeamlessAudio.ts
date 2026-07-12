import { useEffect } from 'react'
import type Hls from 'hls.js'
import { probeAudioToPosition, applyAudioSelection, nativeAudioCount, type VideoWithAudioTracks } from './hlsAudioTracks'

// useSeamlessAudio conecta a troca de faixa de áudio SEM recriar o player (Fase 8).
// Extraído do VideoPlayerElement p/ não inflar sua complexidade cognitiva (o
// componente já está no baseline legacyComplexity). Dois effects:
//  1) HLS nativo (Safari/iOS, sem hls.js): reporta a contagem de faixas da
//     AudioTrackList do WebKit (o hls.js reporta pelo próprio listener no effect
//     do componente). A lista pode só popular após 'loadedmetadata' → add/removetrack.
//  2) aplica a faixa escolhida (hls.audioTrack / video.audioTracks). No-op quando a
//     engine tem ≤1 faixa (troca já foi pelo ?audio=N reload). Idempotente.
export function useSeamlessAudio(params: {
  videoRef: React.RefObject<HTMLVideoElement | null>
  hlsRef: React.MutableRefObject<Hls | null>
  engineActive: boolean
  useHlsJs: boolean
  streamURL: string
  seamlessAudioIndex: number | null
  probeAudioTracks?: readonly { index: number }[]
  onHlsAudioCount?: (n: number) => void
}): void {
  const { videoRef, hlsRef, engineActive, useHlsJs, streamURL, seamlessAudioIndex, probeAudioTracks, onHlsAudioCount } = params
  // HLS nativo = Safari/iOS tocam o .m3u8 direto (sem hls.js e sem motor gapless).
  const nativeHlsActive = !engineActive && !useHlsJs && !!streamURL && streamURL.includes('.m3u8')

  useEffect(() => {
    if (!nativeHlsActive) return
    const v = videoRef.current as VideoWithAudioTracks | null
    const at = v?.audioTracks
    if (!at?.addEventListener) { onHlsAudioCount?.(nativeAudioCount(v)); return }
    const report = () => onHlsAudioCount?.(at.length)
    report()
    at.addEventListener('addtrack', report)
    at.addEventListener('removetrack', report)
    return () => {
      at.removeEventListener?.('addtrack', report)
      at.removeEventListener?.('removetrack', report)
      onHlsAudioCount?.(0)
    }
  }, [nativeHlsActive, streamURL, videoRef, onHlsAudioCount])

  useEffect(() => {
    const pos = probeAudioToPosition(seamlessAudioIndex, probeAudioTracks ?? [])
    if (pos === null) return
    applyAudioSelection(hlsRef.current, videoRef.current as VideoWithAudioTracks | null, pos)
  }, [seamlessAudioIndex, probeAudioTracks, videoRef, hlsRef])
}
