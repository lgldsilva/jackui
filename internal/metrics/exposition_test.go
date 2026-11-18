package metrics

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	_ "github.com/jackc/pgx/v5/stdlib" // driver for an unconnected sql.DB (Stats only)
	"github.com/lgldsilva/jackui/internal/metrics/events"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// newMetricsRouter mirrors the production wiring: the HTTP middleware on the
// engine and promhttp's default-registry handler at /api/metrics.
func newMetricsRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(HTTPMiddleware())
	r.GET("/api/items/:id", func(c *gin.Context) {
		if c.Param("id") == "boom" {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusOK)
	})
	r.GET("/api/metrics", gin.WrapH(promhttp.Handler()))
	return r
}

func do(t *testing.T, r http.Handler, method, target string) int {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, target, nil))
	return w.Code
}

func scrape(t *testing.T, r http.Handler) string {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("scrape status = %d", w.Code)
	}
	b, _ := io.ReadAll(w.Body)
	return string(b)
}

// hasSeries reports whether the exposition has a sample line for the given
// series prefix (name + label set) with a non-zero value.
func hasSeries(body, series string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, series+" ") && !strings.HasSuffix(line, " 0") {
			return true
		}
	}
	return false
}

// TestMetricsExposition exercises every instrumented seam and scrapes
// /api/metrics: each incident series must be exported with its bounded labels.
func TestMetricsExposition(t *testing.T) {
	r := newMetricsRouter()

	db, err := sql.Open("pgx", "postgres://unused@127.0.0.1:1/none?sslmode=disable")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	if err := RegisterDBStats(db); err != nil {
		t.Fatalf("RegisterDBStats: %v", err)
	}
	if err := RegisterDBStats(db); err != nil {
		t.Fatalf("RegisterDBStats must be idempotent, got %v", err)
	}

	do(t, r, http.MethodGet, "/api/items/42")
	do(t, r, http.MethodGet, "/api/items/boom")

	// The seams the instrumented packages call (each call site has its own
	// package-level test asserting it fires on the real path).
	events.HLSSessionStarted(true, false)
	events.HLSSessionFailed(events.HLSFailFFmpegExit)
	events.HLSAdmissionEvent(events.HLSAdmitRejectedCap)
	events.FFprobeFailed(events.ProbeDuration, events.ProbeReasonNoDuration)
	events.DownloadInitFailed(true)
	events.DownloadStalled(true)
	events.DownloadCompleted(events.DownloadKindGroup)
	events.DownloadMoveFailed(events.DownloadKindSingle)

	body := scrape(t, r)
	want := []string{
		`jackui_http_requests_total{method="GET",route="/api/items/:id",status_class="2xx"}`,
		`jackui_http_requests_total{method="GET",route="/api/items/:id",status_class="5xx"}`,
		`jackui_http_request_duration_seconds_count{method="GET",route="/api/items/:id"}`,
		`jackui_hls_sessions_started_total{decode="hw",mode="vod"}`,
		`jackui_hls_session_failures_total{reason="ffmpeg_exit"}`,
		`jackui_hls_admission_events_total{event="rejected_cap"}`,
		`jackui_ffprobe_failures_total{probe="duration",reason="no_duration"}`,
		`jackui_downloads_init_failures_total{outcome="failed"}`,
		`jackui_downloads_stalls_total`,
		`jackui_downloads_stall_pauses_total`,
		`jackui_downloads_completions_total{kind="group"}`,
		`jackui_downloads_move_failures_total{kind="single"}`,
	}
	for _, s := range want {
		if !hasSeries(body, s) {
			t.Errorf("missing non-zero series %s", s)
		}
	}
	// DB pool stats: present even at zero (an idle, never-connected pool).
	for _, s := range []string{
		`go_sql_max_open_connections{db_name="jackui"}`,
		`go_sql_in_use_connections{db_name="jackui"}`,
		`go_sql_wait_count_total{db_name="jackui"}`,
		`go_sql_wait_duration_seconds_total{db_name="jackui"}`,
	} {
		if !strings.Contains(body, s+" ") {
			t.Errorf("missing DB pool series %s", s)
		}
	}
}

// TestHTTPMetricsUnknownRouteIsBounded: a request that matches no route must
// be labelled with a fixed value — never the raw URL, or any scanner hitting
// random paths would create unbounded series.
func TestHTTPMetricsUnknownRouteIsBounded(t *testing.T) {
	r := newMetricsRouter()
	const raw = "/no/such/route-7f3a9c/secret.mkv"
	if code := do(t, r, http.MethodGet, raw); code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
	if code := do(t, r, "PROPFIND", "/api/items/1"); code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an unregistered method", code)
	}
	body := scrape(t, r)
	if strings.Contains(body, "route-7f3a9c") || strings.Contains(body, "secret.mkv") {
		t.Fatal("raw request path leaked into a metric label")
	}
	if strings.Contains(body, "PROPFIND") {
		t.Fatal("arbitrary HTTP method leaked into a metric label")
	}
	if !hasSeries(body, `jackui_http_requests_total{method="GET",route="unmatched",status_class="4xx"}`) {
		t.Error("unknown route must map to route=\"unmatched\"")
	}
	if !hasSeries(body, `jackui_http_requests_total{method="OTHER",route="unmatched",status_class="4xx"}`) {
		t.Error("unknown method must map to method=\"OTHER\"")
	}
}

func TestStatusClass(t *testing.T) {
	for code, want := range map[int]string{
		101: "1xx", 200: "2xx", 206: "2xx", 304: "3xx", 404: "4xx", 429: "4xx", 503: "5xx", 0: "other", 999: "other",
	} {
		if got := statusClass(code); got != want {
			t.Errorf("statusClass(%d) = %q, want %q", code, got, want)
		}
	}
}

func TestRegisterDBStatsNil(t *testing.T) {
	if err := RegisterDBStats(nil); err != nil {
		t.Fatalf("nil pool: %v", err)
	}
}
