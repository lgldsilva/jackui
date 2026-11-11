import { describe, expect, it, vi } from 'vitest'
import { createRejectionHandler, installUnhandledRejectionToast, shouldToastRejection } from './unhandledRejection'

describe('shouldToastRejection', () => {
  it.each([
    [null, false],
    [undefined, false],
    [{ name: 'AbortError' }, false],
    [{ name: 'CanceledError' }, false],
    [{ name: 'NotAllowedError' }, false],
    [{ code: 'ERR_CANCELED' }, false],
    [{ response: { status: 401 } }, false],
    [{ response: { status: 500 } }, true],
    [new Error('boom'), true],
    ['plain string', true],
  ])('%j → %s', (reason, want) => {
    expect(shouldToastRejection(reason)).toBe(want)
  })
})

describe('createRejectionHandler', () => {
  it('prefers the backend error message and dedupes bursts', () => {
    const notify = vi.fn()
    let t = 0
    const h = createRejectionHandler(notify, () => t)
    const err = { response: { status: 500, data: { error: 'playlist not found' } } }
    h({ reason: err })
    t = 1000
    h({ reason: err })
    expect(notify).toHaveBeenCalledTimes(1)
    expect(notify).toHaveBeenCalledWith('playlist not found')
    t = 7000
    h({ reason: err })
    expect(notify).toHaveBeenCalledTimes(2)
    h({ reason: new Error('other') })
    expect(notify).toHaveBeenLastCalledWith('other')
  })

  it('ignores filtered reasons', () => {
    const notify = vi.fn()
    createRejectionHandler(notify)({ reason: { name: 'AbortError' } })
    expect(notify).not.toHaveBeenCalled()
  })
})

describe('installUnhandledRejectionToast', () => {
  it('toasts a real unhandledrejection event and uninstalls', () => {
    const notify = vi.fn()
    const target = new EventTarget()
    const off = installUnhandledRejectionToast(notify, target as unknown as Window)
    const fire = (reason: unknown) => {
      const ev = new Event('unhandledrejection') as Event & { reason: unknown }
      ev.reason = reason
      target.dispatchEvent(ev)
    }
    fire(new Error('x'))
    expect(notify).toHaveBeenCalledWith('x')
    off()
    fire(new Error('y'))
    expect(notify).toHaveBeenCalledTimes(1)
  })
})
