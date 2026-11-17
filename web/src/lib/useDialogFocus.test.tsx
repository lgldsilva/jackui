import { afterEach, describe, expect, it } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { useDialogFocus, type DialogFocusOptions } from './useDialogFocus'

afterEach(cleanup)

function Dialog({ open, children, options }: {
  readonly open: boolean
  readonly children?: ReactNode
  readonly options?: DialogFocusOptions
}) {
  const ref = useDialogFocus(open, options)
  if (!open) return null
  return (
    <div ref={ref} role="dialog" aria-label="test" tabIndex={-1}>
      {children}
    </div>
  )
}

// The outside buttons sit before AND after the dialog in DOM order, so a Tab
// that escapes the trap lands on one of them instead of wrapping by accident.
function Page({ open, children, options }: {
  readonly open: boolean
  readonly children?: ReactNode
  readonly options?: DialogFocusOptions
}) {
  return (
    <>
      <button>before</button>
      <Dialog open={open} options={options}>{children}</Dialog>
      <button>after</button>
    </>
  )
}

// Unmounts the dialog while it is still open (the `{x && <Modal />}` pattern).
function Conditional({ show }: { readonly show: boolean }) {
  return (
    <>
      <button>before</button>
      {show && <Dialog open><button>one</button></Dialog>}
      <button>after</button>
    </>
  )
}

describe('useDialogFocus — initial focus and restore', () => {
  it('focuses the first tabbable element on open', () => {
    render(<Page open><button>one</button><button>two</button></Page>)
    expect(screen.getByRole('button', { name: 'one' })).toHaveFocus()
  })

  it('focuses the container when it has no tabbable element', () => {
    render(<Page open><p>text only</p></Page>)
    expect(screen.getByRole('dialog')).toHaveFocus()
  })

  it('initialFocus "container" focuses the dialog itself, not its first control', () => {
    render(<Page open options={{ initialFocus: 'container' }}><button>one</button></Page>)
    expect(screen.getByRole('dialog')).toHaveFocus()
  })

  it('skips a hidden first control when picking the initial focus', () => {
    render(<Page open><button hidden>ghost</button><button>one</button></Page>)
    expect(screen.getByRole('button', { name: 'one' })).toHaveFocus()
  })

  it('restores focus to the opener when open flips to false', () => {
    const { rerender } = render(<Page open={false} />)
    const opener = screen.getByRole('button', { name: 'before' })
    opener.focus()
    rerender(<Page open><button>one</button></Page>)
    expect(screen.getByRole('button', { name: 'one' })).toHaveFocus()
    rerender(<Page open={false} />)
    expect(opener).toHaveFocus()
  })

  it('restores focus to the opener when the dialog unmounts while open', () => {
    const { rerender } = render(<Conditional show={false} />)
    const opener = screen.getByRole('button', { name: 'before' })
    opener.focus()
    rerender(<Conditional show />)
    expect(screen.getByRole('button', { name: 'one' })).toHaveFocus()
    rerender(<Conditional show={false} />)
    expect(opener).toHaveFocus()
  })

  it('does not pull focus back on unmount when the user already focused something else', () => {
    const { rerender } = render(<Conditional show={false} />)
    screen.getByRole('button', { name: 'before' }).focus()
    rerender(<Conditional show />)
    const elsewhere = screen.getByRole('button', { name: 'after' })
    elsewhere.focus()
    rerender(<Conditional show={false} />)
    expect(elsewhere).toHaveFocus()
  })
})

