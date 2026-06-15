import { Music2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

type Props = {
  readonly active: boolean    // Modo Música ligado (senão não renderiza nada)
  readonly stacked: boolean   // layout full-width (sheet de filtros no mobile)
  readonly showAll: boolean   // escape: mostrando TODOS os resultados
  readonly onToggle: () => void
}

/**
 * Toggle "Só música" na barra de filtros da busca. Aparece só quando o Modo
 * Música está ativo; pressed (roxo) = filtrando só áudio, clicar mostra TODOS os
 * resultados sem sair do modo música. Vive em arquivo próprio para não engordar
 * o SearchPage (god-file) nem a função filterFields (que renderiza este botão
 * como irmão dos demais filtros, via os call-sites).
 */
export function MusicSearchFilterToggle({ active, stacked, showAll, onToggle }: Props) {
  const { t } = useTranslation()
  if (!active) return null
  return (
    <button
      onClick={onToggle}
      title={t('search.music_only_hint')}
      aria-pressed={!showAll}
      className={`flex items-center gap-1.5 text-sm px-3 py-1.5 rounded-lg transition-colors border ${stacked ? 'w-full justify-center' : ''} ${
        showAll
          ? 'bg-surface-tertiary hover:bg-surface-tertiary text-text-primary border-strong'
          : 'bg-purple-500/20 text-purple-700 dark:text-purple-300 border-purple-500/30'
      }`}
    >
      <Music2 className={`w-3.5 h-3.5 ${showAll ? '' : 'fill-current'}`} />
      {showAll ? t('search.music_show_all') : t('search.music_only')}
    </button>
  )
}
