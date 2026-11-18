import { afterEach, describe, it, expect, vi } from 'vitest'
import { cleanup, fireEvent, render, renderHook, screen } from '@testing-library/react'
import { useKeyboardShortcuts } from './playerHooks'
import { shellProps } from './PlayerHeader'
import { FileRow } from './FileRow'
import type { useHoverThumb } from '../FileThumbHover'

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

function pointerFocus(el: HTMLElement) {
  fireEvent.pointerDown(el)
  fireEvent.mouseDown(el)
  el.focus()
}

function keyboardFocus(el: HTMLElement) {
  fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Tab' })
  el.focus()
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

  it('lets Space activate a keyboard-focused button instead of toggling playback', () => {
    const { media } = setup()
    renderPlayerShell(false)
    keyboardFocus(screen.getByTestId('own'))
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

// Regression (review of #248): file rows are <button>s that keep focus after a
// mouse click. Treating them as owning Space re-ran playFile() — which restarts
// the file from 0 — instead of pausing ("click an episode, press Space").
describe('useKeyboardShortcuts — file rows', () => {
  const hoverThumb = { show: vi.fn(), move: vi.fn(), hide: vi.fn() } as unknown as ReturnType<typeof useHoverThumb>
  const file = { index: 3, path: 'Show/S01E01.mkv', size: 1, isVideo: true } as Parameters<typeof FileRow>[0]['file']

  function renderRows(playFile: (i: number) => void) {
    const row = (treeItemProps?: React.HTMLAttributes<HTMLButtonElement>) => (
      <FileRow
        file={file} infoHash="abc" selected hoverThumb={hoverThumb} parseEpisode={() => null}
        playFile={playFile} setPreviewFileIdx={vi.fn()} displayName="S01E01.mkv" treeItemProps={treeItemProps}
      />
    )
    render(
      <div {...shellProps({ minimized: false, audioMode: false, fullViewport: false, onClose: vi.fn(), setMinimized: vi.fn(), ariaLabel: 'Movie' })}>
        <div data-testid="flat">{row()}</div>
        <div role="tree" data-testid="tree">
          {row({ role: 'treeitem', 'aria-selected': true })}
          {/* Folder row: FileTree.onKeyDown consumes Space to expand/collapse. */}
          <button role="treeitem" aria-selected={false} aria-expanded data-testid="folder" onKeyDown={e => { if (e.key === ' ') e.preventDefault() }}>Show</button>
        </div>
      </div>,
    )
  }

  it.each(['flat', 'tree'])('Space on a pointer-focused %s file row toggles playback and does not re-activate the row', (view) => {
    const { media } = setup()
    const playFile = vi.fn()
    renderRows(playFile)
    const rowEl = screen.getByTestId(view).querySelector('button')!
    pointerFocus(rowEl)
    expect(press(rowEl, { key: ' ' })).toBe(true)
    expect(media.play).toHaveBeenCalledTimes(1)
    expect(playFile).not.toHaveBeenCalled()
  })

  it('leaves Space on a keyboard-focused file row to the row (native activation)', () => {
    const { media } = setup()
    renderRows(vi.fn())
    const rowEl = screen.getByTestId('tree').querySelector('button')!
    keyboardFocus(rowEl)
    expect(press(rowEl, { key: ' ' })).toBe(false)
    expect(media.play).not.toHaveBeenCalled()
  })

  it('leaves Enter on a file row to the native button click', () => {
    const { media } = setup()
    renderRows(vi.fn())
    const rowEl = screen.getByTestId('tree').querySelector('button')!
    expect(press(rowEl, { key: 'Enter' })).toBe(false)
    expect(media.play).not.toHaveBeenCalled()
  })

  it('leaves Space on a folder row to the tree (expand/collapse)', () => {
    const { media } = setup()
    renderRows(vi.fn())
    fireEvent.keyDown(screen.getByTestId('folder'), { key: ' ' })
    expect(media.play).not.toHaveBeenCalled()
  })
})
