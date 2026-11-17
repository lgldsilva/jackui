import { afterEach, describe, it, expect, vi } from 'vitest'
import { cleanup, fireEvent, render, renderHook, screen } from '@testing-library/react'
import i18n from '../../lib/i18n'
import { useKeyboardShortcuts } from './playerHooks'
import { MediaNavButtons } from './PlayerOverlays'
import { SimpleAudioControls } from './SimpleAudioControls'
import { renderPlaylistBar } from './PlaylistBar'
import { PlayerExperienceOverlays } from './PlayerExperienceOverlays'

afterEach(cleanup)

// Regression (review of #248): a transport button keeps focus after a mouse
// click. "Click Next, press Space to pause" must pause — not skip another track.
function setupShortcuts() {
  const media = { paused: true, currentTime: 50, duration: 100, play: vi.fn(() => Promise.resolve()), pause: vi.fn() }
  renderHook(() => useKeyboardShortcuts({
    videoRef: { current: media as unknown as HTMLMediaElement },
    minimized: false,
    requestFullscreen: vi.fn(),
  }))
  return media
}

const t = i18n.t.bind(i18n)

function expectSpaceToggles(button: HTMLElement, media: ReturnType<typeof setupShortcuts>, skip: ReturnType<typeof vi.fn>) {
  // fireEvent returns false when a handler called preventDefault().
  expect(fireEvent.keyDown(button, { key: ' ' })).toBe(false)
  expect(media.play).toHaveBeenCalledTimes(1)
  expect(skip).not.toHaveBeenCalled()
}

describe('Space on a focused transport button toggles playback', () => {
  it.each(['player.overlays.prevEpisode', 'player.overlays.nextEpisode'])('MediaNavButtons — %s', (key) => {
    const media = setupShortcuts()
    const skip = vi.fn()
    render(<MediaNavButtons mediaFileIndices={[0, 1, 2]} mediaCursor={1} currentEp={null} onPrevMedia={skip} onNextMedia={skip} hasPrevMedia hasNextMedia />)
    expectSpaceToggles(screen.getByTitle(t(key)), media, skip)
  })

  it.each(['player.controls.prev', 'player.controls.next'])('SimpleAudioControls — %s', (key) => {
    const media = setupShortcuts()
    const skip = vi.fn()
    render(<SimpleAudioControls onPrev={skip} onNext={skip} hasPrev hasNext />)
    expectSpaceToggles(screen.getByRole('button', { name: t(key) }), media, skip)
  })

  it.each(['player.modal.prevItem', 'player.modal.nextItem'])('PlaylistBar — %s', (key) => {
    const media = setupShortcuts()
    const skip = vi.fn()
    render(renderPlaylistBar(
      { name: 'Mix', items: [], currentIndex: 0 },
      { onPrev: skip, onNext: skip, onToggleShuffle: vi.fn(), onCycleRepeat: vi.fn(), shuffle: false, repeat: 'none' },
      t,
    ))
    expectSpaceToggles(screen.getByTitle(t(key)), media, skip)
  })

  it('PlayerExperienceOverlays — up-next "play now"', () => {
    const media = setupShortcuts()
    const skip = vi.fn()
    render(
      <PlayerExperienceOverlays
        videoRef={{ current: null }} currentTime={95} duration={100} hasNext nextLabel="E02"
        audioMode={false} videoError={false} showResumePrompt={false} onNext={skip}
      />,
    )
    expectSpaceToggles(screen.getByRole('button', { name: t('player.overlays.playNow') }), media, skip)
  })

  it('other buttons keep standard semantics (Space is theirs)', () => {
    const media = setupShortcuts()
    render(<SimpleAudioControls onPrev={vi.fn()} onNext={vi.fn()} hasPrev hasNext onToggleShuffle={vi.fn()} />)
    expect(fireEvent.keyDown(screen.getByRole('button', { name: t('player.controls.shuffle') }), { key: ' ' })).toBe(true)
    expect(media.play).not.toHaveBeenCalled()
  })
})
