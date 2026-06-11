import { describe, it, expect } from 'vitest'
import { detectViewerKind, isViewable, isNfoLike } from './viewerKind'

describe('detectViewerKind', () => {
  it('classifies text/code files', () => {
    for (const p of ['a.txt', 'release.NFO', 'sub.srt', 'notes.md', 'config.yaml', 'main.go', 'script.sh', 'page.html', 'data.json', 'movie.en.vtt']) {
      expect(detectViewerKind(p), p).toBe('text')
    }
  })

  it('classifies images including avif and svg', () => {
    for (const p of ['cover.jpg', 'art/POSTER.PNG', 'x.webp', 'x.avif', 'x.svg', 'x.gif', 'x.bmp']) {
      expect(detectViewerKind(p), p).toBe('image')
    }
  })

  it('classifies documents and containers', () => {
    expect(detectViewerKind('doc.pdf')).toBe('pdf')
    expect(detectViewerKind('comic.cbz')).toBe('comic')
    expect(detectViewerKind('comic.CBR')).toBe('comic')
    expect(detectViewerKind('files.zip')).toBe('archive')
    expect(detectViewerKind('files.rar')).toBe('archive')
    expect(detectViewerKind('files.tar')).toBe('archive')
    expect(detectViewerKind('files.tar.gz')).toBe('archive')
    expect(detectViewerKind('files.tgz')).toBe('archive')
    expect(detectViewerKind('book.epub')).toBe('epub')
  })

  it('handles extensionless well-known basenames', () => {
    expect(detectViewerKind('README')).toBe('text')
    expect(detectViewerKind('some/dir/LICENSE')).toBe('text')
    expect(detectViewerKind('Makefile')).toBe('text')
    expect(detectViewerKind('randomfile')).toBe('unknown')
  })

  it('does not classify media or unknown binaries', () => {
    for (const p of ['movie.mkv', 'song.mp3', 'app.exe', 'data.bin', 'x.gz', 'a.', '.hidden']) {
      expect(detectViewerKind(p), p).toBe('unknown')
    }
  })

  it('uses the basename, not directory names', () => {
    expect(detectViewerKind('weird.zip/inner.mkv')).toBe('unknown')
    expect(detectViewerKind('photos.jpg/readme')).toBe('text')
  })
})

describe('isViewable / isNfoLike', () => {
  it('isViewable mirrors detectViewerKind', () => {
    expect(isViewable('a.nfo')).toBe(true)
    expect(isViewable('a.cbz')).toBe(true)
    expect(isViewable('a.mkv')).toBe(false)
  })

  it('isNfoLike matches .nfo/.diz only', () => {
    expect(isNfoLike('release.NFO')).toBe(true)
    expect(isNfoLike('file_id.diz')).toBe(true)
    expect(isNfoLike('a.txt')).toBe(false)
  })
})
