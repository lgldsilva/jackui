import { X, Loader2, Upload, Check } from 'lucide-react'
import { Subtitle } from '../../api/client'
import { SubtitleResultsList } from './SubtitleResultsList'

// Conteúdo do seletor de legendas. Extraído do painel inline do PlayerControlsPanel
// pra ser renderizado dentro de um Sheet (bottom-sheet no mobile) — assim a lista E
// o filtro ficam acessíveis no celular, o que o painel embutido abaixo da dobra não
// permitia (a coluna do player só rola em telas grandes).
export type SubtitlePickerProps = {
  readonly handleCustomSubtitleUpload: (e: React.ChangeEvent<HTMLInputElement>) => void
  readonly customSubName: string | null
  readonly clearCustomSub: () => void
  readonly subLoading: boolean
  readonly subError: string
  readonly subResults: Subtitle[]
  readonly subActive: string | null
  readonly pickSubtitle: (s: Subtitle) => void
  readonly setSubActive: (v: string | null) => void
}

export function SubtitlePicker({
  handleCustomSubtitleUpload,
  customSubName,
  clearCustomSub,
  subLoading,
  subError,
  subResults,
  subActive,
  pickSubtitle,
  setSubActive,
}: SubtitlePickerProps) {
  return (
    <div className="flex flex-col">
      {/* Carregar legenda local */}
      <div className="mb-3 pb-3 border-b border-default/50 flex flex-col gap-2">
        <label className="inline-flex items-center gap-1.5 text-xs bg-surface-tertiary hover:bg-surface-tertiary text-text-primary px-3 py-1.5 rounded-lg cursor-pointer transition-colors border border-strong self-start">
          <Upload className="w-3.5 h-3.5" />
          <span>Carregar Legenda Local (.srt/.vtt)</span>
          <input type="file" accept=".srt,.vtt" onChange={handleCustomSubtitleUpload} className="hidden" />
        </label>
        {customSubName && (
          <div className="flex items-center gap-1.5 text-xs text-green-400 bg-green-500/10 border border-green-500/20 px-2.5 py-1.5 rounded-lg">
            <Check className="w-3.5 h-3.5 flex-shrink-0" />
            <span className="truncate flex-1">Ativa: {customSubName}</span>
            <button onClick={clearCustomSub} className="text-text-secondary hover:text-red-400 font-bold ml-1 p-0.5" title="Remover legenda">
              <X className="w-3.5 h-3.5" />
            </button>
          </div>
        )}
      </div>

      {subLoading && (
        <div className="flex items-center gap-2 text-sm text-text-secondary py-2">
          <Loader2 className="w-4 h-4 animate-spin" />
          Buscando no OpenSubtitles...
        </div>
      )}
      {subError && <p className="text-xs text-red-400 py-2">{subError}</p>}
      {!subLoading && !subError && subResults.length === 0 && (
        <p className="text-xs text-text-muted py-2">Nenhuma legenda encontrada</p>
      )}
      {subResults.length > 0 && (
        <SubtitleResultsList subResults={subResults} subActive={subActive} pickSubtitle={pickSubtitle} />
      )}
      {subActive && (
        <button
          onClick={() => setSubActive(null)}
          className="mt-2 text-xs text-text-muted hover:text-red-400 transition-colors flex items-center gap-1 self-start"
        >
          <X className="w-3 h-3" />
          Remover legenda
        </button>
      )}
    </div>
  )
}
