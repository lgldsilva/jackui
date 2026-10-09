import { cleanup, render, screen } from '@testing-library/react'
import { createRef } from 'react'
import { afterEach, beforeAll, describe, expect, it } from 'vitest'
import type { StreamProbe, TorrentInfo } from '../../api/stream-types'
import i18n from '../../lib/i18n'
import { ActiveStreamView } from './ActiveStreamView'
import { PlayerLoadingOverlay } from './PlayerOverlays'

const SLOW = 'Retrying via server transcode — source is slow'
const CODEC = 'Converting via GPU — incompatible original codec (HEVC/AV1)'
const ACTIVE = 'Transcoding active — the first frames take longer'

beforeAll(async () => {
  await i18n.changeLanguage('en-US')
})

afterEach(cleanup)

function renderOverlay(over: Partial<{
  isTranscoded: boolean
  codecIncompatFallback: boolean
  slowSourceFallback: boolean
}> = {}) {
  return render(
    <PlayerLoadingOverlay
      serverReady
      resumePosition={null}
      info={null}
      selectedFile={0}
      isTranscoded
      codecIncompatFallback={false}
      slowSourceFallback={false}
      formatTime={(s) => String(s)}
      {...over}
    />,
  )
}

describe('PlayerLoadingOverlay transcode hint', () => {
  it.each([
    { codec: false, slow: true, text: SLOW, absent: CODEC },
    { codec: true, slow: false, text: CODEC, absent: SLOW },
    { codec: false, slow: false, text: ACTIVE, absent: SLOW },
    // Probe-backed codec evidence wins when both flags are set.
    { codec: true, slow: true, text: CODEC, absent: SLOW },
  ])('shows "$text"', ({ codec, slow, text, absent }) => {
    renderOverlay({ codecIncompatFallback: codec, slowSourceFallback: slow })
    expect(screen.getByText(text)).toBeInTheDocument()
    expect(screen.queryByText(absent)).not.toBeInTheDocument()
  })

  it('omits the transcode line on direct play', () => {
    renderOverlay({ isTranscoded: false, codecIncompatFallback: true, slowSourceFallback: true })
    expect(screen.queryByText(CODEC)).not.toBeInTheDocument()
    expect(screen.queryByText(SLOW)).not.toBeInTheDocument()
    expect(screen.queryByText(ACTIVE)).not.toBeInTheDocument()
  })
})

function probe(needsTranscode: boolean | undefined): StreamProbe {
  return { audio: [], subtitles: [], needsTranscode }
}

const torrent: TorrentInfo = {
  infoHash: 'a'.repeat(40),
  name: 'Show',
  totalSize: 1,
  files: [],
  peers: 0,
  seeders: 0,
  downRate: 0,
  upRate: 0,
  progress: 0,
  primaryFile: 0,
}

// minimized + a single-file torrent skips the controls panel and the file
// sidebar, so the loading overlay is the only new UI this PR wires up.
function renderView(opts: {
  probe: StreamProbe | null
  transcodeFallbackAttempted: boolean
  isTranscoded: boolean
}) {
  const noop = () => {}
  render(
    <ActiveStreamView
      {...{
        subs: {},
        videoUrls: {
          streamURL: '',
          subtitleVttURL: '',
          vlcURL: '',
          iinaURL: '',
          infuseURL: '',
          directURL: '',
          isTranscoded: opts.isTranscoded,
        },
        downloads: {},
        trackOrder: { order: [], cursor: 0 },
        aggregate: {},
        hoverThumb: {},
        info: torrent,
        selectedFile: 0,
        playlist: null,
        minimized: true,
        sidebarOpen: false,
        audioMode: false,
        videoRef: createRef<HTMLVideoElement>(),
        audioRef: createRef<HTMLAudioElement>(),
        selectedFileRef: createRef<HTMLButtonElement>(),
        activeMediaRef: createRef<HTMLMediaElement>(),
        mediaToken: '',
        serverReady: true,
        videoError: false,
        currentTime: 0,
        duration: 0,
        bufferedEnd: 0,
        bufferedRanges: [],
        disableNativeAutoplay: false,
        showResumePrompt: false,
        resumePosition: null,
        transcodeFallbackAttempted: opts.transcodeFallbackAttempted,
        probe: opts.probe,
        subEnabled: false,
        showMobileOpts: false,
        playbackSpeed: 1,
        currentFile: null,
        currentEp: null,
        videoFiles: [],
        mediaFileIndices: [],
        mediaCursor: 0,
        fileFilter: '',
        fileTypeFilter: 'all',
        fileSortBySize: false,
        fileSizeDesc: false,
        activeAudioIndex: null,
        selectAudio: noop,
        seamlessAudioOn: false,
        seamlessAudioIndex: null,
        onHlsAudioCount: noop,
        forceH264: false,
        burnSubTrack: null,
        shuffle: false,
        repeat: 'none',
        audioDirectSrc: '',
        setShowResumePrompt: noop,
        setResumePosition: noop,
        setVideoError: noop,
        setShowMobileOpts: noop,
        setPlaybackSpeed: noop,
        setForceH264: noop,
        setBurnSubTrack: noop,
        setFileFilter: noop,
        setFileTypeFilter: noop,
        setFileSortBySize: noop,
        setFileSizeDesc: noop,
        setSidebarOpen: noop,
        setPreviewFileIdx: noop,
        renderVideoError: () => null,
        videoDiagnostic: () => ({}),
        onVideoError: noop,
        onTimeUpdate: noop,
        onVideoEnded: noop,
        onVideoCanPlay: noop,
        onPlaybackStarted: noop,
        onAudioTimeUpdate: noop,
        handlePrev: noop,
        handleNext: noop,
        hasPrev: false,
        hasNext: false,
        handleRequestFullscreen: noop,
        playFile: noop,
      } as unknown as Parameters<typeof ActiveStreamView>[0]}
    />,
  )
}

describe('ActiveStreamView overlay wiring', () => {
  it.each([
    // No probe: never blame the codec or the source, even after a fallback.
    { probe: null, attempted: false, text: ACTIVE, absent: SLOW },
    { probe: null, attempted: true, text: ACTIVE, absent: CODEC },
    { probe: probe(true), attempted: false, text: CODEC, absent: SLOW },
    { probe: probe(true), attempted: true, text: CODEC, absent: SLOW },
    { probe: probe(false), attempted: true, text: SLOW, absent: CODEC },
    // Probe object without needsTranscode is not codec evidence.
    { probe: probe(undefined), attempted: true, text: SLOW, absent: CODEC },
    { probe: probe(false), attempted: false, text: ACTIVE, absent: SLOW },
  ])('attempted=$attempted renders $text', ({ probe: p, attempted, text, absent }) => {
    renderView({ probe: p, transcodeFallbackAttempted: attempted, isTranscoded: true })
    expect(screen.getByText(text)).toBeInTheDocument()
    expect(screen.queryByText(absent)).not.toBeInTheDocument()
  })

  it('direct play shows no transcode line', () => {
    renderView({ probe: probe(true), transcodeFallbackAttempted: true, isTranscoded: false })
    expect(screen.queryByText(CODEC)).not.toBeInTheDocument()
    expect(screen.queryByText(SLOW)).not.toBeInTheDocument()
    expect(screen.queryByText(ACTIVE)).not.toBeInTheDocument()
  })
})
