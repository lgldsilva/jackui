package transcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
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

// VODMode gates the finite-VOD (seekbar) HLS path, by client class. See
// StreamConfig.HLSVODMode. The zero value is VODOff (current/safe behaviour).
type VODMode int

const (
	VODOff   VODMode = iota // EVENT/live for everyone (no seekbar)
	VODHLSJS                // VOD for hls.js clients (non-Safari); Safari stays EVENT
	VODAll                  // VOD for everyone, including Safari native HLS
)

// ParseVODMode maps the config/env string to a VODMode (default VODOff).
func ParseVODMode(s string) VODMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "hlsjs", "hls.js":
		return VODHLSJS
	case "all", "on", "true", "1":
		return VODAll
	default:
		return VODOff
	}
}

// allows reports whether a client (nativeHLS = Safari/iOS native HLS) is
// eligible for the VOD path under this mode.
func (m VODMode) allows(nativeHLS bool) bool {
	switch m {
	case VODAll:
		return true
	case VODHLSJS:
		return !nativeHLS
	default:
		return false
	}
}

// SetVODMode sets the VOD policy (called once at wiring time from config).
func (m *HLSSessionManager) SetVODMode(mode VODMode) { m.vodMode = mode }

// shouldVOD decides whether a session serves the finite-VOD (seekbar) path.
// VOD requires a known duration (>0); EVENT/live is the last resort for
// unknown-duration streams. With a known duration it's VOD when EITHER the
// caller forces it (forceVOD — the local-file path, whose sources are complete
// and seekable, so live is simply wrong) OR the per-client policy allows it.
// Torrents pass forceVOD=false, so the global vodMode still guards the #61
// Safari seek instability on incomplete torrent sources.
func shouldVOD(durationSec float64, forceVOD bool, mode VODMode, nativeHLS bool) bool {
	return durationSec > 0 && (forceVOD || mode.allows(nativeHLS))
}

// vodReason names why a session is NOT entering VOD, for the startup log. The
// recurring production question is "why did this client get EVENT?" and the
// answer used to be invisible — vod=false could mean a failed probe OR the
// policy excluding Safari. Pure mirror of shouldVOD's conditions; only
// meaningful when shouldVOD returned false (returns "" otherwise).
func vodReason(durationSec float64, forceVOD bool, mode VODMode, nativeHLS bool) string {
	switch {
	case durationSec <= 0:
		return "no-duration"
	case forceVOD:
		return "" // forced VOD with a known duration never falls to EVENT
	case mode == VODOff:
		return "mode-off"
	case mode == VODHLSJS && nativeHLS:
		return "mode-hlsjs-native"
	default:
		return ""
	}
}

// EffectiveKey maps a raw content key to the session key actually used. When
// VOD is off the key is unchanged (one shared EVENT session per content, zero
// behaviour change). When VOD is on, VOD-eligible and non-eligible clients are
// split into distinct sessions (-vod/-evt) so a VOD session created by one
// client never serves a VOD playlist to a client that must stay on EVENT
// (the Safari #61 safeguard). Master and segment handlers must agree on this.
func (m *HLSSessionManager) EffectiveKey(rawKey string, nativeHLS bool) string {
	if m.vodMode == VODOff {
		return rawKey
	}
	if m.vodMode.allows(nativeHLS) {
		return rawKey + "-vod"
	}
	return rawKey + "-evt"
}

func (m *HLSSessionManager) cachedDuration(contentKey string) float64 {
	m.durMu.Lock()
	defer m.durMu.Unlock()
	return m.durCache[contentKey]
}

func (m *HLSSessionManager) cacheDuration(contentKey string, dur float64) {
	if dur <= 0 {
		return
	}
	m.durMu.Lock()
	defer m.durMu.Unlock()
	if m.durCache == nil {
		m.durCache = make(map[string]float64)
	}
	m.durCache[contentKey] = dur
}

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

