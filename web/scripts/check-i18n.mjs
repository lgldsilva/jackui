#!/usr/bin/env node
// check-i18n.mjs — anti-regressão de i18n (sem deps).
// Varre web/src/**/*.tsx e falha (exit 1) se achar português acentuado em
// posição de UI: filhos JSX, atributos title/placeholder/aria-label, ou
// chamadas alert()/confirm(). Comentários (// e /* */) são ignorados.
//
// Uso: node scripts/check-i18n.mjs [--list]
//   --list  imprime também as linhas ok (debug); por padrão só as violações.
//
// Se algum arquivo ainda não migrado precisar passar, adicione-o à ALLOWLIST
// abaixo (caminho relativo a web/, POSIX). Mantenha a lista mínima.

import { readdirSync, readFileSync, statSync } from 'node:fs'
import { dirname, join, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = dirname(fileURLToPath(import.meta.url))
const SRC = join(__dirname, '..', 'src')
const WEB = join(__dirname, '..')

// Arquivos ainda não internados (caminho POSIX relativo a web/). Vazio = check
// 100% estrito. Mantenha o mínimo possível e documente o porquê.
const ALLOWLIST = new Set([])

const ACCENT = 'À-ÿ' // À-ÿ (Latin-1 acentuado, cobre pt-BR)
const reChild = new RegExp(`>[^<>{}]*[${ACCENT}]`)
const reAttr = new RegExp(`(?:title|placeholder|aria-label)=("|')[^"']*[${ACCENT}]`)
const reCall = new RegExp(`(?:alert|confirm)\\([^)]*[${ACCENT}]`)

function walk(dir) {
  const out = []
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    const st = statSync(p)
    if (st.isDirectory()) {
      if (name === 'locales' || name === 'node_modules') continue
      out.push(...walk(p))
    } else if (name.endsWith('.tsx') && !name.endsWith('.test.tsx')) {
      out.push(p)
    }
  }
  return out
}

// stripComments remove blocos /* */ (inclusive JSX {/* */}) mantendo estado
// entre linhas, e comentários // de fim de linha (preservando ://  de URLs).
function stripComments(lines) {
  let inBlock = false
  return lines.map((line) => {
    let out = ''
    let i = 0
    while (i < line.length) {
      if (inBlock) {
        const end = line.indexOf('*/', i)
        if (end === -1) { i = line.length; break }
        i = end + 2
        inBlock = false
        continue
      }
      const two = line.slice(i, i + 2)
      if (two === '/*') { inBlock = true; i += 2; continue }
      if (two === '//' && line[i - 1] !== ':') { break } // // até EOL (não ://)
      out += line[i]
      i += 1
    }
    return out
  })
}

// Alvos: por padrão todo o src; ou os caminhos passados (arquivos/dirs) —
// útil pra checar só o que você acabou de mexer.
const args = process.argv.slice(2).filter((a) => !a.startsWith('--'))
let targets
if (args.length === 0) {
  targets = walk(SRC)
} else {
  targets = args.flatMap((a) => {
    const st = statSync(a)
    return st.isDirectory() ? walk(a) : [a]
  })
}

let violations = 0
for (const file of targets) {
  const rel = relative(WEB, file).split(sep).join('/')
  if (ALLOWLIST.has(rel)) continue
  const raw = readFileSync(file, 'utf8').split('\n')
  const cleaned = stripComments(raw)
  cleaned.forEach((line, idx) => {
    if (reChild.test(line) || reAttr.test(line) || reCall.test(line)) {
      violations += 1
      console.log(`${rel}:${idx + 1}: ${raw[idx].trim().slice(0, 120)}`)
    }
  })
}

if (violations > 0) {
  console.error(`\ncheck-i18n: ${violations} string(s) PT hardcoded em posição de UI. Interne com t('ns.chave').`)
  process.exit(1)
}
console.log('check-i18n: OK (nenhuma string PT hardcoded em UI)')
