import { StrictMode } from 'react'
import { afterEach, describe, it, expect, vi } from 'vitest'
import { cleanup, fireEvent, render, renderHook, screen } from '@testing-library/react'
import i18n from '../../lib/i18n'
import { useKeyboardShortcuts } from './playerHooks'
import { MediaNavButtons } from './PlayerOverlays'
import { SimpleAudioControls } from './SimpleAudioControls'
import { renderPlaylistBar } from './PlaylistBar'
import { PlayerExperienceOverlays } from './PlayerExperienceOverlays'
import { PlaylistTracksSidebar } from './PlaylistTracksSidebar'
import { ChapterNavButtons, ChaptersPanel } from './ChaptersPanel'
import type { PlaylistGroup } from './playlistTracks'

afterEach(cleanup)

// Regression (reviews of #248): a control keeps focus after a mouse click.
// "Click Next / a track / a chapter, press Space to pause" must pause — not skip,
// restart or re-seek. Space belongs to the control only when focus got there by
// keyboard. One rule for every button, so new controls cannot regress it.
function setupShortcuts(strict = false) {
  const media = { paused: true, currentTime: 50, duration: 100, play: vi.fn(() => Promise.resolve()), pause: vi.fn() }
  const { unmount } = renderHook(() => useKeyboardShortcuts({
    videoRef: { current: media as unknown as HTMLMediaElement },
    minimized: false,
    requestFullscreen: vi.fn(),
  }), strict ? { wrapper: StrictMode } : undefined)
  return { media, unmount }
}

const t = i18n.t.bind(i18n)
const chapters = [
  { index: 0, startSec: 0, endSec: 60 },
  { index: 1, startSec: 60, endSec: 180 },
  { index: 2, startSec: 180, endSec: 300 },
]

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
const prevented = (el: Element, type: 'keyDown' | 'keyUp', key = ' ') => !fireEvent[type](el, { key })

function expectPointerSpaceToggles(button: HTMLElement, action: ReturnType<typeof vi.fn>) {
  const { media } = setupShortcuts()
  pointerFocus(button)
  expect(prevented(button, 'keyDown')).toBe(true)
  expect(media.play).toHaveBeenCalledTimes(1)
  expect(action).not.toHaveBeenCalled()
}

describe('Space on a pointer-focused control toggles playback', () => {
  it.each(['player.overlays.prevEpisode', 'player.overlays.nextEpisode'])('MediaNavButtons — %s', (key) => {
    const skip = vi.fn()
    render(<MediaNavButtons mediaFileIndices={[0, 1, 2]} mediaCursor={1} currentEp={null} onPrevMedia={skip} onNextMedia={skip} hasPrevMedia hasNextMedia />)
    expectPointerSpaceToggles(screen.getByTitle(t(key)), skip)
  })

  it.each(['player.controls.prev', 'player.controls.next'])('SimpleAudioControls — %s', (key) => {
    const skip = vi.fn()
    render(<SimpleAudioControls onPrev={skip} onNext={skip} hasPrev hasNext />)
    expectPointerSpaceToggles(screen.getByRole('button', { name: t(key) }), skip)
  })

  it.each(['player.modal.prevItem', 'player.modal.nextItem'])('PlaylistBar — %s', (key) => {
    const skip = vi.fn()
    render(renderPlaylistBar(
      { name: 'Mix', items: [], currentIndex: 0 },
      { onPrev: skip, onNext: skip, onToggleShuffle: vi.fn(), onCycleRepeat: vi.fn(), shuffle: false, repeat: 'none' },
      t,
    ))
    expectPointerSpaceToggles(screen.getByTitle(t(key)), skip)
  })

  it('PlayerExperienceOverlays — up-next "play now"', () => {
    const skip = vi.fn()
    render(
      <PlayerExperienceOverlays
        videoRef={{ current: null }} currentTime={95} duration={100} hasNext nextLabel="E02"
        audioMode={false} videoError={false} showResumePrompt={false} onNext={skip}
      />,
    )
    expectPointerSpaceToggles(screen.getByRole('button', { name: t('player.overlays.playNow') }), skip)
  })

  it('PlaylistTracksSidebar — track row', () => {
    const playFile = vi.fn()
    const onJump = vi.fn()
    const groups: PlaylistGroup[] = [{
      itemIndex: 0, title: 'Album', infoHash: 'abc', isLocal: false, status: 'ready',
      tracks: [{ fileIndex: 4, name: '01 - Intro.flac', path: 'Album/01 - Intro.flac', size: 1, kind: 'audio' }],
    }]
    render(<PlaylistTracksSidebar groups={groups} ensureLoaded={vi.fn()} currentItemIndex={0} selectedFile={4} playFile={playFile} onJump={onJump} onClose={vi.fn()} />)
    const row = screen.getByText('01 - Intro.flac').closest('button')!
    expectPointerSpaceToggles(row, playFile)
    expect(onJump).not.toHaveBeenCalled()
  })

  it('ChaptersPanel — chapter row', () => {
    const onSeek = vi.fn()
    render(<ChaptersPanel chapters={chapters} currentTime={70} onSeek={onSeek} formatTime={s => `${s}s`} />)
    expectPointerSpaceToggles(screen.getByTitle(t('player.chapters.seekTo', { time: '180s' })), onSeek)
  })

  it.each(['player.chapters.prev', 'player.chapters.next'])('ChapterNavButtons — %s', (key) => {
    const onSeek = vi.fn()
    render(<ChapterNavButtons chapters={chapters} currentTime={70} onSeek={onSeek} />)
    expectPointerSpaceToggles(screen.getByTitle(t(key)), onSeek)
  })

  it('stays the player\'s when a shortcut arrow key is pressed after the click', () => {
    const { media } = setupShortcuts()
    render(<SimpleAudioControls onPrev={vi.fn()} onNext={vi.fn()} hasPrev hasNext />)
    const next = screen.getByRole('button', { name: t('player.controls.next') })
    pointerFocus(next)
    fireEvent.keyDown(next, { key: 'ArrowRight' })
    expect(prevented(next, 'keyDown')).toBe(true)
    expect(media.play).toHaveBeenCalledTimes(1)
  })
})

