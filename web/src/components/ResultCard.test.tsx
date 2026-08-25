import { afterEach, describe, it, expect, vi, beforeAll } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { axe } from 'jest-axe'
import ResultCard from './ResultCard'
import type { SearchResult } from '../api/client'

// jsdom não tem IntersectionObserver nativo
beforeAll(() => {
  vi.stubGlobal('IntersectionObserver', vi.fn(() => ({
    observe: vi.fn(),
    unobserve: vi.fn(),
    disconnect: vi.fn(),
  })))
})

// Mock tmdbMatch pra não fazer chamada real de API
vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    tmdbMatch: vi.fn(() => Promise.resolve(null)),
    convertTorrentToMagnet: vi.fn(),
    favoriteAdd: vi.fn(),
    favoriteRemove: vi.fn(),
    downloadTorrentForResult: vi.fn(),
  }
})

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

describe('ResultCard — estrutura', () => {
  it('usa <div> como wrapper principal (não <a> ou <button>)', () => {
    const { container } = render(
      <ResultCard result={makeResult()} onDownload={vi.fn()} />,
    )
    const cards = container.querySelectorAll('div.card')
    expect(cards.length).toBeGreaterThanOrEqual(1)
    expect(container.querySelector('a.card')).toBeNull()
    expect(container.querySelector('button.card')).toBeNull()
  })

  it('card clicável: wrapper estático (sem role) + título vira botão primário', () => {
    const { container } = render(
      <ResultCard result={makeResult()} onDownload={vi.fn()} onPlay={vi.fn()} />,
    )
    const card = container.querySelector('div.card')!
    expect(card).not.toHaveAttribute('role')
    expect(card).not.toHaveAttribute('tabIndex')
    const titleBtn = screen.getByRole('button', { name: /Test\.Movie\.2024/ })
    expect(titleBtn).toBeInTheDocument()
  })

  it('card não clicável: título segue texto puro (sem botão)', () => {
    // Sem onPlay e sem onExploreContents → card não clicável
    const result = makeResult({ playable: false })
    const { container } = render(
      <ResultCard result={result} onDownload={vi.fn()} />,
    )
    const cardDiv = container.querySelector('div.card')!
    expect(cardDiv).not.toHaveAttribute('role')
    expect(cardDiv).not.toHaveAttribute('tabIndex')
    expect(screen.queryByRole('button', { name: /Test\.Movie\.2024/ })).toBeNull()
  })

  it('não aninha interativos: botões não ficam dentro de role=button/<a> de card', () => {
    const { container } = render(
      <ResultCard result={makeResult()} onDownload={vi.fn()} onPlay={vi.fn()} onExploreContents={vi.fn()} />,
    )
    // Nenhum botão pode ser descendente de outro botão ou de uma âncora.
    for (const btn of container.querySelectorAll('button')) {
      expect(btn.closest('a')).toBeNull()
      const parentBtn = btn.parentElement?.closest('button')
      expect(parentBtn).toBeNull()
    }
  })
})

describe('ResultCard — interação com teclado', () => {
  it('Enter no botão-título chama onPlay', async () => {
    const user = userEvent.setup()
    const onPlay = vi.fn()
    render(
      <ResultCard result={makeResult()} onDownload={vi.fn()} onPlay={onPlay} />,
    )
    screen.getByRole('button', { name: /Test\.Movie\.2024/ }).focus()
    await user.keyboard('{Enter}')
    expect(onPlay).toHaveBeenCalledTimes(1)
  })

  it('Space no botão-título chama onPlay', async () => {
    const user = userEvent.setup()
    const onPlay = vi.fn()
    render(
      <ResultCard result={makeResult()} onDownload={vi.fn()} onPlay={onPlay} />,
    )
    screen.getByRole('button', { name: /Test\.Movie\.2024/ }).focus()
    await user.keyboard(' ')
    expect(onPlay).toHaveBeenCalledTimes(1)
  })

  it('sem onPlay mas com onExploreContents, o botão-título explora', async () => {
    const user = userEvent.setup()
    const onExplore = vi.fn()
    render(
      <ResultCard result={makeResult()} onDownload={vi.fn()} onExploreContents={onExplore} />,
    )
    await user.click(screen.getByRole('button', { name: /Test\.Movie\.2024/ }))
    expect(onExplore).toHaveBeenCalledTimes(1)
  })

  it('Enter em botão filho não propaga duas vezes (swallowClick)', async () => {
    const user = userEvent.setup()
    const onPlay = vi.fn()
    render(
      <ResultCard result={makeResult()} onDownload={vi.fn()} onPlay={onPlay} />,
    )
    // Botão "Play" (texto exato)
    const playBtn = screen.getByRole('button', { name: 'Play' })
    playBtn.focus()
    await user.keyboard('{Enter}')
    expect(onPlay).toHaveBeenCalledTimes(1)
  })
})

describe('ResultCard — axe', () => {
  // color-contrast desligado: jsdom não tem layout/getComputedStyle real.
  const AXE_OPTS = { rules: { 'color-contrast': { enabled: false } } } as const

  it('card completo (play + ações) sem violações axe', async () => {
    const { container } = render(
      <ResultCard
        result={makeResult({ id: 1 })}
        onDownload={vi.fn()}
        onPlay={vi.fn()}
        onAddToPlaylist={vi.fn()}
        onExploreContents={vi.fn()}
        onRefresh={vi.fn()}
      />,
    )
    expect(await axe(container, AXE_OPTS)).toHaveNoViolations()
  })

  it('card estático (sem ação primária) sem violações axe', async () => {
    const { container } = render(
      <ResultCard result={makeResult({ playable: false })} onDownload={vi.fn()} />,
    )
    expect(await axe(container, AXE_OPTS)).toHaveNoViolations()
  })
})

describe('ResultCard — aria-labels', () => {
  it('botão Refresh tem aria-label i18n (inglês: "Refresh seeders/leechers")', () => {
    const onRefresh = vi.fn()
    const result = makeResult({ id: 1 })
    render(
      <ResultCard
        result={result}
        onDownload={vi.fn()}
        onRefresh={onRefresh}
      />,
    )
    const refreshBtn = screen.getByRole('button', { name: /refresh seeders/i })
    expect(refreshBtn).toBeInTheDocument()
    expect(refreshBtn).toHaveAccessibleName()
  })

  it('botão Explore files tem aria-label i18n quando presente', async () => {
    const user = userEvent.setup()
    render(
      <ResultCard
        result={makeResult()}
        onDownload={vi.fn()}
        onPlay={vi.fn()}
        onExploreContents={vi.fn()}
      />,
    )
    await user.click(screen.getByRole('button', { name: 'More actions' }))
    const exploreBtn = screen.getByRole('menuitem', { name: /view files inside/i })
    expect(exploreBtn).toBeInTheDocument()
  })

  it('botão Copy magnet tem aria-label i18n', async () => {
    const user = userEvent.setup()
    render(
      <ResultCard result={makeResult()} onDownload={vi.fn()} onPlay={vi.fn()} />,
    )
    await user.click(screen.getByRole('button', { name: 'More actions' }))
    const copyBtn = screen.getByRole('menuitem', { name: /copy magnet link/i })
    expect(copyBtn).toBeInTheDocument()
  })
})
