import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { DualAudioToggles, type DualAudioPrefKey } from './DualAudioToggles'

afterEach(cleanup)

describe('DualAudioToggles', () => {
  it('renders both toggles, unpressed by default', () => {
    render(<DualAudioToggles preferDual={false} dualOnly={false} onToggle={vi.fn()} />)
    expect(screen.getByRole('button', { name: /prefer dual/i })).toHaveAttribute('aria-pressed', 'false')
    expect(screen.getByRole('button', { name: /dual only/i })).toHaveAttribute('aria-pressed', 'false')
  })

  it('reflects pressed state from props (restored prefs on mount)', () => {
    render(<DualAudioToggles preferDual={true} dualOnly={false} onToggle={vi.fn()} />)
    expect(screen.getByRole('button', { name: /prefer dual/i })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: /dual only/i })).toHaveAttribute('aria-pressed', 'false')
  })

  it('clicking calls onToggle with the matching pref key', async () => {
    const user = userEvent.setup()
    const onToggle = vi.fn()
    render(<DualAudioToggles preferDual={false} dualOnly={false} onToggle={onToggle} />)

    await user.click(screen.getByRole('button', { name: /prefer dual/i }))
    expect(onToggle).toHaveBeenNthCalledWith(1, 'preferDual' satisfies DualAudioPrefKey)

    await user.click(screen.getByRole('button', { name: /dual only/i }))
    expect(onToggle).toHaveBeenNthCalledWith(2, 'dualOnly' satisfies DualAudioPrefKey)
  })

  it('toggles carry a description (title) for each action', () => {
    render(<DualAudioToggles preferDual={false} dualOnly={false} onToggle={vi.fn()} />)
    expect(screen.getByRole('button', { name: /prefer dual/i })).toHaveAttribute('title')
    expect(screen.getByRole('button', { name: /dual only/i })).toHaveAttribute('title')
  })
})
