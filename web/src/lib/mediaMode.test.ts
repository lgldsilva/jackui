import { describe, it, expect } from 'vitest'
import { getMediaMode, setMediaMode } from './mediaMode'

// The DOM bits (localStorage persistence, CustomEvent fan-out) only exist in a
// browser; the vitest env is 'node'. Here we cover the core contract: the
// in-memory value getMediaMode() returns always reflects the last setMediaMode().
describe('mediaMode', () => {
  it('setMediaMode updates the value read by getMediaMode', () => {
    setMediaMode('audio')
    expect(getMediaMode()).toBe('audio')
    setMediaMode('video')
    expect(getMediaMode()).toBe('video')
  })
})
