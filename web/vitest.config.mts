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
    coverage: {
      provider: 'v8',
      reporter: ['text', 'lcov'],
      exclude: [
        'node_modules/**',
        'dist/**',
        'src/**/*.test.{ts,tsx}',
        'src/test-setup.ts',
        '**/*.d.ts',
        '*.config.{js,mjs,ts,mts}',
        'scripts/**',
      ],
      // baseline ratchet — subir em direção a 90% (baseline medido em
      // 2026-08: lines/statements 21.7, branches 75.82, functions 38.22)
      thresholds: {
        lines: 19,
        statements: 19,
        branches: 73,
        functions: 36,
      },
    },
  },
})
