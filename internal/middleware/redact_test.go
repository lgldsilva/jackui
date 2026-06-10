package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRedactToken(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"token no fim da query",
			"/api/stream/hls/abc/0/master.m3u8?token=eyJhbGciOi.PAYLOAD.SIG",
			"/api/stream/hls/abc/0/master.m3u8?token=REDACTED"},
		{"token no meio da query",
			"/api/local/file?token=SECRET123&path=movie.mkv",
			"/api/local/file?token=REDACTED&path=movie.mkv"},
		{"sem token", "/api/search?q=matrix", "/api/search?q=matrix"},
		{"múltiplos params token",
			"/x?token=AAA&y=1&token=BBB",
			"/x?token=REDACTED&y=1&token=REDACTED"},
		{"native_hls preservado",
			"/seg_00001.ts?token=JWT&native_hls=1",
			"/seg_00001.ts?token=REDACTED&native_hls=1"},
		{"dentro de texto livre (diag)",
			`data=map[src:/api/stream/x?token=ABC.DEF]`,
			`data=map[src:/api/stream/x?token=REDACTED]`},
		{"string vazia", "", ""},
	}
	for _, c := range cases {
		if got := RedactToken(c.in); got != c.want {
			t.Errorf("%s: RedactToken(%q)=%q want %q", c.name, c.in, got, c.want)
		}
	}
}

// End-to-end through gin's logger: a media-style request with ?token=<secret>
// must produce an access-log line with the token masked and everything else
// (method, status, rest of the query) intact.
func TestRedactingLogFormatter_AccessLog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var buf bytes.Buffer
	r := gin.New()
	r.Use(gin.LoggerWithConfig(gin.LoggerConfig{Formatter: RedactingLogFormatter, Output: &buf}))
	r.GET("/api/stream/hls/abc/0/master.m3u8", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET",
		"/api/stream/hls/abc/0/master.m3u8?token=eyJSECRETJWT.x.y&native_hls=1", nil))

	line := buf.String()
	if strings.Contains(line, "eyJSECRETJWT") {
		t.Fatalf("JWT leaked into the access log: %s", line)
	}
	if !strings.Contains(line, "token=REDACTED") {
		t.Errorf("expected token=REDACTED in the log line: %s", line)
	}
	if !strings.Contains(line, "native_hls=1") {
		t.Errorf("non-credential query params must survive redaction: %s", line)
	}
	if !strings.Contains(line, "GET") || !strings.Contains(line, "200") {
		t.Errorf("log line must keep method and status (gin-default shape): %s", line)
	}
	if !strings.Contains(line, "/api/stream/hls/abc/0/master.m3u8") {
		t.Errorf("log line must keep the path: %s", line)
	}
}

// Latencies over a minute are truncated to whole seconds, same as gin's
// default formatter (keeps the column readable on slow streaming requests).
func TestRedactingLogFormatter_TruncatesLongLatency(t *testing.T) {
	line := RedactingLogFormatter(gin.LogFormatterParams{
		TimeStamp:  time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC),
		StatusCode: http.StatusOK,
		Latency:    90*time.Second + 123*time.Millisecond,
		ClientIP:   "10.0.0.1",
		Method:     "GET",
		Path:       "/api/stream/hls/x?token=SECRET",
	})
	if !strings.Contains(line, "1m30s") {
		t.Errorf("latency must be truncated to seconds: %s", line)
	}
	if strings.Contains(line, "SECRET") || !strings.Contains(line, "token=REDACTED") {
		t.Errorf("token must be redacted: %s", line)
	}
}

// Requests without a token pass through byte-identical (the common case —
// zero behaviour change for non-media routes).
func TestRedactingLogFormatter_NoToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var buf bytes.Buffer
	r := gin.New()
	r.Use(gin.LoggerWithConfig(gin.LoggerConfig{Formatter: RedactingLogFormatter, Output: &buf}))
	r.GET("/api/search", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/search?q=matrix", nil))

	line := buf.String()
	if !strings.Contains(line, "/api/search?q=matrix") {
		t.Errorf("untokened path must be logged verbatim: %s", line)
	}
	if strings.Contains(line, "REDACTED") {
		t.Errorf("nothing to redact here: %s", line)
	}
}
