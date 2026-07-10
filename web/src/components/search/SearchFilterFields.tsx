import type { ReactNode } from 'react'
import { X, Play, Sparkles, Layers } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { TabState } from '../../lib/searchTabs'

type Props = {
  // `stacked` controla a largura: no desktop os campos fluem no flex-wrap
  // (display:contents nos numéricos); no Sheet ficam full-width.
  readonly stacked: boolean
  readonly tab: TabState
  readonly onUpdate: (patch: Partial<TabState>) => void
  readonly trackers: string[]
  readonly groupSeries: boolean
  readonly onToggleGroupSeries: () => void
  readonly activeFilterCount: number
  readonly onClearFilters: () => void
}

// Campo numérico com rótulo (min-seeders/min-leechers/max-GB). Extraído para
// manter SearchFilterFields com complexidade cognitiva <=15 (CA-1.2).
function NumberFilter({ stacked, label, value, placeholder, widthClass, step, onChange }: {
  readonly stacked: boolean
  readonly label: string
  readonly value: string | number
  readonly placeholder: string
  readonly widthClass: string
  readonly step?: number
  readonly onChange: (raw: string) => void
}) {
  return (
    <label className={`flex items-center gap-1.5 bg-surface-tertiary border border-strong rounded-lg px-3 py-1.5 ${stacked ? 'w-full justify-between' : ''}`}>
      <span className="text-xs text-text-muted whitespace-nowrap">{label}</span>
      <input
        type="number" min={0} step={step}
        value={value}
        placeholder={placeholder}
        onChange={e => onChange(e.target.value)}
        className={`${widthClass} bg-transparent text-base sm:text-sm text-text-primary focus:outline-none`}
      />
    </label>
  )
}

// Botão-toggle de filtro (playable/HDR/série). `icon` já vem montado no call
// site para preservar o fill-current dependente de estado.
function FilterToggle({ stacked, active, activeClass, title, label, icon, onClick }: {
  readonly stacked: boolean
  readonly active: boolean
  readonly activeClass: string
  readonly title: string
  readonly label: string
  readonly icon: ReactNode
  readonly onClick: () => void
}) {
  return (
    <button
      onClick={onClick}
      title={title}
      className={`flex items-center gap-1.5 text-sm px-3 py-1.5 rounded-lg transition-colors border ${stacked ? 'w-full justify-center' : ''} ${
        active ? activeClass : 'bg-surface-tertiary hover:bg-surface-tertiary text-text-primary border-strong'
      }`}
    >
      {icon}
      {label}
    </button>
  )
}

