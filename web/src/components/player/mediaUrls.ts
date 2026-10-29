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
  withToken,
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
  // Virtual auto subtitle (lane W2) when applied: { OpenSubtitles file id,
  // language }. Lowest <track> priority (any manual pick replaces it upstream).
  autoSub: { id: string; language: string } | null
  // settings.burn_default: when true AND the auto subtitle is the active one,
  // the TORRENT stream is routed through the progressive transcode endpoint
  // with ?burnsub=<id> (HLS builds its own args and drops subtitles, so burn
  // would silently no-op on the HLS path).
  burnDefault: boolean
  // Per-session burn-failure latch (lane W2 review F1): the burn transcode can
  // answer 400 (backend ffmpeg without libass). The player's onError chain
  // latches this when the active streamURL is the burn URL; computeMediaUrls
  // then drops the burn routing (burnSubFileId → null), the stream falls back
  // to direct/HLS, and the <track> shows the subtitle unburned instead of
  // hard-failing playback over a subtitle. Reset on torrent/file switch.
  burnFailed?: boolean
  caps: TranscodeCapabilities | null
  authEnabled: boolean
  probe: StreamProbe | null
  playbackID?: string
}

// burnSubTranscodeURL: the PROGRESSIVE transcode URL carrying ?burnsub=<osFileId>
// — the only endpoint that burns an EXTERNAL (OpenSubtitles) subtitle. Used when
// settings.burn_default is on and the auto subtitle is active. The backend can
// answer 400 here (its ffmpeg lacks libass): the player's onError chain then
// latches burnFailed (see MediaUrlInput) and the NEXT computeMediaUrls drops the
// burn routing — the stream falls back to direct/HLS and the <track> keeps
// showing the subtitle unburned.
// audioTrack threads the caller's audio selection into the progressive MP4
// (cross-cutting review finding 2): without it the transcode COPIES the source
// audio, and when useVideoFallback auto-selected a non-default track the copied
// one is exactly the codec the browser can't decode — burned subtitles with no
// sound (and usually no <video onError>, so the burn-failure fallback never
// engages). `audio=<idx>&acodec=aac` mirrors the HLS master builder's audio
// threading; with no selection the URL stays byte-identical to before.
export function burnSubTranscodeURL(hash: string, fileIdx: number, fileId: string, tokenOverride?: string, audioTrack?: number | null): string {
  const audioQ = audioTrack != null && audioTrack >= 0 ? `&audio=${audioTrack}&acodec=aac` : ''
  return withToken(`/api/stream/transcode/${hash}/${fileIdx}?burnsub=${encodeURIComponent(fileId)}${audioQ}`, tokenOverride)
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
  burnSubFileId: string | null
}

function buildStreamURL(input: StreamURLInput): string {
  const { info, selectedFile, serverReady, tokenMissing, isTranscoded, mediaToken, transcodeAudio, playbackID, burnSubFileId } = input
  if (!info || selectedFile < 0 || !serverReady || tokenMissing) return ''
  // Burn-the-auto-subtitle wins over HLS: the HLS muxer drops subtitle streams,
  // so ?burnsub only works on the progressive transcode path. The non-seekable
  // tradeoff is the price of the burned picture (user opted in via settings).
  // transcodeAudio rides along (finding 2): the progressive MP4 must carry the
  // selected (browser-decodable) audio track, not the untouched source one.
  if (burnSubFileId) return burnSubTranscodeURL(info.infoHash, selectedFile, burnSubFileId, mediaToken, transcodeAudio)
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

// buildSubtitleVttURL resolves the <track> source: manual picks first (custom
// upload → sidecar → embedded → external), then the virtual auto subtitle.
// While the burn transcode is routing (burnSubFileId set) the auto branch stays
// silent: the picture already carries the subtitle, and an active overlay
// <track> would render the same text twice on Chrome/Firefox. When the burn
// fails, the burnFailed latch drops burnSubFileId and the auto branch comes
// back — the <track> becomes the unburned fallback.
function buildSubtitleVttURL(input: MediaUrlInput, tokenMissing: boolean, burnSubFileId: string | null): string {
  const { info, selectedFile, customSubURL, sidecarIdx, embeddedSub, subActive, mediaToken, localEmbeddedVttURL, autoSub } = input
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
  // Virtual auto subtitle LAST: every manual source above replaces it upstream
  // (applying auto never sets those states), so this only fires when the auto
  // pick is the active decision. Built per application from the id — never
  // cached across media-token refreshes. Suppressed while the burn transcode
  // carries the same subtitle in the picture (no double captions).
  if (autoSub && !burnSubFileId) return subtitleDownloadURL(autoSub.id, mediaToken)
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
  // OpenSubtitles file id when burn_default + auto subtitle active (null otherwise).
  burnSubFileId?: string | null
  probe: StreamProbe | null
}): boolean {
  const explicitTranscode = input.transcodeAudio !== null || input.forceH264 || input.burnSubTrack !== null || !!input.burnSubFileId
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
  const { info, selectedFile, serverReady, mediaToken, transcodeAudio, forceH264, burnSubTrack, caps, authEnabled, probe, autoSub, burnDefault, burnFailed } = input
  // The media token is only MANDATORY with auth on (<video>/<track> can't send
  // headers → they load via ?token=). With auth off the media routes are public and
  // /auth/media-token answers 404 — gating on the token here would leave the streamURL
  // empty forever and the player would spin without ever loading.
  const tokenMissing = authEnabled && !mediaToken
  // burnsub only exists on the TORRENT progressive endpoint — the local
  // handlers (and their HLS pipeline) have no external-subtitle burn support
  // yet, so local playback keeps its normal routing (documented gap, lane W2).
  // A latched burn failure (onError on the burn URL) drops the routing so the
  // stream falls back to direct/HLS with the subtitle unburned on the <track>.
  const burnSubFileId = burnDefault && !burnFailed && autoSub && info && !isLocalHash(info.infoHash) ? autoSub.id : null
  const isTranscoded = computeIsTranscoded({ info, selectedFile, transcodeAudio, forceH264, burnSubTrack, burnSubFileId, probe })

  const streamURL = buildStreamURL({
    info, selectedFile, serverReady, tokenMissing, isTranscoded, mediaToken, transcodeAudio,
    playbackID: input.playbackID,
    burnSubFileId,
  })
  const subtitleVttURL = buildSubtitleVttURL(input, tokenMissing, burnSubFileId)

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

  // burnSubFileId is returned so the owner (PlayerModal) can tell the error
  // chain when the active src is the burn transcode (burn-routing detection
  // without URL-string sniffing at the call site).
  return { streamURL, subtitleVttURL, vlcURL, iinaURL, infuseURL, directURL: absoluteDirectURL, encoderLabel, isTranscoded, burnSubFileId }
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
