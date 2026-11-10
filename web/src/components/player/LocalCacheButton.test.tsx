import { act, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const status = vi.fn()
vi.mock('../../api/client', () => ({
  parseLocalHash: () => ({ mount: 'm', path: 'a.mkv' }),
  localCacheStatus: (...a: unknown[]) => status(...a),
  localCacheStart: vi.fn(),
}))

import { LocalCacheButton } from './LocalCacheButton'

const st = (s: string, percent = 0) => ({ status: s, size: 100, copied: percent, percent, cacheable: true })

describe('LocalCacheButton', () => {
  beforeEach(() => { vi.useFakeTimers(); status.mockReset() })
  afterEach(() => { vi.useRealTimers() })

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
})
