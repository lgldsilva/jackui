import Hls from 'hls.js'

// Fase 8 (HLS master): trocar a faixa de áudio SEM recriar o player quando o HLS
// master expõe renditions EXT-X-MEDIA TYPE=AUDIO (backend com
// JACKUI_HLS_MEDIA_RENDITIONS ligado). Com o toggle OFF o master traz ≤1 faixa,
// nada aqui ativa e a troca cai no caminho legado ?audio=N (reload) — inércia
// total em prod. Ver docs/HLS_MASTER_PLAYLIST_PLAN.md.

// AudioTrackList/AudioTrack do WebKit (Safari/iOS tocam o master HLS nativo, sem
// hls.js). Não fazem parte dos libs padrão do TS DOM — só os campos usados.
type NativeAudioTrack = { enabled: boolean }
export type NativeAudioTrackList = {
  readonly length: number
  [index: number]: NativeAudioTrack
  addEventListener?: (type: string, cb: () => void) => void
  removeEventListener?: (type: string, cb: () => void) => void
}
export type VideoWithAudioTracks = HTMLVideoElement & { audioTracks?: NativeAudioTrackList }

// seamlessAudioAvailable: o master expôs >1 faixa selecionável → a troca vai por
// hls.audioTrack / video.audioTracks (sem reload). ≤1 = caminho legado ?audio=N.
export function seamlessAudioAvailable(hlsAudioCount: number): boolean {
  return hlsAudioCount > 1
}

// probeAudioToPosition mapeia o índice ABSOLUTO de stream (probe.audio[k].index,
// o que a UI mostra) para a POSIÇÃO k na lista de renditions. O backend emite as
// EXT-X-MEDIA em ordem de probe (writeAudioRenditions), então audioTracks[k] ↔
// probe.audio[k]. null (default) → 0 (a 1ª rendition, a DEFAULT muxada). Devolve
// null quando o índice não bate com nenhuma faixa (não aplica nada).
export function probeAudioToPosition(idx: number | null, probeAudio: readonly { index: number }[]): number | null {
  if (idx === null) return 0
  const pos = probeAudio.findIndex(a => a.index === idx)
  return pos >= 0 ? pos : null
}

// nativeAudioCount lê a contagem de faixas do HLS nativo (Safari/iOS). 0 quando
// não há AudioTrackList (browser não-WebKit ou lista ainda não populada).
export function nativeAudioCount(video: VideoWithAudioTracks | null): number {
  return video?.audioTracks?.length ?? 0
}

// wireHlsAudioSubs registra os listeners de faixa no hls.js: reporta a contagem de
// faixas de áudio (>1 = troca seamless) e DESLIGA as legendas do HLS
// (SUBTITLE_TRACKS_UPDATED → subtitleTrack=-1) — o pipeline <track> do React é a
// fonte única de legenda, senão o EXT-X-MEDIA TYPE=SUBTITLES dobraria a legenda no
// Chrome/Firefox (Fase 8c). Fora do componente p/ não inflar sua complexidade.
export function wireHlsAudioSubs(hls: Hls, onHlsAudioCount?: (n: number) => void): void {
  hls.on(Hls.Events.AUDIO_TRACKS_UPDATED, () => onHlsAudioCount?.(hls.audioTracks.length))
  hls.on(Hls.Events.SUBTITLE_TRACKS_UPDATED, () => { hls.subtitleTrack = -1 })
}

// applyAudioSelection aplica a faixa (posição) na engine ativa sem recriar nada:
// hls.js via hls.audioTrack (o id da faixa, não a posição — os ids do hls.js podem
// não ser 0-based); Safari/iOS HLS nativo via a AudioTrackList do WebKit
// (enabled). No-op quando a engine tem ≤1 faixa (o master não trouxe renditions).
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
