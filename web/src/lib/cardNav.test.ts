import { describe, it, expect, vi, beforeEach } from 'vitest'
import { playHref, searchHref, newTabProps, openInNewTab, anchorNavProps, swallowClick } from './cardNav'

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

  it('a non-primary button click never runs the action (handled by onAuxClick)', () => {
    const onActivate = vi.fn()
    newTabProps('/?play=x', onActivate).onClick(ev({ button: 1 }))
    expect(onActivate).not.toHaveBeenCalled()
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

describe('anchorNavProps (real <a href> card)', () => {
  const ev = (over: Partial<MouseEvent> = {}) => ({
    ctrlKey: false, metaKey: false, shiftKey: false, altKey: false, button: 0,
    preventDefault: vi.fn(), ...over,
  }) as unknown as React.MouseEvent

  it('plain left-click prevents navigation and runs the in-app action', () => {
    const onActivate = vi.fn()
    const e = ev()
    anchorNavProps(onActivate).onClick(e)
    expect(e.preventDefault).toHaveBeenCalledOnce()
    expect(onActivate).toHaveBeenCalledOnce()
  })

  it('ctrl/cmd/shift/alt click falls through to the native new-tab (no preventDefault, no action)', () => {
    for (const mod of [{ ctrlKey: true }, { metaKey: true }, { shiftKey: true }, { altKey: true }]) {
      const onActivate = vi.fn()
      const e = ev(mod)
      anchorNavProps(onActivate).onClick(e)
      expect(e.preventDefault).not.toHaveBeenCalled()
      expect(onActivate).not.toHaveBeenCalled()
    }
  })

  it('middle-click falls through to native new-tab', () => {
    const onActivate = vi.fn()
    const e = ev({ button: 1 })
    anchorNavProps(onActivate).onClick(e)
    expect(e.preventDefault).not.toHaveBeenCalled()
    expect(onActivate).not.toHaveBeenCalled()
  })
})

describe('swallowClick', () => {
  it('prevents default (the card link nav) AND stops propagation', () => {
    const e = { preventDefault: vi.fn(), stopPropagation: vi.fn() } as unknown as React.MouseEvent
    swallowClick(e)
    expect(e.preventDefault).toHaveBeenCalledOnce()
    expect(e.stopPropagation).toHaveBeenCalledOnce()
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
