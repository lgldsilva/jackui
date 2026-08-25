// Tipos do matcher toHaveNoViolations (jest-axe) para o expect do Vitest.
// jest-axe declara seus tipos contra o namespace global do Jest; aqui o
// matcher é registrado na interface Assertion do Vitest.
import 'vitest'

declare module 'vitest' {
  interface Assertion<T> {
    toHaveNoViolations(): void
  }
  interface AsymmetricMatchersContaining {
    toHaveNoViolations(): void
  }
}
