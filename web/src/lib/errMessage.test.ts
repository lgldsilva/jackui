import { describe, it, expect } from 'vitest'
import { errMessage } from './errMessage'

describe('errMessage', () => {
  it('prefers the backend {"error"} body over the generic axios message', () => {
    const axiosErr = {
      message: 'Request failed with status code 500',
      response: { data: { error: 'torrent not active' } },
    }
    expect(errMessage(axiosErr)).toBe('torrent not active')
  })

  it('falls back to err.message when there is no backend error', () => {
    expect(errMessage(new Error('boom'))).toBe('boom')
  })

  it('ignores an empty/non-string backend error and uses message', () => {
    expect(errMessage({ message: 'net down', response: { data: { error: '' } } })).toBe('net down')
    expect(errMessage({ message: 'net down', response: { data: { error: 42 } } })).toBe('net down')
  })

  it('stringifies anything else', () => {
    expect(errMessage('plain string')).toBe('plain string')
    expect(errMessage(null)).toBe('null')
    expect(errMessage(undefined)).toBe('undefined')
  })
})