// launch starts (or restarts) ffmpeg at segment `startSeg` and wires the exit
// watcher. Caller must hold no session lock. The watcher only marks the
// session closed if no newer launch superseded it (generation check), so a
// seek-restart doesn't look like the encoder dying for good.
func (s *HLSSession) launch(startSeg int) error {
	s.mu.Lock()
	if s.dead {
		s.mu.Unlock()
		return errSessionStopped
	}
	s.mu.Unlock()
	ffctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ffctx, s.spec.ffmpegPath, s.spec.args(startSeg)...)
	log.Printf("hls: ffmpeg %s", strings.Join(s.spec.args(startSeg), " "))
	oom := newOOMWatcher("hls/" + s.Key + " ")
	cmd.Stderr = oom
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("ffmpeg start: %w", err)
	}
	s.mu.Lock()
	// Re-check after Start: a reap may have landed in between. stop() already
	// snapshotted the OLD Cmd, so this brand-new encoder is ours to kill.
	if s.dead {
		s.mu.Unlock()
		cancel()
		_ = cmd.Process.Kill()
		return errSessionStopped
	}
	s.Cmd = cmd
	s.Cancel = cancel
	s.oomDetector = oom
	s.startSeg = startSeg
	// Relançar limpa o flag de "encoder morto": um run anterior pode ter terminado
	// (closed=true) e este o ressuscita (ex: seek pra um buraco após o ffmpeg
	// completar perto do fim). Sem isso a sessão segue marcada closed e o GC a reapa.
	s.closed = false
	s.gen++
	myGen := s.gen
	s.mu.Unlock()

	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		// Only this launch's natural exit closes the session — a later restart
		// (higher gen) means this exit was an intentional kill, not the end.
		superseded := s.gen != myGen
		if !superseded {
			s.closed = true
		}
		s.mu.Unlock()
		// CUDA-OOM recovery: ffmpeg died trying to create a hardware decoder with
		// no VRAM. Relaunch the SAME session in software decode (NVENC still
		// encodes) so playback succeeds. Only for a non-superseded HW-decode run
		// that actually failed; tryRecoverFromCUDAOOM no-ops otherwise (and only
		// downgrades once).
		if err != nil && !superseded && s.tryRecoverFromCUDAOOM(ffctx, oom, startSeg) {
			return
		}
		if err != nil && !errors.Is(ffctx.Err(), context.Canceled) && !superseded {
			log.Printf("hls: ffmpeg exited for session %s: %v", s.Key, err)
		}
	}()
	return nil
}

// tryRecoverFromCUDAOOM relaunches the session in SOFTWARE decode after ffmpeg
// failed creating a hardware decoder (CUDA_ERROR_OUT_OF_MEMORY / hwaccel init
// error). It only fires when: the run was NOT cancelled by us, the stderr shows
// the recoverable signature, the spec was using HW decode, and we haven't
// already downgraded this session. On a match it flips spec.swDecode, returns
// the held GPU slot (the HW decoder it couldn't allocate), clears `closed` and
// relaunches at the same segment. Returns true when it took over the recovery
// (caller must not log the exit as a hard failure).
func (s *HLSSession) tryRecoverFromCUDAOOM(ffctx context.Context, oom *oomWatcher, startSeg int) bool {
	if errors.Is(ffctx.Err(), context.Canceled) || oom == nil || !oom.sawOOM() {
		return false
	}
	s.mu.Lock()
	if s.dead || s.swFallbackTried || s.spec == nil || s.spec.swDecode {
		s.mu.Unlock()
		return false
	}
	s.swFallbackTried = true
	s.spec.swDecode = true
	s.mu.Unlock()

	// The HW decoder failed to allocate — give the slot back so another session
	// can use the VRAM this one no longer holds.
	s.releaseGPUSlot()
	log.Printf("hls: CUDA-OOM on session %s — retrying with software decode (NVENC still encodes)", s.Key)
	if err := s.launch(startSeg); err != nil {
		log.Printf("hls: software-decode relaunch of %s failed: %v", s.Key, err)
	}
	return true
}

