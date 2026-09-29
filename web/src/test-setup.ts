// Extends Vitest's matchers with jest-dom's (toBeVisible,
// toHaveFocus, toHaveAttribute, etc.)
import '@testing-library/jest-dom/vitest'

// jest-axe's toHaveNoViolations matcher (types in src/jest-axe.d.ts)
import { expect, vi } from 'vitest'
import { toHaveNoViolations } from 'jest-axe'
expect.extend(toHaveNoViolations)

// jsdom has no IntersectionObserver. Vitest 4 requires an implementation with
// `function`/`class` when the mock is used via `new` (arrow throws
// "is not a constructor").
vi.stubGlobal(
  'IntersectionObserver',
  class {
    observe() { return }
    unobserve() { return }
    disconnect() { return }
    takeRecords() { return [] }
  },
)

// Initializes i18n so useTranslation() resolves keys correctly
import './lib/i18n'

