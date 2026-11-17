// Regression guard: PromoteModal used to render a non-modal `<dialog open>`
// (no aria-modal, no accessible name, no initial focus, no focus trap/restore).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { DownloadEntry } from '../api/client'

const api = vi.hoisted(() => ({
  fetchPromoteDestinations: vi.fn(),
  downloadPromoteBrowse: vi.fn(),
  downloadPromotePreview: vi.fn(),
  downloadPromoteBatch: vi.fn(),
}))

vi.mock('../api/client', async (orig) => ({
  ...(await orig<typeof import('../api/client')>()),
  ...api,
}))

import PromoteModal from './PromoteModal'

function entry(id: number, name: string): DownloadEntry {
  return {
    id, userId: 1, infoHash: `hash-${id}`, fileIndex: 0, filePath: `${name}.mkv`, fileSize: 1,
    name, magnet: '', status: 'completed', bytesDownloaded: 1, progress: 1, createdAt: '2026-01-01T00:00:00Z',
  }
}

const ONE = [entry(1, 'Movie A')]

function Host({ onClose, onPromoted, items = ONE }: {
  readonly onClose: () => void
  readonly onPromoted: () => void
  readonly items?: DownloadEntry[] | null
}) {
  return (
    <>
      <button>outside</button>
      <PromoteModal items={items} onClose={onClose} onPromoted={onPromoted} />
    </>
  )
}

beforeEach(() => {
  api.fetchPromoteDestinations.mockResolvedValue([])
  api.downloadPromoteBrowse.mockResolvedValue({ dirs: ['Movies', 'Series'] })
  api.downloadPromotePreview.mockResolvedValue({ previews: [] })
  api.downloadPromoteBatch.mockResolvedValue({ promoted: [], failed: [] })
})

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('PromoteModal — modal semantics', () => {
  it('is a labelled modal dialog that takes focus on open', async () => {
    render(<Host onClose={vi.fn()} onPromoted={vi.fn()} />)
    const dialog = screen.getByRole('dialog')
    expect(dialog).toHaveAttribute('aria-modal', 'true')
    expect(dialog).toHaveAccessibleName('Promote download')
    expect(dialog).toContainElement(document.activeElement as HTMLElement)
    await within(dialog).findByRole('button', { name: /Movies/ })
  })

  it('keeps Tab and Shift+Tab inside the dialog', async () => {
    const user = userEvent.setup()
    render(<Host onClose={vi.fn()} onPromoted={vi.fn()} />)
    const dialog = screen.getByRole('dialog')
    await within(dialog).findByRole('button', { name: /Movies/ })

    await user.tab({ shift: true })
    expect(within(dialog).getByRole('button', { name: 'Promote' })).toHaveFocus()
    await user.tab()
    expect(within(dialog).getByRole('button', { name: 'Close' })).toHaveFocus()
  })

  it('returns focus to the opener when it closes', () => {
    const { rerender } = render(<Host items={null} onClose={vi.fn()} onPromoted={vi.fn()} />)
    const opener = screen.getByRole('button', { name: 'outside' })
    opener.focus()
    rerender(<Host onClose={vi.fn()} onPromoted={vi.fn()} />)
    expect(screen.getByRole('dialog')).toContainElement(document.activeElement as HTMLElement)
    rerender(<Host items={null} onClose={vi.fn()} onPromoted={vi.fn()} />)
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(opener).toHaveFocus()
  })

  it('closes on Escape', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    render(<Host onClose={onClose} onPromoted={vi.fn()} />)
    await within(screen.getByRole('dialog')).findByRole('button', { name: /Movies/ })
    await user.keyboard('{Escape}')
    expect(onClose).toHaveBeenCalledTimes(1)
  })
})

describe('PromoteModal — behaviour preserved by the move to Sheet', () => {
  it('browses into a subfolder and promotes to subfolder/new folder', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    const onPromoted = vi.fn()
    render(<Host onClose={onClose} onPromoted={onPromoted} />)
    const dialog = screen.getByRole('dialog')

    await user.click(await within(dialog).findByRole('button', { name: /Movies/ }))
    await waitFor(() => expect(api.downloadPromoteBrowse).toHaveBeenLastCalledWith('Movies', undefined))

    await user.type(within(dialog).getByRole('textbox'), '2026')
    await user.click(within(dialog).getByRole('button', { name: 'Promote' }))

    expect(onClose).toHaveBeenCalledTimes(1)
    expect(api.downloadPromoteBatch).toHaveBeenCalledWith([1], {
      keepSeeding: true, targetSubdir: 'Movies/2026', targetBase: undefined, renameIA: false,
    })
    await waitFor(() => expect(onPromoted).toHaveBeenCalledWith({ promoted: [], failed: [] }))
  })

  it('lists every item and counts them in the title and the action', async () => {
    render(
      <PromoteModal items={[entry(1, 'Movie A'), entry(2, 'Movie B')]} onClose={vi.fn()} onPromoted={vi.fn()} />,
    )
    const dialog = screen.getByRole('dialog')
    expect(dialog).toHaveAccessibleName('Promote 2 downloads')
    expect(within(dialog).getByText(/Movie A/)).toBeInTheDocument()
    expect(within(dialog).getByText(/Movie B/)).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Promote (2)' })).toBeInTheDocument()
    await waitFor(() => expect(api.downloadPromoteBrowse).toHaveBeenCalled())
  })
})