// hlsForwardSeekThreshold is how many segments PAST the highest one on disk a
// request must be before we treat it as a real forward seek (and restart the
// encoder) instead of the player's normal read-ahead buffering. The sequential
// encoder reaches anything within this window on its own, so restarting for it
// just thrashes ffmpeg — killing the run that was about to produce the segment.
// Generous (~2 min) so Safari's aggressive VOD buffering never trips it.
// (Was a tiny aheadWindow=2, which made every buffer-ahead request restart the
// encoder, producing a cascade where NO segment ever finished — playback froze
// and the player bailed.)
const hlsForwardSeekThreshold = 30

// hlsRestartCooldown debounces restarts so the burst of parallel segment
// requests a seek fires (around the target) doesn't spawn competing encoders.
const hlsRestartCooldown = 3 * time.Second

// EnsureSegment makes sure an encoder is (or will soon be) producing segment
// `idx`. The segment handler calls this when `idx` isn't on disk yet. It only
// restarts the encoder for a real seek: backward (idx < startSeg — the encoder
// already passed it and won't return) or a far-forward jump (beyond the
// read-ahead window). Everything in between is normal buffering — the running
// sequential encoder will reach it, so we let the caller wait.
func (s *HLSSession) EnsureSegment(idx int) {
	if s.spec == nil || !s.spec.vod {
		return
	}
	s.mu.Lock()
	start := s.startSeg
	closed := s.closed
	s.mu.Unlock()
	// Relança quando: o encoder morreu (closed — ex: terminou de transcodificar
	// após um seek perto do fim, deixando o miolo sem segmentos); seek pra trás
	// (idx < start, o encoder sequencial já passou e não volta); ou seek pra
	// frente além da janela de read-ahead. Sem o caso `closed`, um segmento num
	// buraco deixado por seeks dá 404 pra sempre — e o Safari, em VOD, não
	// refetcha a playlist estática pra respawnar a sessão → playback congela.
	if closed || idx < start || idx > s.highestSeg()+hlsForwardSeekThreshold {
		_ = s.RestartAt(idx)
	}
}

// highestSeg returns the largest seg_NNNNN.ts index currently on disk, or -1
// when none exist. Cheap readdir; only called when a requested segment is
// missing, not on the hot path.
func (s *HLSSession) highestSeg() int {
	entries, _ := os.ReadDir(s.Dir)
	hi := -1
	for _, e := range entries {
		if n, ok := parseSegName(e.Name()); ok && n > hi {
			hi = n
		}
	}
	return hi
}

// IsVOD reports whether this session serves a finite VOD playlist (full
// seekbar) vs the incremental EVENT/live playlist. Decided once at start from
// the VOD policy + known duration; handler and encoder read this single flag.
func (s *HLSSession) IsVOD() bool { return s.spec != nil && s.spec.vod }

// ParseSegIndex is the exported form of parseSegName for handlers that need to
// map a requested segment filename back to its index.
func ParseSegIndex(name string) (int, bool) { return parseSegName(name) }

// parseSegName extracts N from "seg_00042.ts". Returns ok=false for anything
// else (index.m3u8, temp files, etc.).
func parseSegName(name string) (int, bool) {
	if !strings.HasPrefix(name, "seg_") || !strings.HasSuffix(name, ".ts") {
		return 0, false
	}
	n, err := strconv.Atoi(name[len("seg_") : len(name)-len(".ts")])
	if err != nil {
		return 0, false
	}
	return n, true
}

