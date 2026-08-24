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

// Gate axe-core (P1.3, issue #80): cada componente central renderiza com props
// típicas e NÃO pode ter violações axe. color-contrast fica fora porque o
// jsdom não tem layout/getComputedStyle real — contraste é auditado fora do
// jsdom (revisão visual/Sonar).
const AXE_OPTS = { rules: { 'color-contrast': { enabled: false } } } as const

// jsdom não tem IntersectionObserver nem matchMedia nativos (usados por cards
// com lazy load e pelo useFullscreen do player)
beforeAll(() => {
  vi.stubGlobal('IntersectionObserver', vi.fn(() => ({
    observe: vi.fn(),
    unobserve: vi.fn(),
    disconnect: vi.fn(),
  })))
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

// Chamadas de rede mockadas; o restante do api/client real segue. streamAdd
// fica pendente para manter DownloadModal/PlayerModal no estado inicial
// (loading), que é o estado sob auditoria aqui.
vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    getClients: vi.fn().mockResolvedValue([]),
    streamMetadata: vi.fn().mockResolvedValue(null),
    streamAdd: vi.fn(() => new Promise(() => { /* pendente de propósito */ })),
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

describe('axe — componentes centrais', () => {
  it('Sheet (diálogo base) sem violações', async () => {
    const { container } = render(
      <Sheet open onClose={vi.fn()} title="Modal axe">
        <p>Conteúdo do modal</p>
        <button type="button">Ação</button>
      </Sheet>,
    )
    expect(await axe(container, AXE_OPTS)).toHaveNoViolations()
  })

  it('TrailerModal sem violações', async () => {
    const { container } = render(
      <TrailerModal videoKey="abc123" title="Trailer de Teste" onClose={vi.fn()} />,
    )
    // iframes:false — axe tentaria injetar no frame do YouTube e o jsdom não
    // suporta cross-frame messaging ("Respondable target must be a frame").
    expect(await axe(container, { ...AXE_OPTS, iframes: false } as Parameters<typeof axe>[1])).toHaveNoViolations()
  })

  it('ConfirmDialog (via ConfirmProvider) sem violações', async () => {
    function Trigger() {
      const confirm = useConfirm()
      useEffect(() => {
        void confirm({ title: 'Apagar item?', message: 'Esta ação não pode ser desfeita.' })
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

  it('DownloadModal sem violações', async () => {
    const { container } = render(
      <MemoryRouter>
        <DownloadModal result={makeResult()} onClose={vi.fn()} />
      </MemoryRouter>,
    )
    await screen.findByRole('dialog')
    expect(await axe(container, AXE_OPTS)).toHaveNoViolations()
  })

  // Shell do PlayerModal via os helpers reais (shellProps + renderPlayerHeader).
  // Montar o PlayerModal inteiro puxa a subárvore de hooks/views do player pra
  // dentro do gate de cobertura (vitest.config.mts) e derruba branches abaixo
  // do threshold — o que o axe precisa auditar aqui (role="dialog" nomeado +
  // botões do header) são exatamente estes dois helpers.
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

  it('PlayerModal shell (header em loading, info=null) sem violações', async () => {
    const { container } = renderPlayerShell(null)
    expect(await axe(container, AXE_OPTS)).toHaveNoViolations()
  })

  it('PlayerModal shell (header com info: botões Info/Favorito) sem violações', async () => {
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
