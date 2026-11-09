import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import en from './en.json'
import pt from './pt.json'

// Every STATIC key passed to t('…') / i18n.t('…') must exist in BOTH locale
// files — a missing key renders the raw key ("search.copied") in the UI and
// check-i18n (which only hunts for Portuguese literals) can't see it.
// Dynamic keys (template literals, variables) are out of scope.

const SRC = join(__dirname, '..')
const KEY_CALL = /\b(?:i18n\.)?t\(\s*['"]([a-zA-Z0-9_.-]+)['"]/g

function sourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) return name === 'locales' ? [] : sourceFiles(p)
    return /\.(ts|tsx)$/.test(name) && !/\.test\.tsx?$/.test(name) ? [p] : []
  })
}

function has(dict: unknown, key: string): boolean {
  let cur: unknown = dict
  for (const part of key.split('.')) {
    if (cur === null || typeof cur !== 'object' || !(part in cur)) return false
    cur = (cur as Record<string, unknown>)[part]
  }
  return typeof cur === 'string'
}

function staticKeys(): Map<string, string> {
  const keys = new Map<string, string>()
  for (const file of sourceFiles(SRC)) {
    for (const m of readFileSync(file, 'utf8').matchAll(KEY_CALL)) {
      // Plural keys resolve to key_one/key_other.
      if (!keys.has(m[1])) keys.set(m[1], file.slice(SRC.length + 1))
    }
  }
  return keys
}

const resolves = (dict: unknown, key: string) =>
  has(dict, key) || has(dict, `${key}_one`) || has(dict, `${key}_other`)

describe('locale keys', () => {
  const keys = staticKeys()

  it('finds the static keys used by the app', () => {
    expect(keys.size).toBeGreaterThan(500)
  })

  it.each([['en', en], ['pt', pt]] as const)('every static t() key exists in %s.json', (_name, dict) => {
    const missing = [...keys].filter(([k]) => k.includes('.') && !resolves(dict, k)).map(([k, f]) => `${k} (${f})`)
    expect(missing).toEqual([])
  })
})
