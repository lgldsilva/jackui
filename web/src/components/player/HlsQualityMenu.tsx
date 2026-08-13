import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Hls from 'hls.js'

export type HlsLevelInfo = { readonly index: number; readonly height: number; readonly bitrate: number }

export function levelsFromHls(hls: Hls): HlsLevelInfo[] {
  return (hls.levels ?? []).map((lv, index) => ({
    index,
    height: lv.height || 0,
    bitrate: lv.bitrate || 0,
  }))
}

export function formatHlsLevel(lv: HlsLevelInfo): string {
  if (lv.height > 0) return `${lv.height}p`
  if (lv.bitrate > 0) return `${Math.round(lv.bitrate / 1000)} kbps`
  return `#${lv.index}`
}

// Auto = -1 (hls.js ABR). startLevel 0 is the lowest rung after bitrate sort.
export function HlsQualityMenu({ hls }: { readonly hls: Hls | null }) {
  const { t } = useTranslation()
  const [levels, setLevels] = useState<HlsLevelInfo[]>([])
  const [current, setCurrent] = useState(-1)

  useEffect(() => {
    if (!hls) { setLevels([]); return }
    const sync = () => {
      setLevels(levelsFromHls(hls))
      setCurrent(hls.currentLevel)
    }
    sync()
    hls.on(Hls.Events.LEVEL_SWITCHED, sync)
    hls.on(Hls.Events.MANIFEST_PARSED, sync)
    return () => {
      hls.off(Hls.Events.LEVEL_SWITCHED, sync)
      hls.off(Hls.Events.MANIFEST_PARSED, sync)
    }
  }, [hls])

  if (!hls || levels.length < 2) return null

  return (
    <label className="absolute top-2 right-24 z-20 text-[11px] text-white">
      <span className="sr-only">{t('player.overlays.quality')}</span>
      <select
        value={current}
        onChange={e => {
          const n = Number(e.target.value)
          hls.currentLevel = n
          setCurrent(n)
        }}
        className="bg-black/55 hover:bg-black/75 rounded-md px-1.5 py-1 backdrop-blur-sm border-0"
        title={t('player.overlays.quality')}
      >
        <option value={-1}>{t('player.overlays.qualityAuto')}</option>
        {levels.map(lv => (
          <option key={lv.index} value={lv.index}>{formatHlsLevel(lv)}</option>
        ))}
      </select>
    </label>
  )
}
