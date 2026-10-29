// Lane W2: the auto-subtitle pipeline's ranking + gating + cadence.
// rankAutoSubtitles orders candidates pt-BR > pt > other with hearing-impaired
// and forced variants last; useSubtitleAutoFetch polls subtitleAutoStatus once
// per (torrent, file), re-checks pending answers at 1s for the first 10
// attempts then backs off to 2s (~50s window sized for the backend's 15s hash
// + search + download cycle — cross-cutting review finding 3), aborts in-flight
// work on a file switch, and FAILS CLOSED on every gate: a saved per-file
// choice, any session pick, manualPicked, audio files, auto_fetch=false (both
// the threaded prop and a fresh settings read) and a terminal 'none'. The
// status endpoint KICKS the server-side fetch, so the gates must keep it from
// being called at all when disabled.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, renderHook } from '@testing-library/react'

const mocks = vi.hoisted(() => ({
  subtitleAutoStatus: vi.fn(),
  getSubtitleSettings: vi.fn(),
}))

vi.mock('../../api/client', async (importOriginal) => ({
  // Real module for everything else (URL builders, types) — only the two
  // pipeline calls are faked.
  ...(await importOriginal<typeof import('../../api/client')>()),
  subtitleAutoStatus: mocks.subtitleAutoStatus,
  getSubtitleSettings: mocks.getSubtitleSettings,
}))

import type { TorrentInfo } from '../../api/client'
import { load, save } from '../../lib/storage'
import {
  hasSavedSubtitleChoice,
  rankAutoSubtitles,
  useSubtitleAutoFetch,
  SUBTITLE_AUTO_BACKOFF_POLL_MS,
  SUBTITLE_AUTO_FAST_ATTEMPTS,
  SUBTITLE_AUTO_MAX_ATTEMPTS,
  SUBTITLE_AUTO_POLL_MS,
} from './playerHooks'

const HASH = 'a'.repeat(40)
const SETTINGS_ON = { default_languages: 'pt-BR,pt', auto_fetch: true, burn_default: false }

function torrentInfo(): TorrentInfo {
  return {
    infoHash: HASH,
    name: 'Show S01E01',
    totalSize: 100,
    files: [{ index: 0, path: 'Show S01E01.mp4', size: 1, isVideo: true, downloaded: 1, progress: 1, priority: 'normal' }],
    peers: 1,
    seeders: 2,
    downRate: 0,
    upRate: 0,
    progress: 1,
    primaryFile: 0,
  }
}

type AutoFetchOpts = Parameters<typeof useSubtitleAutoFetch>[0]

function makeOpts(over: Partial<AutoFetchOpts> = {}): AutoFetchOpts {
  return {
    info: torrentInfo(),
    selectedFile: 0,
    serverReady: true,
    isVideoFile: true,
    autoFetchEnabled: true,
    manualPicked: false,
    subActive: null,
    embeddedSub: null,
    sidecarIdx: null,
    customSubURL: null,
    onReady: vi.fn(),
    onFailed: vi.fn(),
    ...over,
  }
}

// Flush the settings read + the first status tick (fake timers keep the poll
// cadence deterministic; microtasks resolve through advanceTimersByTimeAsync).
async function flush() {
  await act(async () => { await vi.advanceTimersByTimeAsync(0) })
}

beforeEach(() => {
  localStorage.clear()
  mocks.subtitleAutoStatus.mockReset()
  mocks.getSubtitleSettings.mockReset().mockResolvedValue(SETTINGS_ON)
})

afterEach(() => {
  cleanup()
  vi.useRealTimers()
})

describe('rankAutoSubtitles ordering', () => {
  it('ranks pt-BR above pt above any other language', () => {
    const ranked = rankAutoSubtitles([
      { id: 'en', language: 'en' },
      { id: 'pt', language: 'pt' },
      { id: 'ptbr', language: 'pt-BR' },
    ])
    expect(ranked.map((s) => s.id)).toEqual(['ptbr', 'pt', 'en'])
  })

  it('matches languages case-insensitively', () => {
    const ranked = rankAutoSubtitles([
      { id: 'pt', language: 'PT' },
      { id: 'ptbr', language: 'PT-br' },
    ])
    expect(ranked.map((s) => s.id)).toEqual(['ptbr', 'pt'])
  })

  it('sorts hearing-impaired and forced variants last within a language tier', () => {
    const ranked = rankAutoSubtitles([
      { id: 'hi-forced', language: 'pt-BR', hearingImpaired: true, forced: true },
      { id: 'forced', language: 'pt-BR', forced: true },
      { id: 'clean', language: 'pt-BR' },
      { id: 'hi', language: 'pt-BR', hearingImpaired: true },
    ])
    // Comparator contract: within a language tier, hearingImpaired dominates
    // forced — clean < forced < hi < hi-forced.
    expect(ranked.map((s) => s.id)).toEqual(['clean', 'forced', 'hi', 'hi-forced'])
  })

  it('keeps the server order for equal candidates and does not mutate the input', () => {
    const input = [
      { id: 'first', language: 'en' },
      { id: 'second', language: 'en' },
    ]
    const ranked = rankAutoSubtitles(input)
    expect(ranked.map((s) => s.id)).toEqual(['first', 'second'])
    expect(input.map((s) => s.id)).toEqual(['first', 'second'])
  })
})

