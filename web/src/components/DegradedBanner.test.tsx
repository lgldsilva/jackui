import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import '../test-setup'

const getStatus = vi.fn()
vi.mock('../api/client', () => ({
  getStatus: (...a: unknown[]) => getStatus(...a),
}))

import { DegradedBanner } from './DegradedBanner'

afterEach(() => {
  cleanup()
  getStatus.mockReset()
  vi.useRealTimers()
})

const status = (extra: object) => ({ status: 'ok', db: 'ok', version: '', commit: '', buildTime: '', goVersion: '', time: '', ...extra })

describe('DegradedBanner', () => {
  it('stays hidden on a healthy node', async () => {
    getStatus.mockResolvedValue(status({ role: 'edge', degraded: false }))
    render(<DegradedBanner />)
    await waitFor(() => expect(getStatus).toHaveBeenCalled())
    expect(screen.queryByRole('status')).toBeNull()
    expect(screen.queryByText(/degraded\.title|read-only|somente leitura/i)).toBeNull()
  })

  it('shows the read-only notice when the edge is degraded', async () => {
    getStatus.mockResolvedValue(status({ role: 'edge', degraded: true }))
    const { container } = render(<DegradedBanner />)
    await waitFor(() => expect(container.textContent).not.toBe(''))
  })

  it('ignores an unreachable node', async () => {
    getStatus.mockRejectedValue(new Error('offline'))
    const { container } = render(<DegradedBanner />)
    await waitFor(() => expect(getStatus).toHaveBeenCalled())
    expect(container.textContent).toBe('')
  })

  it('re-polls and clears the notice when the primary returns', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    getStatus.mockResolvedValueOnce(status({ role: 'edge', degraded: true }))
    getStatus.mockResolvedValue(status({ role: 'edge', degraded: false }))
    const { container } = render(<DegradedBanner />)
    await waitFor(() => expect(container.textContent).not.toBe(''))
    await vi.advanceTimersByTimeAsync(60_000)
    await waitFor(() => expect(container.textContent).toBe(''))
  })
})
