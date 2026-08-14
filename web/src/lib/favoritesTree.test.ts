import { describe, it, expect, vi } from 'vitest'
import { buildTree, flattenTree, importTorrentB64, buildImportMsg } from './favoritesTree'
import { streamImport } from '../api/client'
import type { FavoriteFolder, ImportResult } from '../api/client'

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return { ...actual, streamImport: vi.fn() }
})

const mockStreamImport = vi.mocked(streamImport)

const folder = (over: Partial<FavoriteFolder> & { id: number }): FavoriteFolder => ({
  name: 'f',
  parentId: null,
  hidden: false,
  position: 0,
  userId: 1,
  createdAt: '',
  ...over,
} as FavoriteFolder)

describe('buildTree', () => {
  it('organiza pastas em raízes e filhos', () => {
    const folders: FavoriteFolder[] = [
      folder({ id: 1, name: 'Raiz', position: 0 }),
      folder({ id: 2, name: 'Filha', parentId: 1, position: 1 }),
      folder({ id: 3, name: 'Outra raiz', position: 2 }),
    ]
    const tree = buildTree(folders)
    expect(tree).toHaveLength(2)
    expect(tree[0].folder.id).toBe(1)
    expect(tree[0].children[0].folder.id).toBe(2)
    expect(tree[1].folder.id).toBe(3)
  })

  it('órfãos são renderizados na raiz', () => {
    const folders: FavoriteFolder[] = [
      folder({ id: 1, name: 'Orfã', parentId: 999 }),
    ]
    const tree = buildTree(folders)
    expect(tree).toHaveLength(1)
    expect(tree[0].folder.id).toBe(1)
  })
})

describe('flattenTree', () => {
  it('achata em DFS com profundidade', () => {
    const folders: FavoriteFolder[] = [
      folder({ id: 1, name: 'Raiz' }),
      folder({ id: 2, name: 'Filha', parentId: 1 }),
    ]
    const flat = flattenTree(buildTree(folders))
    expect(flat.map(f => ({ id: f.folder.id, depth: f.depth }))).toEqual([
      { id: 1, depth: 0 },
      { id: 2, depth: 1 },
    ])
  })
})

describe('importTorrentB64', () => {
  it('converte arquivos .torrent e chama streamImport', async () => {
    const fileContent = new Uint8Array([0x64, 0x38, 0x3a, 0x61, 0x6e, 0x6e, 0x6f, 0x75, 0x6e, 0x63, 0x65])
    const file = new File([fileContent], 'test.torrent', { type: 'application/x-bittorrent' })
    mockStreamImport.mockResolvedValue({ infoHash: 'abc', name: 'Test', magnet: 'magnet:?xt=urn:btih:abc' } as ImportResult)

    const res = await importTorrentB64([file], null, -1)
    expect(res.ok).toBe(1)
    expect(res.fails).toHaveLength(0)
    expect(res.imported[0].infoHash).toBe('abc')
    expect(mockStreamImport).toHaveBeenCalledWith(expect.objectContaining({ torrentB64: expect.any(String), folderId: null }))
  })

  it('acumula falhas sem interromper o batch', async () => {
    const fileContent = new Uint8Array([0x00])
    const file = new File([fileContent], 'bad.torrent', { type: 'application/x-bittorrent' })
    mockStreamImport.mockRejectedValue(new Error('import failed'))

    const res = await importTorrentB64([file], 2, -1)
    expect(res.ok).toBe(0)
    expect(res.fails).toHaveLength(1)
    expect(res.fails[0]).toContain('import failed')
  })
})

describe('buildImportMsg', () => {
  const t = ((key: string, opts?: Record<string, unknown>) => `${key}:${JSON.stringify(opts || {})}`) as Parameters<typeof buildImportMsg>[4]

  it('monta mensagem de sucesso', () => {
    const msg = buildImportMsg(3, 0, undefined, ' · 3 enfileirados', t)
    expect(msg.kind).toBe('ok')
    expect(msg.text).toContain('3')
    expect(msg.text).toContain('3 enfileirados')
  })

  it('monta mensagem de erro com primeiro erro', () => {
    const msg = buildImportMsg(1, 2, 'foo: bar', '', t)
    expect(msg.kind).toBe('err')
    expect(msg.text).toContain('foo: bar')
  })
})
