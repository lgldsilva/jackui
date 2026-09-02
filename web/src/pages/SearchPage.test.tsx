import { describe, it, expect, vi, beforeAll, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import SearchPage from './SearchPage'

beforeAll(() => {
  window.HTMLElement.prototype.scrollIntoView = vi.fn()
})

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    getIndexers: vi.fn().mockResolvedValue([]),
    getHistory: vi.fn().mockResolvedValue([]),
    favoritesList: vi.fn().mockResolvedValue([]),
    getHistoryResults: vi.fn().mockResolvedValue([]),
  }
})

vi.mock('../components/PlayerProvider', () => ({
  usePlayer: () => ({ playSingle: vi.fn() }),
}))

vi.mock('../components/NavHeader', () => ({ default: () => <nav data-testid="nav-header" /> }))

describe('SearchPage — Tab strip and multi-tab flows', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
  })

  afterEach(cleanup)

  it('renders initial single search tab and allows adding new tabs', async () => {
    render(
      <MemoryRouter>
        <SearchPage />
      </MemoryRouter>,
    )

    // Should have 1 tab initially
    expect(screen.getByRole('button', { name: /^new search$/i })).toBeInTheDocument()

    // Click "+" button to add tab
    const addTabBtn = screen.getByTitle(/new search tab/i)
    await userEvent.click(addTabBtn)

    // Now should have 2 tab buttons in the strip
    const tabButtons = screen.getAllByRole('button', { name: /^new search$/i })
    expect(tabButtons).toHaveLength(2)
  })

  it('allows closing a tab and switches active tab properly', async () => {
    render(
      <MemoryRouter>
        <SearchPage />
      </MemoryRouter>,
    )

    const addTabBtn = screen.getByTitle(/new search tab/i)
    await userEvent.click(addTabBtn)
    await userEvent.click(addTabBtn)

    // Find close buttons (rendered on tabs when tabs.length > 1)
    const closeButtons = document.querySelectorAll('button > svg.lucide-x')
    expect(closeButtons.length).toBeGreaterThan(0)

    // Close one tab
    const firstClose = closeButtons[0].closest('button')
    if (firstClose) {
      await userEvent.click(firstClose)
    }

    // Tab count should decrease
    const remainingCloseButtons = document.querySelectorAll('button > svg.lucide-x')
    expect(remainingCloseButtons.length).toBe(closeButtons.length - 1)
  })

  it('handles keyboard shortcuts (Cmd+T / Cmd+W)', async () => {
    render(
      <MemoryRouter>
        <SearchPage />
      </MemoryRouter>,
    )

    // Cmd+T -> add new tab
    fireEvent.keyDown(window, { key: 't', metaKey: true })

    await waitFor(() => {
      const closeButtons = document.querySelectorAll('button > svg.lucide-x')
      expect(closeButtons.length).toBeGreaterThan(0)
    })

    // Cmd+W -> close active tab
    fireEvent.keyDown(window, { key: 'w', metaKey: true })

    await waitFor(() => {
      const closeButtons = document.querySelectorAll('button > svg.lucide-x')
      expect(closeButtons.length).toBe(0) // Back to 1 tab (which has no close button)
    })
  })
})
