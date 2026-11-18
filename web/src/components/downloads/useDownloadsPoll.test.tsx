import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, renderHook, screen } from '@testing-library/react'
import { ToastProvider } from '../Toast'
import { useDownloadsPoll } from './useDownloadsPoll'

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  act(() => { vi.runOnlyPendingTimers() })
  cleanup()
  vi.useRealTimers()
})

function Page({ reload }: { readonly reload: () => Promise<void> }) {
  const { loadError } = useDownloadsPoll(reload, () => undefined)
  return <ToastProvider>{loadError && <p role="alert">{loadError}</p>}</ToastProvider>
}

describe('useDownloadsPoll', () => {
  it('surfaces an unreachable backend once inline, never as a toast per cycle', async () => {
    const reload = vi.fn<() => Promise<void>>().mockRejectedValue(new Error('Network Error'))
    render(<Page reload={reload} />)

    for (let i = 0; i < 5; i++) {
      await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
    }

    expect(reload).toHaveBeenCalledTimes(5)
    expect(screen.getAllByText('Network Error')).toHaveLength(1)
    expect(screen.getByRole('alert')).toHaveTextContent('Network Error')
    expect(screen.getByRole('status')).toBeEmptyDOMElement()
  })

  it('clears the error on the first successful cycle', async () => {
    const reload = vi.fn<() => Promise<void>>()
      .mockRejectedValueOnce(new Error('Network Error'))
      .mockResolvedValue(undefined)
    render(<Page reload={reload} />)

    await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
    expect(screen.getByRole('alert')).toBeInTheDocument()
    await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('polls the extra loader alongside the reload', async () => {
    const reload = vi.fn<() => Promise<void>>().mockResolvedValue(undefined)
    const extra = vi.fn()
    renderHook(() => useDownloadsPoll(reload, extra))
    await act(async () => { await vi.advanceTimersByTimeAsync(4000) })
    expect(reload).toHaveBeenCalledTimes(2)
    expect(extra).toHaveBeenCalledTimes(2)
  })

  it('reloadDownloadsRef never rejects and tracks the error for manual reloads', async () => {
    const reload = vi.fn<() => Promise<void>>().mockRejectedValueOnce(new Error('boom')).mockResolvedValue(undefined)
    const { result } = renderHook(() => useDownloadsPoll(reload, () => undefined))

    await act(async () => { await expect(result.current.reloadDownloadsRef.current()).resolves.toBeUndefined() })
    expect(result.current.loadError).toBe('boom')
    await act(async () => { await result.current.reloadDownloadsRef.current() })
    expect(result.current.loadError).toBeNull()
  })
})
