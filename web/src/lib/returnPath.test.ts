import { beforeEach, describe, expect, it } from 'vitest'
import { fromLocation, isResumablePath, rememberReturnPath, takeReturnPath } from './returnPath'

beforeEach(() => sessionStorage.clear())

describe('returnPath', () => {
  it('only resumes the pairing page', () => {
    expect(isResumablePath('/link')).toBe(true)
    expect(isResumablePath('/link?code=ABCD-EFGH')).toBe(true)
    expect(isResumablePath('/linkx')).toBe(false)
    expect(isResumablePath('https://evil.example/link')).toBe(false)
    expect(isResumablePath('//evil.example/link')).toBe(false)
  })

  it('remembers once and falls back afterwards', () => {
    rememberReturnPath('/link?code=ABCD-EFGH')
    expect(takeReturnPath('/')).toBe('/link?code=ABCD-EFGH')
    expect(takeReturnPath('/')).toBe('/')
  })

  it('ignores non-resumable paths and tampered storage', () => {
    rememberReturnPath('/settings')
    expect(takeReturnPath('/home')).toBe('/home')
    sessionStorage.setItem('jackui.returnPath', 'https://evil.example')
    expect(takeReturnPath('/')).toBe('/')
  })

  it('rebuilds pathname + search from router state', () => {
    expect(fromLocation({ from: { pathname: '/link', search: '?code=X' } })).toBe('/link?code=X')
    expect(fromLocation({ from: { pathname: '/library' } })).toBe('/library')
    expect(fromLocation(null)).toBe('/')
    expect(fromLocation({})).toBe('/')
  })
})
