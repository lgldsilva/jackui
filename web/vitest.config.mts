import { defineConfig } from 'vitest/config'

// Config dedicada de testes (separada do vite.config, que é ESM-only e quebra o
// loader CommonJS do vitest). Testes de funções puras rodam em node; testes de
// componente usam jsdom com @testing-library/react.
export default defineConfig({
  test: {
    include: ['src/**/*.test.{ts,tsx}'],
    environment: 'jsdom',
    // Node's origin-backed localStorage file is SQLite and cannot be shared by
    // Vitest workers. A single worker keeps the browser-state tests reliable;
    // the suite is small enough that this remains fast (under three seconds).
    pool: 'threads',
    poolOptions: {
      threads: {
        singleThread: true,
      },
    },
    setupFiles: ['src/test-setup.ts'],
  },
})
