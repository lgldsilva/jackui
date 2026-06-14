import { type TransitionMode, looksDirectAudio } from './transition'
import type { PlaylistGroup } from './playlistTracks'

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

// TrackRef descreve a próxima faixa a tocar pelo motor. itemIndex < 0 = MESMO
// álbum/torrent atual (avança via playFile, sem trocar de item); itemIndex >= 0 =
// CROSS-ITEM (próximo item da playlist, avança via onPlaylistJump).
export type TrackRef = { itemIndex: number; infoHash: string; fileIndex: number; path: string }

// firstDirectAudioTrack: a 1ª faixa de ÁUDIO direct-play do próximo item da
// playlist (grupo já resolvido), ou null (item não resolvido / sem faixa direct
// → o motor cai em hard-cut e o avanço normal assume). Pura.
export function firstDirectAudioTrack(groups: readonly PlaylistGroup[], nextItemIndex: number): TrackRef | null {
  if (nextItemIndex < 0) return null
  const g = groups.find((x) => x.itemIndex === nextItemIndex)
  if (!g || g.status !== 'ready') return null
  const tr = g.tracks.find((t) => t.kind === 'audio' && looksDirectAudio(t.path))
  return tr ? { itemIndex: nextItemIndex, infoHash: g.infoHash, fileIndex: tr.fileIndex, path: tr.path } : null
}

// resolveEngineNext: a próxima faixa que o motor deve pré-carregar/transicionar,
// ou null (hard-cut → o avanço normal assume). Pura.
//   1) Próxima do ÁLBUM atual (mesmo torrent): numa playlist multi-item NÃO
//      circula (o fim do álbum transborda pro próximo item); álbum solto respeita
//      repeat via peekNextIndex (all loopa, none para). Só se for direct-play.
//   2) Fim do álbum + playlist → 1ª faixa direct do próximo item (firstDirectAudioTrack).
export function resolveEngineNext(input: {
  inPlaylist: boolean
  mediaIndices: readonly number[]
  mediaCursor: number
  repeat: RepeatMode
  curInfoHash: string
  curFiles: readonly { index: number; path: string }[]
  groups: readonly PlaylistGroup[]
  nextItemIndex: number
}): TrackRef | null {
  let albumNextPos: number
  if (input.inPlaylist) {
    // Numa playlist o álbum NÃO circula (o fim transborda pro próximo item).
    const hasNext = input.mediaCursor >= 0 && input.mediaCursor + 1 < input.mediaIndices.length
    albumNextPos = hasNext ? input.mediaCursor + 1 : -1
  } else {
    // Álbum solto: respeita repeat (all loopa, none para).
    albumNextPos = peekNextIndex(input.mediaIndices.length, input.mediaCursor, input.repeat)
  }
  if (albumNextPos >= 0) {
    const fileIndex = input.mediaIndices[albumNextPos]
    const path = input.curFiles.find((f) => f.index === fileIndex)?.path ?? ''
    // Próxima do álbum não-direct (ex.: um .mkv/HLS no meio do álbum) → hard-cut
    // (null); o motor não tem como transicionar pra HLS.
    return looksDirectAudio(path) ? { itemIndex: -1, infoHash: input.curInfoHash, fileIndex, path } : null
  }
  return firstDirectAudioTrack(input.groups, input.nextItemIndex)
}

// engineEligible: o motor deve assumir a faixa ATUAL? Só áudio direct-play com
// transição ligada — HLS/vídeo/'off' seguem o caminho normal do <video>. Em
// repeat 'one' também caímos no caminho normal (o <video> faz o replay-loop da
// MESMA faixa; o ping-pong do motor é pra faixas DIFERENTES).
export function engineEligible(opts: { mode: TransitionMode; isAudio: boolean; isTranscoded: boolean; repeat: RepeatMode }): boolean {
  return opts.mode !== 'off' && opts.isAudio && !opts.isTranscoded && opts.repeat !== 'one'
}