// RestartAt relaunches ffmpeg to begin producing at segment `seg`. Only
// meaningful in VOD mode. The decision of WHETHER to restart lives in
// EnsureSegment; this just performs it, serialised so concurrent segment
// requests can't spawn duplicate encoders. No-op when already encoding from
// `seg`. Older segments on disk are kept so backward seeks reuse them.
func (s *HLSSession) RestartAt(seg int) error {
	if s.spec == nil || !s.spec.vod {
		return nil
	}
	s.restartMu.Lock()
	defer s.restartMu.Unlock()

	s.mu.Lock()
	cur := s.startSeg
	cancel := s.Cancel
	since := time.Since(s.lastRestart)
	closed := s.closed
	dead := s.dead
	s.mu.Unlock()
	if dead {
		// Reaped/closed by the manager — the segment dir is gone. The client's
		// next master request recreates a fresh session via GetOrStart.
		return errSessionStopped
	}
	// Encoder VIVO já produzindo daqui: nada a fazer. Mas se está closed (morto),
	// precisa ressuscitar mesmo que seg == cur — os segmentos podem não existir.
	if seg == cur && !closed {
		return nil // already encoding from here
	}
	// Debounce: a single seek fires several segment requests near the target.
	// The first restart wins; the rest (different seg numbers) are absorbed so
	// they don't kill the just-launched encoder. A genuine later seek (after the
	// cooldown) still restarts. Um encoder MORTO (closed) ignora o cooldown —
	// senão o playback fica 404 até o cooldown vencer.
	if since < hlsRestartCooldown && !closed {
		return nil
	}

	log.Printf("hls: seek-restart session %s from seg %d → %d (closed=%v)", s.Key, cur, seg, closed)
	if cancel != nil {
		cancel() // kill the current ffmpeg; gen bump in launch() guards the watcher
	}
	s.mu.Lock()
	s.lastRestart = time.Now()
	s.mu.Unlock()
	return s.launch(seg)
}

// GetOrStart returns an existing session keyed by opts.Key or starts a new one.
// On new-session, ffmpeg begins encoding immediately; the caller should poll
// for index.m3u8 to appear via WaitForMaster.
//
// Ownership: GetOrStart takes opts.Source. A new session keeps it (and closes
// it on stop); when an existing session is returned or creation fails, the
// source is closed here (io.Closer sources only). Callers must NOT close it.
func (m *HLSSessionManager) GetOrStart(ctx context.Context, opts HLSStartOpts) (*HLSSession, error) {
	// effKey splits VOD-eligible from non-eligible clients (see EffectiveKey).
	effKey := m.EffectiveKey(opts.Key, opts.NativeHLS)
	for {
		m.mu.Lock()
		if s, ok := m.sess[effKey]; ok {
			s.mu.Lock()
			s.LastAccess = time.Now()
			s.mu.Unlock()
			m.mu.Unlock()
			// The session already owns ITS source; this caller's copy would leak.
			closeIfCloser(opts.Source)
			return s, nil
		}
		ch, inFlight := m.starting[effKey]
		if !inFlight {
			ch = make(chan struct{})
			m.starting[effKey] = ch
			m.mu.Unlock()
			break // we are the builder
		}
		m.mu.Unlock()
		// Another request is building this session (probe + launch can take
		// ~30s). Wait and re-check instead of spawning a duplicate encoder
		// into the same segment directory.
		select {
		case <-ch:
		case <-ctx.Done():
			closeIfCloser(opts.Source)
			return nil, ctx.Err()
		}
	}

	s, err := m.buildSession(ctx, effKey, opts)
	m.mu.Lock()
	if err == nil {
		m.sess[effKey] = s
	}
	ch := m.starting[effKey]
	delete(m.starting, effKey)
	m.mu.Unlock()
	close(ch) // wake waiters: on success they find the session; on failure one retries
	if err != nil {
		closeIfCloser(opts.Source)
		return nil, err
	}
	return s, nil
}

// resolveDuration determines the total media duration for a new session.
// Prefer a value the caller already probed (KnownDurationSec, set by the
// local-file path), then the per-content cache (a re-created session reuses
// it), and only then run the 30s seekable probe. The probe also pulls the
// moov/Cues tail into the torrent cache — but ffmpeg reads through the same
// Range-capable source, so skipping it just defers that fetch to ffmpeg, not
// a correctness loss. A non-zero result unlocks VOD; 0 means unknown (EVENT).
func (m *HLSSessionManager) resolveDuration(ctx context.Context, effKey string, opts HLSStartOpts, ffmpegPath, inputURL string) float64 {
	if opts.KnownDurationSec > 0 {
		log.Printf("hls: using known duration=%.1fs for session %s", opts.KnownDurationSec, effKey)
		return opts.KnownDurationSec
	}
	if cached := m.cachedDuration(opts.Key); cached > 0 {
		log.Printf("hls: using cached duration=%.1fs for session %s", cached, effKey)
		return cached
	}
	durationSec := probeDurationFn(ctx, ffmpegPath, inputURL)
	log.Printf("hls: probed duration=%.1fs for session %s (0 = unknown → EVENT fallback)", durationSec, effKey)
	return durationSec
}

