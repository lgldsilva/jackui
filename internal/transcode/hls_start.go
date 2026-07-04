package transcode

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Start/build de sessão HLS (GetOrStart, buildSession, reserva de GPU) — extraído de hls.go.
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
