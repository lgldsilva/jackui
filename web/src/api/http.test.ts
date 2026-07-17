import { beforeEach, describe, expect, it } from 'vitest'
import { setRevealHidden } from '../lib/reveal'
import { withToken } from './http'

describe('withToken media credentials', () => {
  beforeEach(() => {
    localStorage.clear()
    setRevealHidden(false)
  })

  it('omits revealHidden while the curtain is closed', () => {
    expect(withToken('/api/local/file?mount=M&path=p')).toBe('/api/local/file?mount=M&path=p')
  })

  it('adds revealHidden even when auth is disabled', () => {
    setRevealHidden(true)
    expect(withToken('/api/local/file?mount=M&path=p')).toBe('/api/local/file?mount=M&path=p&revealHidden=1')
  })

  it('replaces stale credentials without duplicating other parameters', () => {
    setRevealHidden(true)
    const first = withToken('/api/local/file?mount=M&revealHidden=0&token=old&path=p', 'new')
    const second = withToken(first, 'new')
    expect(second).toBe('/api/local/file?mount=M&path=p&revealHidden=1&token=new')
  })

  it('removes stale revealHidden after the curtain closes', () => {
    expect(withToken('/api/local/file?mount=M&revealHidden=1&path=p')).toBe('/api/local/file?mount=M&path=p')
  })
})
