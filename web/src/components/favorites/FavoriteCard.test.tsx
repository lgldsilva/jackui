import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import FavoriteCard from './FavoriteCard'
import type { StreamFavorite, FavoriteFolder, DownloadEntry } from '../../api/client'

vi.mock('../Thumbnail', () => ({ default: () => <div data-testid="thumbnail" /> }))
vi.mock('../SeedBadge', () => ({ default: () => <span data-testid="seed-badge" /> }))

afterEach(cleanup)

const fav = (over: Partial<StreamFavorite> = {}): StreamFavorite => ({
  name: 'Test.Movie.2024',
  infoHash: 'deadbeef',
  magnet: 'magnet:?xt=urn:btih:deadbeef',
  userId: 1,
  favoritedAt: '2024-01-01T00:00:00Z',
  reason: 'manual',
  folderId: null,
  ...over,
})

const folders: FavoriteFolder[] = []

const renderCard = (props: Partial<Parameters<typeof FavoriteCard>[0]> = {}) => {
  const defaults = {
    fav: fav(),
    selected: false,
    anySelected: false,
    folders,
    seedRefresh: 0,
    onToggleSelected: vi.fn(),
    onDragStart: vi.fn(),
    onPlay: vi.fn(),
    onRemove: vi.fn(),
    onDownload: vi.fn(),
    onOpenContents: vi.fn(),
    onMoveToFolder: vi.fn(),
  }
  return render(<FavoriteCard {...defaults} {...props} />)
}

describe('FavoriteCard', () => {
  it('shows the "Not downloaded" badge when there is no download', () => {
    renderCard()
    expect(screen.getByText('Not downloaded')).toBeInTheDocument()
  })

  it('renders the right badge for each download status', () => {
    const statuses: DownloadEntry['status'][] = ['queued', 'downloading', 'moving', 'paused', 'completed', 'failed']
    const textMap: Record<typeof statuses[number], string> = {
      queued: 'Queued',
      downloading: 'Downloading',
      moving: 'Downloading',
      paused: 'Paused',
      completed: 'Downloaded',
      failed: 'Failed',
    }
    for (const status of statuses) {
      cleanup()
      renderCard({ download: { status } as DownloadEntry })
      expect(screen.getByText(textMap[status])).toBeInTheDocument()
    }
  })

  it('disables Play/Download/Contents buttons when the magnet is invalid', () => {
    renderCard({ fav: fav({ magnet: '' }) })
    expect(screen.getByTitle('Magnet not saved — re-favorite')).toBeDisabled()
    expect(screen.getByTitle('Download (choose destination and files)')).toBeDisabled()
    expect(screen.getByTitle('View contents and details')).toBeDisabled()
  })

  it('calls onToggleSelected when the checkbox is clicked', async () => {
    const onToggleSelected = vi.fn()
    renderCard({ anySelected: true, onToggleSelected })
    const checkbox = screen.getByRole('checkbox', { name: /select/i })
    await userEvent.click(checkbox)
    expect(onToggleSelected).toHaveBeenCalledTimes(1)
  })

  it('calls onDownload when the download button is clicked', async () => {
    const onDownload = vi.fn()
    renderCard({ onDownload })
    const btn = screen.getByTitle('Download (choose destination and files)')
    await userEvent.click(btn)
    expect(onDownload).toHaveBeenCalledTimes(1)
  })
})
