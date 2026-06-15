import { usePersistedState } from '../../lib/storage'

// Transição entre faixas no player de música. Só vale para faixas DIRECT-PLAY
// (áudio que toca sem transcode HLS); HLS/vídeo/'off' caem no comportamento atual.
export type TransitionMode = 'off' | 'gapless' | 'crossfade'

export const CROSSFADE_MIN = 1
export const CROSSFADE_MAX = 12
export const CROSSFADE_DEFAULT = 6

const AUDIO_DIRECT_RE = /\.(mp3|m4a|aac|ogg|opus|wav|flac|alac|wma)$/i

// looksDirectAudio: a faixa provavelmente toca DIRECT (sem transcode HLS)? É só
// uma heurística barata pra decidir se VALE pré-armar a transição da PRÓXIMA
// faixa (ainda não temos o probe dela). A verdade final da faixa ATUAL é o
// `isTranscoded` (computeMediaUrls). Se a próxima resolver pra HLS ao carregar,
// o engine cai em hard-cut (sem regressão).
export function looksDirectAudio(path: string): boolean {
  return AUDIO_DIRECT_RE.test(path)
}

export function clampCrossfadeSec(sec: number): number {
  return Math.min(CROSSFADE_MAX, Math.max(CROSSFADE_MIN, sec))
}

// useTransitionConfig lê/escreve a preferência de transição (localStorage,
// namespace jackui:). crossfadeSec é sempre clampado a [1,12].
export function useTransitionConfig() {
  const [mode, setMode] = usePersistedState<TransitionMode>('audio:transition', 'off')
  const [sec, setSec] = usePersistedState<number>('audio:crossfadeSec', CROSSFADE_DEFAULT)
  return { mode, setMode, crossfadeSec: clampCrossfadeSec(sec), setSec }
}
