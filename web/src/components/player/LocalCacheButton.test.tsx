import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const status = vi.fn()
const start = vi.fn()
vi.mock('../../api/client', () => ({
  parseLocalHash: () => ({ mount: 'm', path: 'a.mkv' }),
  localCacheStatus: (...a: unknown[]) => status(...a),
  localCacheStart: (...a: unknown[]) => start(...a),
}))

import { LocalCacheButton } from './LocalCacheButton'

const st = (s: string, percent = 0) => ({ status: s, size: 100, copied: percent, percent, cacheable: true })

describe('LocalCacheButton', () => {
  beforeEach(() => { vi.useFakeTimers(); status.mockReset(); start.mockReset() })
  afterEach(() => { cleanup(); vi.useRealTimers() })

  // Regression (#200): reopening the player while a copy is in flight showed a
  // frozen "Caching 10%" forever — polling only started from the click handler.
  it('resumes polling when mounted with a copy already in flight', async () => {
    status.mockResolvedValueOnce(st('copying', 10)).mockResolvedValue(st('ready', 100))
    render(<LocalCacheButton hash="local-x" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(status).toHaveBeenCalledTimes(1)

    await act(async () => { await vi.advanceTimersByTimeAsync(2100) })
    expect(status).toHaveBeenCalledTimes(2)
    expect(screen.getByRole('button')).toBeDisabled()

    // Settled → polling stops.
    await act(async () => { await vi.advanceTimersByTimeAsync(6000) })
    expect(status).toHaveBeenCalledTimes(2)
  })

  it('does not poll for a settled file', async () => {
    status.mockResolvedValue(st('none'))
    render(<LocalCacheButton hash="local-x" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    expect(status).toHaveBeenCalledTimes(1)
  })

  it('stops polling on unmount', async () => {
    status.mockResolvedValue(st('copying', 10))
    const { unmount } = render(<LocalCacheButton hash="local-x" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    unmount()
    await act(async () => { await vi.advanceTimersByTimeAsync(6000) })
    expect(status).toHaveBeenCalledTimes(1)
  })

  it('starts a copy on click and polls until it settles', async () => {
    status.mockResolvedValueOnce(st('none')).mockResolvedValue(st('ready', 100))
    start.mockResolvedValue(st('queued'))
    render(<LocalCacheButton hash="local-x" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })

    await act(async () => { screen.getByRole('button').click(); await vi.advanceTimersByTimeAsync(0) })
    expect(start).toHaveBeenCalledWith('m', 'a.mkv')
    expect(screen.getByRole('button')).toBeDisabled()

    await act(async () => { await vi.advanceTimersByTimeAsync(2100) })
    expect(status).toHaveBeenCalledTimes(2)
    await act(async () => { await vi.advanceTimersByTimeAsync(6000) })
    expect(status).toHaveBeenCalledTimes(2)
  })

  it('keeps the button usable when starting the copy fails', async () => {
    status.mockResolvedValue(st('error'))
    start.mockRejectedValue(new Error('io'))
    render(<LocalCacheButton hash="local-x" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    await act(async () => { screen.getByRole('button').click(); await vi.advanceTimersByTimeAsync(0) })
    expect(start).toHaveBeenCalledTimes(1)
    expect(screen.getByRole('button')).not.toBeDisabled()
  })

  it('ignores a click while ready', async () => {
    status.mockResolvedValue(st('ready', 100))
    render(<LocalCacheButton hash="local-x" />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    await act(async () => { screen.getByRole('button').click() })
    expect(start).not.toHaveBeenCalled()
  })
})
