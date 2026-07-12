import { AlertCircle, Subtitles, Check, Cpu, Volume2, Flame } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { StreamProbe, SidecarSubtitle } from '../../api/client'
import { audioTrackTitle } from './playerFormat'

type EmbeddedTracksPanelProps = {
  readonly probe: StreamProbe
  readonly sidecars: SidecarSubtitle[]
  // Fase 8: activeAudioIndex = faixa ativa (índice absoluto do probe, null=default),
  // seamless OU legado; selectAudio bifurca (hls.audioTrack × ?audio= reload);
  // seamlessAudioOn = a troca é sem reencode (sem badge "GPU encoding").
  readonly activeAudioIndex: number | null
  readonly selectAudio: (v: number | null) => void
  readonly seamlessAudioOn: boolean
  readonly forceH264: boolean
  readonly burnSubTrack: number | null
  readonly isTranscoded: boolean
  readonly sidecarIdx: number | null
  readonly embeddedSub: number | null
  readonly clearCustomSub: () => void
  readonly setForceH264: (fn: (prev: boolean) => boolean) => void
  readonly setBurnSubTrack: (v: number | null) => void
  readonly setSidecarIdx: (v: number | null) => void
  readonly setEmbeddedSub: (v: number | null) => void
  readonly setSubActive: (v: string | null) => void
  readonly setAutoSource: (v: 'hash' | 'title' | 'embedded' | null) => void
}

