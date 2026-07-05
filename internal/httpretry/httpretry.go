// Package httpretry retries idempotent HTTP calls with exponential backoff.
// It is meant for GET requests against external services (TMDB, Jackett,
// OpenSubtitles, indexer .torrent links, image search) where a transient
// failure — a network blip, a 429, or a 5xx — should not surface to the user as
// a hard error. Non-idempotent calls (POST that mutates) must NOT use this.
package httpretry

import (
	"context"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

// Policy controls the retry behaviour. The zero value is usable — missing
// fields fall back to sane defaults (see withDefaults).
type Policy struct {
	MaxAttempts int           // total attempts including the first (default 3)
	BaseDelay   time.Duration // first backoff; doubles each attempt (default 300ms)
	MaxDelay    time.Duration // cap for any single wait (default 5s)
	// RetryOn decides whether a result is worth retrying. Default: network
	// errors, 429, and 5xx. Build a custom one with RetryOnStatuses.
	RetryOn func(resp *http.Response, err error) bool
}

// Do retries an idempotent request. The request must have a nil body or a
// non-nil GetBody so it can be replayed across attempts. Use DoFunc when you
// need to rebuild the request from scratch each time.
func Do(ctx context.Context, client *http.Client, req *http.Request, p Policy) (*http.Response, error) {
	return DoFunc(ctx, p, func() (*http.Request, error) {
		return cloneReq(ctx, req)
	}, client.Do)
}

// DoFunc runs do(build()) with retry/backoff. build is called once per attempt
// so each try gets a fresh request (and body). It honours ctx between attempts,
// respects Retry-After, and drains+closes the body of discarded responses so
// connections are reused.
func DoFunc(ctx context.Context, p Policy, build func() (*http.Request, error), do func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	p = withDefaults(p)
	var resp *http.Response
	var err error
	for attempt := 0; attempt < p.MaxAttempts; attempt++ {
		var req *http.Request
		if req, err = build(); err != nil {
			return nil, err
		}
		resp, err = do(req)
		if !p.RetryOn(resp, err) {
			return resp, err
		}
		if attempt == p.MaxAttempts-1 {
			break // out of attempts: return the last (failing) result as-is
		}
		drainClose(resp)
		if waitErr := sleepBackoff(ctx, p, attempt, resp); waitErr != nil {
			return nil, waitErr
		}
	}
	return resp, err
}

// RetryOnStatuses returns a RetryOn that retries on the default transient
// conditions (network error, 429, 5xx) PLUS any extra status codes given — e.g.
// 404 for a cold indexer that briefly 404s a freshly-published .torrent link.
func RetryOnStatuses(extra ...int) func(*http.Response, error) bool {
	return func(resp *http.Response, err error) bool {
		if defaultRetryOn(resp, err) {
			return true
		}
		if resp == nil {
			return false
		}
		for _, s := range extra {
			if resp.StatusCode == s {
				return true
			}
		}
		return false
	}
}

func withDefaults(p Policy) Policy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 3
	}
	if p.BaseDelay <= 0 {
		p.BaseDelay = 300 * time.Millisecond
	}
	if p.MaxDelay <= 0 {
		p.MaxDelay = 5 * time.Second
	}
	if p.RetryOn == nil {
		p.RetryOn = defaultRetryOn
	}
	return p
}

func defaultRetryOn(resp *http.Response, err error) bool {
	if err != nil {
		return true
	}
	return resp != nil && (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500)
}

func cloneReq(ctx context.Context, req *http.Request) (*http.Request, error) {
	r := req.Clone(ctx)
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		r.Body = body
	}
	return r, nil
}

func drainClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
}

func sleepBackoff(ctx context.Context, p Policy, attempt int, resp *http.Response) error {
	timer := time.NewTimer(backoffDelay(p, attempt, resp))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// backoffDelay picks the wait before the next attempt: the server's Retry-After
// when present (typical on 429), else BaseDelay*2^attempt plus jitter, capped at
// MaxDelay.
func backoffDelay(p Policy, attempt int, resp *http.Response) time.Duration {
	if ra := retryAfter(resp); ra > 0 {
		return capDelay(ra, p.MaxDelay)
	}
	d := p.BaseDelay << uint(attempt)
	// #nosec G404 -- rand nao-cripto adequado p/ jitter de backoff
	d += time.Duration(rand.Int63n(int64(p.BaseDelay) + 1)) // jitter in [0, BaseDelay]
	return capDelay(d, p.MaxDelay)
}

func capDelay(d, max time.Duration) time.Duration {
	if d > max {
		return max
	}
	return d
}

// retryAfter parses a Retry-After header (delay-seconds or HTTP-date). Returns 0
// when absent or unparseable.
func retryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
