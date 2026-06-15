import { describe, it, expect } from 'vitest'
import { bootRouteTarget } from './bootRoute'

describe('bootRouteTarget', () => {
  it('restaura a rota salva quando em "/" sem player', () => {
    expect(bootRouteTarget('/', '', '/downloads?tab=paused')).toBe('/downloads?tab=paused')
  })
  it('não age fora de "/" (deep-link direto cuida de si)', () => {
    expect(bootRouteTarget('/library', '', '/downloads')).toBeNull()
  })
  it('nunca sequestra um player deep-link (?play=)', () => {
    expect(bootRouteTarget('/', '?play=HASH&f=2', '/downloads')).toBeNull()
  })
  it('ignora rota salva vazia, raiz ou de login', () => {
    expect(bootRouteTarget('/', '', '')).toBeNull()
    expect(bootRouteTarget('/', '', '/')).toBeNull()
    expect(bootRouteTarget('/', '', '/login')).toBeNull()
  })
})
