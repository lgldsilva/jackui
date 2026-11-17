import { test } from 'node:test'
import assert from 'node:assert/strict'
import { posix, win32 } from 'node:path'
import { resolveDownloadPath, sanitizeDownloadName } from './downloadName.js'

const WIN_DIR = 'C:\\Users\\me\\Downloads'
const POSIX_DIR = '/home/me/Downloads'

test('keeps an ordinary file name untouched', () => {
  assert.equal(resolveDownloadPath(POSIX_DIR, 'Movie (2024) [1080p].mkv', posix), '/home/me/Downloads/Movie (2024) [1080p].mkv')
  assert.equal(resolveDownloadPath(WIN_DIR, 'Movie (2024) [1080p].mkv', win32), 'C:\\Users\\me\\Downloads\\Movie (2024) [1080p].mkv')
})

// Regression: a torrent path component with backslashes survives the renderer's
// split('/') and used to escape the download folder on Windows.
test('backslash traversal cannot leave the folder on Windows', () => {
  const evil = '..\\..\\AppData\\Roaming\\Microsoft\\Windows\\Start Menu\\Programs\\Startup\\x.bat'
  assert.equal(resolveDownloadPath(WIN_DIR, evil, win32), 'C:\\Users\\me\\Downloads\\x.bat')
})

test('forward-slash traversal and absolute paths collapse to the base name', () => {
  assert.equal(resolveDownloadPath(POSIX_DIR, '../../.ssh/authorized_keys', posix), '/home/me/Downloads/authorized_keys')
  assert.equal(resolveDownloadPath(POSIX_DIR, '/etc/cron.d/evil', posix), '/home/me/Downloads/evil')
  assert.equal(resolveDownloadPath(WIN_DIR, 'D:\\evil\\x.exe', win32), 'C:\\Users\\me\\Downloads\\x.exe')
  assert.equal(resolveDownloadPath(WIN_DIR, '\\\\host\\share\\x.exe', win32), 'C:\\Users\\me\\Downloads\\x.exe')
})

test('empty, dot and non-string names fall back to a safe default', () => {
  for (const name of ['', '.', '..', '../', '..\\', 'a/..', ' .. ', '...', '\0', undefined, null, 42, {}]) {
    assert.equal(sanitizeDownloadName(name, win32), 'download', `name=${JSON.stringify(name)}`)
    assert.equal(resolveDownloadPath(WIN_DIR, name, win32), 'C:\\Users\\me\\Downloads\\download')
  }
  assert.equal(sanitizeDownloadName('..', posix), 'download')
})

test('strips NUL and control characters', () => {
  assert.equal(sanitizeDownloadName('a\0b\n.mkv', posix), 'ab.mkv')
})

test('neutralizes Windows-only hazards (drive/stream colon, trailing dots)', () => {
  assert.equal(sanitizeDownloadName('C:evil.bat', win32), 'C_evil.bat')
  assert.equal(sanitizeDownloadName('file.txt:stream', win32), 'file.txt_stream')
  assert.equal(sanitizeDownloadName('x.bat. . ', win32), 'x.bat')
  assert.equal(sanitizeDownloadName('CON', win32), '_CON')
  assert.equal(sanitizeDownloadName('nul.txt', win32), '_nul.txt')
  assert.equal(sanitizeDownloadName('console.log', win32), 'console.log')
  // Legal on POSIX; keep the user's file name intact there.
  assert.equal(sanitizeDownloadName('Show: Part 1?.mkv', posix), 'Show: Part 1?.mkv')
})

test('result always stays strictly inside dir', () => {
  const names = ['..', '..\\..\\x', '../../x', 'C:', 'C:\\', '/', '\\', 'a\\..\\..\\b', 'CON', 'x'.repeat(400)]
  for (const p of [posix, win32]) {
    const dir = p === win32 ? WIN_DIR : POSIX_DIR
    for (const name of names) {
      const out = resolveDownloadPath(dir, name, p)
      assert.equal(p.dirname(out), dir, `name=${name}`)
      assert.notEqual(p.basename(out), '')
    }
  }
})
