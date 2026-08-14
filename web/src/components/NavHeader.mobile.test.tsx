import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'

vi.mock('./UserBadge', () => ({ default: () => <div data-testid="user-badge" /> }))
vi.mock('./NotificationsBell', () => ({ default: () => <button type="button" aria-label="notifications" /> }))
vi.mock('./RateWidget', () => ({ default: () => <div data-testid="rate-widget" /> }))
vi.mock('./ThemeToggle', () => ({ default: () => <button type="button" aria-label="theme" /> }))
vi.mock('../lib/incognito', () => ({
  isIncognito: () => false,
  useIncognito: () => [false, vi.fn()],
}))
vi.mock('../lib/mediaMode', () => ({ useMediaMode: () => ['video', vi.fn()] }))
vi.mock('../lib/reveal', () => ({
  isRevealHidden: () => false,
  useRevealHidden: () => [false, vi.fn()],
}))
vi.mock('../lib/useSwipe', () => ({ useSwipe: vi.fn() }))

import NavHeader from './NavHeader'

afterEach(cleanup)

describe('NavHeader mobile drawer', () => {
  beforeEach(() => {
    vi.stubGlobal('matchMedia', () => ({
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }))
  })

  it('opens from the hamburger, keeps essential routes reachable, and closes from the backdrop', () => {
    const { container } = render(
      <MemoryRouter initialEntries={['/']}>
        <NavHeader />
      </MemoryRouter>,
    )

    const panel = screen.getByRole('complementary')
    expect(panel).toHaveClass('-translate-x-full', 'md:translate-x-0')

    fireEvent.click(screen.getByRole('button', { name: /open menu/i }))
    expect(panel).toHaveClass('translate-x-0')
    for (const label of ['Home', 'Search', 'Trending', 'Continue Watching', 'Downloads', 'Local Files', 'Settings']) {
      expect(screen.getByRole('link', { name: label })).toBeInTheDocument()
    }

    const backdrop = container.querySelector('div[aria-hidden="true"]')
    expect(backdrop).not.toBeNull()
    fireEvent.click(backdrop!)
    expect(panel).toHaveClass('-translate-x-full')
  })
})
