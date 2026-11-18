package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	alog "github.com/anacrolix/log"
	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/history"
	"github.com/lgldsilva/jackui/internal/library"
	"github.com/lgldsilva/jackui/internal/mailer"
	"github.com/lgldsilva/jackui/internal/playlists"
	"github.com/lgldsilva/jackui/internal/watchlist"
)

// captureLogger runs setupLogger with JACKUI_LOG_FORMAT=format while stdout
// is a pipe, runs fn, and returns every line written. Global logger state is
// restored afterwards.
func captureLogger(t *testing.T, format string, fn func()) []string {
	t.Helper()
	t.Setenv("JACKUI_LOG_FORMAT", format)
	origStdout, origSlog := os.Stdout, slog.Default()
	origGinOut, origGinErr := gin.DefaultWriter, gin.DefaultErrorWriter
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	// Production gin writes to os.Stdout; mirror that so text-mode access
	// lines land in the capture too.
	gin.DefaultWriter, gin.DefaultErrorWriter = w, w
	done := make(chan []byte)
	go func() { b, _ := io.ReadAll(r); done <- b }()
	setupLogger()
	func() {
		defer func() {
			os.Stdout = origStdout
			slog.SetDefault(origSlog)
			log.SetOutput(os.Stderr)
			log.SetFlags(log.LstdFlags)
			gin.DefaultWriter, gin.DefaultErrorWriter = origGinOut, origGinErr
			alog.Default.SetHandlers(alog.DefaultHandler)
		}()
		fn()
	}()
	_ = w.Close()
	out := <-done
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			lines = append(lines, sc.Text())
		}
	}
	return lines
}

func logTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := setupRouter(&appDeps{
		cfg:            &config.Config{},
		mlr:            mailer.New(config.SMTPConfig{}),
		historyStore:   &history.Store{},
		libraryStore:   &library.Store{},
		downloadsStore: &downloads.Store{},
		playlistsStore: &playlists.Store{},
		watchlistStore: &watchlist.Store{},
		authStore:      &auth.Store{},
	})
	router.GET("/logtest/item/:id", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	router.GET("/logtest/boom", func(*gin.Context) { panic("kaboom") })
	return router
}

func serve(router http.Handler, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func parseJSONLines(t *testing.T, lines []string) []map[string]any {
	t.Helper()
	var objs []map[string]any
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Errorf("non-JSON log line in json mode: %q", l)
			continue
		}
		if m["level"] == nil || m["msg"] == nil {
			t.Errorf("JSON line without level/msg: %q", l)
		}
		objs = append(objs, m)
	}
	return objs
}

func findLog(objs []map[string]any, pred func(map[string]any) bool) map[string]any {
	for _, o := range objs {
		if pred(o) {
			return o
		}
	}
	return nil
}

