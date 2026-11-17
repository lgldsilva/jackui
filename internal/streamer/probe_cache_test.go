package streamer

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

const probeStubJSON = `{"streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":1920,"height":1080}],"format":{"duration":"12.5","format_name":"mov,mp4"}}`

type ffprobeStub func(ctx context.Context, input string, stdin io.Reader) ([]byte, error)

// stubFFprobe swaps the ffprobe seam and returns the invocation counter. It
// also isolates the package-level probe caches for the test.
func stubFFprobe(t *testing.T, fn ffprobeStub) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	probeCacheMu.Lock()
	prevRun, prevNow := ffprobeRun, probeNow
	prevCache, prevFailures, prevFlights := probeCache, probeFailures, probeFlights
	probeCache = make(map[hashKey]ProbeResult)
	probeFailures = make(map[hashKey]probeFailure)
	probeFlights = make(map[hashKey]*probeFlight)
	ffprobeRun = func(ctx context.Context, input string, stdin io.Reader) ([]byte, error) {
		calls.Add(1)
		return fn(ctx, input, stdin)
	}
	probeCacheMu.Unlock()
	t.Cleanup(func() {
		probeCacheMu.Lock()
		defer probeCacheMu.Unlock()
		ffprobeRun, probeNow = prevRun, prevNow
		probeCache, probeFailures, probeFlights = prevCache, prevFailures, prevFlights
	})
	return &calls
}

func setProbeClock(t *testing.T, now time.Time) {
	t.Helper()
	probeCacheMu.Lock()
	defer probeCacheMu.Unlock()
	probeNow = func() time.Time { return now }
}

func probeResolverStreamer() *Streamer {
	s := NewForTesting()
	s.SetFilePathResolver(func(metainfo.Hash, int) (string, bool) { return "/nonexistent/video.mp4", true })
	return s
}

// waitProbeWaiters blocks until the in-flight probe for key has n waiters.
func waitProbeWaiters(t *testing.T, key hashKey, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		probeCacheMu.Lock()
		fl := probeFlights[key]
		got := 0
		if fl != nil {
			got = fl.waiters
		}
		probeCacheMu.Unlock()
		if got == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("flight for %v never reached %d waiters", key, n)
}

func TestProbe_ConcurrentCallersShareOneFFprobe(t *testing.T) {
	release := make(chan struct{})
	calls := stubFFprobe(t, func(context.Context, string, io.Reader) ([]byte, error) {
		<-release
		return []byte(probeStubJSON), nil
	})
	s := probeResolverStreamer()
	hash := metainfo.HashBytes([]byte("flight-shared"))

	const callers = 8
	var wg sync.WaitGroup
	results := make([]ProbeResult, callers)
	errs := make([]error, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = s.Probe(context.Background(), hash, 0)
		}()
	}
	waitProbeWaiters(t, hashKey{hash, 0}, callers)
	close(release)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("ffprobe invocations = %d for %d concurrent callers, want 1", got, callers)
	}
	for i := range callers {
		if errs[i] != nil || results[i].VideoHeight != 1080 || results[i].DurationSec != 12.5 {
			t.Fatalf("caller %d: res=%+v err=%v", i, results[i], errs[i])
		}
	}
	// The success is cached: no further ffprobe.
	if _, err := s.Probe(context.Background(), hash, 0); err != nil || calls.Load() != 1 {
		t.Fatalf("cached probe: err=%v calls=%d", err, calls.Load())
	}
}

