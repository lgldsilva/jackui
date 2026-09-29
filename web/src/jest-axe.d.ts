// Types for jest-axe's toHaveNoViolations matcher on Vitest's expect.
// jest-axe declares its types against Jest's global namespace; here the
// matcher is registered on Vitest's Assertion interface.
import 'vitest'

declare module 'vitest' {
  interface Assertion<T> {
    toHaveNoViolations(): void
  }
  interface AsymmetricMatchersContaining {
    toHaveNoViolations(): void
  }
}
