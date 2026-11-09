import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { TransfersProvider, useTransfers } from './transfers'

const list = vi.fn()
vi.mock('../api/transfers', () => ({
  transfersList: () => list(),
  transferCancel: vi.fn(() => Promise.resolve()),
}))
// Stable identity, like the real AuthContext state (a fresh object per render
// would re-run the provider effect on every render).
const authState = { user: { id: 1 } }
vi.mock('../auth/AuthContext', () => ({ useAuth: () => authState }))

const wrapper = ({ children }: { children: ReactNode }) => <TransfersProvider>{children}</TransfersProvider>

// Resolvers for in-flight transfersList calls, so the test controls ordering.
let pending: Array<(v: unknown[]) => void> = []

beforeEach(() => {
  vi.useFakeTimers()
  pending = []
  list.mockImplementation(() => new Promise((r) => pending.push(r)))
})
afterEach(() => {
  cleanup()
  vi.useRealTimers()
  list.mockReset()
})

const flush = () => act(async () => { await Promise.resolve() })

describe('TransfersProvider polling', () => {
  // Regression: bump() while a request was in flight cleared an already-fired
  // timeout and started a SECOND loop; each bump leaked another permanent poller.
  it('keeps exactly one poll loop across bumps during an in-flight request', async () => {
    const { result } = renderHook(() => useTransfers(), { wrapper })
    expect(list).toHaveBeenCalledTimes(1) // mount poll, still in flight

    act(() => { result.current.bump(); result.current.bump(); result.current.bump() })
    expect(list).toHaveBeenCalledTimes(4)

    // Every request resolves (idle list → 5s cadence).
    await act(async () => { pending.splice(0).forEach((r) => r([])) })
    await flush()

    list.mockClear()
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    expect(list).toHaveBeenCalledTimes(1) // ONE loop rescheduled, not four
    await act(async () => { pending.splice(0).forEach((r) => r([])) })
    await flush()
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    expect(list).toHaveBeenCalledTimes(2)
  })

  it('ignores a stale response that resolves after a newer one', async () => {
    const { result } = renderHook(() => useTransfers(), { wrapper })
    act(() => { result.current.bump() })
    const [first, second] = pending.splice(0)
    const fresh = [{ id: 'new', status: 'running' }]
    await act(async () => { second(fresh) })
    await act(async () => { first([{ id: 'old', status: 'done' }]) })
    expect(result.current.transfers).toEqual(fresh)
  })

  it('pauses while the tab is hidden and resumes on visibilitychange', async () => {
    renderHook(() => useTransfers(), { wrapper })
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(true)
    await act(async () => { pending.splice(0).forEach((r) => r([])) })
    list.mockClear()
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(list).not.toHaveBeenCalled() // paused: no timer rescheduled

    hidden.mockReturnValue(false)
    await act(async () => { document.dispatchEvent(new Event('visibilitychange')) })
    expect(list).toHaveBeenCalledTimes(1)
    hidden.mockRestore()
  })

  it('polls fast while a job is running and cancel() drops it optimistically', async () => {
    const { result } = renderHook(() => useTransfers(), { wrapper })
    await act(async () => { pending.splice(0).forEach((r) => r([{ id: 'j1', status: 'running' }])) })
    expect(result.current.transfers).toHaveLength(1)
    list.mockClear()
    await act(async () => { await vi.advanceTimersByTimeAsync(1000) })
    expect(list).toHaveBeenCalledTimes(1) // 1s cadence while running

    await act(async () => { result.current.cancel('j1') })
    expect(result.current.transfers).toHaveLength(0)
  })

  it('survives a failed poll and keeps polling', async () => {
    renderHook(() => useTransfers(), { wrapper })
    list.mockImplementationOnce(() => Promise.reject(new Error('401')))
    // first (mount) call is pending; fail it via a fresh rejected poll path
    await act(async () => { pending.splice(0).forEach((r) => r([])) })
    list.mockClear()
    list.mockImplementationOnce(() => Promise.reject(new Error('401')))
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    expect(list).toHaveBeenCalledTimes(1)
    list.mockImplementation(() => new Promise((r) => pending.push(r)))
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    expect(list).toHaveBeenCalledTimes(2) // backed off to idle and retried
  })
})
