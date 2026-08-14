import { spawnSync } from 'node:child_process'
import { rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

// Node 24+ exposes localStorage only when a backing file is supplied. The
// frontend tests exercise the browser storage API, so give each invocation an
// isolated temporary file and keep the command portable across macOS/Linux/CI.
const storageFile = join(tmpdir(), `jackui-vitest-${process.pid}.localstorage`)
const nodeOptions = [process.env.NODE_OPTIONS, `--localstorage-file=${storageFile}`]
  .filter(Boolean)
  .join(' ')
const forwardedArgs = process.argv.slice(2)
const watch = forwardedArgs.includes('--watch') || forwardedArgs.includes('-w')
const command = watch ? 'watch' : 'run'
const result = spawnSync(
  process.execPath,
  [`--localstorage-file=${storageFile}`, 'node_modules/vitest/vitest.mjs', command, ...forwardedArgs.filter(arg => arg !== '--watch' && arg !== '-w')],
  { stdio: 'inherit', env: { ...process.env, NODE_OPTIONS: nodeOptions } },
)

try {
  rmSync(storageFile, { force: true })
} catch {
  // A locked temp file is harmless and will be reclaimed by the OS.
}

process.exit(result.status ?? 1)
