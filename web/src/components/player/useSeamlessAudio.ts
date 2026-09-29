import { useEffect } from 'react'
import type Hls from 'hls.js'
import { probeAudioToPosition, applyAudioSelection, nativeAudioCount, type VideoWithAudioTracks } from './hlsAudioTracks'

// useSeamlessAudio wires the audio track switch WITHOUT recreating the player (Phase 8).
// Extracted from VideoPlayerElement so its cognitive complexity isn't inflated (the
// component is already at the legacyComplexity baseline). Two effects:
//  1) native HLS (Safari/iOS, no hls.js): reports the WebKit
//     AudioTrackList's track count (hls.js reports through its own listener in the
//     component's effect). The list may only populate after 'loadedmetadata' → add/removetrack.
//  2) applies the chosen track (hls.audioTrack / video.audioTracks). No-op when the
//     engine has ≤1 track (the switch already went through the ?audio=N reload). Idempotent.
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
