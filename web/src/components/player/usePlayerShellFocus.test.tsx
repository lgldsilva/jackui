// The player shell declares role=dialog + aria-modal while expanded, so it must
// own focus there — and must NOT while minimized (PiP / audio bar), where the
// page behind stays fully usable.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { shellProps } from './PlayerHeader'
import { usePlayerShellFocus } from './usePlayerShellFocus'

afterEach(cleanup)

function Shell({ minimized, fullViewport = false }: { readonly minimized: boolean; readonly fullViewport?: boolean }) {
  const shellRef = usePlayerShellFocus(minimized)
  return (
    <div
      ref={shellRef}
      data-testid="shell"
      {...shellProps({ minimized, audioMode: false, fullViewport, onClose: vi.fn(), setMinimized: vi.fn(), ariaLabel: 'Some Movie' })}
    >
      <button>favorite</button>
      <video data-testid="video" controls><track kind="captions" /></video>
      <button>minimize</button>
    </div>
  )
}

function Page(props: { readonly minimized: boolean; readonly fullViewport?: boolean }) {
  return (
    <>
      <button>card</button>
      <Shell {...props} />
      <button>page link</button>
    </>
  )
}

describe('usePlayerShellFocus', () => {
  it('moves focus onto the expanded shell itself, not onto a control or the video', () => {
    render(<Page minimized={false} />)
    const shell = screen.getByRole('dialog', { name: 'Some Movie' })
    expect(shell).toHaveAttribute('aria-modal', 'true')
    expect(shell).toHaveFocus()
  })

  it('traps Tab inside the expanded shell in both directions', async () => {
    const user = userEvent.setup()
    render(<Page minimized={false} />)
    await user.tab({ shift: true })
    expect(screen.getByRole('button', { name: 'minimize' })).toHaveFocus()
    await user.tab()
    expect(screen.getByRole('button', { name: 'favorite' })).toHaveFocus()
    await user.tab({ shift: true })
    expect(screen.getByRole('button', { name: 'minimize' })).toHaveFocus()
  })

  it('applies to the full-viewport (deep-link) shell too', () => {
    render(<Page minimized={false} fullViewport />)
    expect(screen.getByRole('dialog', { name: 'Some Movie' })).toHaveFocus()
  })

  it('takes no focus and sets no dialog semantics when it starts minimized', () => {
    render(<Page minimized />)
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(screen.getByTestId('shell')).not.toHaveAttribute('aria-modal')
    expect(document.body).toHaveFocus()
  })

  it('lets Tab leave the minimized player into the rest of the page', async () => {
    const user = userEvent.setup()
    render(<Page minimized />)
    screen.getByRole('button', { name: 'minimize' }).focus()
    await user.tab()
    expect(screen.getByRole('button', { name: 'page link' })).toHaveFocus()
    screen.getByRole('button', { name: 'favorite' }).focus()
    await user.tab({ shift: true })
    expect(screen.getByRole('button', { name: 'card' })).toHaveFocus()
  })

  it('hands focus back to the opener on minimize and re-takes it on expand', () => {
    const { rerender } = render(<Page minimized />)
    const card = screen.getByRole('button', { name: 'card' })
    card.focus()
    rerender(<Page minimized={false} />)
    expect(screen.getByTestId('shell')).toHaveFocus()
    rerender(<Page minimized />)
    expect(card).toHaveFocus()
    rerender(<Page minimized={false} />)
    expect(screen.getByTestId('shell')).toHaveFocus()
  })

  it('hands focus back to the opener when the player closes while expanded', () => {
    const { rerender } = render(<><button>card</button></>)
    const card = screen.getByRole('button', { name: 'card' })
    card.focus()
    rerender(<><button>card</button><Shell minimized={false} /></>)
    expect(screen.getByTestId('shell')).toHaveFocus()
    rerender(<><button>card</button></>)
    expect(card).toHaveFocus()
  })
})
