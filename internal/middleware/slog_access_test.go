package middleware

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/logging"
)

func TestRequestIDValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var seenCtx string
	r.Use(RequestID())
	r.GET("/x", func(c *gin.Context) {
		seenCtx = logging.RequestID(c.Request.Context())
		c.Status(http.StatusNoContent)
	})
	cases := []struct {
		name, in string
		keep     bool
	}{
		{"sane token", "abc-DEF_1.2:3", true},
		{"empty", "", false},
		{"too long", strings.Repeat("a", 65), false},
		{"newline injection", "id\n{\"level\":\"ERROR\"}", false},
		{"control char", "id\x07", false},
		{"space", "a b", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		if tc.in != "" {
			req.Header.Set(HeaderRequestID, tc.in)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		got := w.Header().Get(HeaderRequestID)
		if tc.keep && got != tc.in {
			t.Errorf("%s: got %q want echo of %q", tc.name, got, tc.in)
		}
		if !tc.keep && (got == tc.in || len(got) != 32) {
			t.Errorf("%s: got %q, want fresh 32-hex id", tc.name, got)
		}
		if seenCtx != got {
			t.Errorf("%s: ctx id %q != header %q", tc.name, seenCtx, got)
		}
	}
	if !validRequestID(strings.Repeat("a", 64)) {
		t.Error("64-char id should be accepted")
	}
}

func jsonRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not JSON: %q", l)
		}
		out = append(out, m)
	}
	return out
}

func TestSlogAccessLogAndRecovery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var buf bytes.Buffer
	logger := slog.New(logging.ContextHandler{Handler: slog.NewJSONHandler(&buf, nil)})
	r := gin.New()
	r.Use(RequestID(), SlogAccessLog(logger, func(*gin.Context) (int, bool) { return 7, true }), SlogRecovery(logger))
	r.GET("/item/:id", func(c *gin.Context) {
		_ = c.Error(errors.New("token=LEAK detail"))
		c.Status(http.StatusNotFound)
	})
	r.GET("/boom", func(*gin.Context) { panic("kaboom") })
	r.GET("/pipe", func(*gin.Context) {
		panic(&net.OpError{Op: "write", Err: os.NewSyscallError("write", syscall.EPIPE)})
	})

	for _, p := range []string{"/item/9?token=SECRET", "/boom", "/pipe"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		req.Header.Set(HeaderRequestID, "rid")
		r.ServeHTTP(httptest.NewRecorder(), req)
	}
	recs := jsonRecords(t, &buf)
	if len(recs) != 5 {
		t.Fatalf("want 5 records (3 access + 2 panic), got %d: %s", len(recs), buf.String())
	}
	a := recs[0]
	if a["level"] != "WARN" || a["route"] != "/item/:id" || a["status"] != float64(404) || a["user_id"] != float64(7) ||
		a["request_id"] != "rid" || a["method"] != "GET" || a["latency_ms"] == nil {
		t.Errorf("access record: %v", a)
	}
	if strings.Contains(buf.String(), "SECRET") || strings.Contains(buf.String(), "LEAK") {
		t.Error("token leaked into log")
	}
	if recs[1]["msg"] != "panic recovered" || recs[1]["level"] != "ERROR" || recs[1]["broken_pipe"] != false {
		t.Errorf("panic record: %v", recs[1])
	}
	if recs[2]["level"] != "ERROR" || recs[2]["status"] != float64(500) {
		t.Errorf("500 access record: %v", recs[2])
	}
	if recs[3]["broken_pipe"] != true {
		t.Errorf("broken pipe record: %v", recs[3])
	}
	if strings.Count(buf.String(), `"request_id"`) != 5 {
		t.Errorf("request_id duplicated/missing: %s", buf.String())
	}
}

func TestAccessLevelAndBrokenPipe(t *testing.T) {
	if accessLevel(200) != slog.LevelInfo || accessLevel(302) != slog.LevelInfo {
		t.Error("2xx/3xx should be INFO")
	}
	if isBrokenPipe(nil) || isBrokenPipe(errors.New("x")) || isBrokenPipe(&net.OpError{Err: errors.New("x")}) {
		t.Error("non broken-pipe values misclassified")
	}
	if !isBrokenPipe(&net.OpError{Err: os.NewSyscallError("read", syscall.ECONNRESET)}) {
		t.Error("connection reset not detected")
	}
	gin.SetMode(gin.TestMode)
	var buf bytes.Buffer
	r := gin.New()
	r.Use(SlogAccessLog(slog.New(slog.NewJSONHandler(&buf, nil)), nil))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Contains(buf.String(), "user_id") {
		t.Error("nil userID func should omit user_id")
	}
}

// A handler that rewrites URL.Path (spaFallback serves index.html by setting
// it to "/") must not change what the access log reports.
func TestSlogAccessLogKeepsOriginalPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var buf bytes.Buffer
	r := gin.New()
	r.Use(SlogAccessLog(slog.New(slog.NewJSONHandler(&buf, nil)), nil))
	r.NoRoute(func(c *gin.Context) {
		c.Request.URL.Path = "/"
		c.Request.URL.RawQuery = ""
		c.Status(http.StatusOK)
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/library/deep?x=1&token=SECRET", nil))
	recs := jsonRecords(t, &buf)
	if len(recs) != 1 || recs[0]["path"] != "/library/deep?x=1&token=REDACTED" {
		t.Fatalf("logged path should be the original request URI: %v", recs)
	}
}

func TestSlogRecoveryAbortHandlerIsBrokenPipe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	r := gin.New()
	var ginErrs int
	r.Use(func(c *gin.Context) { c.Next(); ginErrs = len(c.Errors) }, SlogRecovery(logger))
	r.GET("/abort", func(*gin.Context) { panic(http.ErrAbortHandler) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/abort", nil))
	recs := jsonRecords(t, &buf)
	if len(recs) != 1 || recs[0]["broken_pipe"] != true {
		t.Fatalf("ErrAbortHandler should be treated as a dead connection: %v", recs)
	}
	if ginErrs != 1 {
		t.Errorf("broken pipe should be attached via c.Error, got %d errors", ginErrs)
	}
	if !isBrokenPipe(os.NewSyscallError("write", syscall.EPIPE)) {
		t.Error("bare EPIPE syscall error should count as broken pipe")
	}
}

func TestSlogLogsStripLineBreaksFromRequestValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	r := gin.New()
	r.Use(SlogAccessLog(logger, nil), SlogRecovery(logger))
	r.NoRoute(func(*gin.Context) { panic("boom") })
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.URL.Path = "/a\r\nb"
	req.RequestURI = "/a\r\nb"
	r.ServeHTTP(httptest.NewRecorder(), req)
	recs := jsonRecords(t, &buf)
	if len(recs) != 2 {
		t.Fatalf("want recovery + access records, got %v", recs)
	}
	for _, rec := range recs {
		if p, _ := rec["path"].(string); strings.ContainsAny(p, "\r\n") {
			t.Errorf("CR/LF must not reach logged request values: path=%q", p)
		}
	}
}
