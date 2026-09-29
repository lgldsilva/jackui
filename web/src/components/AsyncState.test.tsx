import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AsyncState } from './AsyncState'
import { StatusBanner } from './StatusBanner'
import { RetryPanel } from './RetryPanel'
import '../test-setup'

afterEach(() => cleanup())

describe('AsyncState', () => {
  it('shows loading when loading=true', () => {
    render(
      <AsyncState loading loadingLabel="Loading…">
        <p>content</p>
      </AsyncState>,
    )
    expect(screen.getByRole('status')).toHaveAttribute('aria-busy', 'true')
    expect(screen.queryByText('content')).toBeNull()
  })

  it('shows error with retry', async () => {
    const onRetry = vi.fn()
    render(
      <AsyncState error="failed" onRetry={onRetry}>
        <p>content</p>
      </AsyncState>,
    )
    expect(screen.getByRole('alert')).toHaveTextContent('failed')
    await userEvent.click(screen.getByRole('button', { name: /try again/i }))
    expect(onRetry).toHaveBeenCalledOnce()
  })

  it('renders children on success', () => {
    render(<AsyncState><p>ok</p></AsyncState>)
    expect(screen.getByText('ok')).toBeTruthy()
  })
})

describe('StatusBanner', () => {
  it('uses role=alert for the error variant', () => {
    render(<StatusBanner variant="error" title="Error">detail</StatusBanner>)
    const el = screen.getByRole('alert')
    expect(el).toHaveTextContent('Error')
    expect(el).toHaveTextContent('detail')
  })
})

describe('RetryPanel', () => {
  it('fires onRetry', async () => {
    const fn = vi.fn()
    render(<RetryPanel onRetry={fn} label="Try again" />)
    await userEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(fn).toHaveBeenCalledOnce()
  })
})
