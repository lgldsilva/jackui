// Correctness regression: the 2s progress poll
// `streamInfo(info.infoHash).then(setInfo)` had no staleness guard — an
// in-flight tick started for the PREVIOUS track resolves after the switch and
// stomps the new track's `info` (wrong file list/progress in the player UI).
// Mirrors DownloadsPage's loadSeqRef pattern: a poll may only apply its result
// while the hash it was started for is still the current one.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, renderHook } from '@testing-library/react'
import { useStreamSession } from './useStreamSession'
import type { SearchResult, TorrentInfo, TranscodeCapabilities } from '../../api/client'

const mocks = vi.hoisted(() => ({
  streamAdd: vi.fn(),
  streamMetadata: vi.fn(),
  streamInfo: vi.fn(),
  streamViewerOpen: vi.fn(),
  streamViewerClose: vi.fn(),
  subtitlesEnabled: vi.fn(),
  fetchMediaToken: vi.fn(),
  transcodeCapabilities: vi.fn(),
  pickTorrentSource: vi.fn((r: { magnetUri?: string; link?: string }) => r.magnetUri || r.link || ''),
}))

vi.mock('../../api/client', () => ({
  api: { get: vi.fn(), post: vi.fn() },
  streamAdd: mocks.streamAdd,
  streamMetadata: mocks.streamMetadata,
  streamInfo: mocks.streamInfo,
  streamViewerOpen: mocks.streamViewerOpen,
  streamViewerClose: mocks.streamViewerClose,
  subtitlesEnabled: mocks.subtitlesEnabled,
  fetchMediaToken: mocks.fetchMediaToken,
  transcodeCapabilities: mocks.transcodeCapabilities,
  pickTorrentSource: mocks.pickTorrentSource,
}))

const HASH_A = 'a'.repeat(40)
const HASH_B = 'b'.repeat(40)

function makeResult(infoHash: string): SearchResult {
  return {
    title: `Track ${infoHash[0]}`,
    tracker: 'MockTracker',
    categoryId: 3000,
    category: 'Music',
    size: 10_000_000,
    seeders: 10,
    leechers: 1,
    age: '1h',
    magnetUri: `magnet:?xt=urn:btih:${infoHash}`,
    link: 'https://mock.tracker/dl.torrent',
    infoHash,
    publishDate: '2024-01-01',
  }
}

function torrent(hash: string, progress: number): TorrentInfo {
  return {
    infoHash: hash,
    name: `torrent-${hash[0]}`,
    totalSize: 100,
    files: [],
    peers: 1,
    seeders: 2,
    downRate: 0,
    upRate: 0,
    progress,
    primaryFile: 0,
  }
}

type Deps = Parameters<typeof useStreamSession>[0]

// One deps object per test: the setter mocks keep a stable identity across
// rerenders so assertions can inspect the accumulated setInfo calls.
function makeDeps(): Deps {
  return {
    result: null,
    audioMode: true,
    initialFileIndex: undefined,
    t: ((k: string) => k) as Deps['t'],
    info: null,
    selectedFile: 0,
    caps: {} as TranscodeCapabilities, // non-null → caps probe branch skipped
    blessed: false,
    setLoading: vi.fn(),
    setError: vi.fn(),
    setInfo: vi.fn(),
    setSelectedFile: vi.fn(),
    setServerReady: vi.fn(),
    setMediaToken: vi.fn(),
    setSubEnabled: vi.fn(),
    setCaps: vi.fn(),
    setBlessed: vi.fn(),
    resetForNewResult: vi.fn(),
  }
}

beforeEach(() => {
  vi.useFakeTimers()
  mocks.streamMetadata.mockResolvedValue(null)
  mocks.streamViewerOpen.mockResolvedValue(undefined)
  mocks.streamViewerClose.mockResolvedValue(undefined)
  mocks.subtitlesEnabled.mockResolvedValue(false)
  mocks.fetchMediaToken.mockResolvedValue('media-token')
  mocks.transcodeCapabilities.mockResolvedValue({})
})

afterEach(() => {
  cleanup()
  vi.useRealTimers()
  vi.restoreAllMocks()
})

async function flushMicrotasks() {
  await act(async () => { await vi.advanceTimersByTimeAsync(0) })
}

async function tick() {
  await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
}

describe('useStreamSession — progress poll staleness guard', () => {
  it('does NOT apply an in-flight poll tick from the previous track after the switch', async () => {
    const deps = makeDeps()
    // Track A opens; streamAdd resolves with the authoritative info.
    mocks.streamAdd.mockResolvedValueOnce(torrent(HASH_A, 0.5))
    const initial: Deps = { ...deps, result: makeResult(HASH_A), info: null }
    const { rerender } = renderHook((d: Deps) => useStreamSession(d), { initialProps: initial })
    await flushMicrotasks()
    expect(deps.setInfo).toHaveBeenCalledWith(expect.objectContaining({ infoHash: HASH_A, progress: 0.5 }))

    // Parent state mirrors setInfo(torrentA) → the poll effect starts for A.
    rerender({ ...deps, result: makeResult(HASH_A), info: torrent(HASH_A, 0.5) })
    await flushMicrotasks()

    // First tick starts an in-flight streamInfo(A) that stays unresolved
    // across the track switch.
    let resolveA: (v: TorrentInfo) => void = () => {}
    mocks.streamInfo.mockImplementationOnce(() => new Promise<TorrentInfo>(res => { resolveA = res }))
    await tick()
    expect(mocks.streamInfo).toHaveBeenCalledWith(HASH_A)

    // User switches to track B: streamAdd resolves B, parent state follows.
    mocks.streamAdd.mockResolvedValueOnce(torrent(HASH_B, 0.1))
    rerender({ ...deps, result: makeResult(HASH_B), info: null })
    await flushMicrotasks()
    rerender({ ...deps, result: makeResult(HASH_B), info: torrent(HASH_B, 0.1) })
    await flushMicrotasks()

    // A tick for B runs and applies normally.
    mocks.streamInfo.mockResolvedValueOnce(torrent(HASH_B, 0.2))
    await tick()
    expect(deps.setInfo).toHaveBeenCalledWith(expect.objectContaining({ infoHash: HASH_B, progress: 0.2 }))

    // THEN the stale tick for A finally resolves — it must be dropped.
    resolveA(torrent(HASH_A, 0.99))
    await flushMicrotasks()

    expect(deps.setInfo).not.toHaveBeenCalledWith(expect.objectContaining({ infoHash: HASH_A, progress: 0.99 }))
  })

  it('still applies poll ticks for the CURRENT hash (guard is not over-eager)', async () => {
    const deps = makeDeps()
    mocks.streamAdd.mockResolvedValue(torrent(HASH_A, 0.5))
    const initial: Deps = { ...deps, result: makeResult(HASH_A), info: null }
    const { rerender } = renderHook((d: Deps) => useStreamSession(d), { initialProps: initial })
    await flushMicrotasks()
    rerender({ ...deps, result: makeResult(HASH_A), info: torrent(HASH_A, 0.5) })
    await flushMicrotasks()

    mocks.streamInfo.mockResolvedValue(torrent(HASH_A, 0.6))
    await tick()

    expect(deps.setInfo).toHaveBeenCalledWith(expect.objectContaining({ infoHash: HASH_A, progress: 0.6 }))
  })
})
