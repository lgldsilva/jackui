import { describe, expect, it, vi } from 'vitest'

type Handler = (evt: string, data: unknown) => void

const { FakeHls, instances } = vi.hoisted(() => {
const instances: unknown[] = []
class FakeHls {
  static readonly Events = { ERROR: 'hlsError', MANIFEST_PARSED: 'manifestParsed', AUDIO_TRACKS_UPDATED: 'a', SUBTITLE_TRACKS_UPDATED: 's' }
  static readonly ErrorTypes = { NETWORK_ERROR: 'networkError', MEDIA_ERROR: 'mediaError', OTHER_ERROR: 'otherError' }
  static isSupported() { return true }
  handlers = new Map<string, Handler[]>()
  destroy = vi.fn()
  startLoad = vi.fn()
  recoverMediaError = vi.fn()
  loadSource = vi.fn()
  attachMedia = vi.fn()
  audioTracks = []
  subtitleTracks = []
  constructor() { instances.push(this) }
  on(evt: string, h: Handler) { this.handlers.set(evt, [...(this.handlers.get(evt) ?? []), h]) }
  emit(evt: string, data: unknown) { for (const h of this.handlers.get(evt) ?? []) h(evt, data) }
}
  return { FakeHls, instances: instances as InstanceType<typeof FakeHls>[] }
})
vi.mock('hls.js', () => ({ default: FakeHls }))

import { attachHlsJs } from './VideoPlayerElement'

function setup() {
  const hlsRef = { current: null as unknown }
  const onHls = vi.fn()
  const onAudio = vi.fn()
  const video = document.createElement('video')
  const cleanup = attachHlsJs(video, '/m.m3u8', onAudio, hlsRef as never, onHls)
  return { hlsRef, onHls, onAudio, cleanup, hls: instances[instances.length - 1] }
}

describe('attachHlsJs', () => {
  // Regression (#200): after an unrecoverable fatal error hls.js destroyed the
  // instance but hlsRef/state still pointed at it.
  it('drops references when a fatal error destroys the instance', () => {
    const { hlsRef, onHls, onAudio, cleanup, hls } = setup()
    expect(hlsRef.current).toBe(hls)

    hls.emit(FakeHls.Events.ERROR, { fatal: true, type: FakeHls.ErrorTypes.OTHER_ERROR })
    expect(hls.destroy).toHaveBeenCalledTimes(1)
    expect(hlsRef.current).toBeNull()
    expect(onHls).toHaveBeenLastCalledWith(null)
    expect(onAudio).toHaveBeenLastCalledWith(0)

    cleanup() // must not destroy twice
    expect(hls.destroy).toHaveBeenCalledTimes(1)
  })

  it('keeps the instance on a recoverable fatal error', () => {
    const { hlsRef, hls } = setup()
    hls.emit(FakeHls.Events.ERROR, { fatal: true, type: FakeHls.ErrorTypes.NETWORK_ERROR })
    expect(hls.startLoad).toHaveBeenCalled()
    expect(hlsRef.current).toBe(hls)
    hls.emit(FakeHls.Events.ERROR, { fatal: false, type: FakeHls.ErrorTypes.OTHER_ERROR })
    expect(hlsRef.current).toBe(hls)
  })

  it('cleanup destroys and releases', () => {
    const { hlsRef, onHls, cleanup, hls } = setup()
    cleanup()
    expect(hls.destroy).toHaveBeenCalledTimes(1)
    expect(hlsRef.current).toBeNull()
    expect(onHls).toHaveBeenLastCalledWith(null)
  })
})
