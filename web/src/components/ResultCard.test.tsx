import { afterEach, describe, it, expect, vi } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { axe } from 'jest-axe'
import ResultCard from './ResultCard'
import type { SearchResult } from '../api/client'

// Mock tmdbMatch so no real API call is made
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

describe('ResultCard — structure', () => {
  it('uses <div> as the main wrapper (not <a> or <button>)', () => {
    const { container } = render(
      <ResultCard result={makeResult()} onDownload={vi.fn()} />,
    )
    const cards = container.querySelectorAll('div.card')
    expect(cards.length).toBeGreaterThanOrEqual(1)
    expect(container.querySelector('a.card')).toBeNull()
    expect(container.querySelector('button.card')).toBeNull()
  })

  it('clickable card: static wrapper (no role) + title becomes the primary button', () => {
    const { container } = render(
      <ResultCard result={makeResult()} onDownload={vi.fn()} onPlay={vi.fn()} />,
    )
    const card = container.querySelector('div.card')!
    expect(card).not.toHaveAttribute('role')
    expect(card).not.toHaveAttribute('tabIndex')
    const titleBtn = screen.getByRole('button', { name: /Test\.Movie\.2024/ })
    expect(titleBtn).toBeInTheDocument()
  })

  it('non-clickable card: title stays plain text (no button)', () => {
    // Without onPlay and without onExploreContents → card not clickable
    const result = makeResult({ playable: false })
    const { container } = render(
      <ResultCard result={result} onDownload={vi.fn()} />,
    )
    const cardDiv = container.querySelector('div.card')!
    expect(cardDiv).not.toHaveAttribute('role')
    expect(cardDiv).not.toHaveAttribute('tabIndex')
    expect(screen.queryByRole('button', { name: /Test\.Movie\.2024/ })).toBeNull()
  })

  it('does not nest interactives: buttons don\'t sit inside the card\'s role=button/<a>', () => {
    const { container } = render(
      <ResultCard result={makeResult()} onDownload={vi.fn()} onPlay={vi.fn()} onExploreContents={vi.fn()} />,
    )
    // No button may be a descendant of another button or of an anchor.
    for (const btn of container.querySelectorAll('button')) {
      expect(btn.closest('a')).toBeNull()
      const parentBtn = btn.parentElement?.closest('button')
      expect(parentBtn).toBeNull()
    }
  })
})

describe('ResultCard — keyboard interaction', () => {
  it('Enter on the title button calls onPlay', async () => {
    const user = userEvent.setup()
    const onPlay = vi.fn()
    render(
      <ResultCard result={makeResult()} onDownload={vi.fn()} onPlay={onPlay} />,
    )
    screen.getByRole('button', { name: /Test\.Movie\.2024/ }).focus()
    await user.keyboard('{Enter}')
    expect(onPlay).toHaveBeenCalledTimes(1)
  })

  it('Space on the title button calls onPlay', async () => {
    const user = userEvent.setup()
    const onPlay = vi.fn()
    render(
      <ResultCard result={makeResult()} onDownload={vi.fn()} onPlay={onPlay} />,
    )
    screen.getByRole('button', { name: /Test\.Movie\.2024/ }).focus()
    await user.keyboard(' ')
    expect(onPlay).toHaveBeenCalledTimes(1)
  })

  it('without onPlay but with onExploreContents, the title button explores', async () => {
    const user = userEvent.setup()
    const onExplore = vi.fn()
    render(
      <ResultCard result={makeResult()} onDownload={vi.fn()} onExploreContents={onExplore} />,
    )
    await user.click(screen.getByRole('button', { name: /Test\.Movie\.2024/ }))
    expect(onExplore).toHaveBeenCalledTimes(1)
  })

  it('Enter on a child button doesn\'t propagate twice (swallowClick)', async () => {
    const user = userEvent.setup()
    const onPlay = vi.fn()
    render(
      <ResultCard result={makeResult()} onDownload={vi.fn()} onPlay={onPlay} />,
    )
    // "Play" button (exact text)
    const playBtn = screen.getByRole('button', { name: 'Play' })
    playBtn.focus()
    await user.keyboard('{Enter}')
    expect(onPlay).toHaveBeenCalledTimes(1)
  })
})

describe('ResultCard — axe', () => {
  // color-contrast off: jsdom has no real layout/getComputedStyle.
  const AXE_OPTS = { rules: { 'color-contrast': { enabled: false } } } as const

  it('full card (play + actions) with no axe violations', async () => {
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

  it('static card (no primary action) with no axe violations', async () => {
    const { container } = render(
      <ResultCard result={makeResult({ playable: false })} onDownload={vi.fn()} />,
    )
    expect(await axe(container, AXE_OPTS)).toHaveNoViolations()
  })
})

describe('ResultCard — aria-labels', () => {
  it('Refresh button has an i18n aria-label (English: "Refresh seeders/leechers")', () => {
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

  it('Explore files button has an i18n aria-label when present', async () => {
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

  it('Copy magnet button has an i18n aria-label', async () => {
    const user = userEvent.setup()
    render(
      <ResultCard result={makeResult()} onDownload={vi.fn()} onPlay={vi.fn()} />,
    )
    await user.click(screen.getByRole('button', { name: 'More actions' }))
    const copyBtn = screen.getByRole('menuitem', { name: /copy magnet link/i })
    expect(copyBtn).toBeInTheDocument()
  })
})
