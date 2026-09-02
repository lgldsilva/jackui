import { defineConfig } from 'vitest/config'

// Config dedicada de testes (separada do vite.config, que é ESM-only e quebra o
// loader CommonJS do vitest). Testes de funções puras rodam em node; testes de
// componente usam jsdom com @testing-library/react.
export default defineConfig({
  test: {
    include: ['src/**/*.test.{ts,tsx}'],
    environment: 'jsdom',
    // Node's origin-backed localStorage file is SQLite and cannot be shared by
    // Vitest workers. A single worker keeps the browser-state tests reliable.
    // Vitest 4 dropped poolOptions.threads.singleThread — maxWorkers: 1 is the
    // replacement. Keep isolate (default true) so vi.mock stays per-file.
    pool: 'threads',
    maxWorkers: 1,
    setupFiles: ['src/test-setup.ts'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'lcov'],
      // Vitest 4 dropped coverage.all; without include only files loaded by
      // tests are reported, which would inflate the ratchet. Scope to src so
      // uncovered modules still count (same baseline as v3).
      include: ['src/**'],
      exclude: [
        'src/**/*.test.{ts,tsx}',
        'src/test-setup.ts',
        '**/*.d.ts',
      ],
      // baseline ratchet — subir em direção a 90%. Vitest 4 V8 AST remapping
      // rebaselined numbers (2026-09: lines 27.36, statements 26.2, branches
      // 20.14, functions 20.21). Slack ~2pt below measured, same policy as v3.
      thresholds: {
        lines: 24,
        statements: 24,
        branches: 18,
        functions: 18,
      },
    },
  },
})
