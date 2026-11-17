package streamer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

const (
	probeCacheMax = 2000
	// probeFailureTTL bounds how long a failed probe answers from memory. Long
	// enough to absorb the player's immediate retries (each would otherwise hold
	// a request and a 16 MB torrent read for the full 60s timeout on a starved
	// swarm), short enough that a swarm that recovers is probed again soon.
	probeFailureTTL = 30 * time.Second
)

type probeFailure struct {
	err     error
	expires time.Time
}

// probeFlight is one in-flight ffprobe shared by every caller asking for the
// same (torrent, file). It runs on a context detached from any single caller,
// so one client hanging up does not fail the others; it is cancelled only when
// the last waiter leaves.
type probeFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	res     ProbeResult
	err     error
	waiters int
	// abandoned: every waiter left before the result arrived. Whatever the
	// killed ffprobe returns afterwards is not trustworthy, so it is dropped.
	abandoned bool
}

var (
	probeCacheMu  sync.Mutex
	probeCache    = make(map[hashKey]ProbeResult)
	probeFailures = make(map[hashKey]probeFailure)
	probeFlights  = make(map[hashKey]*probeFlight)

	// Seams: tests count ffprobe invocations and step the clock.
	ffprobeRun = runFFprobe
	probeNow   = time.Now
)

// Probe runs ffprobe on the torrent file and lists audio + subtitle tracks.
// Reads at most 16 MB from the start of the file (enough for MKV/MP4 headers).
// Results are cached per (torrent, file); concurrent callers share one ffprobe,
// and a failure is remembered for probeFailureTTL.
func (s *Streamer) Probe(ctx context.Context, hash metainfo.Hash, fileIdx int) (ProbeResult, error) {
	key := hashKey{hash, fileIdx}
	probeCacheMu.Lock()
	if r, ok := probeCache[key]; ok {
		probeCacheMu.Unlock()
		return r, nil
	}
	if err := cachedProbeFailureLocked(key); err != nil {
		probeCacheMu.Unlock()
		return ProbeResult{}, err
	}
	if err := ctx.Err(); err != nil {
		probeCacheMu.Unlock()
		return ProbeResult{}, fmt.Errorf("ffprobe: %w", err)
	}
	fl := s.joinProbeFlightLocked(ctx, key)
	probeCacheMu.Unlock()
	return fl.wait(ctx, key)
}

func cachedProbeFailureLocked(key hashKey) error {
	f, ok := probeFailures[key]
	if !ok {
		return nil
	}
	if !probeNow().Before(f.expires) {
		delete(probeFailures, key)
		return nil
	}
	return f.err
}

func rememberProbeFailureLocked(key hashKey, err error) {
	if len(probeFailures) >= probeCacheMax {
		now := probeNow()
		for k, f := range probeFailures {
			if !now.Before(f.expires) {
				delete(probeFailures, k)
			}
		}
	}
	if len(probeFailures) >= probeCacheMax {
		probeFailures = make(map[hashKey]probeFailure)
	}
	probeFailures[key] = probeFailure{err: err, expires: probeNow().Add(probeFailureTTL)}
}

func (s *Streamer) joinProbeFlightLocked(ctx context.Context, key hashKey) *probeFlight {
	if fl, ok := probeFlights[key]; ok {
		fl.waiters++
		return fl
	}
	fctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	fl := &probeFlight{done: make(chan struct{}), cancel: cancel, waiters: 1}
	probeFlights[key] = fl
	go s.runProbeFlight(fctx, fl, key)
	return fl
}

func (fl *probeFlight) wait(ctx context.Context, key hashKey) (ProbeResult, error) {
	select {
	case <-fl.done:
		return fl.res, fl.err
	case <-ctx.Done():
	}
	if fl.leave(key, ctx.Err()) {
		return fl.res, fl.err
	}
	return ProbeResult{}, fmt.Errorf("ffprobe: %w", ctx.Err())
}

// leave detaches one waiter whose context ended. It reports true when the
// flight finished in the meantime, so the caller still gets the result. The
// last waiter out cancels the flight. Only a deadline is remembered as a
// failure: it means the probe really ran out of time, whereas a cancellation
// (client gone, shutdown) says nothing about the source.
func (fl *probeFlight) leave(key hashKey, cause error) bool {
	probeCacheMu.Lock()
	defer probeCacheMu.Unlock()
	select {
	case <-fl.done:
		return true
	default:
	}
	fl.waiters--
	if fl.waiters > 0 {
		return false
	}
	fl.abandoned = true
	delete(probeFlights, key)
	fl.cancel()
	if errors.Is(cause, context.DeadlineExceeded) {
		rememberProbeFailureLocked(key, fmt.Errorf("ffprobe: %w", cause))
	}
	return false
}

func (s *Streamer) runProbeFlight(ctx context.Context, fl *probeFlight, key hashKey) {
	// A resolve failure (torrent not active yet, bad index) is a state that
	// flips the moment the torrent is added — never remember it.
	pi, err := s.resolveProbeInput(key.hash, key.fileIdx, 2*1024*1024)
	if err != nil {
		fl.finish(key, ProbeResult{}, err, false)
		return
	}
	res, err := probeFromInput(ctx, pi)
	fl.finish(key, res, err, true)
}

func (fl *probeFlight) finish(key hashKey, res ProbeResult, err error, cacheFailure bool) {
	probeCacheMu.Lock()
	fl.res, fl.err = res, err
	if !fl.abandoned {
		delete(probeFlights, key)
		storeProbeOutcomeLocked(key, res, err, cacheFailure)
	}
	close(fl.done)
	probeCacheMu.Unlock()
	fl.cancel()
}

func storeProbeOutcomeLocked(key hashKey, res ProbeResult, err error, cacheFailure bool) {
	if err != nil {
		if cacheFailure {
			rememberProbeFailureLocked(key, err)
		}
		return
	}
	if len(probeCache) >= probeCacheMax {
		probeCache = make(map[hashKey]ProbeResult)
	}
	probeCache[key] = res
}

func probeFromInput(ctx context.Context, pi probeInput) (ProbeResult, error) {
	// Probe uses "pipe:" (not pipe0/pipe:0) and a limited reader
	input := pi.input
	stdin := pi.stdin
	if pi.input == pipe0 {
		input = "pipe:"
		stdin = io.LimitReader(pi.stdin, 16*1024*1024)
	}
	if pi.closeFn != nil {
		defer func() { _ = pi.closeFn() }()
	}

	out, err := ffprobeRun(ctx, input, stdin)
	if err != nil {
		return ProbeResult{}, err
	}
	result, perr := parseProbeOutput(out)
	if perr != nil {
		return ProbeResult{}, fmt.Errorf("decode ffprobe: %w", perr)
	}
	return *result, nil
}
