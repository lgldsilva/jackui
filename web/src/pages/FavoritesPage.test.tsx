import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import FavoritesPage from './FavoritesPage'

// Only the network calls are mocked; the rest of the real api/client remains.
vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    favoritesList: vi.fn().mockResolvedValue([]),
    folderList: vi.fn().mockResolvedValue([]),
    downloadsList: vi.fn().mockResolvedValue([]),
  }
})

vi.mock('../auth/AuthContext', async () => {
  const actual = await vi.importActual<typeof import('../auth/AuthContext')>('../auth/AuthContext')
  return { ...actual, useAuth: () => ({ isAdmin: false }) }
})

vi.mock('../components/PlayerProvider', async () => {
  const actual = await vi.importActual<typeof import('../components/PlayerProvider')>('../components/PlayerProvider')
  return { ...actual, usePlayer: () => ({ playSingle: vi.fn() }) }
})

vi.mock('../components/NavHeader', () => ({ default: () => null }))
vi.mock('../components/ConfirmDialog', () => ({ useConfirm: () => vi.fn(async () => true) }))
vi.mock('../components/Toast', () => ({ useToast: () => ({ notify: vi.fn(), notifyError: vi.fn() }) }))

describe('FavoritesPage — import sheet', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    // Without prior storage, usePersistedState applies the code's default — which is
    // exactly what the test evaluates.
    localStorage.clear()
  })

  afterEach(cleanup)

  // Origin of the reported pollution: favorites "not checked for download" gained
  // download rows because the "also download" checkbox came ON by default.
  // The correct default is off — downloading must be an explicit choice.
  it('"also download" comes unchecked by default', async () => {
    render(
      <MemoryRouter>
        <FavoritesPage />
      </MemoryRouter>,
    )
    const openBtn = await screen.findByRole('button', { name: 'Import torrent' })
    await userEvent.click(openBtn)

    const checkbox = await screen.findByRole('checkbox')
    expect(checkbox).not.toBeChecked()
  })
})
