package transcode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HLSSessionManager owns the lifecycle of ffmpeg-driven HLS transcoding
// sessions. One session per (info_hash, file_index, encoder options) tuple —
// concurrent viewers of the same content share a single ffmpeg + segments dir.
//
// Why HLS specifically: Safari (macOS + iOS) refuses progressive fragmented MP4
// via <video src> with chunked transfer encoding. Empirically every
// combination of -movflags, profile, level, GOP, B-frame count we tried
// produced bytes Safari rejects with MediaError.SRC_NOT_SUPPORTED before any
// frames decode. Apple's documented streaming path is HLS (.m3u8 + .ts
// segments) — `<video src="...m3u8">` is the only thing Safari treats as a
// first-class video source. Jellyfin / Plex / Emby all do this for browser
// clients. Stop trial-and-error, follow Apple's contract.
//
// Trade-off vs progressive MP4: needs disk space for ~2-segment-buffer per
// active session (~20-40 MB at 720p, ~80 MB at 1080p) and a small directory
// per session. ffmpeg writes segments to disk as it encodes; the handler
// serves them on request. Cleanup on idle keeps the footprint bounded.
type HLSSessionManager struct {
	baseDir string
	mu      sync.Mutex
	sess    map[string]*HLSSession
	// starting dedupes concurrent GetOrStart misses for the same key: the
	// first caller builds the session (probe + ffmpeg launch happen OUTSIDE
	// m.mu — they take up to 30s), later callers wait on the channel and then
	// re-check the map. Without it, two simultaneous plays of the same content
	// spawned two ffmpegs writing into the SAME segment dir, and the session
	// that lost the map insert leaked its encoder forever.
	starting map[string]chan struct{}
	vodMode  VODMode
	// durCache memoises the probed duration per content key (the raw key, shared
	// across the -vod/-evt session variants) so a re-created session on a slow
	// rclone/Drive mount enters VOD immediately instead of re-probing for 30s.
	durMu    sync.Mutex
	durCache map[string]float64

	// stopCh halts gcLoop; closed once by Stop(). stopped guards against a
	// double close / double drain (Stop is registered as a shutdown cleanup).
	stopCh  chan struct{}
	stopped bool

	// gpuSem caps concurrent HARDWARE-decode sessions so the GPU's VRAM doesn't
	// run out (CUDA_ERROR_OUT_OF_MEMORY). nil = unlimited (the single-transcode
	// common case). A session over the cap decodes in software (NVENC still
	// encodes). Sized from JACKUI_MAX_GPU_TRANSCODES at wiring time.
	gpuSem *gpuSem
}

// VODMode is defined in hls_vod.go.
// HLSSession is a single ongoing HLS transcode. Same key = same session
// (deduped across concurrent requests for the same content).
type HLSSession struct {
	Key        string
	Dir        string
	Cmd        *exec.Cmd
	Cancel     context.CancelFunc
	StartedAt  time.Time
	LastAccess time.Time
	// DurationSec is the total media duration, probed (seekably) once at
	// startup. 0 means "unknown" — the source's moov/Cues weren't reachable
	// in time, so callers must fall back to the live/EVENT playlist instead
	// of generating a finite VOD playlist.
	DurationSec float64
	// VOD mode (DurationSec > 0) bookkeeping for seek-restart. `spec` holds
	// everything needed to relaunch ffmpeg at an arbitrary segment offset;
	// `startSeg` is the -start_number of the CURRENT invocation; `gen` is a
	// generation counter so a relaunch's exit watcher doesn't mark a session
	// closed after a newer invocation replaced it.
	spec        *encodeSpec
	startSeg    int
	gen         int
	lastRestart time.Time // debounces seek-restart against bursty parallel requests
	restartMu   sync.Mutex
	mu          sync.Mutex
	closed      bool
	// dead marks a session stop()ed by the manager (idle reap / explicit
	// close). A dead session must never relaunch ffmpeg: a segment handler
	// holding a stale pointer could otherwise resurrect an encoder writing
	// into the directory stop() just removed.
	dead bool
	// source is the seekable input handed over by GetOrStart. The session owns
	// it from then on and closes it (when it is an io.Closer) in stop() — the
	// torrent FileReader used to leak on every playlist request that found the
	// session already running.
	source io.ReadSeeker
	// sourceSrv is an ephemeral HTTP loopback server that exposes the input
	// source (an io.ReadSeeker over the torrent file) so ffmpeg can fetch it
	// via Range requests. Without this, ffmpeg consumes stdin as a non-
	// seekable pipe — fatal for MP4 sources whose `moov` atom is at the END
	// of the file, since pipe input has no way to seek back to read it.
	sourceSrv *http.Server
	// retryCancel stops the background duration re-probe of an EVENT session
	// born with an unknown duration (see retryDuration). Called by stop() so a
	// reaped session never leaks the goroutine.
	retryCancel context.CancelFunc

	// mgr backlinks the owning manager so the exit watcher can release the
	// GPU-decode slot and trigger a software-decode relaunch on CUDA-OOM.
	mgr *HLSSessionManager
	// holdsGPUSlot is true while this session occupies a GPU-decode semaphore
	// slot — released exactly once in stop() (or when downgraded to software
	// decode on a CUDA-OOM recovery). Guarded by s.mu.
	holdsGPUSlot bool
	// oomDetector watches ffmpeg's stderr for a CUDA-OOM / hwaccel-init failure
	// signature; the exit watcher reads it to decide a software-decode relaunch.
	oomDetector *oomWatcher
	// swFallbackTried guards against an infinite relaunch loop: a session only
	// downgrades HW→software decode ONCE. Guarded by s.mu.
	swFallbackTried bool
}

