// Client-side chunking for capped batch endpoints.
//
// Each batch route rejects a payload larger than its per-endpoint cap with HTTP
// 413. A "select all" over a big list (600 favourites, a 700-row seeding pack,
// 350 completed torrents) would send the whole set unchunked, hit the cap, and
// the caller's `.catch(() => {})` swallowed the 413 into a SILENT no-op that the
// UI still reported as success. Splitting the input below the cap keeps every
// item processed and lets the wrapper aggregate the per-chunk results.
//
// Chunk sizes mirror the server caps (favoritesBatchMax=500, stopSeed=500,
// streamDropBatchMax=300). The server rejects `len > cap`, so a chunk of exactly
// `cap` passes.

/** Cap (server-side `len > cap` ⇒ 413) for each capped batch endpoint. */
export const BATCH_CAPS = {
  favorites: 500, // favoritesBatchMax — internal/handlers/favorites_batch.go
  stopSeed: 500, // downloadsStopSeedBatchMax — internal/handlers/downloads_batch.go
  streamDrop: 300, // streamDropBatchMax — internal/handlers/stream_favs_controls.go
} as const

// runChunked splits `items` into `chunkSize`-sized slices, runs `run` on each
// (sequentially — the server already fans out per chunk, and the big-list case
// is rare), and folds the results with `merge`, starting from `empty`. Returns
// `empty` for an empty input and a single un-sliced call when it already fits.
export async function runChunked<T, R>(
  items: readonly T[],
  chunkSize: number,
  run: (chunk: T[]) => Promise<R>,
  merge: (a: R, b: R) => R,
  empty: R,
): Promise<R> {
  if (items.length === 0) return empty
  if (items.length <= chunkSize) return run(items.slice())
  let acc = empty
  for (let i = 0; i < items.length; i += chunkSize) {
    acc = merge(acc, await run(items.slice(i, i + chunkSize)))
  }
  return acc
}
