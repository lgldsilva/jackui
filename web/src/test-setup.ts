// Extende os matchers do Vitest com os do jest-dom (toBeVisible,
// toHaveFocus, toHaveAttribute, etc.)
import '@testing-library/jest-dom/vitest'

// Matcher toHaveNoViolations do jest-axe (tipos em src/jest-axe.d.ts)
import { expect, vi } from 'vitest'
import { toHaveNoViolations } from 'jest-axe'
expect.extend(toHaveNoViolations)

// jsdom não tem IntersectionObserver. Vitest 4 exige implementação com
// `function`/`class` quando o mock é usado via `new` (arrow throws
// "is not a constructor").
vi.stubGlobal(
  'IntersectionObserver',
  class {
    observe() { /* no-op: jsdom has no IntersectionObserver */ }
    unobserve() { /* no-op: jsdom has no IntersectionObserver */ }
    disconnect() { /* no-op: jsdom has no IntersectionObserver */ }
    takeRecords() { return [] }
  },
)

// Inicializa o i18n para que useTranslation() resolva chaves corretamente
import './lib/i18n'