// NewHLSManager constructs a manager rooted at baseDir/hls/. The directory
// is created on demand; existing contents (from previous server runs) are
// purged to avoid serving stale segments tied to old encoder options.
func NewHLSManager(baseDir string) (*HLSSessionManager, error) {
	root := filepath.Join(baseDir, "hls")
	if err := os.RemoveAll(root); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	// #nosec G301 -- dir de midia/cache; 0755 intencional p/ leitura pelo servidor de midia
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	m := &HLSSessionManager{
		baseDir:  root,
		sess:     make(map[string]*HLSSession),
		starting: make(map[string]chan struct{}),
		stopCh:   make(chan struct{}),
		gpuSem:   newGPUSem(gpuTranscodeLimitFromEnv()),
	}
	go m.gcLoop()
	return m, nil
}

// defaultMaxGPUTranscodes caps concurrent hardware-decode HLS sessions when
// JACKUI_MAX_GPU_TRANSCODES is unset. Picked to fit a modest GPU's VRAM (the
// production GTX 1070 with 8 GB ran out of memory around 7 concurrent CUVID
// decoders): 3 leaves headroom; the 4th+ session decodes in software.
const defaultMaxGPUTranscodes = 3

// gpuTranscodeLimitFromEnv reads JACKUI_MAX_GPU_TRANSCODES. A positive value
// caps concurrent HW-decode sessions; "0" means unlimited (opt out of the cap);
// unset/invalid uses defaultMaxGPUTranscodes.
func gpuTranscodeLimitFromEnv() int {
	v := strings.TrimSpace(os.Getenv("JACKUI_MAX_GPU_TRANSCODES"))
	if v == "" {
		return defaultMaxGPUTranscodes
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return defaultMaxGPUTranscodes
	}
	return n // 0 = unlimited
}

// SetGPUTranscodeLimit overrides the concurrent HW-decode cap (tests; or a
// future live-config path). limit <= 0 means unlimited.
func (m *HLSSessionManager) SetGPUTranscodeLimit(limit int) { m.gpuSem = newGPUSem(limit) }

// Stop reaps every live session (kills ffmpeg, closes its loopback server and
// removes its segment dir) and halts gcLoop. Called on graceful shutdown so no
// encoder is left orphaned writing into the cache. Idempotent.
func (m *HLSSessionManager) Stop() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	close(m.stopCh)
	sessions := make([]*HLSSession, 0, len(m.sess))
	for k, s := range m.sess {
		sessions = append(sessions, s)
		delete(m.sess, k)
	}
	m.mu.Unlock()
	// stop() outside m.mu: it blocks on the loopback-server shutdown and removes
	// segments — same reason gcLoop reaps outside the lock.
	for _, s := range sessions {
		s.stop()
	}
}

// errSessionStopped means the manager already reaped/closed this session —
// callers holding a stale pointer must re-enter via GetOrStart.
var errSessionStopped = errors.New("hls session stopped")

// hlsIdleReapAfter is how long a session may go without a segment request
// before it's reaped. Was 60s, but in VOD mode Safari pre-buffers aggressively
// (it knows the total duration), then STOPS requesting for a while once its
// buffer is full — at 60s the session was killed mid-playback and the next
// request (resume or seek) hit "session not active" → playback died. 5 min
// tolerates a full buffer / a paused tab without leaking ffmpeg for long.
const hlsIdleReapAfter = 5 * time.Minute