// buildSession does the slow part of GetOrStart (loopback server, duration
// probe, ffmpeg launch) outside the manager lock. The caller holds the
// `starting` slot for effKey, so exactly one build runs per key.
func (m *HLSSessionManager) buildSession(ctx context.Context, effKey string, opts HLSStartOpts) (*HLSSession, error) {
	caps := Cached()
	if caps == nil {
		return nil, errors.New("transcode caps not probed yet")
	}

	dir := filepath.Join(m.baseDir, effKey)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir hls dir: %w", err)
	}

	encoder := caps.Preferred
	if opts.VideoCodec != "" {
		encoder = opts.VideoCodec
	}

	// Stand up an ephemeral loopback HTTP server that serves the source via
	// http.ServeContent — gives ffmpeg full Range support so it can seek to
	// `moov` atoms at end of file (the production failure mode with MP4
	// torrents that aren't faststart-encoded).
	if opts.Source == nil {
		return nil, errors.New("HLSStartOpts.Source is required (seekable input)")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("loopback listen: %w", err)
	}
	srcSize := opts.SourceSize
	sourceReader := &readSeekerContent{ReadSeeker: opts.Source}
	// If the caller didn't pass a size we discover it once at startup. This
	// path is exercised mainly by tests; production always supplies SourceSize
	// from torrent.File.Length().
	if srcSize <= 0 {
		if sz, err := sourceReader.size(); err == nil {
			srcSize = sz
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/source", func(w http.ResponseWriter, r *http.Request) {
		// Custom Range handler — see serveSource for the race condition that
		// makes http.ServeContent unsafe with a single-cursor underlying reader.
		serveSource(w, r, sourceReader, srcSize)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(listener) }()
	inputURL := fmt.Sprintf("http://%s/source", listener.Addr().String())

	durationSec := m.resolveDuration(ctx, effKey, opts, caps.FFmpegPath, inputURL)
	m.cacheDuration(opts.Key, durationSec)

	// Encoding flags live in encodeSpec.args so seek-restart can rebuild them.
	// vod=true switches on forced 4s keyframes + the handler's synthesised finite
	// playlist; vod=false keeps the proven EVENT/live path. See shouldVOD.
	mode := m.vodMode
	vod := shouldVOD(durationSec, opts.ForceVOD, mode, opts.NativeHLS)
	s := &HLSSession{
		Key:         effKey,
		Dir:         dir,
		StartedAt:   time.Now(),
		LastAccess:  time.Now(),
		DurationSec: durationSec,
		sourceSrv:   srv,
		source:      opts.Source,
		mgr:         m,
		spec: &encodeSpec{
			dir:        dir,
			inputURL:   inputURL,
			encoder:    encoder,
			ffmpegPath: caps.FFmpegPath,
			vod:        vod,
			audioOnly:  opts.AudioOnly,
			audioTrack: opts.AudioTrack,
		},
	}

	// Cap concurrent HARDWARE decoders so the GPU doesn't run out of VRAM. When
	// the spec would use HW decode but no slot is free, this downgrades the spec
	// to software decode (NVENC still encodes) so the session always starts.
	m.reserveDecodeMode(s)

	reason := ""
	if !vod {
		reason = " reason=" + vodReason(durationSec, opts.ForceVOD, mode, opts.NativeHLS)
	}
	log.Printf("hls: starting session %s (vod=%v sw_decode=%v%s)", effKey, s.spec.vod, s.spec.swDecode, reason)
	if err := s.launch(0); err != nil {
		s.releaseGPUSlot()
		_ = srv.Close()
		_ = os.RemoveAll(dir)
		return nil, err
	}

	if durationSec <= 0 && mode != VODOff {
		// Born EVENT only because the probe failed — re-probe in background so
		// a later session can be VOD. Pointless when VOD is disabled entirely.
		// the duration cache is warm for the next session (started AFTER launch:
		// a failed build tears the loopback server down). Detached from the
		// request ctx on purpose; stop() cancels it via retryCancel.
		retryCtx, cancel := context.WithCancel(context.Background())
		s.mu.Lock()
		s.retryCancel = cancel
		s.mu.Unlock()
		go m.retryDuration(retryCtx, s, opts.Key, durationRetryAttempts, durationRetryBackoff, probeDurationFn)
	}

	return s, nil
}

