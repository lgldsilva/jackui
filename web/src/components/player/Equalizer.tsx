import { useTranslation } from 'react-i18next'
import { EQ_FREQUENCIES, EQ_MIN_DB, EQ_MAX_DB, type WebAudioGraph } from './useWebAudioGraph'
import { EQ_PRESETS, activePresetKey } from './eqPresets'

function formatFreq(hz: number): string {
  return hz >= 1000 ? `${hz / 1000}k` : `${hz}`
}

// PresetRow: one-tap standard EQ curves (Flat/Rock/Pop/Jazz/Bass/Treble/Vocal).
// The matching preset is highlighted; editing any band makes it "custom" (none).
function PresetRow({ graph, t }: { readonly graph: WebAudioGraph; readonly t: (k: string) => string }) {
  const active = activePresetKey(graph.bandGains)
  return (
    <div className="mb-2 flex flex-wrap gap-1" aria-label={t('player.eq.presets')}>
      {EQ_PRESETS.map((p) => (
        <button
          key={p.key}
          type="button"
          onClick={() => graph.setBands(p.gains)}
          aria-pressed={active === p.key}
          className={`rounded px-2 py-0.5 text-[11px] transition-colors ${
            active === p.key
              ? 'bg-primary text-white'
              : 'bg-surface-3 text-text-muted hover:text-text'
          }`}
        >
          {t(`player.eq.preset.${p.key}`)}
        </button>
      ))}
    </div>
  )
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
      <PresetRow graph={graph} t={t} />
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
