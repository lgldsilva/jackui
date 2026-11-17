import { afterEach, describe, it, expect, vi } from 'vitest'
import { cleanup, fireEvent, render, renderHook, screen } from '@testing-library/react'
import { useKeyboardShortcuts } from './playerHooks'
import { shellProps } from './PlayerHeader'

afterEach(cleanup)

type FakeMedia = {
  paused: boolean
  muted: boolean
  currentTime: number
  duration: number
  volume: number
  play: ReturnType<typeof vi.fn>
  pause: ReturnType<typeof vi.fn>
}

function setup() {
  const media: FakeMedia = {
    paused: true, muted: false, currentTime: 50, duration: 100, volume: 0.5,
    play: vi.fn(() => Promise.resolve()), pause: vi.fn(),
  }
  const onNext = vi.fn()
  renderHook(() => useKeyboardShortcuts({
    videoRef: { current: media as unknown as HTMLMediaElement },
    minimized: false,
    requestFullscreen: vi.fn(),
    onNext,
  }))
  return { media, onNext }
}

// Renders the real player shell with an overlay inside it (Sheet/DownloadModal
// are rendered in the shell, without a portal).
function renderPlayerShell(overlay: boolean) {
  render(
    <div {...shellProps({ minimized: false, audioMode: false, fullViewport: false, onClose: vi.fn(), setMinimized: vi.fn(), ariaLabel: 'Movie' })}>
      <button data-testid="own">Own control</button>
      <div role="slider" tabIndex={0} aria-valuenow={0} aria-label="Seek" data-testid="seek" />
      <div role="menu"><button role="menuitem" data-testid="menuitem">VLC</button></div>
      {overlay && (
        <div role="dialog" aria-modal="true" aria-label="Overlay">
          <button data-testid="layered">Layered control</button>
        </div>
      )}
    </div>,
  )
}

// fireEvent returns false when a handler called preventDefault().
const press = (el: Element | Document, init: KeyboardEventInit) => !fireEvent.keyDown(el, init)

describe('useKeyboardShortcuts — baseline', () => {
  it('handles a plain shortcut on the page and on the player shell', () => {
    const { media } = setup()
    renderPlayerShell(false)
    expect(press(document.body, { key: 'm' })).toBe(true)
    expect(media.muted).toBe(true)
    expect(press(screen.getByTestId('own'), { key: 'l' })).toBe(true)
    expect(media.currentTime).toBe(60)
  })

  it('keeps Space/letters working on the seek slider', () => {
    const { media } = setup()
    renderPlayerShell(false)
    expect(press(screen.getByTestId('seek'), { key: ' ' })).toBe(true)
    expect(media.play).toHaveBeenCalledTimes(1)
  })
})

// Regression (#223): the global listener mapped by `e.key` alone.
describe('useKeyboardShortcuts — guards', () => {
  it.each([
    ['Ctrl+L', { key: 'l', ctrlKey: true }],
    ['Cmd+K', { key: 'k', metaKey: true }],
    ['Alt+ArrowLeft', { key: 'ArrowLeft', altKey: true }],
    ['Ctrl+F', { key: 'f', ctrlKey: true }],
  ])('leaves %s to the browser', (_name, init) => {
    const { media } = setup()
    expect(press(document.body, init)).toBe(false)
    expect(media.currentTime).toBe(50)
    expect(media.play).not.toHaveBeenCalled()
  })

  it('ignores a key some other handler already consumed', () => {
    const { media } = setup()
    const consume = (e: Event) => e.preventDefault()
    document.body.addEventListener('keydown', consume)
    try {
      fireEvent.keyDown(document.body, { key: 'ArrowRight' })
    } finally {
      document.body.removeEventListener('keydown', consume)
    }
    expect(media.currentTime).toBe(50)
  })

  it('ignores keys during IME composition', () => {
    const { media } = setup()
    expect(press(document.body, { key: 'k', isComposing: true })).toBe(false)
    expect(media.play).not.toHaveBeenCalled()
  })

  it('lets Space activate a focused button instead of toggling playback', () => {
    const { media } = setup()
    renderPlayerShell(false)
    expect(press(screen.getByTestId('own'), { key: ' ' })).toBe(false)
    expect(media.play).not.toHaveBeenCalled()
  })

  it('ignores keys typed in a contenteditable', () => {
    const { media } = setup()
    render(<div contentEditable suppressContentEditableWarning data-testid="rich">x</div>)
    expect(press(screen.getByTestId('rich'), { key: 'm' })).toBe(false)
    expect(media.muted).toBe(false)
  })

  it('ignores keys inside a menu', () => {
    const { media } = setup()
    renderPlayerShell(false)
    expect(press(screen.getByTestId('menuitem'), { key: 'ArrowUp' })).toBe(false)
    expect(media.volume).toBe(0.5)
  })

  it('ignores keys from a dialog layered over the player', () => {
    const { media, onNext } = setup()
    renderPlayerShell(true)
    expect(press(screen.getByTestId('layered'), { key: 'n' })).toBe(false)
    expect(onNext).not.toHaveBeenCalled()
    expect(media.currentTime).toBe(50)
  })

  it('ignores keys while an overlay is open in the player, wherever focus is', () => {
    const { media } = setup()
    renderPlayerShell(true)
    expect(press(document.body, { key: 'm' })).toBe(false)
    expect(press(screen.getByTestId('own'), { key: 'j' })).toBe(false)
    expect(media.muted).toBe(false)
    expect(media.currentTime).toBe(50)
  })
})
