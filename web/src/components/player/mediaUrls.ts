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
  // localEmbeddedVttURL: blob URL of a LOCAL embedded sub fetched with retry (the
  // server extracts large rclone files in the background). '' while extracting —
  // the <track> stays empty until ready instead of 502ing. Local-only; torrent
  // embedded subs keep the direct streamSubtrackURL.
  localEmbeddedVttURL: string
  caps: TranscodeCapabilities | null
  authEnabled: boolean
  probe: StreamProbe | null
  playbackID?: string
}

// buildStreamURL: empty when it can't play; direct-play (streamFileURL) when
// no transcode is needed; otherwise HLS-VOD for ALL browsers (segmented +
// seekable). Safari/iOS play it natively; the others attach via hls.js (see the effect
// in VideoPlayerElement). HLS replaces the old progressive MP4, which wasn't
// seekable and had ffmpeg dying on every byte-range (Chrome AND iOS Edge). (HLS uses the
// default audio track → AAC; non-default track selection and image-based subtitle
// burn don't go through here — the HLS-everywhere tradeoff.)
type StreamURLInput = Pick<MediaUrlInput, 'info' | 'selectedFile' | 'serverReady' | 'mediaToken' | 'transcodeAudio' | 'playbackID'> & {
  tokenMissing: boolean
  isTranscoded: boolean
}

function buildStreamURL(input: StreamURLInput): string {
  const { info, selectedFile, serverReady, tokenMissing, isTranscoded, mediaToken, transcodeAudio, playbackID } = input
  if (!info || selectedFile < 0 || !serverReady || tokenMissing) return ''
  if (!isTranscoded) return streamFileURL(info.infoHash, selectedFile, mediaToken)
  return appendNativeHLS(streamHLSMasterURL(info.infoHash, selectedFile, mediaToken, transcodeAudio ?? undefined, playbackID))
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

// computeIsTranscoded: does the track go through HLS-transcode (true) or direct-play
// (false)? Decided by the REAL CODEC (backend probe, browser-agnostic:
// MKV/HEVC/AV1/AC3/DTS don't direct-play in any browser). It used to be by NAME, which
// sent incompatible ones to direct-play → errorCode 4 on Safari. The probe
// (useTrackProbe) arrives soon; until then, falls to a name heuristic just
// to shrink the window — the probe overwrites as soon as available. Extracted so
// PlayerModal can gate the gapless engine (direct-play only) BEFORE the early-return,
// using the SAME truth as computeMediaUrls.
export function computeIsTranscoded(input: {
  info: TorrentInfo | null
  selectedFile: number
  transcodeAudio: number | null
  forceH264: boolean
  burnSubTrack: number | null
  probe: StreamProbe | null
}): boolean {
  const explicitTranscode = input.transcodeAudio !== null || input.forceH264 || input.burnSubTrack !== null
  if (explicitTranscode) return true
  if (input.info && isLocalHash(input.info.infoHash) && input.info.localPlaybackKind) {
    return input.info.localPlaybackKind === 'hls'
  }
  const selectedFilename = input.info?.files?.[input.selectedFile]?.path ?? ''
  const nameSuggestsTranscode =
    /(x265|h\.?265|hevc|av1|vp9|2160p?|4k|uhd)/i.test(selectedFilename) ||
    /\.(mkv|avi|ts|m2ts|wmv|flv|mpg|mpeg|ogv)$/i.test(selectedFilename)
  const needsTranscode = input.probe?.needsTranscode ?? nameSuggestsTranscode
  return needsTranscode
}

export function computeMediaUrls(input: MediaUrlInput) {
  const { info, selectedFile, serverReady, mediaToken, transcodeAudio, forceH264, burnSubTrack, caps, authEnabled, probe } = input
  // The media token is only MANDATORY with auth on (<video>/<track> can't send
  // headers → they load via ?token=). With auth off the media routes are public and
  // /auth/media-token answers 404 — gating on the token here would leave the streamURL
  // empty forever and the player would spin without ever loading.
  const tokenMissing = authEnabled && !mediaToken
  const isTranscoded = computeIsTranscoded({ info, selectedFile, transcodeAudio, forceH264, burnSubTrack, probe })

  const streamURL = buildStreamURL({
    info, selectedFile, serverReady, tokenMissing, isTranscoded, mediaToken, transcodeAudio,
    playbackID: input.playbackID,
  })
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

// recoverHlsFatal handles hls.js FATAL errors outside the component (keeps
// VideoPlayerElement's cognitive complexity low). The DECISION is pure
// (hlsFatalAction, testable); here we only apply the effect to the Hls object.
export function recoverHlsFatal(hls: Hls, data: ErrorData) {
  if (!data.fatal) return
  switch (hlsFatalAction(data.type, Hls.ErrorTypes)) {
    case 'startLoad': hls.startLoad(); break
    case 'recoverMedia': hls.recoverMediaError(); break
    default: hls.destroy()
  }
}

// tryAutoplayMutedFallback: iOS/Safari ignores the autoPlay attribute when there's
// an audio track (Apple's auto-play policy — only plays by itself muted, without sound
// or after a gesture). Tries play() with sound; if the policy blocks it (NotAllowed without
// gesture), falls back to MUTED (always allowed inline) and the user just unmutes. On
// desktop, where autoplay with sound is allowed, the first play() already succeeds and the
// video does NOT stay muted. Used both on hls.js (desktop) and the native <video>.
export function tryAutoplayMutedFallback(v: HTMLVideoElement) {
  v.play().catch(() => {
    v.muted = true
    v.play().catch(() => {})
  })
}

// kickPastStartGap: applies the nudge computed by startGapNudgeTarget. If the video
// is stuck in the initial t=0 gap (see startGapNudgeTarget), jumps the
// currentTime into the buffer and (re)tries autoplay — unlocks Safari
// when it won't start because buffered.start(0) is a hair > 0. No-op when there's no
// such gap. Idempotent: after the nudge currentTime is past buffered.start(0),
// so the next call already returns null and nothing happens (no reseek loop).
export function kickPastStartGap(v: HTMLVideoElement): boolean {
  const start = v.buffered.length > 0 ? v.buffered.start(0) : null
  const target = startGapNudgeTarget(v.currentTime, start)
  if (target === null) return false
  v.currentTime = target
  tryAutoplayMutedFallback(v)
  return true
}