// Embedded tracks panel (audio + subtitles inside the file, plus sidecar .srt
// files shipped alongside the video in the torrent).
export function EmbeddedTracksPanel({
  probe,
  sidecars,
  activeAudioIndex,
  selectAudio,
  seamlessAudioOn,
  forceH264,
  burnSubTrack,
  isTranscoded,
  sidecarIdx,
  embeddedSub,
  clearCustomSub,
  setForceH264,
  setBurnSubTrack,
  setSidecarIdx,
  setEmbeddedSub,
  setSubActive,
  setAutoSource,
}: EmbeddedTracksPanelProps) {
  const { t } = useTranslation()
  return (
    <div className="px-3 sm:px-4 py-3 border-b border-default flex flex-col gap-3">
      {/* Audio tracks — clicking a non-default triggers transcoded remux */}
      {probe.audio.length > 1 && (
        <div>
          <p className="text-xs text-text-muted mb-1.5 flex items-center gap-2">
            <Volume2 className="w-3 h-3" />
            {t('player.embeddedTracks.audioTracks', { count: probe.audio.length })}
            {/* "GPU encoding" só no caminho legado (troca por reencode/reload). No
                modo seamless (master multi-áudio) trocar de faixa não reencoda o
                vídeo — a rendition já existe — então o badge sairia enganoso. */}
            {activeAudioIndex !== null && !seamlessAudioOn && (
              <span className="text-[10px] text-purple-700 dark:text-purple-300 bg-purple-500/15 border border-purple-500/30 px-1.5 py-0.5 rounded">
                <Cpu className="w-2.5 h-2.5 inline mr-0.5" />GPU encoding
              </span>
            )}
          </p>
          <div className="flex flex-wrap gap-1">
            <button
              onClick={() => selectAudio(null)}
              className={`text-[11px] px-2 py-1 rounded border transition-colors ${
                activeAudioIndex === null
                  ? 'bg-blue-500/20 text-blue-700 dark:text-blue-300 border-blue-500/30'
                  : 'bg-surface-secondary text-text-muted border-default hover:text-text-primary'
              }`}
              title={t('player.embeddedTracks.defaultHint')}
            >
              {t('player.embeddedTracks.default')}
            </button>
            {probe.audio.map(a => (
              <button
                key={a.index}
                onClick={() => selectAudio(a.index)}
                title={audioTrackTitle(a)}
                className={`text-[11px] px-2 py-1 rounded border transition-colors ${(() => {
                  if (activeAudioIndex === a.index) return 'bg-purple-500/20 text-purple-700 dark:text-purple-300 border-purple-500/30'
                  if (a.default) return 'bg-blue-500/10 text-blue-400 border-blue-500/20 hover:bg-blue-500/20'
                  return 'bg-surface-tertiary/40 text-text-secondary border-default hover:text-text-primary'
                })()}`}
              >
                {a.language ? a.language.toUpperCase() : '??'}
                <span className="text-text-muted ml-1">{a.codec}{a.channels ? `·${a.channels}ch` : ''}</span>
                {a.default && <span className="ml-1 text-[9px]">★</span>}
              </button>
            ))}
          </div>
        </div>
      )}

      {/* Force H.264 toggle — useful for HEVC files in Chrome */}
      <div className="flex items-center justify-between gap-2 flex-wrap">
        <button
          onClick={() => setForceH264(v => !v)}
          title={t('player.embeddedTracks.forceH264Hint')}
          className={`flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-lg border transition-colors ${
            forceH264
              ? 'bg-purple-500/20 text-purple-700 dark:text-purple-300 border-purple-500/30'
              : 'bg-surface-tertiary/50 text-text-secondary border-default hover:text-text-primary'
          }`}
        >
          <Cpu className="w-3.5 h-3.5" />
          {t('player.embeddedTracks.forceH264')}
          {forceH264 && <Check className="w-3 h-3" />}
        </button>

        {/* Stream mode indicator */}
        {isTranscoded && (
          <span className="text-[11px] text-yellow-400 flex items-center gap-1">
            <AlertCircle className="w-3 h-3" />
            {t('player.embeddedTracks.transcodedSeekLimited')}
          </span>
        )}
      </div>

      {/* Sidecar subtitles (.srt files alongside the video in the torrent) */}
      {sidecars.length > 0 && (
        <div>
          <p className="text-xs text-text-muted mb-1.5 flex items-center gap-2">
            <Subtitles className="w-3 h-3" />
            {t('player.embeddedTracks.torrentSubs', { count: sidecars.length })} <span className="text-[10px] text-text-muted italic">{t('player.embeddedTracks.torrentSubsHint')}</span>
          </p>
          <div className="flex flex-wrap gap-1">
            <button
              onClick={() => {
                setSidecarIdx(null)
                clearCustomSub()
              }}
              className={`text-[11px] px-2 py-1 rounded border transition-colors ${
                sidecarIdx === null
                  ? 'bg-surface-tertiary text-text-primary border-strong'
                  : 'bg-surface-secondary text-text-muted border-default hover:text-text-primary'
              }`}
            >
              {t('player.embeddedTracks.none')}
            </button>
            {sidecars.map(s => (
              <button
                key={s.index}
                onClick={() => {
                  setSidecarIdx(s.index)
                  setEmbeddedSub(null)
                  setSubActive(null)
                  setAutoSource('embedded')
                  clearCustomSub()
                }}
                title={s.path}
                className={`text-[11px] px-2 py-1 rounded border transition-colors ${
                  sidecarIdx === s.index
                    ? 'bg-emerald-500/20 text-emerald-700 dark:text-emerald-300 border-emerald-500/30'
                    : 'bg-surface-tertiary/40 text-text-secondary border-default hover:text-text-primary'
                }`}
              >
                {(s.language || '??').toUpperCase()}
                <span className="text-text-muted ml-1">.{s.format}</span>
              </button>
            ))}
          </div>
        </div>
      )}

      {/* Embedded subtitles — pickable (text subs as track, image subs as burn-in) */}
      {probe.subtitles.length > 0 && (
        <div>
          <p className="text-xs text-text-muted mb-1.5 flex items-center gap-2">
            <Subtitles className="w-3 h-3" />
            {t('player.embeddedTracks.embeddedSubs', { count: probe.subtitles.length })}
            {burnSubTrack !== null && (
              <span className="text-[10px] text-orange-700 dark:text-orange-300 bg-orange-500/15 border border-orange-500/30 px-1.5 py-0.5 rounded">
                <Flame className="w-2.5 h-2.5 inline mr-0.5" />Burn-in
              </span>
            )}
          </p>
          {/* Seletor único (dropdown) em vez de N botões: arquivos com dezenas de
              faixas (ex.: 34 subs sem rótulo) enchiam a tela de botões. Legendas
              image-based (PGS/DVD) ficam como <option disabled> — o HLS atual roda
              -sn (sem burn-in), então selecioná-las só silenciava a legenda (#411). */}
          <select
            value={embeddedSub ?? ''}
            onChange={(e) => {
              const v = e.target.value
              clearCustomSub()
              setBurnSubTrack(null)
              if (v === '') {
                setEmbeddedSub(null)
                return
              }
              setEmbeddedSub(Number(v))
              setSubActive(null)
              setAutoSource('embedded')
            }}
            className="text-[11px] px-2 py-1.5 rounded border bg-surface-secondary text-text-primary border-default max-w-full"
          >
            <option value="">{t('player.embeddedTracks.none')}</option>
            {probe.subtitles.map((s, i) => {
              // Sem tag de língua (comum em releases tipo MeGusta com N subs sem
              // rótulo), usa o título ou um ordinal "Faixa N" p/ distinguir.
              const subLabel = s.language ? s.language.toUpperCase() : (s.title || t('player.embeddedTracks.trackN', { n: i + 1 }))
              const tags = [s.codec, s.forced ? 'FORCED' : '', s.image ? 'IMG' : ''].filter(Boolean).join(' · ')
              return (
                <option key={s.index} value={s.index} disabled={s.image}>
                  {subLabel} — {tags}{s.image ? ` (${t('player.burnUnsupported')})` : ''}
                </option>
              )
            })}
          </select>
        </div>
      )}
    </div>
  )
}
