import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ConfirmProvider, useConfirm, type ConfirmOptions } from './ConfirmDialog'

afterEach(cleanup)

// jsdom's navigator.language is en-US, so misc.confirm/misc.cancel resolve to
// "Confirm"/"Cancel".
function Opener({ opts, onResult }: { readonly opts: ConfirmOptions; readonly onResult: (ok: boolean) => void }) {
  const confirm = useConfirm()
  return <button onClick={() => { void confirm(opts).then(onResult) }}>ask</button>
}

function setup(opts: ConfirmOptions = {}) {
  const onResult = vi.fn()
  render(
    <ConfirmProvider>
      <Opener opts={opts} onResult={onResult} />
    </ConfirmProvider>,
  )
  return { onResult, user: userEvent.setup() }
}

describe('ConfirmProvider / useConfirm', () => {
  it('renders no dialog until confirm() is called', () => {
    setup()
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('resolves true on confirm and closes', async () => {
    const { onResult, user } = setup({ title: 'Delete file?', message: 'This cannot be undone.' })
    await user.click(screen.getByRole('button', { name: 'ask' }))

    const dialog = screen.getByRole('dialog')
    expect(dialog).toHaveAttribute('aria-modal', 'true')
    expect(dialog).toHaveAccessibleName('Delete file?')
    expect(within(dialog).getByText('This cannot be undone.')).toBeInTheDocument()

    await user.click(within(dialog).getByRole('button', { name: 'Confirm' }))
    expect(onResult).toHaveBeenCalledExactlyOnceWith(true)
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('resolves false on cancel', async () => {
    const { onResult, user } = setup()
    await user.click(screen.getByRole('button', { name: 'ask' }))
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel' }))
    expect(onResult).toHaveBeenCalledExactlyOnceWith(false)
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('resolves false on Escape and on the header close button', async () => {
    const { onResult, user } = setup()
    await user.click(screen.getByRole('button', { name: 'ask' }))
    await user.keyboard('{Escape}')
    expect(onResult).toHaveBeenLastCalledWith(false)
    expect(screen.queryByRole('dialog')).toBeNull()

    await user.click(screen.getByRole('button', { name: 'ask' }))
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Close' }))
    expect(onResult).toHaveBeenCalledTimes(2)
    expect(onResult).toHaveBeenLastCalledWith(false)
  })

  it('uses custom labels and falls back to the default title', async () => {
    const { user } = setup({ confirmLabel: 'Wipe', cancelLabel: 'Keep' })
    await user.click(screen.getByRole('button', { name: 'ask' }))
    const dialog = screen.getByRole('dialog')
    expect(dialog).toHaveAccessibleName('Confirm')
    expect(within(dialog).getByRole('button', { name: 'Wipe' })).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Keep' })).toBeInTheDocument()
  })

  it('styles the confirm button as destructive by default and neutral on opt-out', async () => {
    const { user } = setup({ destructive: false, confirmLabel: 'Go' })
    await user.click(screen.getByRole('button', { name: 'ask' }))
    expect(within(screen.getByRole('dialog')).getByRole('button', { name: 'Go' }).className).toContain('bg-green-500')
    cleanup()

    const again = setup({ confirmLabel: 'Go' })
    await again.user.click(screen.getByRole('button', { name: 'ask' }))
    expect(within(screen.getByRole('dialog')).getByRole('button', { name: 'Go' }).className).toContain('bg-red-500')
  })

  it('traps focus in the dialog and returns it to the opener afterwards', async () => {
    const { user } = setup()
    const opener = screen.getByRole('button', { name: 'ask' })
    await user.click(opener)
    const dialog = screen.getByRole('dialog')
    expect(dialog).toContainElement(document.activeElement as HTMLElement)

    // Close → Cancel → Confirm → wraps to Close: never back to the opener.
    for (let i = 0; i < 4; i++) {
      await user.tab()
      expect(dialog).toContainElement(document.activeElement as HTMLElement)
    }

    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(opener).toHaveFocus()
  })

  it('a second confirm() cancels the pending one and shows the new prompt', async () => {
    let confirm!: (opts: ConfirmOptions) => Promise<boolean>
    function Grab() {
      confirm = useConfirm()
      return null
    }
    render(<ConfirmProvider><Grab /></ConfirmProvider>)

    let first!: Promise<boolean>
    let second!: Promise<boolean>
    act(() => { first = confirm({ title: 'First' }) })
    act(() => { second = confirm({ title: 'Second' }) })

    await expect(first).resolves.toBe(false)
    const dialog = screen.getByRole('dialog')
    expect(dialog).toHaveAccessibleName('Second')

    await userEvent.setup().click(within(dialog).getByRole('button', { name: 'Confirm' }))
    await expect(second).resolves.toBe(true)
  })

  it('useConfirm throws outside a ConfirmProvider', () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
    function Bare() {
      useConfirm()
      return null
    }
    expect(() => render(<Bare />)).toThrow('useConfirm must be used within a <ConfirmProvider>')
    spy.mockRestore()
  })
})
