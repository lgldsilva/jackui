import { describe, expect, it } from 'vitest'
import { hasSecondaryRoute, isRouteActive, PRIMARY_LINKS, SECONDARY_GROUPS } from './NavHeader'

describe('navigation information architecture', () => {
  it('keeps high-frequency mobile destinations in the primary rail', () => {
    const primary = PRIMARY_LINKS.map(link => link.to)
    expect(primary).toEqual(expect.arrayContaining(['/', '/search', '/library', '/downloads', '/local', '/settings']))
    expect(hasSecondaryRoute('/downloads')).toBe(false)
    expect(hasSecondaryRoute('/local')).toBe(false)
    expect(hasSecondaryRoute('/settings')).toBe(false)
  })

  it('groups less frequent destinations without breaking nested routes', () => {
    expect(SECONDARY_GROUPS.flatMap(group => group.links.map(link => link.to))).toEqual(
      expect.arrayContaining(['/playlists', '/watchlist', '/favorites', '/history', '/stats']),
    )
    expect(hasSecondaryRoute('/playlists/42')).toBe(true)
    expect(isRouteActive('/library', '/library')).toBe(true)
    expect(isRouteActive('/', '/search')).toBe(false)
  })
})