describe('Space on a keyboard-focused control belongs to the control', () => {
  it('is left alone after Tab focus (no toggle, native activation)', () => {
    const { media } = setupShortcuts()
    render(<SimpleAudioControls onPrev={vi.fn()} onNext={vi.fn()} hasPrev hasNext />)
    const next = screen.getByRole('button', { name: t('player.controls.next') })
    keyboardFocus(next)
    expect(prevented(next, 'keyDown')).toBe(false)
    expect(prevented(next, 'keyUp')).toBe(false)
    expect(media.play).not.toHaveBeenCalled()
  })

  it('becomes the player\'s again once the same control is clicked', () => {
    const { media } = setupShortcuts()
    render(<SimpleAudioControls onPrev={vi.fn()} onNext={vi.fn()} hasPrev hasNext />)
    const next = screen.getByRole('button', { name: t('player.controls.next') })
    keyboardFocus(next)
    fireEvent.pointerDown(next)
    expect(prevented(next, 'keyDown')).toBe(true)
    expect(media.play).toHaveBeenCalledTimes(1)
  })
})

// Firefox fires a button's click on Space keyup even when keydown was
// preventDefault-ed, which would re-activate the row/button.
describe('Space keyup after a handled keydown', () => {
  it('is preventDefault-ed once, only for the keydown the player handled', () => {
    setupShortcuts()
    render(<SimpleAudioControls onPrev={vi.fn()} onNext={vi.fn()} hasPrev hasNext />)
    const next = screen.getByRole('button', { name: t('player.controls.next') })
    pointerFocus(next)
    expect(prevented(next, 'keyUp')).toBe(false)
    fireEvent.keyDown(next, { key: ' ' })
    expect(prevented(next, 'keyUp')).toBe(true)
    expect(prevented(next, 'keyUp')).toBe(false)
  })

  it('does not touch other keyups', () => {
    setupShortcuts()
    render(<SimpleAudioControls onPrev={vi.fn()} onNext={vi.fn()} hasPrev hasNext />)
    const next = screen.getByRole('button', { name: t('player.controls.next') })
    pointerFocus(next)
    fireEvent.keyDown(next, { key: ' ' })
    expect(prevented(next, 'keyUp', 'k')).toBe(false)
  })
})

describe('focus-modality listeners follow the hook lifecycle', () => {
  it('stops handling keys after unmount', () => {
    const { media, unmount } = setupShortcuts()
    unmount()
    expect(prevented(document.body, 'keyDown', 'k')).toBe(false)
    expect(prevented(document.body, 'keyUp')).toBe(false)
    expect(media.play).not.toHaveBeenCalled()
  })

  it('tracks modality once under StrictMode (double-invoked effects) and cleans up', () => {
    const { media, unmount } = setupShortcuts(true)
    render(<SimpleAudioControls onPrev={vi.fn()} onNext={vi.fn()} hasPrev hasNext />)
    const next = screen.getByRole('button', { name: t('player.controls.next') })
    keyboardFocus(next)
    expect(prevented(next, 'keyDown')).toBe(false)
    pointerFocus(next)
    expect(prevented(next, 'keyDown')).toBe(true)
    expect(media.play).toHaveBeenCalledTimes(1)
    unmount()
    expect(prevented(next, 'keyDown')).toBe(false)
    expect(media.play).toHaveBeenCalledTimes(1)
  })
})