// gcLoop reaps sessions idle for more than hlsIdleReapAfter. Real users keep
// the segment loop warm; prolonged silence means tab closed / moved on. Ffmpeg
// keeps writing forever if we let it.
func (m *HLSSessionManager) gcLoop() {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-tick.C:
		}
		now := time.Now()
		var reaped []*HLSSession
		m.mu.Lock()
		for k, s := range m.sess {
			s.mu.Lock()
			idle := now.Sub(s.LastAccess)
			s.mu.Unlock()
			// NÃO reapar só por `closed`: em VOD o ffmpeg pode ter TERMINADO de
			// transcodificar (segmentos válidos no disco) e o player ainda assiste
			// — ou pode seekar pra um buraco e ressuscitar o encoder via
			// EnsureSegment. Reapa só por inatividade real; qualquer requisição de
			// segmento renova o LastAccess (ver WaitForSegment).
			if idle > hlsIdleReapAfter {
				log.Printf("hls: reaping idle session %s (idle=%s)", k, idle)
				reaped = append(reaped, s)
				delete(m.sess, k)
			}
		}
		m.mu.Unlock()
		// stop() outside m.mu: it blocks for up to 2s on the loopback-server
		// shutdown and removes GBs of segments — holding the manager lock here
		// froze every playlist/segment request meanwhile.
		for _, s := range reaped {
			s.stop()
		}
	}
}

// HLSStartOpts groups what's needed to spin up a session. The source is the
// torrent file source; the manager doesn't know about anacrolix specifically.
//
// IMPORTANT: Source MUST implement io.ReadSeeker. We expose it to ffmpeg via
// an ephemeral loopback HTTP server (one per session) so ffmpeg can issue
// Range requests and seek freely. Direct pipe-to-stdin (the previous design)
// fails on MP4 with `moov` at end of file because pipe input is non-seekable
// — ffmpeg can't walk past a multi-GB mdat box to read the metadata.
type HLSStartOpts struct {
	Key                 string        // raw content key, e.g. `${hash}-${fileIdx}`; EffectiveKey may add a mode suffix
	Source              io.ReadSeeker // seekable input — wrapped by an internal HTTP server
	SourceSize          int64         // total size hint; required when the underlying reader lies about EOF
	VideoCodec          string        // "h264_nvenc" | "libx264" | etc.
	PreserveSourceAudio bool          // when true and source audio is AAC, -c:a copy; else transcode to AAC
	// NativeHLS marks a Safari/iOS client (native HLS). Combined with the VOD
	// policy it decides whether this session uses the finite-VOD path.
	NativeHLS bool
	// KnownDurationSec lets the caller supply a duration it already probed (the
	// local-file path runs ffprobe at play time). >0 skips the in-session 30s
	// seekable probe — the rclone/Drive latency win.
	KnownDurationSec float64
	// ForceVOD opts this session into the finite-VOD (seekbar) path whenever the
	// duration is known, BYPASSING the per-client vodMode gate. Used by the
	// local-file path: a fully-downloaded file on disk/rclone is complete and
	// seekable, so EVENT/live (the last-resort path for unknown-duration
	// streams) is wrong for it — VOD is the correct default per the playback
	// premise. Torrents leave this false so the global vodMode still guards the
	// #61 Safari seek instability on (incomplete) torrent sources.
	ForceVOD bool
	// AudioOnly transcodes a pure-audio source (FLAC/OGG/Opus/ALAC/WMA/…) to an
	// AAC HLS stream with NO video map (`-vn`). The local-file path sets it for
	// codecs the target browser can't direct-play (Safari refuses FLAC/OGG/Opus),
	// since the video pipeline's unconditional `-map 0:v:0` would fail on a file
	// with no video stream.
	AudioOnly bool
	// AudioTrack é o índice ABSOLUTO da faixa de áudio a mapear no vídeo (>0 =
	// escolhida; <=0 = primeira/default). A sessão é keyed pela faixa (ver
	// hlsSessionKey) pra que trocar o áudio gere um transcode novo, não reuse o cache.
	AudioTrack int
}