func TestProbe_FailureIsCachedForBoundedTTL(t *testing.T) {
	boom := errors.New("ffprobe: exit status 1")
	calls := stubFFprobe(t, func(context.Context, string, io.Reader) ([]byte, error) { return nil, boom })
	start := time.Unix(1_700_000_000, 0)
	setProbeClock(t, start)
	s := probeResolverStreamer()
	hash := metainfo.HashBytes([]byte("neg-ttl"))

	for i := range 3 {
		if _, err := s.Probe(context.Background(), hash, 0); !errors.Is(err, boom) {
			t.Fatalf("call %d: err = %v, want %v", i, err, boom)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("ffprobe invocations = %d for 3 probes of a failing file, want 1", got)
	}

	setProbeClock(t, start.Add(probeFailureTTL-time.Millisecond))
	_, _ = s.Probe(context.Background(), hash, 0)
	if got := calls.Load(); got != 1 {
		t.Fatalf("ffprobe invocations = %d inside the TTL, want 1", got)
	}

	// The failure must not stick: once the TTL lapses the file is probed again.
	setProbeClock(t, start.Add(probeFailureTTL))
	_, _ = s.Probe(context.Background(), hash, 0)
	if got := calls.Load(); got != 2 {
		t.Fatalf("ffprobe invocations = %d after the TTL lapsed, want 2", got)
	}
}

func TestProbe_DecodeFailureIsCached(t *testing.T) {
	calls := stubFFprobe(t, func(context.Context, string, io.Reader) ([]byte, error) { return []byte("not json"), nil })
	s := probeResolverStreamer()
	hash := metainfo.HashBytes([]byte("neg-decode"))
	for range 2 {
		if _, err := s.Probe(context.Background(), hash, 0); err == nil || !strings.Contains(err.Error(), "decode ffprobe") {
			t.Fatalf("err = %v, want decode ffprobe", err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("ffprobe invocations = %d, want 1", got)
	}
}

// blockUntilCtx is an ffprobe that never answers: it returns only once its
// context is cancelled, like the real one on a starved swarm.
func blockUntilCtx(killed chan<- struct{}) ffprobeStub {
	return func(ctx context.Context, _ string, _ io.Reader) ([]byte, error) {
		<-ctx.Done()
		killed <- struct{}{}
		return nil, ctx.Err()
	}
}

func TestProbe_CancelledContextIsNotCached(t *testing.T) {
	killed := make(chan struct{}, 1)
	calls := stubFFprobe(t, blockUntilCtx(killed))
	s := probeResolverStreamer()
	hash := metainfo.HashBytes([]byte("neg-cancel"))
	key := hashKey{hash, 0}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := s.Probe(ctx, hash, 0)
		errCh <- err
	}()
	waitProbeWaiters(t, key, 1)
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	<-killed // the abandoned ffprobe was cancelled, not left running

	probeCacheMu.Lock()
	_, negative := probeFailures[key]
	_, flying := probeFlights[key]
	probeCacheMu.Unlock()
	if negative || flying {
		t.Fatalf("cancelled probe left state behind: negative=%v flying=%v", negative, flying)
	}

	// The next caller must get a real probe, not a remembered cancellation.
	probeCacheMu.Lock()
	ffprobeRun = func(context.Context, string, io.Reader) ([]byte, error) {
		calls.Add(1)
		return []byte(probeStubJSON), nil
	}
	probeCacheMu.Unlock()
	res, err := s.Probe(context.Background(), hash, 0)
	if err != nil || res.VideoCodec != "h264" {
		t.Fatalf("probe after cancel: res=%+v err=%v", res, err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("ffprobe invocations = %d, want 2 (cancelled + fresh)", got)
	}
}

func TestProbe_DeadlineIsCachedBriefly(t *testing.T) {
	killed := make(chan struct{}, 1)
	calls := stubFFprobe(t, blockUntilCtx(killed))
	start := time.Unix(1_700_000_000, 0)
	setProbeClock(t, start)
	s := probeResolverStreamer()
	hash := metainfo.HashBytes([]byte("neg-deadline"))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.Probe(ctx, hash, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	<-killed

	// A retry inside the TTL answers from memory instead of starving again.
	if _, err := s.Probe(context.Background(), hash, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("retry err = %v, want the remembered deadline", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("ffprobe invocations = %d, want 1", got)
	}

	setProbeClock(t, start.Add(probeFailureTTL))
	ctx2, cancel2 := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := s.Probe(ctx2, hash, 0)
		errCh <- err
	}()
	waitProbeWaiters(t, hashKey{hash, 0}, 1)
	cancel2()
	<-errCh
	<-killed
	if got := calls.Load(); got != 2 {
		t.Fatalf("ffprobe invocations = %d after the TTL lapsed, want 2", got)
	}
}

func TestProbe_OneCallerLeavingKeepsTheFlight(t *testing.T) {
	release := make(chan struct{})
	calls := stubFFprobe(t, func(ctx context.Context, _ string, _ io.Reader) ([]byte, error) {
		select {
		case <-release:
			return []byte(probeStubJSON), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	s := probeResolverStreamer()
	hash := metainfo.HashBytes([]byte("flight-leave"))
	key := hashKey{hash, 0}

	stayCh := make(chan error, 1)
	go func() {
		_, err := s.Probe(context.Background(), hash, 0)
		stayCh <- err
	}()
	waitProbeWaiters(t, key, 1)

	ctx, cancel := context.WithCancel(context.Background())
	leaveCh := make(chan error, 1)
	go func() {
		_, err := s.Probe(ctx, hash, 0)
		leaveCh <- err
	}()
	waitProbeWaiters(t, key, 2)
	cancel()
	if err := <-leaveCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("leaver err = %v, want context.Canceled", err)
	}
	waitProbeWaiters(t, key, 1)

	close(release)
	if err := <-stayCh; err != nil {
		t.Fatalf("remaining caller failed after another one left: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("ffprobe invocations = %d, want 1", got)
	}
}

func TestProbe_ResolveFailureIsNotCached(t *testing.T) {
	calls := stubFFprobe(t, func(context.Context, string, io.Reader) ([]byte, error) { return []byte(probeStubJSON), nil })
	s := NewForTesting()
	hash := metainfo.HashBytes([]byte("neg-resolve"))

	for range 2 {
		if _, err := s.Probe(context.Background(), hash, 0); !errors.Is(err, ErrTorrentNotActive) {
			t.Fatalf("err = %v, want ErrTorrentNotActive", err)
		}
	}
	// The torrent becomes available: the very next probe must succeed.
	s.SetFilePathResolver(func(metainfo.Hash, int) (string, bool) { return "/nonexistent/video.mp4", true })
	if _, err := s.Probe(context.Background(), hash, 0); err != nil {
		t.Fatalf("probe after the source appeared: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("ffprobe invocations = %d, want 1", got)
	}
}

func TestProbe_DeadContextSpawnsNothing(t *testing.T) {
	calls := stubFFprobe(t, func(context.Context, string, io.Reader) ([]byte, error) { return []byte(probeStubJSON), nil })
	s := probeResolverStreamer()
	hash := metainfo.HashBytes([]byte("dead-ctx"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := s.Probe(ctx, hash, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("ffprobe invocations = %d for a dead context, want 0", got)
	}
	// A cached result is still served to a dead context, as before.
	if _, err := s.Probe(context.Background(), hash, 0); err != nil {
		t.Fatalf("warm-up: %v", err)
	}
	if _, err := s.Probe(ctx, hash, 0); err != nil {
		t.Fatalf("cached probe with a dead context: %v", err)
	}
}

func TestProbeFlightLeave_AfterFinishReturnsResult(t *testing.T) {
	stubFFprobe(t, func(context.Context, string, io.Reader) ([]byte, error) { return nil, nil })
	key := hashKey{metainfo.HashBytes([]byte("leave-late")), 0}
	fl := &probeFlight{done: make(chan struct{}), cancel: func() {}, waiters: 1}
	fl.finish(key, ProbeResult{VideoCodec: "vp9"}, nil, true)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !fl.leave(key, ctx.Err()) {
		t.Fatal("leave after finish must report the flight as done")
	}
	if fl.abandoned {
		t.Fatal("a finished flight must not be marked abandoned")
	}
}

func TestProbeFlightFinish_AbandonedStoresNothing(t *testing.T) {
	stubFFprobe(t, func(context.Context, string, io.Reader) ([]byte, error) { return nil, nil })
	key := hashKey{metainfo.HashBytes([]byte("abandoned")), 0}
	fl := &probeFlight{done: make(chan struct{}), cancel: func() {}, waiters: 1}
	if fl.leave(key, context.Canceled) {
		t.Fatal("leave before finish must not report done")
	}
	fl.finish(key, ProbeResult{VideoCodec: "h264"}, nil, true)

	probeCacheMu.Lock()
	defer probeCacheMu.Unlock()
	if len(probeCache) != 0 || len(probeFailures) != 0 {
		t.Fatalf("abandoned flight cached its outcome: ok=%d failed=%d", len(probeCache), len(probeFailures))
	}
}

func TestProbeCaches_StayBounded(t *testing.T) {
	stubFFprobe(t, func(context.Context, string, io.Reader) ([]byte, error) { return nil, nil })
	now := time.Unix(1_700_000_000, 0)
	setProbeClock(t, now)
	probeCacheMu.Lock()
	defer probeCacheMu.Unlock()

	fill := func(expires time.Time) {
		probeFailures = make(map[hashKey]probeFailure)
		for i := range probeCacheMax {
			probeFailures[hashKey{metainfo.Hash{}, i}] = probeFailure{err: io.EOF, expires: expires}
		}
	}
	fresh := hashKey{metainfo.Hash{}, -1}

	// Expired entries are swept first; live ones survive.
	fill(now.Add(-time.Second))
	live := hashKey{metainfo.Hash{}, 0}
	probeFailures[live] = probeFailure{err: io.EOF, expires: now.Add(time.Minute)}
	rememberProbeFailureLocked(fresh, io.EOF)
	if _, ok := probeFailures[live]; !ok || len(probeFailures) != 2 {
		t.Fatalf("sweep kept %d entries (live present=%v), want 2", len(probeFailures), ok)
	}

	// Nothing expired: fall back to dropping the map.
	fill(now.Add(time.Minute))
	rememberProbeFailureLocked(fresh, io.EOF)
	if len(probeFailures) != 1 {
		t.Fatalf("full failure cache holds %d entries after insert, want 1", len(probeFailures))
	}

	for i := range probeCacheMax {
		probeCache[hashKey{metainfo.Hash{}, i}] = ProbeResult{}
	}
	storeProbeOutcomeLocked(fresh, ProbeResult{}, nil, true)
	if len(probeCache) != 1 {
		t.Fatalf("full result cache holds %d entries after insert, want 1", len(probeCache))
	}
}

func TestProbeFromInput_PipeIsLimitedAndClosed(t *testing.T) {
	var gotInput string
	var gotStdin io.Reader
	stubFFprobe(t, func(_ context.Context, input string, stdin io.Reader) ([]byte, error) {
		gotInput, gotStdin = input, stdin
		return []byte(probeStubJSON), nil
	})
	closed := 0
	pi := probeInput{input: pipe0, stdin: strings.NewReader("x"), closeFn: func() error { closed++; return nil }}
	res, err := probeFromInput(context.Background(), pi)
	if err != nil || res.Container != "mov" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	lr, ok := gotStdin.(*io.LimitedReader)
	if gotInput != "pipe:" || !ok || lr.N != 16*1024*1024 {
		t.Fatalf("input=%q stdin=%T, want pipe: + 16 MB LimitedReader", gotInput, gotStdin)
	}
	if closed != 1 {
		t.Fatalf("closeFn called %d times, want 1", closed)
	}
}
