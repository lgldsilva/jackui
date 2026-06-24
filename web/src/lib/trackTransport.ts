// trackTransport: núcleo PURO da navegação de faixa DENTRO de um torrent
// (álbum com vários arquivos / série com vários episódios). Decide o próximo
// passo dado a ordem de reprodução das faixas (já embaralhada ou não), o
// fileIndex atual, o modo de repetição e se há um contexto de playlist
// multi-torrent para "transbordar" (spill) ao chegar na borda.
//
// repeat-one NÃO é tratado aqui: o replay da mesma faixa acontece no onEnded
// (replay no mesmo elemento <audio>). Os botões prev/next pulam a faixa
// normalmente mesmo em repeat-one — é o comportamento de UX esperado.
export type RepeatMode = 'none' | 'one' | 'all'

export type TrackStep =
  // Tocar esta faixa do MESMO torrent.
  | { readonly kind: 'track'; readonly fileIndex: number }
  // Borda do álbum → delegar para o nível PLAYLIST (próximo/anterior torrent).
  | { readonly kind: 'spill' }
  // repeat-all SEM playlist → re-embaralhar e tocar a 1ª da nova passada.
  | { readonly kind: 'wrap-rebuild' }

// stepAtEnd resolve a borda (fim no next, início no prev): a prioridade do
// spill para a playlist evita "wrap duplo" — num contexto multi-torrent o fim
// do álbum transborda para o próximo torrent e o wrap de repeat-all acontece
// só no nível playlist (goTo). Sem playlist, o wrap-rebuild da faixa é o único.
function stepAtEnd(repeat: RepeatMode, hasPlaylistNeighbor: boolean): TrackStep {
  if (hasPlaylistNeighbor) return { kind: 'spill' }
  if (repeat === 'all') return { kind: 'wrap-rebuild' }
  return { kind: 'spill' }
}

export function nextTrack(
  order: readonly number[],
  currentFileIndex: number,
  repeat: RepeatMode,
  hasPlaylistNext: boolean,
): TrackStep {
  if (order.length === 0) return { kind: 'spill' }
  const cursor = order.indexOf(currentFileIndex)
  // Faixa atual fora da ordem (filtro mudou, etc.) → começa pela 1ª.
  if (cursor < 0) return { kind: 'track', fileIndex: order[0] }
  if (cursor < order.length - 1) return { kind: 'track', fileIndex: order[cursor + 1] }
  return stepAtEnd(repeat, hasPlaylistNext)
}

export function prevTrack(
  order: readonly number[],
  currentFileIndex: number,
  repeat: RepeatMode,
  hasPlaylistPrev: boolean,
): TrackStep {
  if (order.length === 0) return { kind: 'spill' }
  const cursor = order.indexOf(currentFileIndex)
  if (cursor < 0) return { kind: 'track', fileIndex: order[0] }
  if (cursor > 0) return { kind: 'track', fileIndex: order[cursor - 1] }
  // Início do álbum: spill pro torrent anterior; senão repeat-all volta pra última.
  if (hasPlaylistPrev) return { kind: 'spill' }
  if (repeat === 'all') return { kind: 'track', fileIndex: order[order.length - 1] }
  return { kind: 'spill' }
}