// reserveDecodeMode decides whether a new session decodes on the GPU or in
// software, and reserves a GPU-decode slot when it does. A spec that wouldn't
// use HW decode anyway (CPU encoder) needs no slot and is left as-is. When the
// semaphore is at its cap it first tries to RECLAIM a slot by reaping the
// oldest idle HW-decode session (a superseded play the user moved on from), and
// only if that still leaves no slot does it downgrade THIS session to software
// decode. Sets s.spec.swDecode / s.holdsGPUSlot accordingly.
func (m *HLSSessionManager) reserveDecodeMode(s *HLSSession) {
	if !s.spec.usesHWDecode() {
		return // CPU encoder — no GPU decoder to cap.
	}
	if m.gpuSem.tryAcquire() {
		s.mu.Lock()
		s.holdsGPUSlot = true
		s.mu.Unlock()
		return
	}
	// At the cap. Free the oldest idle HW-decode session (a play the user likely
	// moved on from) and retry once before falling back to software decode.
	if m.reclaimIdleGPUSlot() && m.gpuSem.tryAcquire() {
		s.mu.Lock()
		s.holdsGPUSlot = true
		s.mu.Unlock()
		return
	}
	log.Printf("hls: GPU-decode cap reached (%d in use) — session %s decodes in software (NVENC still encodes)", m.gpuSem.held(), s.Key)
	s.spec.swDecode = true
}

// reclaimIdleGPUSlot reaps the single oldest IDLE hardware-decode session to
// free a GPU-decode slot for a new play. "Idle" = no segment request for at
// least gpuReclaimIdleAfter (a much shorter window than the 5-min general idle
// reaper) so a session a viewer is actively watching is never torn out from
// under them. Returns true when it reaped one (its slot is released by stop()).
func (m *HLSSessionManager) reclaimIdleGPUSlot() bool {
	now := time.Now()
	m.mu.Lock()
	var victim *HLSSession
	var victimKey string
	var oldest time.Time
	for k, cand := range m.sess {
		cand.mu.Lock()
		idle := now.Sub(cand.LastAccess)
		holds := cand.holdsGPUSlot
		last := cand.LastAccess
		cand.mu.Unlock()
		if !holds || idle < gpuReclaimIdleAfter {
			continue
		}
		if victim == nil || last.Before(oldest) {
			victim, victimKey, oldest = cand, k, last
		}
	}
	if victim != nil {
		delete(m.sess, victimKey)
	}
	m.mu.Unlock()
	if victim == nil {
		return false
	}
	log.Printf("hls: reclaiming GPU slot from idle session %s (idle=%s) for a new play", victimKey, now.Sub(oldest))
	victim.stop() // releases its GPU slot
	return true
}

// gpuReclaimIdleAfter is how long a HW-decode session must have gone without a
// segment request before a NEW play may reap it to reclaim its GPU slot. Short
// (vs the 5-min general idle reaper) because the goal is to free VRAM the moment
// the user starts a new file, but long enough not to disturb a session whose
// player merely paused its segment loop after pre-buffering.
const gpuReclaimIdleAfter = 20 * time.Second

