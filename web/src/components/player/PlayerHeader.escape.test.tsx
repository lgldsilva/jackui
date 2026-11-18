import { afterEach, describe, it, expect, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { shellProps } from './PlayerHeader'

afterEach(cleanup)

type ShellOpts = Partial<Parameters<typeof shellProps>[0]>

// Regression (#223): the shell reacted to ANY Escape bubbling through it, so
// closing an overlay layered over the player also minimized the player — or, on
// a full-viewport deep-link tab, navigated Home.
function renderShell(opts: ShellOpts = {}) {
  const setMinimized = vi.fn()
  const onHome = vi.fn()
  const onClose = vi.fn()
  render(
    <div {...shellProps({ minimized: false, audioMode: false, fullViewport: false, onHome, onClose, setMinimized, ariaLabel: 'Movie', ...opts })}>
      <button data-testid="own">Own control</button>
      <div role="dialog" aria-label="Overlay">
        <button data-testid="layered">Layered control</button>
      </div>
      <dialog open aria-label="Native overlay">
        <button data-testid="native">Native control</button>
      </dialog>
    </div>,
  )
  return { setMinimized, onHome, onClose }
}

describe('shellProps — Escape layering', () => {
  it('minimizes on Escape from the player itself', () => {
    const { setMinimized } = renderShell()
    fireEvent.keyDown(screen.getByTestId('own'), { key: 'Escape' })
    expect(setMinimized).toHaveBeenCalledWith(true)
  })

  it('does NOT minimize on Escape from a dialog layered over the player', () => {
    const { setMinimized } = renderShell()
    fireEvent.keyDown(screen.getByTestId('layered'), { key: 'Escape' })
    fireEvent.keyDown(screen.getByTestId('native'), { key: 'Escape' })
    expect(setMinimized).not.toHaveBeenCalled()
  })

  it('full-viewport: goes Home on Escape from the player itself', () => {
    const { onHome } = renderShell({ fullViewport: true })
    fireEvent.keyDown(screen.getByTestId('own'), { key: 'Escape' })
    expect(onHome).toHaveBeenCalledTimes(1)
  })

  it('full-viewport: does NOT go Home on Escape from a layered dialog', () => {
    const { onHome, onClose } = renderShell({ fullViewport: true })
    fireEvent.keyDown(screen.getByTestId('layered'), { key: 'Escape' })
    expect(onHome).not.toHaveBeenCalled()
    expect(onClose).not.toHaveBeenCalled()
  })
})
