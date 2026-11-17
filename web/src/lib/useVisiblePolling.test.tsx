import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useVisiblePolling } from './useVisiblePolling'

let hidden = false

function setHidden(value: boolean) {
  hidden = value
  act(() => { document.dispatchEvent(new Event('visibilitychange')) })
}

function deferred() {
  let resolve!: () => void
  let reject!: (err: unknown) => void
  const promise = new Promise<void>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

beforeEach(() => {
  vi.useFakeTimers()
  hidden = false
  vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden)
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.useRealTimers()
})

describe('useVisiblePolling', () => {
  it('runs fn every interval, not on mount', () => {
    const fn = vi.fn()
    renderHook(() => useVisiblePolling(fn, 2000))
    expect(fn).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1999)
    expect(fn).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    expect(fn).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(4000)
    expect(fn).toHaveBeenCalledTimes(3)
  })

  it('always calls the latest fn without restarting the timer', () => {
    const a = vi.fn()
    const b = vi.fn()
    const { rerender } = renderHook(({ fn }) => useVisiblePolling(fn, 2000), { initialProps: { fn: a } })
    vi.advanceTimersByTime(1000)
    rerender({ fn: b })
    vi.advanceTimersByTime(1000)
    expect(a).not.toHaveBeenCalled()
    expect(b).toHaveBeenCalledTimes(1)
  })

  it('pauses while the tab is hidden and runs once immediately on return', () => {
    const fn = vi.fn()
    renderHook(() => useVisiblePolling(fn, 2000))
    setHidden(true)
    vi.advanceTimersByTime(10000)
    expect(fn).not.toHaveBeenCalled()

    setHidden(false)
    expect(fn).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(2000)
    expect(fn).toHaveBeenCalledTimes(2)
  })

  it('does not start when mounted in a hidden tab', () => {
    hidden = true
    const fn = vi.fn()
    renderHook(() => useVisiblePolling(fn, 2000))
    vi.advanceTimersByTime(10000)
    expect(fn).not.toHaveBeenCalled()
  })

  it('does nothing while disabled and starts when enabled', () => {
    const fn = vi.fn()
    const { rerender } = renderHook(({ on }) => useVisiblePolling(fn, 2000, on), { initialProps: { on: false } })
    vi.advanceTimersByTime(10000)
    setHidden(false)
    expect(fn).not.toHaveBeenCalled()
    rerender({ on: true })
    vi.advanceTimersByTime(2000)
    expect(fn).toHaveBeenCalledTimes(1)
  })

  it('stops on unmount', () => {
    const fn = vi.fn()
    const { unmount } = renderHook(() => useVisiblePolling(fn, 2000))
    unmount()
    vi.advanceTimersByTime(10000)
    setHidden(false)
    expect(fn).not.toHaveBeenCalled()
  })

  it('waits for a pending async fn instead of stacking requests', async () => {
    const first = deferred()
    const fn = vi.fn<() => Promise<void> | undefined>().mockReturnValueOnce(first.promise)
    renderHook(() => useVisiblePolling(fn, 2000))
    await vi.advanceTimersByTimeAsync(2000)
    expect(fn).toHaveBeenCalledTimes(1)

    // The backend is slow: no second request while the first is in flight.
    await vi.advanceTimersByTimeAsync(10000)
    expect(fn).toHaveBeenCalledTimes(1)

    first.resolve()
    await vi.advanceTimersByTimeAsync(1999)
    expect(fn).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(fn).toHaveBeenCalledTimes(2)
  })

  it('keeps polling after an async fn rejects', async () => {
    const fn = vi.fn<() => Promise<void> | undefined>().mockRejectedValueOnce(new Error('down'))
    renderHook(() => useVisiblePolling(fn, 2000))
    await vi.advanceTimersByTimeAsync(2000)
    await vi.advanceTimersByTimeAsync(2000)
    expect(fn).toHaveBeenCalledTimes(2)
  })

  it('does not overlap when the tab returns mid-request', async () => {
    const first = deferred()
    const fn = vi.fn<() => Promise<void> | undefined>().mockReturnValueOnce(first.promise)
    renderHook(() => useVisiblePolling(fn, 2000))
    await vi.advanceTimersByTimeAsync(2000)
    setHidden(true)
    setHidden(false)
    expect(fn).toHaveBeenCalledTimes(1)

    first.resolve()
    await vi.advanceTimersByTimeAsync(2000)
    expect(fn).toHaveBeenCalledTimes(2)
  })
})
