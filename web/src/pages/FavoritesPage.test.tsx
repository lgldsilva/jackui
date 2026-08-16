import { describe, it, expect, vi, beforeAll, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import FavoritesPage from './FavoritesPage'

// jsdom não tem IntersectionObserver nativo (cards da grade usam lazy render)
beforeAll(() => {
  vi.stubGlobal('IntersectionObserver', vi.fn(() => ({
    observe: vi.fn(),
    unobserve: vi.fn(),
    disconnect: vi.fn(),
  })))
})

// Apenas as chamadas de rede são mockadas; o resto do api/client real segue.
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

describe('FavoritesPage — sheet de import', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    // Sem storage prévio o usePersistedState aplica o default do código — é
    // exatamente o que o teste avalia.
    localStorage.clear()
  })

  afterEach(cleanup)

  // Origem da poluição relatada: favoritos "não marcados pra baixar" ganharam
  // rows de download porque o checkbox "também baixar" vinha LIGADO por padrão.
  // O default correto é desligado — baixar precisa ser escolha explícita.
  it('"também baixar" vem desmarcado por padrão', async () => {
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
