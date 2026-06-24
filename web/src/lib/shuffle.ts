// shuffledOrder devolve uma permutação de [0..n-1] com `startIndex` fixo na
// posição 0 (o item atual continua tocando) e o restante embaralhado por
// Fisher-Yates usando crypto.getRandomValues. Garante "bag shuffle": uma
// permutação cobre todos os índices uma única vez → nenhum repete até todos
// tocarem. Compartilhado pelo nível PLAYLIST (PlayerProvider) e pelo nível
// FAIXA (useTrackOrder).
export function shuffledOrder(n: number, startIndex: number): number[] {
  if (n <= 0) return []
  const rest = Array.from({ length: n }, (_, i) => i).filter(i => i !== startIndex)
  const rand = new Uint32Array(rest.length)
  crypto.getRandomValues(rand)
  for (let i = rest.length - 1; i > 0; i--) {
    const j = rand[i] % (i + 1)
    ;[rest[i], rest[j]] = [rest[j], rest[i]]
  }
  // startIndex só entra na frente quando é um índice válido; caso contrário
  // (ex.: -1, sem faixa atual) devolve só o restante já embaralhado.
  return startIndex >= 0 && startIndex < n ? [startIndex, ...rest] : rest
}
