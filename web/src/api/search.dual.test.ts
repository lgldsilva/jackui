import { vi, describe, it, expect, beforeEach } from 'vitest'

// Mock the HTTP core so client.ts (and the domain modules it barrels) get a
// fake axios instance — same pattern as client.test.ts.
const getMock = vi.fn()
vi.mock('./http', () => ({
  api: { get: (...a: unknown[]) => getMock(...a), post: vi.fn(), delete: vi.fn() },
  withToken: (u: string) => u,
  fetchMediaToken: vi.fn(),
  MAGNET_PREFIX: 'magnet:?xt=urn:btih:',
}))

import { searchTorrents, appendAudioSearchParams, type AudioSearchPrefs, type SearchResult } from './client'

function lastCalledUrl(): string {
  const call = getMock.mock.calls.at(-1)
  return String(call?.[0] ?? '')
}

describe('searchTorrents — dual-audio query params', () => {
  beforeEach(() => {
    getMock.mockReset()
    getMock.mockResolvedValue({ status: 200, data: [] as SearchResult[] })
  })

  it('sends no dual-audio params by default', async () => {
    await searchTorrents('matrix', ['all'], 'all')
    const url = lastCalledUrl()
    expect(url).toContain('/search?')
    expect(url).toContain('q=matrix')
    expect(url).not.toContain('prefer_dual')
    expect(url).not.toContain('dual_only')
  })

  it('sends prefer_dual=1 when preferDual is on', async () => {
    await searchTorrents('matrix', ['all'], 'all', { preferDual: true })
    const url = lastCalledUrl()
    expect(url).toContain('prefer_dual=1')
    expect(url).not.toContain('dual_only')
  })

  it('sends both params when both prefs are on', async () => {
    await searchTorrents('matrix', ['all'], 'all', { preferDual: true, dualOnly: true })
    const url = lastCalledUrl()
    expect(url).toContain('prefer_dual=1')
    expect(url).toContain('dual_only=1')
  })

  it('sends dual_only=1 alone when dualOnly is on and preferDual is off (filter only)', async () => {
    await searchTorrents('matrix', ['all'], 'all', { preferDual: false, dualOnly: true })
    const url = lastCalledUrl()
    expect(url).not.toContain('prefer_dual')
    expect(url).toContain('dual_only=1')
  })
})

describe('appendAudioSearchParams', () => {
  it('is a no-op for undefined prefs', () => {
    const params = new URLSearchParams({ q: 'x' })
    appendAudioSearchParams(params, undefined)
    expect(params.toString()).toBe('q=x')
  })

  it('is a no-op for falsy prefs', () => {
    const params = new URLSearchParams({ q: 'x' })
    const prefs: AudioSearchPrefs = { preferDual: false, dualOnly: false }
    appendAudioSearchParams(params, prefs)
    expect(params.toString()).toBe('q=x')
  })

  it('sets both params as "1" (backend contract: "1"/"true" truthy)', () => {
    const params = new URLSearchParams({ q: 'x' })
    appendAudioSearchParams(params, { preferDual: true, dualOnly: true })
    expect(params.get('prefer_dual')).toBe('1')
    expect(params.get('dual_only')).toBe('1')
  })
})
