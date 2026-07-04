import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { ChevronRight, Home } from 'lucide-react'
import { useIsMobile } from '../../lib/useMediaQuery'

export function Breadcrumbs({
  mountName,
  path,
  onNavigate,
}: {
  readonly mountName: string
  readonly path: string
  readonly onNavigate: (p: string) => void
}) {
  const { t } = useTranslation()
  const segments = useMemo(() => (path === '' ? [] : path.split('/')), [path])
  const isMobile = useIsMobile()
  // No mobile, paths profundos poluem a barra. Colapsa pra Home › … › atual
  // (o … sobe um nível). No desktop mostra o caminho inteiro.
  const collapsed = isMobile && segments.length > 2
  const shown = collapsed ? segments.slice(-1) : segments

  return (
    <nav className="flex items-center gap-1 text-sm text-text-primary flex-wrap min-w-0">
      <button
        onClick={() => onNavigate('')}
        className="flex items-center gap-1 hover:text-green-400 transition-colors min-w-0"
      >
        <Home className="w-4 h-4 flex-shrink-0" />
        {/* No mobile o dropdown de mount já mostra o nome — exibir de novo aqui
            duplicava o texto e estourava a linha por cima dos botões. */}
        <span className="truncate hidden md:inline">{mountName}</span>
      </button>
      {collapsed && (
        <span className="flex items-center gap-1 flex-shrink-0">
          <ChevronRight className="w-4 h-4 text-text-muted" />
          <button
            onClick={() => onNavigate(segments.slice(0, -1).join('/'))}
            title={t('local.browser.upLevel')}
            className="px-1 hover:text-green-400 transition-colors"
          >
            …
          </button>
        </span>
      )}
      {shown.map((seg, i) => {
        const idx = collapsed ? segments.length - 1 : i
        const target = segments.slice(0, idx + 1).join('/')
        const isLast = idx === segments.length - 1
        return (
          <span key={target} className="flex items-center gap-1 min-w-0">
            <ChevronRight className="w-4 h-4 text-text-muted flex-shrink-0" />
            <button
              onClick={() => onNavigate(target)}
              className={`hover:text-green-400 transition-colors truncate max-w-[55vw] sm:max-w-none ${
                isLast ? 'text-text-primary font-medium' : ''
              }`}
            >
              {seg}
            </button>
          </span>
        )
      })}
    </nav>
  )
}
