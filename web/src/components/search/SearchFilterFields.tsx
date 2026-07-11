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
        <label className={`flex items-center gap-1.5 bg-surface-tertiary border border-strong rounded-lg px-3 py-1.5 ${stacked ? 'w-full justify-between' : ''}`}>
          <span className="text-xs text-text-muted whitespace-nowrap">{t('search.filter_seeds_min')}</span>
          <input
            type="number" min={0}
            value={tab.minSeeders || ''}
            placeholder="0"
            onChange={e => onUpdate({ minSeeders: Math.max(0, Number.parseInt(e.target.value) || 0) })}
            className="w-12 bg-transparent text-base sm:text-sm text-text-primary focus:outline-none"
          />
        </label>
        <label className={`flex items-center gap-1.5 bg-surface-tertiary border border-strong rounded-lg px-3 py-1.5 ${stacked ? 'w-full justify-between' : ''}`}>
          <span className="text-xs text-text-muted whitespace-nowrap">{t('search.filter_leech_min')}</span>
          <input
            type="number" min={0}
            value={tab.minLeechers || ''}
            placeholder="0"
            onChange={e => onUpdate({ minLeechers: Math.max(0, Number.parseInt(e.target.value) || 0) })}
            className="w-12 bg-transparent text-base sm:text-sm text-text-primary focus:outline-none"
          />
        </label>
        <label className={`flex items-center gap-1.5 bg-surface-tertiary border border-strong rounded-lg px-3 py-1.5 ${stacked ? 'w-full justify-between' : ''}`}>
          <span className="text-xs text-text-muted whitespace-nowrap">{t('search.filter_max_gb')}</span>
          <input
            type="number" min={0} step={0.1}
            value={tab.maxSizeGb}
            placeholder="∞"
            onChange={e => onUpdate({ maxSizeGb: e.target.value })}
            className="w-14 bg-transparent text-base sm:text-sm text-text-primary focus:outline-none"
          />
        </label>
      </div>
      <button
        onClick={() => onUpdate({ onlyPlayable: !tab.onlyPlayable })}
        title={t('search.playable_title')}
        className={`flex items-center gap-1.5 text-sm px-3 py-1.5 rounded-lg transition-colors border ${stacked ? 'w-full justify-center' : ''} ${
          tab.onlyPlayable
            ? 'bg-purple-500/20 text-purple-700 dark:text-purple-300 border-purple-500/30'
            : 'bg-surface-tertiary hover:bg-surface-tertiary text-text-primary border-strong'
        }`}
      >
        <Play className={`w-3.5 h-3.5 ${tab.onlyPlayable ? 'fill-current' : ''}`} />
        {t('search.playable')}
      </button>
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
      <button
        onClick={() => onUpdate({ hdrOnly: !tab.hdrOnly })}
        title={t('search.hdr_title')}
        className={`flex items-center gap-1.5 text-sm px-3 py-1.5 rounded-lg transition-colors border ${stacked ? 'w-full justify-center' : ''} ${
          tab.hdrOnly
            ? 'bg-yellow-500/20 text-yellow-700 dark:text-yellow-300 border-yellow-500/30'
            : 'bg-surface-tertiary hover:bg-surface-tertiary text-text-primary border-strong'
        }`}
      >
        <Sparkles className={`w-3.5 h-3.5 ${tab.hdrOnly ? 'fill-current' : ''}`} />
        {t('search.hdr')}
      </button>
      <button
        onClick={onToggleGroupSeries}
        title={t('search.series_title')}
        className={`flex items-center gap-1.5 text-sm px-3 py-1.5 rounded-lg transition-colors border ${stacked ? 'w-full justify-center' : ''} ${
          groupSeries
            ? 'bg-green-500/20 text-green-700 dark:text-green-300 border-green-500/30'
            : 'bg-surface-tertiary hover:bg-surface-tertiary text-text-primary border-strong'
        }`}
      >
        <Layers className="w-3.5 h-3.5" />
        {t('search.series')}
      </button>
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
