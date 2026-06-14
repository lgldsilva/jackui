import type { TransitionMode } from './transition'

// Decisões PURAS do motor de áudio gapless/crossfade (useAudioEngine). Isoladas
// aqui pra serem testadas em node (sem DOM/AudioContext). O efeito real (rampa de
// ganho, play(), readyState) fica no hook, que delega estas decisões pra cá.

export type RepeatMode = 'none' | 'one' | 'all'

// crossfadeDue: a faixa atual entrou na janela de crossfade? Resta
// <= crossfadeSec até o fim, com duração conhecida e finita. (O readyState da
// próxima e o 'fading' em curso são checados à parte, no hook — não são puros.)
export function crossfadeDue(currentTime: number, duration: number, crossfadeSec: number): boolean {
  if (!Number.isFinite(duration) || duration <= 0) return false
  return duration - currentTime <= crossfadeSec
}

// peekNextIndex: índice da próxima faixa na fila, JÁ em ordem de reprodução
// (shuffle é resolvido a montante, ao montar a lista). Respeita repeat:
//   'one' → mesma faixa (replay/loop)
//   'all' → circula pro início no fim
//   'none' → -1 no fim
// -1 também quando a lista está vazia ou o índice atual é inválido.
export function peekNextIndex(length: number, currentIndex: number, repeat: RepeatMode): number {
  if (length <= 0 || currentIndex < 0 || currentIndex >= length) return -1
  if (repeat === 'one') return currentIndex
  const next = currentIndex + 1
  if (next < length) return next
  return repeat === 'all' ? 0 : -1
}

// engineEligible: o motor deve assumir a faixa ATUAL? Só áudio direct-play com
// transição ligada — HLS/vídeo/'off' seguem o caminho normal do <video>.
export function engineEligible(opts: { mode: TransitionMode; isAudio: boolean; isTranscoded: boolean }): boolean {
  return opts.mode !== 'off' && opts.isAudio && !opts.isTranscoded
}
