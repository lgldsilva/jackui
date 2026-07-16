import { describe, expect, it, vi } from 'vitest'
import { BATCH_CAPS, runChunked } from './batchChunk'

type R = { affected: number; total: number; failed: string[] }
const merge = (a: R, b: R): R => ({
  affected: a.affected + b.affected,
  total: a.total + b.total,
  failed: [...a.failed, ...b.failed],
})
const empty: R = { affected: 0, total: 0, failed: [] }

describe('runChunked', () => {
  it('returns empty for an empty input without calling run', async () => {
    const run = vi.fn()
    expect(await runChunked([], 3, run, merge, empty)).toEqual(empty)
    expect(run).not.toHaveBeenCalled()
  })

  it('runs a single un-sliced call when the input fits the chunk size', async () => {
    const run = vi.fn(async (chunk: number[]) => ({ affected: chunk.length, total: chunk.length, failed: [] }))
    const out = await runChunked([1, 2, 3], 3, run, merge, empty)
    expect(run).toHaveBeenCalledTimes(1)
    expect(run).toHaveBeenCalledWith([1, 2, 3])
    expect(out.affected).toBe(3)
  })

  it('splits above the cap and aggregates every chunk (no lost items)', async () => {
    const seen: number[][] = []
    const run = async (chunk: number[]): Promise<R> => {
      seen.push(chunk)
      return { affected: chunk.length, total: chunk.length, failed: [] }
    }
    const items = Array.from({ length: 7 }, (_, i) => i)
    const out = await runChunked(items, 3, run, merge, empty)
    expect(seen).toEqual([[0, 1, 2], [3, 4, 5], [6]])
    expect(out.affected).toBe(7)
    expect(out.total).toBe(7)
  })

  it('merges per-chunk failed lists across chunks', async () => {
    const run = async (chunk: string[]): Promise<R> => ({
      affected: chunk.length - 1,
      total: chunk.length,
      failed: chunk.slice(0, 1),
    })
    const out = await runChunked(['a', 'b', 'c', 'd'], 2, run, merge, empty)
    expect(out.failed).toEqual(['a', 'c'])
  })

  it('a chunk of exactly the cap is a single call (server rejects only len > cap)', async () => {
    const run = vi.fn(async (chunk: number[]) => ({ affected: chunk.length, total: chunk.length, failed: [] }))
    const items = Array.from({ length: BATCH_CAPS.streamDrop }, (_, i) => i)
    await runChunked(items, BATCH_CAPS.streamDrop, run, merge, empty)
    expect(run).toHaveBeenCalledTimes(1)
  })
})
