// Extende os matchers do Vitest com os do jest-dom (toBeVisible,
// toHaveFocus, toHaveAttribute, etc.)
import '@testing-library/jest-dom/vitest'

// Inicializa o i18n para que useTranslation() resolva chaves corretamente
import './lib/i18n'
