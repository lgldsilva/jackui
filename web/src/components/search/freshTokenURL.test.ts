import { afterEach, describe, expect, it, vi } from 'vitest'

const get = vi.hoisted(() => vi.fn())
vi.mock('../../api/http', async (orig) => {
  const actual = await orig<typeof import('../../api/http')>()
  return { ...actual, api: { ...actual.api, get } }
})

import { freshTokenURL } from './useSearchStreams'

describe('freshTokenURL', () => {
  afterEach(() => { localStorage.clear(); get.mockReset() })

  // The /auth/me ping lets the 401 interceptor store a refreshed token BEFORE
  // the URL is rebuilt, so a reconnect never replays an expired ?token=.
  it('pings /auth/me and then builds the URL with the current token', async () => {
    get.mockImplementation(async () => { localStorage.setItem('jackui:auth.access', JSON.stringify('fresh')) })
    const url = await freshTokenURL('/api/search/stream?q=x')
    expect(get).toHaveBeenCalledWith('/auth/me')
    expect(url).toBe('/api/search/stream?q=x&token=fresh')
  })

  it('still builds the URL when the ping fails', async () => {
    get.mockRejectedValue(new Error('offline'))
    localStorage.setItem('jackui:auth.access', JSON.stringify('old'))
    expect(await freshTokenURL('/api/search/stream?q=x')).toBe('/api/search/stream?q=x&token=old')
  })
})