// Campos de filtro compartilhados entre a barra inline (desktop) e o Sheet
// (mobile). Extraído do SearchPage (god-file): antes era a função filterFields
// que engordava o componente.
export function SearchFilterFields({
  stacked, tab, onUpdate, trackers,
  groupSeries, onToggleGroupSeries, activeFilterCount, onClearFilters,
}: Props) {
  const { t } = useTranslation()
  return (
    <>
      <input
        type="text"
        placeholder={t('search.filter_title')}
        value={tab.titleFilter}
        onChange={e => onUpdate({ titleFilter: e.target.value })}
        className={`bg-surface-tertiary border border-strong rounded-lg px-3 py-1.5 text-base sm:text-sm text-text-primary placeholder-gray-500 focus:outline-none focus:border-green-500 ${stacked ? 'w-full' : 'w-full sm:w-44'}`}
      />
      <select
        value={tab.trackerFilter}
        onChange={e => onUpdate({ trackerFilter: e.target.value })}
        className={`bg-surface-tertiary border border-strong rounded-lg px-3 py-1.5 text-base sm:text-sm text-text-primary focus:outline-none focus:border-green-500 ${stacked ? 'w-full' : ''}`}
      >
        {trackers.map(tOption => (
          <option key={tOption} value={tOption}>{tOption === 'all' ? t('search.all_servers') : tOption}</option>
        ))}
      </select>
      <div className={stacked ? 'grid grid-cols-3 gap-2' : 'contents'}>
        <NumberFilter
          stacked={stacked}
          label={t('search.filter_seeds_min')}
          value={tab.minSeeders || ''}
          placeholder="0"
          widthClass="w-12"
          onChange={raw => onUpdate({ minSeeders: Math.max(0, Number.parseInt(raw) || 0) })}
        />
        <NumberFilter
          stacked={stacked}
          label={t('search.filter_leech_min')}
          value={tab.minLeechers || ''}
          placeholder="0"
          widthClass="w-12"
          onChange={raw => onUpdate({ minLeechers: Math.max(0, Number.parseInt(raw) || 0) })}
        />
        <NumberFilter
          stacked={stacked}
          label={t('search.filter_max_gb')}
          value={tab.maxSizeGb}
          placeholder="∞"
          widthClass="w-14"
          step={0.1}
          onChange={raw => onUpdate({ maxSizeGb: raw })}
        />
      </div>
      <FilterToggle
        stacked={stacked}
        active={tab.onlyPlayable}
        activeClass="bg-purple-500/20 text-purple-700 dark:text-purple-300 border-purple-500/30"
        title={t('search.playable_title')}
        label={t('search.playable')}
        icon={<Play className={`w-3.5 h-3.5 ${tab.onlyPlayable ? 'fill-current' : ''}`} />}
        onClick={() => onUpdate({ onlyPlayable: !tab.onlyPlayable })}
      />
      <select
        value={tab.resolution}
        onChange={e => onUpdate({ resolution: e.target.value })}
        title={t('search.resolution_title')}
        className={`bg-surface-tertiary border border-strong rounded-lg px-3 py-1.5 text-base sm:text-sm text-text-primary focus:outline-none focus:border-green-500 ${stacked ? 'w-full' : ''}`}
      >
        <option value="">{t('search.resolution')}</option>
        <option value="2160p">4K (2160p)</option>
        <option value="1080p">1080p</option>
        <option value="720p">720p</option>
        <option value="480p">480p</option>
      </select>
      <select
        value={tab.codecGroup}
        onChange={e => onUpdate({ codecGroup: e.target.value })}
        title={t('search.codec_title')}
        className={`bg-surface-tertiary border border-strong rounded-lg px-3 py-1.5 text-base sm:text-sm text-text-primary focus:outline-none focus:border-green-500 ${stacked ? 'w-full' : ''}`}
      >
        <option value="">{t('search.codec')}</option>
        <option value="hevc">H.265 / HEVC</option>
        <option value="h264">H.264</option>
        <option value="av1">AV1</option>
      </select>
      <FilterToggle
        stacked={stacked}
        active={tab.hdrOnly}
        activeClass="bg-yellow-500/20 text-yellow-700 dark:text-yellow-300 border-yellow-500/30"
        title={t('search.hdr_title')}
        label={t('search.hdr')}
        icon={<Sparkles className={`w-3.5 h-3.5 ${tab.hdrOnly ? 'fill-current' : ''}`} />}
        onClick={() => onUpdate({ hdrOnly: !tab.hdrOnly })}
      />
      <FilterToggle
        stacked={stacked}
        active={groupSeries}
        activeClass="bg-green-500/20 text-green-700 dark:text-green-300 border-green-500/30"
        title={t('search.series_title')}
        label={t('search.series')}
        icon={<Layers className="w-3.5 h-3.5" />}
        onClick={onToggleGroupSeries}
      />
      {activeFilterCount > 0 && (
        <button
          onClick={onClearFilters}
          className={`text-xs text-text-muted hover:text-red-400 transition-colors flex items-center gap-1 ${stacked ? 'w-full justify-center py-2' : ''}`}
          title={t('search.clean_filters')}
        >
          <X className="w-3.5 h-3.5" />{t('search.clean')}
        </button>
      )}
    </>
  )
}
