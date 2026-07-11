// Promote/reclassify de arquivos locais (mover pra biblioteca com rename via IA)
// e upload multipart. Extraído de local.ts (#417 follow-up).
import { api } from './http'
import { localQS, withViewAs } from './local-base'

export type LocalUploadResult = { uploaded: string; path: string }

// localUpload streams a file to the destination folder via multipart/form-data.
// axios sets the multipart boundary automatically when handed a FormData; the
// auth interceptor injects the Bearer token. onProgress reports bytes for the
// progress bar; signal lets the caller cancel an in-flight transfer.
export const localUpload = async (
  mount: string,
  path: string,
  file: File,
  onProgress?: (loaded: number, total: number) => void,
  signal?: AbortSignal,
): Promise<LocalUploadResult> => {
  const form = new FormData()
  form.append('file', file)
  const { data } = await api.post<LocalUploadResult>(`/local/upload?${localQS(mount, path)}`, form, {
    onUploadProgress: (e) => onProgress?.(e.loaded, e.total ?? file.size),
    signal,
  })
  return data
}

// PromoteItemResult is the per-item outcome of a batch promote/reclassify, keyed
// by the ORIGINAL (un-scoped) source path the UI sent — so the reclassify table
// can mark each row succeeded/failed.
export type PromoteItemResult = { path: string; ok: boolean; error?: string }

export type PromoteResult = {
  moved: number
  failed: number
  errors: { path: string; error: string }[]
  results?: PromoteItemResult[]
}

export const localPromote = async (
  mount: string,
  path: string,
  targetSubdir: string,
  targetBase?: string,
  renameIA?: boolean,
  paths?: string[],
  // overrides maps a source path → user-edited target RELATIVE to the base. The
  // backend re-sanitizes each (path traversal, unsafe chars, category reuse)
  // before honouring it; an invalid override silently falls back to the IA path.
  overrides?: Record<string, string>,
): Promise<PromoteResult> => {
  const { data } = await api.post<PromoteResult>(withViewAs('/local/promote'), {
    mount, path, paths, targetSubdir, targetBase, renameIA, overrides,
  })
  return data
}

export type PromotePreviewEntry = {
  id?: number
  path?: string
  originalName: string
  cleanName: string
  targetPath: string
  kind: 'movie' | 'tv'
  year?: number
  season?: number
  episode?: number
  episodeName?: string
  // reusedFolder is set when the IA landed the item in an EXISTING destination
  // category folder (e.g. "Movies") instead of creating a near-duplicate.
  reusedFolder?: string
  error?: string
}

export const localPromotePreview = async (
  mount: string,
  path: string,
  targetSubdir: string,
  targetBase?: string,
  paths?: string[],
): Promise<{ previews: PromotePreviewEntry[] }> => {
  const { data } = await api.post<{ previews: PromotePreviewEntry[] }>(withViewAs('/local/promote/preview'), {
    mount,
    path,
    paths,
    targetSubdir,
    targetBase,
  })
  return data
}
