import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import DownloadsPage from './DownloadsPage'
import { ToastProvider } from '../components/Toast'
import { downloadsList } from '../api/client'

// Only the network calls are mocked; the rest of the real api/client remains.
vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    downloadsList: vi.fn(),
    streamActive: vi.fn().mockResolvedValue([]),
    streamGetLimits: vi.fn().mockResolvedValue({ down: 0, up: 0 }),
    downloadTrackers: vi.fn().mockResolvedValue([]),
    downloadCategories: vi.fn().mockResolvedValue([]),
    localMounts: vi.fn().mockResolvedValue([]),
    getDownloadsQueueSettings: vi.fn().mockResolvedValue({ maxActive: 3 }),
  }
})

vi.mock('../auth/AuthContext', async () => {
  const actual = await vi.importActual<typeof import('../auth/AuthContext')>('../auth/AuthContext')
  return { ...actual, useAuth: () => ({ isAdmin: false, isGuest: false }) }
})

vi.mock('../components/PlayerProvider', async () => {
  const actual = await vi.importActual<typeof import('../components/PlayerProvider')>('../components/PlayerProvider')
  return { ...actual, usePlayer: () => ({ playSingle: vi.fn(), playLocal: vi.fn() }) }
})

vi.mock('../components/NavHeader', () => ({ default: () => null }))
vi.mock('../components/ConfirmDialog', () => ({ useConfirm: () => vi.fn(async () => true) }))
vi.mock('../components/PromoteModal', () => ({ default: () => null }))
vi.mock('../components/DownloadInspectModal', () => ({ default: () => null }))
vi.mock('../components/DownloadModal', () => ({ default: () => null }))
vi.mock('../components/AddTorrentModal', () => ({ default: () => null }))

const listMock = vi.mocked(downloadsList)

async function cycles(n: number) {
  for (let i = 0; i < n; i++) {
    await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
  }
}

function renderPage() {
  return render(
    <MemoryRouter>
      <ToastProvider>
        <DownloadsPage />
      </ToastProvider>
    </MemoryRouter>,
  )
}

describe('DownloadsPage — poll failures', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    localStorage.clear()
    listMock.mockReset()
  })

  afterEach(() => {
    act(() => { vi.runOnlyPendingTimers() })
    cleanup()
    vi.useRealTimers()
  })

  it('shows an unreachable backend as one inline banner, not a toast per poll cycle', async () => {
    listMock.mockRejectedValue(new Error('Network Error'))
    renderPage()
    await cycles(4)

    expect(listMock.mock.calls.length).toBeGreaterThanOrEqual(4)
    // Exactly one occurrence, and it is the banner: no toast carries it.
    expect(screen.getAllByText('Network Error')).toHaveLength(1)
    expect(within(screen.getByRole('alert')).getByText('Network Error')).toBeInTheDocument()
  })

  it('clears the banner as soon as a poll succeeds again', async () => {
    listMock.mockRejectedValue(new Error('Network Error'))
    renderPage()
    await cycles(2)
    expect(screen.getByRole('alert')).toBeInTheDocument()

    listMock.mockResolvedValue([])
    await cycles(1)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
