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
      // Baseline ratchet — climb toward 90%. Thresholds sit at the measured
      // baseline, rounded DOWN to whole percents (2026-11: lines 32.79,
      // statements 31.3, branches 25.45, functions 24.61) — never above
      // measured. Re-measure with `make test-coverage` before bumping.
      thresholds: {
        lines: 32,
        statements: 31,
        branches: 25,
        functions: 24,
      },
    },
  },
})