// The #61 finite-VOD path (synthetic playlist + forced keyframes + seek-restart)
// gives a full seekbar but regressed HLS-transcode playback on Safari (HEVC→H.264
// sources buffered only ~1 segment then stalled). It is now gated at runtime by
// the per-manager VODMode (config JACKUI_HLS_VOD_MODE) instead of a compile-time
// const, so it can be enabled gradually — non-Safari first ("hlsjs"), then "all"
// once validated on a real Safari — and rolled back instantly to "off". The vod
// flag per session is `durationSec > 0 && vodMode.allows(nativeHLS)`. Direct-play
// (H.264) sources never use HLS and are unaffected either way.

// ffprobePathFrom derives the ffprobe binary path from the ffmpeg path so a
// custom install (e.g. /usr/local/bin/ffmpeg) finds its sibling ffprobe. Falls
// back to "ffprobe" on PATH when the ffmpeg path doesn't end in "ffmpeg".
func ffprobePathFrom(ffmpegPath string) string {
	if strings.HasSuffix(ffmpegPath, ffBinary) {
		return ffmpegPath[:len(ffmpegPath)-len(ffBinary)] + "ffprobe"
	}
	return "ffprobe"
}

// probeDurationSeekable reads the total media duration via ffprobe against the
// loopback source URL. Going through the Range-capable source server means
// ffprobe can seek to a `moov` atom at the END of an MP4 — the anacrolix
// reader blocks until that piece downloads, so this call also "pulls the tail"
// before we commit to a finite VOD playlist (the user's "require the end before
// allowing play" idea, done automatically). Returns 0 (not an error) when the
// duration can't be determined within the timeout; callers treat 0 as
// "unknown" and fall back to the EVENT/live playlist.
func probeDurationSeekable(ctx context.Context, ffmpegPath, inputURL string) float64 {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// #nosec G204 -- binario fixo/de config; valores de usuario sao operandos de -i ou inteiros; exec sem shell
	cmd := exec.CommandContext(cctx, ffprobePathFrom(ffmpegPath),
		ffHideBanner, ffLogLevel, "error",
		ffSeekable, "1", ffMultipleReq, "1",
		ffProbesize, "10M", ffAnalyzeDuration, "3M",
		"-of", "json", "-show_format",
		"-i", inputURL,
	)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	var parsed struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return 0
	}
	d, perr := strconv.ParseFloat(parsed.Format.Duration, 64)
	if perr != nil || d <= 0 {
		return 0
	}
	return d
}

// durationProbeFn matches probeDurationSeekable so tests can stub the probe.
type durationProbeFn func(ctx context.Context, ffmpegPath, inputURL string) float64

// Background re-probe schedule for sessions born EVENT because the startup
// duration probe failed/timed out. Package vars (not consts) so tests can
// shorten the backoff and stub the probe without spawning real ffprobes.
// buildSession snapshots them on the caller's goroutine and passes them to
// retryDuration BY VALUE — the goroutine must not read these vars directly or
// it races a test cleanup restoring them.
var (
	durationRetryAttempts                 = 2
	durationRetryBackoff                  = 15 * time.Second
	probeDurationFn       durationProbeFn = probeDurationSeekable
)

// retryDuration re-probes the duration of a session that was born EVENT
// because the startup probe came up empty (slow swarm: the moov/Cues tail
// wasn't downloadable within the 30s probe window). On success the value
// lands in the manager's per-content duration cache, so the NEXT session of
// the same raw key (re-play or respawn after reap) is born VOD with a seekbar.
// The CURRENT session deliberately stays EVENT: switching
// EXT-X-PLAYLIST-TYPE mid-session violates the HLS spec (RFC 8216 — the type
// is immutable for the playlist's lifetime) and players don't re-evaluate it.
//
// The probe reuses the session's live loopback inputURL — the session owns
// its Source, and opening a second reader over the same torrent file would
// fight the encoder for the single cursor (see readSeekerContent).
func (m *HLSSessionManager) retryDuration(ctx context.Context, s *HLSSession, rawKey string, attempts int, backoff time.Duration, probe durationProbeFn) {
	for attempt := 1; attempt <= attempts; attempt++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if dur := probe(ctx, s.spec.ffmpegPath, s.spec.inputURL); dur > 0 {
			m.cacheDuration(rawKey, dur)
			log.Printf("hls: background re-probe got duration=%.1fs for %s (attempt %d) — next session of this content enters VOD", dur, s.Key, attempt)
			return
		}
	}
}

// launch, tryRecoverFromCUDAOOM, hlsForwardSeekThreshold, hlsRestartCooldown,
// EnsureSegment, highestSeg, IsVOD, ParseSegIndex, parseSegName, RestartAt,
// WaitForMaster, WaitForSegment, and stop are defined in hls_session.go.