describe('hasSavedSubtitleChoice', () => {
  it('is false without a torrent or a valid file index', () => {
    expect(hasSavedSubtitleChoice(null, 0)).toBe(false)
    expect(hasSavedSubtitleChoice(torrentInfo(), -1)).toBe(false)
  })

  it('is false when nothing or an all-null choice is saved', () => {
    expect(hasSavedSubtitleChoice(torrentInfo(), 0)).toBe(false)
    save(`sub.${HASH}.0`, { external: null, embedded: null, sidecar: null, offset: 0 })
    expect(hasSavedSubtitleChoice(torrentInfo(), 0)).toBe(false)
  })

  it('is true when any of external/embedded/sidecar is saved', () => {
    save(`sub.${HASH}.0`, { external: '77', embedded: null, sidecar: null, offset: 0 })
    expect(hasSavedSubtitleChoice(torrentInfo(), 0)).toBe(true)
    save(`sub.${HASH}.0`, { external: null, embedded: 1, sidecar: null, offset: 0 })
    expect(hasSavedSubtitleChoice(torrentInfo(), 0)).toBe(true)
    save(`sub.${HASH}.0`, { external: null, embedded: null, sidecar: 2, offset: 0 })
    expect(hasSavedSubtitleChoice(torrentInfo(), 0)).toBe(true)
  })
})

