import { useTranslation } from 'react-i18next'
import { EQ_FREQUENCIES, EQ_MIN_DB, EQ_MAX_DB, type WebAudioGraph } from './useWebAudioGraph'

function formatFreq(hz: number): string {
  return hz >= 1000 ? `${hz / 1000}k` : `${hz}`
}

// Equalizer renders the 10 vertical band sliders for the Web Audio graph. Each
// slider is an <input type=range> (implicit role=slider) with an aria-label
// (frequency) + aria-valuetext (gain in dB) so it's screen-reader friendly,
// matching the player's existing a11y conventions.
export function Equalizer({ graph }: { readonly graph: WebAudioGraph }) {
  const { t } = useTranslation()
  return (
    <section className="rounded-lg bg-surface-2 p-3" aria-label={t('player.eq.title')}>
      <div className="mb-2 flex items-center justify-between">
        <span className="text-sm font-medium text-text">{t('player.eq.title')}</span>
        <button
          type="button"
          onClick={graph.resetBands}
          className="text-xs text-text-muted hover:text-text"
        >
          {t('player.eq.reset')}
        </button>
      </div>
      <div className="flex items-end justify-between gap-1">
        {EQ_FREQUENCIES.map((hz, i) => {
          const db = graph.bandGains[i] ?? 0
          return (
            <label key={hz} className="flex flex-1 flex-col items-center gap-1 text-[10px] text-text-muted">
              <input
                type="range"
                min={EQ_MIN_DB}
                max={EQ_MAX_DB}
                step={1}
                value={db}
                onChange={(e) => graph.setBandGain(i, Number(e.target.value))}
                aria-label={t('player.eq.bandLabel', { freq: `${formatFreq(hz)}Hz` })}
                aria-valuetext={t('player.eq.valueText', { db })}
                className="h-24 cursor-pointer accent-primary"
                style={{ writingMode: 'vertical-lr', direction: 'rtl', width: 16 }}
              />
              <span>{formatFreq(hz)}</span>
            </label>
          )
        })}
      </div>
    </section>
  )
}