// releaseGPUSlot returns this session's GPU-decode slot to the semaphore, if it
// held one. Idempotent: clears holdsGPUSlot so a double stop()/downgrade can't
// under-count the semaphore. Safe with a nil manager.
func (s *HLSSession) releaseGPUSlot() {
	s.mu.Lock()
	held := s.holdsGPUSlot
	s.holdsGPUSlot = false
	mgr := s.mgr
	s.mu.Unlock()
	if held && mgr != nil {
		mgr.gpuSem.release()
	}
}

// WaitForMaster blocks up to `timeout` waiting for the master `index.m3u8`
// to appear. ffmpeg only writes the playlist after the first segment is
// completely encoded, so the wait is bounded by `-hls_time 4` plus encoder
// startup. We bail if the session ends without writing one.
func (s *HLSSession) WaitForMaster(timeout time.Duration) error {
	path := filepath.Join(s.Dir, hlsPlaylistFile)
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()

	for {
		// Check the file FIRST so a fast encode (whole input <4s) that
		// closes between our last check and now still gets a positive
		// result.
		if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
			return nil
		}
		// Keep the session alive while a client is actively waiting.
		s.mu.Lock()
		s.LastAccess = time.Now()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			// One last check — fsync race: ffmpeg may have written the
			// playlist just before exiting.
			if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
				return nil
			}
			return errors.New("hls session ended before producing playlist")
		}

		select {
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("hls master not ready within %s", timeout)
			}
		}
	}
}

// WaitForSegment blocks for the named segment file (basename only) to exist
// and be fully written. ffmpeg's `temp_file` flag means segments appear
// atomically — once `seg_NNN.ts` is visible, it's complete.
func (s *HLSSession) WaitForSegment(name string, timeout time.Duration) (string, error) {
	// Defensively reject anything with a slash to prevent path traversal.
	if strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return "", errors.New("invalid segment name")
	}
	path := filepath.Join(s.Dir, name)
	// Qualquer requisição de segmento conta como atividade — mesmo se ainda não
	// existe (404) — senão o GC reapa a sessão durante a janela de buracos
	// pós-seek em que o player insiste pedindo segmentos ainda não gerados.
	s.mu.Lock()
	s.LastAccess = time.Now()
	s.mu.Unlock()
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()

	for {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
			s.mu.Lock()
			s.LastAccess = time.Now()
			s.mu.Unlock()
			return path, nil
		}
		if closed {
			// Last chance: ffmpeg exited; check one more time, otherwise 404.
			if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
				return path, nil
			}
			return "", errors.New("segment not found and encoder ended")
		}

		select {
		case <-ticker.C:
			if time.Now().After(deadline) {
				return "", fmt.Errorf("segment %s not ready within %s", name, timeout)
			}
		}
	}
}

// stop kills ffmpeg, shuts down the loopback source server, closes the input
// source, and removes the segment dir. Idempotent. Cmd/Cancel are snapshotted
// UNDER s.mu (launch writes them under the same lock — reading them bare was a
// data race), and `dead` is set first so a concurrent launch/RestartAt holding
// a stale session pointer can't resurrect an encoder into the removed dir.
func (s *HLSSession) stop() {
	s.mu.Lock()
	already := s.closed
	s.dead = true
	cancel, cmd, srv, src := s.Cancel, s.Cmd, s.sourceSrv, s.source
	retryCancel := s.retryCancel
	s.mu.Unlock()
	if retryCancel != nil {
		retryCancel() // halt the background duration re-probe, if any
	}
	if cancel != nil {
		cancel()
	}
	if !already && cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if srv != nil {
		shutdownCtx, cancelSrv := context.WithTimeout(context.Background(), 2*time.Second)
		_ = srv.Shutdown(shutdownCtx)
		cancelSrv()
	}
	closeIfCloser(src)
	s.releaseGPUSlot() // return the GPU-decode slot (if held) to the semaphore
	_ = os.RemoveAll(s.Dir)
}

