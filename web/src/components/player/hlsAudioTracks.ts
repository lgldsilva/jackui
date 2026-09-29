import Hls from 'hls.js'

// Phase 8 (HLS master): switch the audio track WITHOUT recreating the player when the HLS
// master exposes EXT-X-MEDIA TYPE=AUDIO renditions (backend with
// JACKUI_HLS_MEDIA_RENDITIONS on). With the toggle OFF the master carries ≤1 track,
// nothing here activates and the switch falls to the legacy ?audio=N path (reload) —
// zero impact in prod. See docs/HLS_MASTER_PLAYLIST_PLAN.md.

// WebKit's AudioTrackList/AudioTrack (Safari/iOS play the HLS master natively, without
// hls.js). Not part of the standard TS DOM libs — only the fields used.
type NativeAudioTrack = { enabled: boolean }
export type NativeAudioTrackList = {
  readonly length: number
  [index: number]: NativeAudioTrack
  addEventListener?: (type: string, cb: () => void) => void
  removeEventListener?: (type: string, cb: () => void) => void
}
export type VideoWithAudioTracks = HTMLVideoElement & { audioTracks?: NativeAudioTrackList }

// seamlessAudioAvailable: the master exposed >1 selectable track → the switch goes via
// hls.audioTrack / video.audioTracks (no reload). ≤1 = legacy ?audio=N path.
export function seamlessAudioAvailable(hlsAudioCount: number): boolean {
  return hlsAudioCount > 1
}

// probeAudioToPosition maps the ABSOLUTE stream index (probe.audio[k].index,
// what the UI shows) to the POSITION k in the rendition list. The backend emits the
// EXT-X-MEDIA entries in probe order (writeAudioRenditions), so audioTracks[k] ↔
// probe.audio[k]. null (default) → 0 (the 1st rendition, the muxed DEFAULT). Returns
// null when the index doesn't match any track (applies nothing).
export function probeAudioToPosition(idx: number | null, probeAudio: readonly { index: number }[]): number | null {
  if (idx === null) return 0
  const pos = probeAudio.findIndex(a => a.index === idx)
  return pos >= 0 ? pos : null
}

// nativeAudioCount reads the native HLS (Safari/iOS) track count. 0 when
// there's no AudioTrackList (non-WebKit browser or list not yet populated).
export function nativeAudioCount(video: VideoWithAudioTracks | null): number {
  return video?.audioTracks?.length ?? 0
}

// wireHlsAudioSubs registers the track listeners on hls.js: reports the audio
// track count (>1 = seamless switch) and turns the HLS subtitles OFF
// (SUBTITLE_TRACKS_UPDATED → subtitleTrack=-1) — React's <track> pipeline is the
// single source of subtitles, otherwise EXT-X-MEDIA TYPE=SUBTITLES would double the subtitle on
// Chrome/Firefox (Phase 8c). Outside the component so its complexity isn't inflated.
export function wireHlsAudioSubs(hls: Hls, onHlsAudioCount?: (n: number) => void): void {
  hls.on(Hls.Events.AUDIO_TRACKS_UPDATED, () => onHlsAudioCount?.(hls.audioTracks.length))
  hls.on(Hls.Events.SUBTITLE_TRACKS_UPDATED, () => { hls.subtitleTrack = -1 })
}

// applyAudioSelection applies the track (position) on the active engine without recreating anything:
// hls.js via hls.audioTrack (the track id, not the position — hls.js ids may
// not be 0-based); Safari/iOS native HLS via WebKit's AudioTrackList
// (enabled). No-op when the engine has ≤1 track (the master brought no renditions).
export function applyAudioSelection(hls: Hls | null, video: VideoWithAudioTracks | null, pos: number): void {
  if (hls && hls.audioTracks.length > 1) {
    const track = hls.audioTracks[pos]
    if (track && hls.audioTrack !== track.id) hls.audioTrack = track.id
    return
  }
  const at = video?.audioTracks
  if (at && at.length > 1 && pos >= 0 && pos < at.length) {
    for (let i = 0; i < at.length; i++) at[i].enabled = i === pos
  }
}
