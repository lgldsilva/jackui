import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'
import { ToastProvider, dismissToast, notify, notifyError, useToast } from './Toast'

beforeEach(() => {
  vi.useFakeTimers()
})

// The toast store is module-level: drain it (auto-dismiss) while the provider
// is still mounted so one test's toasts never leak into the next.
afterEach(() => {
  act(() => { vi.runAllTimers() })
  cleanup()
  vi.useRealTimers()
})

function renderProvider() {
  return render(<ToastProvider><span>app</span></ToastProvider>)
}

function visible(message: string) {
  return screen.queryAllByText(message)
}

describe('ToastProvider', () => {
  it('renders a toast in a polite live region and auto-dismisses it', () => {
    renderProvider()
    act(() => { notify('saved', 'success') })
    expect(screen.getByRole('status')).toHaveAttribute('aria-live', 'polite')
    expect(visible('saved')).toHaveLength(1)

    act(() => { vi.advanceTimersByTime(3999) })
    expect(visible('saved')).toHaveLength(1)
    act(() => { vi.advanceTimersByTime(1) })
    expect(visible('saved')).toHaveLength(0)
  })

  it('collapses identical live toasts into one with a repeat count', () => {
    renderProvider()
    let first = 0
    let second = 0
    act(() => {
      first = notifyError(new Error('Network Error'))
      second = notifyError(new Error('Network Error'))
      notifyError(new Error('Network Error'))
    })
    expect(visible('Network Error')).toHaveLength(1)
    expect(second).toBe(first)
    expect(screen.getByText('×3')).toBeInTheDocument()
  })

  it('does not collapse the same message with a different kind', () => {
    renderProvider()
    act(() => {
      notify('done', 'info')
      notify('done', 'success')
    })
    expect(visible('done')).toHaveLength(2)
  })

  it('a repeat refreshes the auto-dismiss timer', () => {
    renderProvider()
    act(() => { notify('offline', 'error') })
    act(() => { vi.advanceTimersByTime(3000) })
    act(() => { notify('offline', 'error') })
    expect(visible('offline')).toHaveLength(1)
    act(() => { vi.advanceTimersByTime(3999) })
    expect(visible('offline')).toHaveLength(1)
    act(() => { vi.advanceTimersByTime(1) })
    expect(visible('offline')).toHaveLength(0)
  })

  it('shows the message as new again once the previous one is gone', () => {
    renderProvider()
    act(() => { notify('offline', 'error') })
    act(() => { vi.advanceTimersByTime(4000) })
    act(() => { notify('offline', 'error') })
    expect(visible('offline')).toHaveLength(1)
    expect(screen.queryByText(/^×/)).not.toBeInTheDocument()
  })

  it('caps the stack at 3, dropping the oldest', () => {
    renderProvider()
    act(() => {
      for (const m of ['one', 'two', 'three', 'four', 'five']) notify(m)
    })
    expect(visible('one')).toHaveLength(0)
    expect(visible('two')).toHaveLength(0)
    for (const m of ['three', 'four', 'five']) expect(visible(m)).toHaveLength(1)
  })

  it('dismisses through the close button and through dismissToast', () => {
    renderProvider()
    let id = 0
    act(() => {
      notify('first')
      id = notify('second')
    })
    act(() => { screen.getAllByRole('button', { name: 'Close' })[0].click() })
    expect(visible('first')).toHaveLength(0)
    act(() => { dismissToast(id) })
    expect(visible('second')).toHaveLength(0)
  })

  it('useToast exposes the same store-backed functions', () => {
    function Trigger() {
      const { notifyError: fail } = useToast()
      return <button onClick={() => fail('boom')}>fail</button>
    }
    render(<ToastProvider><Trigger /></ToastProvider>)
    act(() => { screen.getByRole('button', { name: 'fail' }).click() })
    expect(visible('boom')).toHaveLength(1)
  })

  it('shows toasts raised before the provider mounted', () => {
    notify('early')
    renderProvider()
    expect(visible('early')).toHaveLength(1)
  })
})
