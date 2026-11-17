import { describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { StrictMode, useState, type ReactNode } from 'react'
import { useLatestRequest } from './useLatestRequest'
import { deferred, type Deferred } from './deferred.testutil'

const strict = ({ children }: { children: ReactNode }) => <StrictMode>{children}</StrictMode>

// A consumer shaped like the pages: load(key) awaits a fetch and applies it
// only when it is still the newest request.
function useResource(fetch: (key: string) => Promise<string>) {
  const req = useLatestRequest()
  const [data, setData] = useState<string | null>(null)
  const load = async (key: string) => {
    const isLatest = req.begin()
    const value = await fetch(key)
    if (isLatest()) setData(value)
  }
  return { data, load, invalidate: req.invalidate }
}

function setup(wrapper?: typeof strict) {
  const pending = new Map<string, Deferred<string>>()
  const fetch = (key: string) => {
    const d = deferred<string>()
    pending.set(key, d)
    return d.promise
  }
  const hook = renderHook(() => useResource(fetch), { wrapper })
  const settle = (key: string) => act(async () => { pending.get(key)?.resolve(`data:${key}`) })
  return { ...hook, settle }
}

describe('useLatestRequest', () => {
  it('drops a response that arrives after a newer request was started', async () => {
    const { result, settle } = setup()
    act(() => { void result.current.load('a') })
    act(() => { void result.current.load('b') })
    await settle('b')
    await settle('a')
    expect(result.current.data).toBe('data:b')
  })

  it('applies responses that arrive in order', async () => {
    const { result, settle } = setup()
    act(() => { void result.current.load('a') })
    await settle('a')
    expect(result.current.data).toBe('data:a')
    act(() => { void result.current.load('b') })
    await settle('b')
    expect(result.current.data).toBe('data:b')
  })

  it('drops an in-flight response after invalidate()', async () => {
    const { result, settle } = setup()
    act(() => { void result.current.load('a') })
    act(() => { result.current.invalidate() })
    await settle('a')
    expect(result.current.data).toBeNull()
  })

  it('reports not-latest after unmount', () => {
    const { result, unmount } = renderHook(() => useLatestRequest())
    const isLatest = result.current.begin()
    expect(isLatest()).toBe(true)
    unmount()
    expect(isLatest()).toBe(false)
  })

  it('still works after a StrictMode double mount', async () => {
    const { result, settle } = setup(strict)
    act(() => { void result.current.load('a') })
    await settle('a')
    expect(result.current.data).toBe('data:a')
  })

  it('keeps a stable identity across renders', () => {
    const { result, rerender } = renderHook(() => useLatestRequest())
    const first = result.current
    rerender()
    expect(result.current).toBe(first)
  })
})
