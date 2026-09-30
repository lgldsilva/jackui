// Security guard: videoKey is API data interpolated into the YouTube embed
// URL. Only a well-formed 11-char video id may render the iframe; anything
// else must leave the modal shell (still closable) without an iframe.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import TrailerModal, { isValidYouTubeKey } from './TrailerModal'

afterEach(cleanup)

describe('isValidYouTubeKey', () => {
  it('accepts a standard 11-char video id', () => {
    expect(isValidYouTubeKey('dQw4w9WgXcQ')).toBe(true)
    expect(isValidYouTubeKey('abc_efg-hij')).toBe(true) // - and _ allowed
  })

  it('rejects wrong length and forbidden characters', () => {
    expect(isValidYouTubeKey('short')).toBe(false)
    expect(isValidYouTubeKey('dQw4w9WgXcQX')).toBe(false) // 12 chars
    expect(isValidYouTubeKey('../../etc')).toBe(false) // path traversal
    expect(isValidYouTubeKey('a.b?c=d#e')).toBe(false) // query/fragment injection
  })

  it('rejects empty input', () => {
    expect(isValidYouTubeKey('')).toBe(false)
  })
})

describe('TrailerModal — iframe gating', () => {
  it('renders the iframe for a valid videoKey', () => {
    const { container } = render(<TrailerModal videoKey="dQw4w9WgXcQ" title="Trailer" onClose={() => {}} />)
    const iframe = container.querySelector('iframe')
    expect(iframe).not.toBeNull()
    expect(iframe).toHaveAttribute('src', expect.stringContaining('/embed/dQw4w9WgXcQ'))
  })

  it('renders NO iframe for an unvalidated (malicious) videoKey', () => {
    const { container } = render(<TrailerModal videoKey="../../evil" title="Trailer" onClose={() => {}} />)
    expect(container.querySelector('iframe')).toBeNull()
    // The dialog shell stays up so the user can still close it.
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('still calls onClose via the close button when the iframe is suppressed', async () => {
    const user = (await import('@testing-library/user-event')).default
    const onClose = vi.fn()
    render(<TrailerModal videoKey="bad" title="Trailer" onClose={onClose} />)
    await user.setup().click(screen.getByRole('button', { name: /close/i }))
    expect(onClose).toHaveBeenCalledTimes(1)
  })
})
