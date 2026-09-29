import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest'
import { useEffect } from 'react'
import { render, screen, cleanup } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { axe } from 'jest-axe'
import { Sheet } from './Sheet'
import TrailerModal from './TrailerModal'
import { ConfirmProvider, useConfirm } from './ConfirmDialog'
import DownloadModal from './DownloadModal'
import { shellProps, renderPlayerHeader } from './player/PlayerHeader'
import i18n from '../lib/i18n'
import type { SearchResult, TorrentInfo } from '../api/client'

// axe-core gate (P1.3, issue #80): each core component renders with typical props
// and MUST NOT have axe violations. color-contrast is excluded because
// jsdom has no real layout/getComputedStyle — contrast is audited outside
// jsdom (visual review/Sonar).
const AXE_OPTS = { rules: { 'color-contrast': { enabled: false } } } as const

// jsdom has no native matchMedia (player's useFullscreen)
beforeAll(() => {
  vi.stubGlobal('matchMedia', vi.fn((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    addListener: vi.fn(),
    removeListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })))
})

// Network calls mocked; the rest of the real api/client remains. streamAdd
// stays pending to keep DownloadModal/PlayerModal in the initial
// (loading) state, which is the state under audit here.
vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    getClients: vi.fn().mockResolvedValue([]),
    streamMetadata: vi.fn().mockResolvedValue(null),
    streamAdd: vi.fn(() => new Promise(() => { /* pending on purpose */ })),
    dedupCheck: vi.fn().mockResolvedValue(null),
  }
})

vi.mock('../auth/AuthContext', async () => {
  const actual = await vi.importActual<typeof import('../auth/AuthContext')>('../auth/AuthContext')
  return { ...actual, useAuth: () => ({ enabled: false, isAdmin: false }) }
})

vi.mock('./Toast', () => ({ useToast: () => ({ notify: vi.fn(), notifyError: vi.fn() }) }))

afterEach(cleanup)

function makeResult(overrides: Partial<SearchResult> = {}): SearchResult {
  return {
    title: 'Test.Movie.2024.2160p.WEB.h265-GROUP',
    tracker: 'MockTracker',
    categoryId: 2000,
    category: 'Movies',
    size: 8_000_000_000,
    seeders: 42,
    leechers: 7,
    age: '2h',
    magnetUri: 'magnet:?xt=urn:btih:deadbeef',
    link: 'https://mock.tracker/download/test.torrent',
    infoHash: 'deadbeefdeadbeefdeadbeefdeadbeefdeadbeef',
    publishDate: '2024-01-01',
    ...overrides,
  }
}

describe('axe — core components', () => {
  it('Sheet (base dialog) with no violations', async () => {
    const { container } = render(
      <Sheet open onClose={vi.fn()} title="Modal axe">
        <p>Modal content</p>
        <button type="button">Action</button>
      </Sheet>,
    )
    expect(await axe(container, AXE_OPTS)).toHaveNoViolations()
  })

  it('TrailerModal with no violations', async () => {
    const { container } = render(
      <TrailerModal videoKey="abc123" title="Test Trailer" onClose={vi.fn()} />,
    )
    // iframes:false — axe would try to inject into the YouTube frame and jsdom
    // doesn't support cross-frame messaging ("Respondable target must be a frame").
    expect(await axe(container, { ...AXE_OPTS, iframes: false } as Parameters<typeof axe>[1])).toHaveNoViolations()
  })

  it('ConfirmDialog (via ConfirmProvider) with no violations', async () => {
    function Trigger() {
      const confirm = useConfirm()
      useEffect(() => {
        void confirm({ title: 'Delete item?', message: 'This action cannot be undone.' })
      }, [confirm])
      return null
    }
    const { container } = render(
      <ConfirmProvider>
        <Trigger />
      </ConfirmProvider>,
    )
    await screen.findByRole('dialog')
    expect(await axe(container, AXE_OPTS)).toHaveNoViolations()
  })

  it('DownloadModal with no violations', async () => {
    const { container } = render(
      <MemoryRouter>
        <DownloadModal result={makeResult()} onClose={vi.fn()} />
      </MemoryRouter>,
    )
    await screen.findByRole('dialog')
    expect(await axe(container, AXE_OPTS)).toHaveNoViolations()
  })

  // PlayerModal shell via the real helpers (shellProps + renderPlayerHeader).
  // Mounting the whole PlayerModal pulls the player's hooks/views subtree into
  // the coverage gate (vitest.config.mts) and drops branches below the
  // threshold — what axe needs to audit here (named role="dialog" +
  // header buttons) are exactly these two helpers.
  function renderPlayerShell(info: TorrentInfo | null) {
    const ariaLabel = info?.name ?? 'Test.Movie.2024'
    return render(
      <div {...shellProps({ minimized: false, audioMode: false, fullViewport: false, onClose: vi.fn(), setMinimized: vi.fn(), ariaLabel })}>
        {renderPlayerHeader({
          minimized: false,
          info,
          result: makeResult(),
          isTranscoded: false,
          caps: null,
          encoderLabel: '',
          isFavorite: false,
          toggleFavorite: vi.fn(),
          incognito: false,
          setIncognito: vi.fn(),
          setMinimized: vi.fn(),
          onClose: vi.fn(),
          onShowInfo: vi.fn(),
          headerRef: { current: null },
          t: i18n.t,
        })}
      </div>,
    )
  }

  it('PlayerModal shell (header loading, info=null) with no violations', async () => {
    const { container } = renderPlayerShell(null)
    expect(await axe(container, AXE_OPTS)).toHaveNoViolations()
  })

  it('PlayerModal shell (header with info: Info/Favorite buttons) with no violations', async () => {
    const info: TorrentInfo = {
      infoHash: 'deadbeefdeadbeefdeadbeefdeadbeefdeadbeef',
      name: 'Test.Movie.2024',
      totalSize: 8_000_000_000,
      files: [],
      peers: 10,
      seeders: 42,
      downRate: 0,
      upRate: 0,
      progress: 0,
      primaryFile: 0,
    }
    const { container } = renderPlayerShell(info)
    expect(await axe(container, AXE_OPTS)).toHaveNoViolations()
  })
})