// closeIfCloser closes a source that supports it (torrent FileReader,
// *os.File). Sources owned elsewhere (e.g. localstream.Session, returned to
// its registry) simply don't implement io.Closer and pass through.
func closeIfCloser(src io.ReadSeeker) {
	if c, ok := src.(io.Closer); ok && c != nil {
		_ = c.Close()
	}
}

// Peek returns an existing session without starting one. Used by the
// segment handler which must NOT race the playlist handler into creating
// a duplicate ffmpeg. Returns an error when the session isn't tracked.
func (m *HLSSessionManager) Peek(key string) (*HLSSession, error) {
	if m == nil {
		return nil, errors.New("nil manager")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sess[key]
	if !ok {
		return nil, errors.New("session not found")
	}
	s.mu.Lock()
	s.LastAccess = time.Now()
	s.mu.Unlock()
	return s, nil
}

// Close terminates the session immediately. Called by handlers when the
// underlying torrent is dropped or the user explicitly cancels.
func (m *HLSSessionManager) Close(key string) {
	m.mu.Lock()
	s, ok := m.sess[key]
	if ok {
		delete(m.sess, key)
	}
	m.mu.Unlock()
	if ok {
		s.stop()
	}
}

// CloseForHash para TODAS as sessões HLS de um torrent (keys "<hash>-<fileIdx>").
// Chamado quando o player fecha (Drop) pra não deixar o ffmpeg do transcode
// órfão consumindo CPU até o idle-reaper (5min). Idempotente; no-op se não houver.
func (m *HLSSessionManager) CloseForHash(hashHex string) {
	if hashHex == "" {
		return
	}
	prefix := hashHex + "-"
	m.mu.Lock()
	var stopping []*HLSSession
	for k, s := range m.sess {
		if strings.HasPrefix(k, prefix) {
			stopping = append(stopping, s)
			delete(m.sess, k)
		}
	}
	m.mu.Unlock()
	for _, s := range stopping {
		s.stop()
	}
}

// logWriter routes ffmpeg stderr lines to log.Printf with a stable prefix.
type logWriter struct {
	prefix string
	buf    []byte
}

func newLogWriter(prefix string) *logWriter { return &logWriter{prefix: prefix} }

func (w *logWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := strings.IndexByte(string(w.buf), '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
		if line != "" {
			log.Print(w.prefix, line)
		}
	}
	return len(p), nil
}

// HLSSessionSnapshot is a read-only representation of an active transcode session.
type HLSSessionSnapshot struct {
	Key           string    `json:"key"`
	Codec         string    `json:"codec"`
	SegmentsReady int       `json:"segmentsReady"`
	StartedAt     time.Time `json:"startedAt"`
	LastActivity  time.Time `json:"lastActivity"`
	Pid           int       `json:"pid"`
}

// Sessions returns all currently active transcode sessions in the manager.
func (m *HLSSessionManager) Sessions() []HLSSessionSnapshot {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	var snapshots []HLSSessionSnapshot
	for key, s := range m.sess {
		snapshots = appendSnapshotIfActive(snapshots, key, s)
	}
	return snapshots
}

func appendSnapshotIfActive(snapshots []HLSSessionSnapshot, key string, s *HLSSession) []HLSSessionSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return snapshots
	}
	snapshots = append(snapshots, HLSSessionSnapshot{
		Key:           key,
		Codec:         sessionEncoder(s),
		StartedAt:     s.StartedAt,
		LastActivity:  s.LastAccess,
		Pid:           sessionPid(s),
		SegmentsReady: sessionSegmentsReady(s),
	})
	return snapshots
}

func sessionPid(s *HLSSession) int {
	if s.Cmd != nil && s.Cmd.Process != nil {
		return s.Cmd.Process.Pid
	}
	return 0
}

func sessionEncoder(s *HLSSession) string {
	if s.spec != nil {
		return s.spec.encoder
	}
	return "cpu"
}

func sessionSegmentsReady(s *HLSSession) int {
	if s.Dir == "" {
		return 0
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return 0
	}
	var n int
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".ts") {
			n++
		}
	}
	return n
}
