// shuffledOrder returns a permutation of [0..n-1] with `startIndex` fixed at
// position 0 (the current item keeps playing) and the rest shuffled by
// Fisher-Yates using crypto.getRandomValues. Guarantees "bag shuffle": a
// permutation covers every index exactly once → none repeats until all
// have played. Shared by the PLAYLIST level (PlayerProvider) and the
// TRACK level (useTrackOrder).
export function shuffledOrder(n: number, startIndex: number): number[] {
  if (n <= 0) return []
  const rest = Array.from({ length: n }, (_, i) => i).filter(i => i !== startIndex)
  const rand = new Uint32Array(rest.length)
  crypto.getRandomValues(rand)
  for (let i = rest.length - 1; i > 0; i--) {
    const j = rand[i] % (i + 1)
    ;[rest[i], rest[j]] = [rest[j], rest[i]]
  }
  // startIndex only goes in front when it's a valid index; otherwise
  // (e.g. -1, no current track) returns just the already-shuffled rest.
  return startIndex >= 0 && startIndex < n ? [startIndex, ...rest] : rest
}