func TestJSONLogFormatEveryLineIsStructured(t *testing.T) {
	var okRec, boomRec, evilRec *httptest.ResponseRecorder
	lines := captureLogger(t, "json", func() {
		router := logTestRouter(t)
		log.Printf("transcode: ffmpeg FAILED: exit status 1\nstderr:\nboom")
		log.Printf("downloads: list active failed: timeout")
		log.Printf("JackUI starting")
		okRec = serve(router, "/logtest/item/42?token=SECRET", map[string]string{"X-Request-ID": "abc-123"})
		boomRec = serve(router, "/logtest/boom", nil)
		evilRec = serve(router, "/logtest/item/1", map[string]string{"X-Request-ID": "x\n{\"level\":\"INFO\"}"})
	})
	objs := parseJSONLines(t, lines)

	if got := okRec.Header().Get("X-Request-ID"); got != "abc-123" {
		t.Errorf("incoming X-Request-ID not echoed: %q", got)
	}
	if boomRec.Code != http.StatusInternalServerError {
		t.Errorf("panic route status=%d want 500", boomRec.Code)
	}
	evilID := evilRec.Header().Get("X-Request-ID")
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(evilID) {
		t.Errorf("malicious X-Request-ID not replaced: %q", evilID)
	}

	ff := findLog(objs, func(o map[string]any) bool { return strings.Contains(o["msg"].(string), "ffmpeg FAILED") })
	if ff == nil || ff["level"] != "ERROR" {
		t.Errorf("ffmpeg FAILED line: want level ERROR, got %v", ff)
	}
	lf := findLog(objs, func(o map[string]any) bool { return strings.Contains(o["msg"].(string), "list active failed") })
	if lf == nil || lf["level"] != "WARN" {
		t.Errorf("lower-case failure line: want level WARN, got %v", lf)
	}

	access := findLog(objs, func(o map[string]any) bool { return o["route"] == "/logtest/item/:id" && o["request_id"] == "abc-123" })
	if access == nil {
		t.Fatalf("no JSON access line with route template + request_id; lines=%v", lines)
	}
	if access["method"] != "GET" || access["status"] != float64(200) || access["latency_ms"] == nil || access["level"] != "INFO" {
		t.Errorf("access line missing fields: %v", access)
	}
	if strings.Contains(access["path"].(string), "SECRET") {
		t.Errorf("token leaked into access log: %v", access["path"])
	}

	panicLine := findLog(objs, func(o map[string]any) bool { return o["msg"] == "panic recovered" })
	if panicLine == nil || panicLine["level"] != "ERROR" || panicLine["request_id"] == "" || panicLine["stack"] == nil {
		t.Errorf("panic recovery line: want ERROR with request_id+stack, got %v", panicLine)
	}
}

// legacyTextPipeline is the logger setup shipped before structured JSON mode,
// kept verbatim so the text output is pinned byte-for-byte.
func legacyTextPipeline(w io.Writer) {
	handler := slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(handler))
	log.SetOutput(slog.NewLogLogger(handler, slog.LevelInfo).Writer())
	log.SetFlags(0)
}

var timeAttrRe = regexp.MustCompile(`^time=\S+ `)
var ginTimeRe = regexp.MustCompile(`^\[GIN\] \d{4}/\d{2}/\d{2} - \d{2}:\d{2}:\d{2} \|\s+(\d+) \|\s+\S+ \|`)

func TestTextLogFormatUnchanged(t *testing.T) {
	msgs := []string{"transcode: ffmpeg FAILED: x", "hello \"quoted\" world", "WARNING: thing"}

	var legacy bytes.Buffer
	origSlog := slog.Default()
	legacyTextPipeline(&legacy)
	for _, m := range msgs {
		log.Print(m)
	}
	slog.SetDefault(origSlog)
	log.SetOutput(os.Stderr)

	var rec *httptest.ResponseRecorder
	lines := captureLogger(t, "", func() {
		for _, m := range msgs {
			log.Print(m)
		}
		rec = serve(logTestRouter(t), "/logtest/item/7", nil)
	})

	// setupRouter logs its own boot notice (mailer); only compare ours.
	lines = slices.DeleteFunc(lines, func(l string) bool { return strings.Contains(l, "Email (SMTP)") })
	want := strings.Split(strings.TrimSpace(legacy.String()), "\n")
	if len(lines) != len(want)+1 {
		t.Fatalf("got %d lines want %d: %q", len(lines), len(want)+1, lines)
	}
	for i := range want {
		if timeAttrRe.ReplaceAllString(lines[i], "") != timeAttrRe.ReplaceAllString(want[i], "") {
			t.Errorf("text line %d changed:\n got %q\nwant %q", i, lines[i], want[i])
		}
	}
	if !ginTimeRe.MatchString(lines[len(want)]) || !strings.HasSuffix(lines[len(want)], `GET     "/logtest/item/7"`) {
		t.Errorf("text access line changed: %q", lines[len(want)])
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("text mode should still echo a request id header")
	}
}
