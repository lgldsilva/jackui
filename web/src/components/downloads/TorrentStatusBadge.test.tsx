import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { TorrentStatusBadge } from './TorrentStatusBadge'

afterEach(cleanup)

// TorrentStatusBadge hard-codes its label per status (no i18n layer), so each
// render must show the English label AND the color class of that status branch.
// Guards the SonarCloud-covered lines against silent regressions when the
// labels are reworded.
describe('TorrentStatusBadge', () => {
  it.each([
    { status: 'downloading' as const, label: 'Downloading', cls: 'bg-emerald-500/15', spinning: true },
    { status: 'paused' as const, label: 'Paused', cls: 'bg-gray-500/15', spinning: false },
    { status: 'seeding' as const, label: 'Seeding', cls: 'bg-violet-500/15', spinning: false },
    { status: 'complete' as const, label: 'Complete', cls: 'bg-green-500/15', spinning: false },
  ])('renders "$label" with the $status styling', ({ status, label, cls, spinning }) => {
    const { container } = render(<TorrentStatusBadge status={status} />)

    const badge = screen.getByText(label)
    expect(badge).toBeInTheDocument()
    // The label is a text node inside the pill — the pill carries the status colors.
    expect(badge.closest('span')).toHaveClass(cls)
    // Only "downloading" renders the animated spinner icon.
    if (spinning) {
      expect(container.querySelector('.animate-spin')).not.toBeNull()
    } else {
      expect(container.querySelector('.animate-spin')).toBeNull()
    }
  })

  it('renders the pill chrome shared by every status', () => {
    const { container } = render(<TorrentStatusBadge status="complete" />)
    expect(container.firstElementChild).toHaveClass('inline-flex', 'rounded-md', 'border', 'font-medium')
  })
})