describe('useDialogFocus — Tab trap', () => {
  it('wraps Tab from a trailing <select> back to the first control', async () => {
    const user = userEvent.setup()
    render(
      <Page open>
        <button>one</button>
        <select aria-label="quality"><option>1080p</option></select>
      </Page>,
    )
    await user.tab()
    expect(screen.getByRole('combobox', { name: 'quality' })).toHaveFocus()
    await user.tab()
    expect(screen.getByRole('button', { name: 'one' })).toHaveFocus()
  })

  it('wraps Shift+Tab from the first control to a trailing <select>', async () => {
    const user = userEvent.setup()
    render(
      <Page open>
        <button>one</button>
        <select aria-label="quality"><option>1080p</option></select>
      </Page>,
    )
    await user.tab({ shift: true })
    expect(screen.getByRole('combobox', { name: 'quality' })).toHaveFocus()
  })

  it('wraps Tab from a trailing <summary>', async () => {
    const user = userEvent.setup()
    render(
      <Page open>
        <button>one</button>
        <details><summary>more</summary><p>body</p></details>
      </Page>,
    )
    const summary = screen.getByText('more')
    summary.focus()
    await user.tab()
    expect(screen.getByRole('button', { name: 'one' })).toHaveFocus()
  })

  it('ignores a trailing control hidden with the hidden attribute', async () => {
    const user = userEvent.setup()
    render(
      <Page open>
        <button>one</button>
        <button>two</button>
        <button hidden>ghost</button>
      </Page>,
    )
    await user.tab()
    expect(screen.getByRole('button', { name: 'two' })).toHaveFocus()
    await user.tab()
    expect(screen.getByRole('button', { name: 'one' })).toHaveFocus()
  })

  it('ignores trailing controls inside a display:none or inert ancestor', async () => {
    const user = userEvent.setup()
    render(
      <Page open>
        <button>one</button>
        <button>two</button>
        <div style={{ display: 'none' }}><button>collapsed</button></div>
        <div {...{ inert: '' }}><button>inert</button></div>
      </Page>,
    )
    screen.getByRole('button', { name: 'two' }).focus()
    await user.tab()
    expect(screen.getByRole('button', { name: 'one' })).toHaveFocus()
  })

  it('ignores controls taken out of the tab order with tabindex=-1', async () => {
    const user = userEvent.setup()
    render(
      <Page open>
        <button>one</button>
        <button>two</button>
        <button tabIndex={-1}>skipped</button>
      </Page>,
    )
    screen.getByRole('button', { name: 'two' }).focus()
    await user.tab()
    expect(screen.getByRole('button', { name: 'one' })).toHaveFocus()
  })

  it('wraps Shift+Tab to the last control when the container itself has focus', async () => {
    const user = userEvent.setup()
    render(
      <Page open options={{ initialFocus: 'container' }}>
        <button>one</button>
        <button>two</button>
      </Page>,
    )
    expect(screen.getByRole('dialog')).toHaveFocus()
    await user.tab({ shift: true })
    expect(screen.getByRole('button', { name: 'two' })).toHaveFocus()
  })

  it('keeps focus on the container when Tab is pressed with nothing tabbable', async () => {
    const user = userEvent.setup()
    render(<Page open><p>text only</p></Page>)
    await user.tab()
    expect(screen.getByRole('dialog')).toHaveFocus()
  })

  it('leaves a Tab already handled by a nested trap alone', async () => {
    const user = userEvent.setup()
    const tree = (nested: boolean) => (
      <Page open>
        <button>outer</button>
        <Dialog open={nested}>
          <button>inner</button>
        </Dialog>
      </Page>
    )
    const { rerender } = render(tree(false))
    rerender(tree(true))
    const inner = screen.getByRole('button', { name: 'inner' })
    expect(inner).toHaveFocus()
    // "inner" is the last tabbable of BOTH traps. The nested trap keeps focus
    // on it; the outer one must not then wrap to "outer" behind the nested dialog.
    await user.tab()
    expect(inner).toHaveFocus()
    await user.tab({ shift: true })
    expect(inner).toHaveFocus()
  })

  it('does not trap Tab while closed', async () => {
    const user = userEvent.setup()
    render(<Page open={false} />)
    screen.getByRole('button', { name: 'before' }).focus()
    await user.tab()
    expect(screen.getByRole('button', { name: 'after' })).toHaveFocus()
  })
})
