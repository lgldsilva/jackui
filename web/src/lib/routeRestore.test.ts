import { describe, it, expect } from 'vitest'
import { shouldRestoreRoute } from './routeRestore'

const base = {
  standalone: true,
  authenticated: true,
  pathname: '/',
  search: '',
  lastRoute: '/downloads',
}

describe('shouldRestoreRoute', () => {
  it('restores in a PWA on "/" with a saved non-root route', () => {
    expect(shouldRestoreRoute(base)).toBe(true)
  })

  it('does NOT restore in a normal browser tab (the refresh-redirect bug)', () => {
    // A plain browser refresh on "/" must keep the user on "/", not redirect to
    // a previously-opened page.
    expect(shouldRestoreRoute({ ...base, standalone: false })).toBe(false)
  })

  it('does not restore when unauthenticated', () => {
    expect(shouldRestoreRoute({ ...base, authenticated: false })).toBe(false)
  })

  it('does not restore when not on "/" (a real deep link / intentional route)', () => {
    expect(shouldRestoreRoute({ ...base, pathname: '/downloads' })).toBe(false)
  })

  it('does not hijack an active player deep-link', () => {
    expect(shouldRestoreRoute({ ...base, search: '?play=abc123' })).toBe(false)
  })

  it('does not restore when the saved route is empty or root', () => {
    expect(shouldRestoreRoute({ ...base, lastRoute: '' })).toBe(false)
    expect(shouldRestoreRoute({ ...base, lastRoute: '/' })).toBe(false)
  })
})
