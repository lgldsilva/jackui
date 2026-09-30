import { afterEach, describe, it, expect, vi } from 'vitest'
import { render, screen, within, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Sheet } from './Sheet'

// Note on i18n in the test environment:
// jsdom uses navigator.language="en-US" by default, so
// t('misc.close') → "Close" (not "Fechar").
// We use toHaveAccessibleName() and English names to match.

afterEach(cleanup)

function renderSheet(overrides: Record<string, unknown> = {}) {
  return render(
    <Sheet open onClose={vi.fn()} title="Test modal" {...overrides}>
      <p>Modal content</p>
    </Sheet>,
  )
}

describe('Sheet — accessibility', () => {
  it('renders with role="dialog" and aria-modal="true"', () => {
    renderSheet()
    const dialog = screen.getByRole('dialog')
    expect(dialog).toHaveAttribute('aria-modal', 'true')
  })

  it('aria-labelledby points to the title id', () => {
    renderSheet()
    const dialog = screen.getByRole('dialog')
    const labelledby = dialog.getAttribute('aria-labelledby')
    expect(labelledby).toBeTruthy()
    const titleEl = document.getElementById(labelledby!)
    expect(titleEl).toBeInTheDocument()
    expect(titleEl).toHaveTextContent('Test modal')
  })

  it('close button has a translated aria-label', () => {
    renderSheet()
    const closeBtn = screen.getByRole('button', { name: 'Close' })
    expect(closeBtn).toBeInTheDocument()
    expect(closeBtn).toHaveAccessibleName('Close')
  })

  it('focuses the first focusable element on open', () => {
    renderSheet()
    const closeBtn = screen.getByRole('button', { name: 'Close' })
    expect(closeBtn).toHaveFocus()
  })

  it('restores focus to the previous element when closed', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()

    const { rerender } = render(
      <>
        <button data-testid="trigger">Open</button>
        <Sheet open={false} onClose={onClose} title="Modal">
          <p>content</p>
        </Sheet>
      </>,
    )

    const trigger = screen.getByTestId('trigger')
    await user.click(trigger)
    expect(trigger).toHaveFocus()

    // Opens the modal
    rerender(
      <>
        <button data-testid="trigger">Open</button>
        <Sheet open onClose={onClose} title="Modal">
          <p>content</p>
        </Sheet>
      </>,
    )

    const closeBtn = screen.getByRole('button', { name: 'Close' })
    expect(closeBtn).toHaveFocus()

    // Closes the modal
    rerender(
      <>
        <button data-testid="trigger">Open</button>
        <Sheet open={false} onClose={onClose} title="Modal">
          <p>content</p>
        </Sheet>
      </>,
    )

    // Focus was restored to the trigger
    expect(trigger).toHaveFocus()
  })

  it('cycles focus with Tab inside the modal (focus trap)', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()

    render(
      <Sheet open onClose={onClose} title="Focus modal">
        <button data-testid="btn-1">Button 1</button>
        <button data-testid="btn-2">Button 2</button>
        <a data-testid="link-1" href="https://example.com/">Link</a>
      </Sheet>,
    )

    const dialog = screen.getByRole('dialog')
    const closeBtn = within(dialog).getByRole('button', { name: 'Close' })

    // Focus starts on the close button (first focusable)
    expect(closeBtn).toHaveFocus()

    // Tab → Button 1
    await user.tab()
    expect(within(dialog).getByTestId('btn-1')).toHaveFocus()

    // Tab → Button 2
    await user.tab()
    expect(within(dialog).getByTestId('btn-2')).toHaveFocus()

    // Tab → Link
    await user.tab()
    expect(within(dialog).getByTestId('link-1')).toHaveFocus()

    // Tab → back to the close button (cycle)
    await user.tab()
    expect(closeBtn).toHaveFocus()
  })

  it('Shift+Tab cycles focus in reverse', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()

    render(
      <Sheet open onClose={onClose} title="Shift tab modal">
        <button data-testid="btn-1">Button 1</button>
        <button data-testid="btn-2">Button 2</button>
      </Sheet>,
    )

    const dialog = screen.getByRole('dialog')
    const closeBtn = within(dialog).getByRole('button', { name: 'Close' })

    // Shift+Tab on the first focusable → goes to the last (btn-2)
    await user.tab({ shift: true })
    expect(within(dialog).getByTestId('btn-2')).toHaveFocus()

    // Shift+Tab → Button 1
    await user.tab({ shift: true })
    expect(within(dialog).getByTestId('btn-1')).toHaveFocus()

    // Shift+Tab → close button
    await user.tab({ shift: true })
    expect(closeBtn).toHaveFocus()
  })

  it('renders nothing when open=false', () => {
    const { container } = render(
      <Sheet open={false} onClose={vi.fn()} title="Invisible">
        <p>should not appear</p>
      </Sheet>,
    )
    expect(container).toBeEmptyDOMElement()
  })

  it('has no aria-labelledby when hideHeader=true', () => {
    render(
      <Sheet open onClose={vi.fn()} title="No header" hideHeader>
        <p>content</p>
      </Sheet>,
    )
    const dialog = screen.getByRole('dialog')
    expect(dialog).not.toHaveAttribute('aria-labelledby')
  })
})
