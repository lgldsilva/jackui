package httpretry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fastPolicy keeps the backoff tiny so tests don't actually sleep.
func fastPolicy() Policy {
	return Policy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}
}

func TestDo_RetriesUntilSuccess(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&hits, 1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := Do(context.Background(), srv.Client(), req, fastPolicy())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if hits != 3 {
		t.Fatalf("hits = %d, want 3 (two 500s then 200)", hits)
	}
}

func TestDo_404NotRetriedByDefault(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := Do(context.Background(), srv.Client(), req, fastPolicy())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if hits != 1 {
		t.Fatalf("hits = %d, want 1 (404 must not be retried by default)", hits)
	}
}

func TestDo_404RetriedWhenOptedIn(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&hits, 1) < 3 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := fastPolicy()
	p.RetryOn = RetryOnStatuses(http.StatusNotFound)
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := Do(context.Background(), srv.Client(), req, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if hits != 3 {
		t.Fatalf("hits = %d, want 3 (404 retried when opted-in)", hits)
	}
}

func TestDo_RespectsRetryAfter(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.Header().Set("Retry-After", "1") // 1 second
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// MaxDelay caps the wait below the Retry-After so the test stays fast while
	// still proving the header drives the backoff (we measure it was honoured).
	p := Policy{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: 50 * time.Millisecond}
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	start := time.Now()
	resp, err := Do(context.Background(), srv.Client(), req, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Retry-After=1s capped at MaxDelay=50ms → waited ~50ms, not the BaseDelay 1ms.
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Fatalf("elapsed = %v, expected the Retry-After-driven wait (≥40ms)", elapsed)
	}
}

func TestDo_StopsOnContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	p := Policy{MaxAttempts: 5, BaseDelay: time.Second, MaxDelay: time.Second}
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	_, err := Do(ctx, srv.Client(), req, p)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestDoFunc_ReplaysBodyEachAttempt(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if len(bodies) < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	build := func() (*http.Request, error) {
		return http.NewRequest(http.MethodGet, srv.URL, strings.NewReader("payload"))
	}
	resp, err := DoFunc(context.Background(), fastPolicy(), build, srv.Client().Do)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if len(bodies) != 2 {
		t.Fatalf("got %d attempts, want 2", len(bodies))
	}
	for i, b := range bodies {
		if b != "payload" {
			t.Fatalf("attempt %d body = %q, want %q (body must be replayed)", i, b, "payload")
		}
	}
}

func TestRetryAfter_ParsesSecondsAndDate(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", "2")
	if got := retryAfter(resp); got != 2*time.Second {
		t.Fatalf("seconds: got %v, want 2s", got)
	}
	resp.Header.Set("Retry-After", "")
	if got := retryAfter(resp); got != 0 {
		t.Fatalf("empty: got %v, want 0", got)
	}
	resp.Header.Set("Retry-After", time.Now().Add(3*time.Second).UTC().Format(http.TimeFormat))
	if got := retryAfter(resp); got <= 0 || got > 4*time.Second {
		t.Fatalf("http-date: got %v, want ~3s", got)
	}
}
