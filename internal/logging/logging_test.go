package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"os"
	"strings"
	"testing"

	alog "github.com/anacrolix/log"
)

func TestInferLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"transcode: ffmpeg FAILED: exit 1":            slog.LevelError,
		"ERROR: boom":                                 slog.LevelError,
		"FATAL: cannot bind":                          slog.LevelError,
		"WARNING: auth disabled":                      slog.LevelWarn,
		"Warning: tmdb client init: x":                slog.LevelWarn,
		"downloads: list active failed: timeout":      slog.LevelWarn,
		"streamer: piece verify I/O error (key=x): y": slog.LevelWarn,
		"ntfy request err: eof":                       slog.LevelWarn,
		"batchDelete total=3 deleted=2 failed=1":      slog.LevelInfo,
		"JackUI starting on :8989":                    slog.LevelInfo,
		"errors are counted elsewhere":                slog.LevelInfo,
	}
	for msg, want := range cases {
		if got := InferLevel(msg); got != want {
			t.Errorf("InferLevel(%q)=%v want %v", msg, got, want)
		}
	}
}

func decode(t *testing.T, b *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not JSON: %q", l)
		}
		out = append(out, m)
	}
	return out
}

func TestLineWriter(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	w := NewLineWriter(h, "gin")
	for _, s := range []string{"\n", "info is filtered\n", "x failed: y\nmore\n"} {
		if n, err := w.Write([]byte(s)); err != nil || n != len(s) {
			t.Fatalf("Write(%q)=%d,%v", s, n, err)
		}
	}
	got := decode(t, &buf)
	if len(got) != 1 || got[0]["level"] != "WARN" || got[0]["msg"] != "x failed: y\nmore" || got[0]["logger"] != "gin" {
		t.Fatalf("unexpected records: %v", got)
	}
}

func TestContextHandlerAddsRequestIDOnce(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(ContextHandler{slog.NewJSONHandler(&buf, nil)}).With("k", "v").WithGroup("g")
	ctx := WithRequestID(context.Background(), "rid-1")
	l.InfoContext(ctx, "a")
	l.InfoContext(ctx, "b", "request_id", "explicit")
	l.Info("c")
	got := decode(t, &buf)
	if len(got) != 3 {
		t.Fatalf("records=%d", len(got))
	}
	if g, _ := got[0]["g"].(map[string]any); g == nil || g["request_id"] != "rid-1" {
		t.Errorf("request_id not attached: %v", got[0])
	}
	if strings.Count(buf.String(), "request_id") != 2 {
		t.Errorf("request_id duplicated or missing: %s", buf.String())
	}
	if RequestID(nil) != "" || RequestID(context.Background()) != "" { //nolint:staticcheck // nil ctx is a supported input
		t.Error("RequestID on empty ctx should be empty")
	}
}

func TestAnacrolixHandler(t *testing.T) {
	var buf bytes.Buffer
	h := AnacrolixHandler{H: slog.NewJSONHandler(&buf, nil)}
	h.Handle(alog.Record{Msg: alog.Str("dht down"), Level: alog.Critical, Names: []string{"dht"}})
	h.Handle(alog.Record{Msg: alog.Str("warn"), Level: alog.Warning})
	h.Handle(alog.Record{Msg: alog.Str("unset"), Level: alog.NotSet})
	h.Handle(alog.Record{Msg: alog.Str("dbg"), Level: alog.Debug}) // below Info: dropped
	got := decode(t, &buf)
	if len(got) != 3 || got[0]["level"] != "ERROR" || got[1]["level"] != "WARN" || got[2]["level"] != "INFO" || got[0]["logger"] != "anacrolix" {
		t.Fatalf("unexpected records: %v", got)
	}
	if anacrolixLevel(alog.Debug) != slog.LevelDebug {
		t.Error("debug mapping")
	}
}

func TestSetupModes(t *testing.T) {
	origSlog := slog.Default()
	t.Cleanup(func() {
		slog.SetDefault(origSlog)
		log.SetOutput(os.Stderr)
		alog.Default.SetHandlers(alog.DefaultHandler)
		jsonMode.Store(false)
	})

	var buf bytes.Buffer
	Setup("json", &buf)
	if !JSONEnabled() {
		t.Fatal("json mode not flagged")
	}
	log.Printf("ffmpeg FAILED: x")
	alog.Default.Levelf(alog.Error, "tracker announce error")
	got := decode(t, &buf)
	if len(got) != 2 || got[0]["level"] != "ERROR" || got[1]["logger"] != "anacrolix" {
		t.Fatalf("json setup records: %v", got)
	}

	buf.Reset()
	Setup("", &buf)
	if JSONEnabled() {
		t.Fatal("text mode flagged as json")
	}
	log.Printf("hello")
	if !strings.Contains(buf.String(), `level=INFO msg=hello`) {
		t.Fatalf("text output: %q", buf.String())
	}
}
