import { describe, it, expect, vi, beforeEach } from 'vitest'
import { playHref, searchHref, newTabProps, openInNewTab } from './cardNav'

describe('playHref', () => {
  it('builds a bare deep-link from a hash', () => {
    expect(playHref('abc123')).toBe('/?play=abc123')
  })
  it('includes file index and seek when meaningful', () => {
    expect(playHref('abc', 3, 120)).toBe('/?play=abc&f=3&t=120')
  })
  it('omits f=0 and t=0 (treated as unset)', () => {
    expect(playHref('abc', 0, 0)).toBe('/?play=abc')
  })
  it('works with a local pseudo-hash', () => {
    expect(playHref('local-Zm9v')).toBe('/?play=local-Zm9v')
  })
})

describe('searchHref', () => {
  it('encodes the query', () => {
    expect(searchHref('wind rose')).toBe('/?q=wind%20rose')
  })
})

describe('newTabProps', () => {
  beforeEach(() => {
    vi.stubGlobal('open', vi.fn())
  })

  const ev = (over: Partial<MouseEvent> = {}) => ({
    ctrlKey: false, metaKey: false, shiftKey: false, button: 0,
    preventDefault: vi.fn(), ...over,
  }) as unknown as React.MouseEvent

  it('plain click runs the in-app action, not a new tab', () => {
    const onActivate = vi.fn()
    newTabProps('/?play=x', onActivate).onClick(ev())
    expect(onActivate).toHaveBeenCalledOnce()
    expect(globalThis.open).not.toHaveBeenCalled()
  })

  it('ctrl/cmd click opens a new tab and skips the action', () => {
    const onActivate = vi.fn()
    newTabProps('/?play=x', onActivate).onClick(ev({ metaKey: true }))
    expect(onActivate).not.toHaveBeenCalled()
    expect(globalThis.open).toHaveBeenCalledWith('/?play=x', '_blank', 'noopener')
  })

  it('middle click (aux) opens a new tab', () => {
    const onActivate = vi.fn()
    newTabProps('/?play=x', onActivate).onAuxClick(ev({ button: 1 }))
    expect(globalThis.open).toHaveBeenCalledWith('/?play=x', '_blank', 'noopener')
    expect(onActivate).not.toHaveBeenCalled()
  })

  it('non-middle aux click does nothing', () => {
    newTabProps('/?play=x', vi.fn()).onAuxClick(ev({ button: 2 }))
    expect(globalThis.open).not.toHaveBeenCalled()
  })
})

describe('openInNewTab', () => {
  it('delegates to window.open with noopener', () => {
    const open = vi.fn()
    vi.stubGlobal('open', open)
    openInNewTab('/x')
    expect(open).toHaveBeenCalledWith('/x', '_blank', 'noopener')
  })
})
