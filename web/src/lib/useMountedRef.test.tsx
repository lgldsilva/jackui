import { describe, expect, it } from 'vitest'
import { renderHook } from '@testing-library/react'
import { StrictMode, type ReactNode } from 'react'
import { useMountedRef } from './useMountedRef'

const strict = ({ children }: { children: ReactNode }) => <StrictMode>{children}</StrictMode>

describe('useMountedRef', () => {
  it('is true after a StrictMode double mount', () => {
    const { result } = renderHook(() => useMountedRef(), { wrapper: strict })
    expect(result.current.current).toBe(true)
  })

  it('is false after unmount', () => {
    const { result, unmount } = renderHook(() => useMountedRef())
    const ref = result.current
    unmount()
    expect(ref.current).toBe(false)
  })
})
