// Extende os matchers do Vitest com os do jest-dom (toBeVisible,
// toHaveFocus, toHaveAttribute, etc.)
import '@testing-library/jest-dom/vitest'

// Matcher toHaveNoViolations do jest-axe (tipos em src/jest-axe.d.ts)
import { expect } from 'vitest'
import { toHaveNoViolations } from 'jest-axe'
expect.extend(toHaveNoViolations)

// Inicializa o i18n para que useTranslation() resolva chaves corretamente
import './lib/i18n'