describe('useSubtitleAutoFetch', () => {
  it('fires once per file and applies the ready answer as the dedicated auto pick', async () => {
    vi.useFakeTimers()
    mocks.subtitleAutoStatus.mockResolvedValueOnce({ status: 'ready', subtitle: { id: '77', language: 'pt-BR' } })
    const o = makeOpts()
    const { rerender } = renderHook(({ v }) => useSubtitleAutoFetch(v), { initialProps: { v: o } })
    await flush()

    expect(mocks.getSubtitleSettings).toHaveBeenCalledTimes(1)
    expect(mocks.subtitleAutoStatus).toHaveBeenCalledTimes(1)
    expect(mocks.subtitleAutoStatus).toHaveBeenCalledWith(HASH, 0, expect.any(AbortSignal))
    expect(o.onReady).toHaveBeenCalledTimes(1)
    expect(o.onReady).toHaveBeenCalledWith({ id: '77', language: 'pt-BR' })
    expect(o.onFailed).not.toHaveBeenCalled()

    // Same deps again (debounce of unrelated parent renders) → no re-fire.
    rerender({ v: o })
    await flush()
    expect(mocks.subtitleAutoStatus).toHaveBeenCalledTimes(1)
  })

  it('re-checks the first pending answers at the 1s cadence and calls onReady exactly once', async () => {
    vi.useFakeTimers()
    mocks.subtitleAutoStatus
      .mockResolvedValueOnce({ status: 'pending' })
      .mockResolvedValueOnce({ status: 'pending' })
      .mockResolvedValueOnce({ status: 'ready', subtitle: { id: '5', language: 'pt' } })
    const o = makeOpts()
    renderHook(() => useSubtitleAutoFetch(o))
    await flush()
    expect(mocks.subtitleAutoStatus).toHaveBeenCalledTimes(1)
    expect(o.onReady).not.toHaveBeenCalled()

    await act(async () => { await vi.advanceTimersByTimeAsync(SUBTITLE_AUTO_POLL_MS) })
    expect(mocks.subtitleAutoStatus).toHaveBeenCalledTimes(2)

    await act(async () => { await vi.advanceTimersByTimeAsync(SUBTITLE_AUTO_POLL_MS) })
    expect(mocks.subtitleAutoStatus).toHaveBeenCalledTimes(3)
    expect(o.onReady).toHaveBeenCalledTimes(1)
    expect(o.onReady).toHaveBeenCalledWith({ id: '5', language: 'pt' })

    // Settled: more time passing never re-applies (toast/pick happen once per file).
    await act(async () => { await vi.advanceTimersByTimeAsync(SUBTITLE_AUTO_POLL_MS * 5) })
    expect(mocks.subtitleAutoStatus).toHaveBeenCalledTimes(3)
    expect(o.onReady).toHaveBeenCalledTimes(1)
  })

  it('stops at the attempt budget when the pipeline stays pending', async () => {
    vi.useFakeTimers()
    mocks.subtitleAutoStatus.mockResolvedValue({ status: 'pending' })
    const o = makeOpts()
    renderHook(() => useSubtitleAutoFetch(o))
    // 49s exhausts the budget (9×1s + 20×2s); the ×2 headroom proves it settles
    // AT the budget instead of polling forever.
    await act(async () => { await vi.advanceTimersByTimeAsync(SUBTITLE_AUTO_POLL_MS * SUBTITLE_AUTO_MAX_ATTEMPTS * 2) })
    expect(mocks.subtitleAutoStatus).toHaveBeenCalledTimes(SUBTITLE_AUTO_MAX_ATTEMPTS)
    expect(o.onReady).not.toHaveBeenCalled()
    expect(o.onFailed).not.toHaveBeenCalled()
  })

  // Cross-cutting review finding 3: the ~50s window must actually cover the
  // backend's 15s hash budget + search + download cycle. The backoff shape is
  // pinned explicitly: 10 fast attempts, then 2s — and the phase transition is
  // NOT 1s.
  it('backs off to 2s after the first 10 fast attempts (~50s window over the fetch cycle)', async () => {
    vi.useFakeTimers()
    mocks.subtitleAutoStatus.mockResolvedValue({ status: 'pending' })
    const o = makeOpts()
    renderHook(() => useSubtitleAutoFetch(o))
    // Attempts 1..10 fire at 1s intervals: 9s after the first call all ten ran.
    await act(async () => { await vi.advanceTimersByTimeAsync(SUBTITLE_AUTO_POLL_MS * (SUBTITLE_AUTO_FAST_ATTEMPTS - 1)) })
    expect(mocks.subtitleAutoStatus).toHaveBeenCalledTimes(SUBTITLE_AUTO_FAST_ATTEMPTS)
    // Attempt 11 is NOT due 1s after the 10th — only at +2s.
    await act(async () => { await vi.advanceTimersByTimeAsync(SUBTITLE_AUTO_POLL_MS) })
    expect(mocks.subtitleAutoStatus).toHaveBeenCalledTimes(SUBTITLE_AUTO_FAST_ATTEMPTS)
    await act(async () => { await vi.advanceTimersByTimeAsync(SUBTITLE_AUTO_BACKOFF_POLL_MS) })
    expect(mocks.subtitleAutoStatus).toHaveBeenCalledTimes(SUBTITLE_AUTO_FAST_ATTEMPTS + 1)
    // Beyond the budget (well past the ~49s window) the poller never exceeds it.
    await act(async () => { await vi.advanceTimersByTimeAsync(SUBTITLE_AUTO_BACKOFF_POLL_MS * SUBTITLE_AUTO_MAX_ATTEMPTS) })
    expect(mocks.subtitleAutoStatus).toHaveBeenCalledTimes(SUBTITLE_AUTO_MAX_ATTEMPTS)
    expect(o.onReady).not.toHaveBeenCalled()
    expect(o.onFailed).not.toHaveBeenCalled()
  })

  it('aborts the in-flight status call on a file switch and drops the stale answer', async () => {
    vi.useFakeTimers()
    let resolveStale!: (v: unknown) => void
    mocks.subtitleAutoStatus.mockImplementationOnce(() => new Promise((resolve) => { resolveStale = resolve }))
    mocks.subtitleAutoStatus.mockResolvedValueOnce({ status: 'ready', subtitle: { id: '9', language: 'pt-BR' } })
    const first = makeOpts()
    const { rerender } = renderHook(({ v }) => useSubtitleAutoFetch(v), { initialProps: { v: first } })
    await flush()
    expect(mocks.subtitleAutoStatus).toHaveBeenCalledTimes(1)
    const staleSignal = mocks.subtitleAutoStatus.mock.calls[0][2] as AbortSignal
    expect(staleSignal.aborted).toBe(false)

    rerender({ v: makeOpts({ selectedFile: 1 }) })
    await flush()
    // The abandoned controller was aborted and the next file polls on its own.
    expect(staleSignal.aborted).toBe(true)
    expect(mocks.subtitleAutoStatus).toHaveBeenCalledTimes(2)
    expect(mocks.subtitleAutoStatus).toHaveBeenLastCalledWith(HASH, 1, expect.any(AbortSignal))

    // The stale ready answer must NEVER apply after the switch.
    resolveStale({ status: 'ready', subtitle: { id: '77', language: 'pt-BR' } })
    await flush()
    expect(first.onReady).not.toHaveBeenCalled()
  })

  it('does not fire when a saved per-file choice exists', async () => {
    vi.useFakeTimers()
    save(`sub.${HASH}.0`, { external: '77', embedded: null, sidecar: null, offset: 0 })
    const o = makeOpts()
    renderHook(() => useSubtitleAutoFetch(o))
    await flush()
    expect(mocks.subtitleAutoStatus).not.toHaveBeenCalled()
    expect(o.onReady).not.toHaveBeenCalled()
  })

  it('does not fire when a session pick already exists (embedded)', async () => {
    vi.useFakeTimers()
    const o = makeOpts({ embeddedSub: 1 })
    renderHook(() => useSubtitleAutoFetch(o))
    await flush()
    expect(mocks.subtitleAutoStatus).not.toHaveBeenCalled()
  })

  it('does not fire when manualPicked is set (manual pick disabled the session)', async () => {
    vi.useFakeTimers()
    const o = makeOpts({ manualPicked: true })
    renderHook(() => useSubtitleAutoFetch(o))
    await flush()
    expect(mocks.subtitleAutoStatus).not.toHaveBeenCalled()
  })

  it('does not fire when the threaded auto_fetch flag is false', async () => {
    vi.useFakeTimers()
    const o = makeOpts({ autoFetchEnabled: false })
    renderHook(() => useSubtitleAutoFetch(o))
    await flush()
    // Fail closed BEFORE even reading settings: the endpoint kicks the fetch.
    expect(mocks.getSubtitleSettings).not.toHaveBeenCalled()
    expect(mocks.subtitleAutoStatus).not.toHaveBeenCalled()
  })

  it('fails closed when the settings read says auto_fetch is off', async () => {
    vi.useFakeTimers()
    mocks.getSubtitleSettings.mockResolvedValue({ ...SETTINGS_ON, auto_fetch: false })
    const o = makeOpts()
    renderHook(() => useSubtitleAutoFetch(o))
    await flush()
    expect(mocks.getSubtitleSettings).toHaveBeenCalledTimes(1)
    expect(mocks.subtitleAutoStatus).not.toHaveBeenCalled()
    expect(o.onFailed).not.toHaveBeenCalled()
  })

  it('skips silently when the settings read fails', async () => {
    vi.useFakeTimers()
    mocks.getSubtitleSettings.mockRejectedValue(new Error('offline'))
    const o = makeOpts()
    renderHook(() => useSubtitleAutoFetch(o))
    await flush()
    expect(mocks.subtitleAutoStatus).not.toHaveBeenCalled()
    expect(o.onFailed).not.toHaveBeenCalled()
  })

  it('treats a none answer as terminal: no callbacks, no further polls', async () => {
    vi.useFakeTimers()
    mocks.subtitleAutoStatus.mockResolvedValueOnce({ status: 'none' })
    const o = makeOpts()
    renderHook(() => useSubtitleAutoFetch(o))
    await flush()
    expect(mocks.subtitleAutoStatus).toHaveBeenCalledTimes(1)
    expect(o.onReady).not.toHaveBeenCalled()
    expect(o.onFailed).not.toHaveBeenCalled()
    await act(async () => { await vi.advanceTimersByTimeAsync(SUBTITLE_AUTO_POLL_MS * 3) })
    expect(mocks.subtitleAutoStatus).toHaveBeenCalledTimes(1)
  })

  it.each([
    ['quota', 'quota'],
    ['no_results', 'no_results'],
  ] as const)('reports a failed pipeline with its reason (%s)', async (_name, reason) => {
    vi.useFakeTimers()
    mocks.subtitleAutoStatus.mockResolvedValueOnce({ status: 'failed', reason })
    const o = makeOpts()
    renderHook(() => useSubtitleAutoFetch(o))
    await flush()
    expect(o.onFailed).toHaveBeenCalledTimes(1)
    expect(o.onFailed).toHaveBeenCalledWith(reason)
    expect(o.onReady).not.toHaveBeenCalled()
  })

  it('reports a status-call error without a reason', async () => {
    vi.useFakeTimers()
    mocks.subtitleAutoStatus.mockRejectedValueOnce(new Error('502'))
    const o = makeOpts()
    renderHook(() => useSubtitleAutoFetch(o))
    await flush()
    expect(o.onFailed).toHaveBeenCalledTimes(1)
    expect(o.onFailed).toHaveBeenCalledWith(undefined)
  })

  it('never writes the per-file choice key (the auto pick is not persisted)', async () => {
    vi.useFakeTimers()
    mocks.subtitleAutoStatus.mockResolvedValueOnce({ status: 'ready', subtitle: { id: '77', language: 'pt-BR' } })
    renderHook(() => useSubtitleAutoFetch(makeOpts()))
    await flush()
    expect(load(`sub.${HASH}.0`, null)).toBeNull()
  })
})
