// @vitest-environment node
import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

// Regression (#200): icon-192/512.png were only generated inside the Docker
// build, so `make build`, Electron and the dev server served 404 for the PWA
// manifest icons and apple-touch-icon. They are committed under public/ now.
const webDir = join(__dirname, '..')
const publicDir = join(webDir, 'public')

function referencedAssets(): string[] {
  const manifest = JSON.parse(readFileSync(join(publicDir, 'manifest.webmanifest'), 'utf8')) as { icons: { src: string }[] }
  const html = readFileSync(join(webDir, 'index.html'), 'utf8')
  const fromHtml = [...html.matchAll(/<link[^>]+rel="(?:icon|apple-touch-icon)"[^>]*href="([^"]+)"/g)].map(m => m[1])
  return [...manifest.icons.map(i => i.src), ...fromHtml]
}

describe('PWA icon assets', () => {
  it('finds the referenced icons', () => {
    expect(referencedAssets()).toEqual(expect.arrayContaining(['/icon-192.png', '/icon-512.png']))
  })
  it.each(referencedAssets())('%s exists in web/public', (src) => {
    expect(existsSync(join(publicDir, src.replace(/^\//, '')))).toBe(true)
  })
})
