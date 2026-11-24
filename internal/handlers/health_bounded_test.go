package handlers

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/history"
	"github.com/lgldsilva/jackui/internal/jackett"
)

const probeSecret = "pq: password authentication failed for user \"jackui\" host=10.0.0.7"

// probeDriver is a database/sql driver that records how the health probe
// reaches the DB. With hang set, every call blocks until its context ends
// (or release closes, so a test of unfixed code can still clean up).
type probeDriver struct {
	err     error
	hang    bool
	release chan struct{}

	mu          sync.Mutex
	pings       int
	queries     int
	hasDeadline bool
	budget      time.Duration
}

func (d *probeDriver) Connect(context.Context) (driver.Conn, error) { return &probeConn{d}, nil }
func (d *probeDriver) Driver() driver.Driver                        { return d }
func (d *probeDriver) Open(string) (driver.Conn, error)             { return &probeConn{d}, nil }

func (d *probeDriver) observe(ctx context.Context, query bool) error {
	d.mu.Lock()
	if query {
		d.queries++
	} else {
		d.pings++
	}
	if dl, ok := ctx.Deadline(); ok {
		d.hasDeadline = true
		d.budget = time.Until(dl)
	}
	d.mu.Unlock()
	if d.hang {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-d.release:
			return errors.New("released")
		}
	}
	return d.err
}

type probeConn struct{ d *probeDriver }

func (c *probeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unsupported") }
func (c *probeConn) Close() error                        { return nil }
func (c *probeConn) Begin() (driver.Tx, error)           { return nil, errors.New("unsupported") }
func (c *probeConn) Ping(ctx context.Context) error      { return c.d.observe(ctx, false) }

func (c *probeConn) QueryContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	if err := c.d.observe(ctx, true); err != nil {
		return nil, err
	}
	return emptyRows{}, nil
}

type emptyRows struct{}

func (emptyRows) Columns() []string         { return []string{"query", "count", "last_seen"} }
func (emptyRows) Close() error              { return nil }
func (emptyRows) Next([]driver.Value) error { return io.EOF }

func newProbeStore(t *testing.T, d *probeDriver) *history.Store {
	t.Helper()
	d.release = make(chan struct{})
	pool := sql.OpenDB(d)
	t.Cleanup(func() {
		close(d.release)
		_ = pool.Close()
	})
	store, err := history.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func okJackett(t *testing.T) *jackett.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return jackett.New(srv.URL, "testkey")
}

// serveProbe runs h against ctx and fails instead of hanging the suite when
// the handler does not come back.
func serveProbe(t *testing.T, ctx context.Context, h gin.HandlerFunc) (int, map[string]any, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		h(c)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not return: the DB probe ignores the request context")
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON %q: %v", w.Body.String(), err)
	}
	return w.Code, body, w.Body.String()
}

func probeHandlers(t *testing.T, store *history.Store) map[string]gin.HandlerFunc {
	t.Helper()
	return map[string]gin.HandlerFunc{
		"/status":     BuildInfo(store, "primary"),
		"/healthz":    Health(store, func() bool { return true }),
		"/api/status": Status(okJackett(t), store),
	}
}

func TestHealthProbe_DBErrorIsNotLeaked(t *testing.T) {
	wantCode := map[string]int{
		"/status":     http.StatusOK,
		"/healthz":    http.StatusServiceUnavailable,
		"/api/status": http.StatusServiceUnavailable,
	}
	for route, h := range probeHandlers(t, newProbeStore(t, &probeDriver{err: errors.New(probeSecret)})) {
		t.Run(route, func(t *testing.T) {
			code, body, raw := serveProbe(t, context.Background(), h)
			if code != wantCode[route] {
				t.Errorf("status = %d, want %d", code, wantCode[route])
			}
			if body["db"] != "down" {
				t.Errorf("db = %q, want the generic \"down\"", body["db"])
			}
			if strings.Contains(raw, "password") || strings.Contains(raw, "10.0.0.7") {
				t.Errorf("response leaks the raw DB error: %s", raw)
			}
		})
	}
}

func TestHealthProbe_PingsUnderDeadlineWithoutQuerying(t *testing.T) {
	for _, route := range []string{"/status", "/healthz", "/api/status"} {
		t.Run(route, func(t *testing.T) {
			d := &probeDriver{}
			h := probeHandlers(t, newProbeStore(t, d))[route]
			code, body, _ := serveProbe(t, context.Background(), h)
			if code != http.StatusOK || body["db"] != "ok" || body["status"] != "ok" {
				t.Fatalf("healthy probe = %d %v, want 200 with db/status ok", code, body)
			}
			d.mu.Lock()
			defer d.mu.Unlock()
			if d.queries != 0 {
				t.Errorf("probe ran %d SQL queries, want none (ping only)", d.queries)
			}
			if d.pings == 0 {
				t.Error("probe never pinged the DB")
			}
			if !d.hasDeadline || d.budget > healthDBTimeout {
				t.Errorf("probe deadline set=%v budget=%v, want one within %v", d.hasDeadline, d.budget, healthDBTimeout)
			}
		})
	}
}

func TestHealthProbe_HangingDBHonoursRequestContext(t *testing.T) {
	wantCode := map[string]int{
		"/status":     http.StatusOK,
		"/healthz":    http.StatusServiceUnavailable,
		"/api/status": http.StatusServiceUnavailable,
	}
	for route, h := range probeHandlers(t, newProbeStore(t, &probeDriver{hang: true})) {
		t.Run(route, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			code, body, _ := serveProbe(t, ctx, h)
			if code != wantCode[route] || body["db"] != "down" {
				t.Errorf("got %d db=%v, want %d db=down", code, body["db"], wantCode[route])
			}
		})
	}
}

func TestHealthProbe_HangingDBTimesOut(t *testing.T) {
	prev := healthDBTimeout
	healthDBTimeout = 50 * time.Millisecond
	t.Cleanup(func() { healthDBTimeout = prev })

	code, body, _ := serveProbe(t, context.Background(), Health(newProbeStore(t, &probeDriver{hang: true}), nil))
	if code != http.StatusServiceUnavailable || body["db"] != "down" || body["status"] != "degraded" {
		t.Errorf("got %d %v, want 503 degraded with db=down", code, body)
	}
}

func TestHealthProbe_HealthyShapeUnchanged(t *testing.T) {
	store := newProbeStore(t, &probeDriver{})
	cases := map[string]struct {
		h    gin.HandlerFunc
		keys []string
	}{
		"/status":     {BuildInfo(store, "primary"), []string{"status", "db", "role", "version", "commit", "buildTime", "goVersion", "time"}},
		"/healthz":    {Health(store, func() bool { return true }), []string{"status", "db", "streamer", "time"}},
		"/api/status": {Status(okJackett(t), store), []string{"status", "db", "jackett", "time"}},
	}
	for route, tc := range cases {
		t.Run(route, func(t *testing.T) {
			code, body, _ := serveProbe(t, context.Background(), tc.h)
			if code != http.StatusOK {
				t.Errorf("status = %d, want 200", code)
			}
			if len(body) != len(tc.keys) {
				t.Errorf("fields = %v, want exactly %v", body, tc.keys)
			}
			for _, k := range tc.keys {
				if _, ok := body[k]; !ok {
					t.Errorf("missing field %q", k)
				}
			}
			if body["status"] != "ok" || body["db"] != "ok" {
				t.Errorf("status=%v db=%v, want ok/ok", body["status"], body["db"])
			}
		})
	}
}
