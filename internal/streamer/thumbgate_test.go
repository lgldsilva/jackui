package streamer

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

// Hover previews used to start one ffmpeg per 10s bucket with no bound: on a
// torrent that isn't downloaded yet each one waits on pieces at a random offset
// (stealing bandwidth from the playing video) and dozens piled up at ~50 MB each.
func TestThumbGateTryRejectsWhenFull(t *testing.T) {
	g := newThumbGate(2)
	release := make(chan struct{})
	var started sync.WaitGroup
	started.Add(2)
	var wg sync.WaitGroup
	for _, k := range []string{"a", "b"} {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			_, _ = g.try(k, func() ([]byte, error) {
				started.Done()
				<-release
				return []byte(k), nil
			})
		}(k)
	}
	started.Wait()

	ran := false
	_, err := g.try("c", func() ([]byte, error) { ran = true; return nil, nil })
	if !errors.Is(err, ErrThumbnailBusy) || ran {
		t.Fatalf("third distinct extraction: err=%v ran=%v, want ErrThumbnailBusy without running", err, ran)
	}
	close(release)
	wg.Wait()

	if out, err := g.try("c", func() ([]byte, error) { return []byte("c"), nil }); err != nil || string(out) != "c" {
		t.Fatalf("slot not released: %q %v", out, err)
	}
}

func TestThumbGateDedupesSameKey(t *testing.T) {
	g := newThumbGate(1)
	var calls atomic.Int32
	release := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]string, 5)
	errs := make([]error, 5)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, err := g.try("same", func() ([]byte, error) {
				calls.Add(1)
				<-release
				return []byte("jpeg"), nil
			})
			results[i], errs[i] = string(out), err
		}(i)
	}
	time.Sleep(50 * time.Millisecond) // let every caller join the in-flight one
	close(release)
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("same bucket extracted %d times, want 1", n)
	}
	for i := range results {
		if errs[i] != nil || results[i] != "jpeg" {
			t.Fatalf("caller %d got %q %v", i, results[i], errs[i])
		}
	}
}

// Background callers (art frame capture) wait for a slot instead of failing,
// bounded by their context.
func TestThumbGateWaitBlocksUntilSlotOrCtx(t *testing.T) {
	g := newThumbGate(1)
	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		_, _ = g.try("hold", func() ([]byte, error) { close(started); <-release; return nil, nil })
	}()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := g.wait(ctx, "w1", func() ([]byte, error) { return []byte("x"), nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait on full gate: err=%v, want deadline exceeded", err)
	}

	done := make(chan string, 1)
	go func() {
		out, _ := g.wait(context.Background(), "w2", func() ([]byte, error) { return []byte("ok"), nil })
		done <- string(out)
	}()
	close(release)
	select {
	case got := <-done:
		if got != "ok" {
			t.Fatalf("wait result %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wait never got the released slot")
	}
}

// The hover entry point fails fast while every slot is held, then works once
// one frees up.
func TestTryExtractThumbnailBusyThenServes(t *testing.T) {
	path := strmGenVideo(t)
	s := strmResolverStreamer(t, path)
	hash := metainfo.HashBytes([]byte("strm-thumb-try"))

	for i := 0; i < maxThumbExtractions; i++ {
		thumbExtractions.sem <- struct{}{}
	}
	if _, _, err := s.TryExtractThumbnail(context.Background(), hash, 0, 0); !errors.Is(err, ErrThumbnailBusy) {
		t.Fatalf("full gate: err=%v, want ErrThumbnailBusy", err)
	}
	for i := 0; i < maxThumbExtractions; i++ {
		<-thumbExtractions.sem
	}
	var noCtx context.Context // legacy callers pass nil; it must still work
	jpeg, _, err := s.TryExtractThumbnail(noCtx, hash, 0, 0)
	if err != nil || len(jpeg) == 0 {
		t.Fatalf("free gate: %d bytes, err=%v", len(jpeg), err)
	}
}
