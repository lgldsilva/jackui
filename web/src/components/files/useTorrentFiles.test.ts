import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import type { SearchResult, TorrentInfo } from '../../api/client'
import { useTorrentFiles } from './useTorrentFiles'

const add = vi.fn()
vi.mock('../../api/client', async (orig) => ({
  ...(await orig<typeof import('../../api/client')>()),
  streamMetadata: () => Promise.resolve(null),
  streamAdd: (src: string) => add(src),
}))
afterEach(() => { cleanup(); add.mockReset() })

const msgs = { notYet: 'not yet', fallbackFailed: 'failed' }
const result = { infoHash: 'b'.repeat(40) } as SearchResult
const files = [{ index: 0, path: 'a.mkv', size: 1 }]

describe('useTorrentFiles', () => {
  it('loads files and hands them to onResolved', async () => {
    add.mockResolvedValue({ files } as unknown as TorrentInfo)
    const onResolved = vi.fn()
    const { result: h } = renderHook(() => useTorrentFiles(result, true, msgs, onResolved))
    expect(h.current.loading).toBe(true)
    await waitFor(() => expect(h.current.loading).toBe(false))
    expect(h.current.files).toEqual(files)
    expect(onResolved).toHaveBeenCalledWith(files)
  })

  it('surfaces the add error', async () => {
    add.mockRejectedValue(new Error('no peers'))
    const { result: h } = renderHook(() => useTorrentFiles(result, true, msgs, vi.fn()))
    await waitFor(() => expect(h.current.error).toBe('no peers'))
    expect(h.current.files).toBeNull()
  })

  // Regression: switching to an external client mid-load left the spinner on.
  it('clears loading when disabled mid-load and ignores the late result', async () => {
    let resolve!: (v: unknown) => void
    add.mockImplementation(() => new Promise((r) => { resolve = r }))
    const onResolved = vi.fn()
    const { result: h, rerender } = renderHook(
      ({ on }: { on: boolean }) => useTorrentFiles(result, on, msgs, onResolved),
      { initialProps: { on: true } },
    )
    await waitFor(() => expect(add).toHaveBeenCalled())
    rerender({ on: false })
    expect(h.current.loading).toBe(false)
    await act(async () => { resolve({ files }) })
    expect(h.current.files).toBeNull()
    expect(onResolved).not.toHaveBeenCalled()
  })

  it('does nothing without a result', () => {
    const { result: h } = renderHook(() => useTorrentFiles(null, true, msgs, vi.fn()))
    expect(h.current).toEqual({ files: null, loading: false, error: '' })
    expect(add).not.toHaveBeenCalled()
  })
})
