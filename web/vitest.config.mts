import { defineConfig } from 'vitest/config'

// Config dedicada de testes (separada do vite.config, que é ESM-only e quebra o
// loader CommonJS do vitest). Testes hoje são de funções puras — sem DOM.
export default defineConfig({
  test: {
    include: ['src/**/*.test.ts'],
    environment: 'node',
  },
})
