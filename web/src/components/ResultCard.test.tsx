import { afterEach, describe, it, expect, vi, beforeAll } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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

  it('tem role="button" e tabIndex={0} quando clicável (playable)', () => {
    const { container } = render(
      <ResultCard result={makeResult()} onDownload={vi.fn()} onPlay={vi.fn()} />,
    )
    const card = container.querySelector('div.card')!
    expect(card).toHaveAttribute('role', 'button')
    expect(card).toHaveAttribute('tabIndex', '0')
  })

  it('não tem role button quando não clicável', () => {
    // Sem onPlay e sem onExploreContents → card não clicável
    const result = makeResult({ playable: false })
    const { container } = render(
      <ResultCard result={result} onDownload={vi.fn()} />,
    )
    const cardDiv = container.querySelector('div.card')!
    expect(cardDiv).not.toHaveAttribute('role')
    expect(cardDiv).not.toHaveAttribute('tabIndex')
  })
})

describe('ResultCard — interação com teclado', () => {
  it('Enter no card chama onPlay', async () => {
    const user = userEvent.setup()
    const onPlay = vi.fn()
    const { container } = render(
      <ResultCard result={makeResult()} onDownload={vi.fn()} onPlay={onPlay} />,
    )
    const card = container.querySelector<HTMLElement>('div.card')!
    card.focus()
    await user.keyboard('{Enter}')
    expect(onPlay).toHaveBeenCalledTimes(1)
  })

  it('Space no card chama onPlay', async () => {
    const user = userEvent.setup()
    const onPlay = vi.fn()
    const { container } = render(
      <ResultCard result={makeResult()} onDownload={vi.fn()} onPlay={onPlay} />,
    )
    const card = container.querySelector<HTMLElement>('div.card')!
    card.focus()
    await user.keyboard(' ')
    expect(onPlay).toHaveBeenCalledTimes(1)
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

  it('botão Explore files tem aria-label i18n quando presente', () => {
    render(
      <ResultCard
        result={makeResult()}
        onDownload={vi.fn()}
        onPlay={vi.fn()}
        onExploreContents={vi.fn()}
      />,
    )
    const exploreBtn = screen.getByRole('button', { name: /view files inside/i })
    expect(exploreBtn).toBeInTheDocument()
  })

  it('botão Copy magnet tem aria-label i18n', () => {
    render(
      <ResultCard result={makeResult()} onDownload={vi.fn()} onPlay={vi.fn()} />,
    )
    const copyBtn = screen.getByRole('button', { name: /copy magnet link/i })
    expect(copyBtn).toBeInTheDocument()
  })
})
