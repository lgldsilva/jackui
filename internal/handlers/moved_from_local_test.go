package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/lyrics"
	"github.com/lgldsilva/jackui/internal/mailer"
)

func TestNotifyWithMailer(t *testing.T) {
	mlr := mailer.New(config.SMTPConfig{})
	notify(mlr, "", "Subject", "Intro", "http://link")
}

func TestNotifyWithMailerAndTo(t *testing.T) {
	mlr := mailer.New(config.SMTPConfig{})
	notify(mlr, "test@example.com", "Subject", "Intro", "http://link")
}

func TestErrStr_Nil(t *testing.T) {
	if got := errStr(nil); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestBuildPromoteDests_Empty(t *testing.T) {
	dests := BuildPromoteDests("", nil)
	if len(dests) != 0 {
		t.Errorf("expected 0 dests, got %d", len(dests))
	}
}

func TestBuildPromoteDests_WithSharedDir(t *testing.T) {
	dests := BuildPromoteDests("/shared", nil)
	if len(dests) != 1 || dests[0].Name != "Biblioteca" {
		t.Errorf("expected [Biblioteca], got %v", dests)
	}
}

func TestBuildPromoteDests_WithExtra(t *testing.T) {
	dests := BuildPromoteDests("/shared", []httpshared.PromoteDest{{Name: "Extra", Path: "/extra"}})
	if len(dests) != 2 {
		t.Errorf("expected 2 dests, got %d", len(dests))
	}
}

func TestSetSSEHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/search/stream?q=test", nil)

	setSSEHeaders(c)

	if w.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", w.Header().Get("Content-Type"))
	}
	if w.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", w.Header().Get("Cache-Control"))
	}
	if w.Header().Get("X-Accel-Buffering") != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", w.Header().Get("X-Accel-Buffering"))
	}
}

func TestNotify_NilMailer(t *testing.T) {
	notify(nil, "test@example.com", "Subject", "Intro text", "http://link")
}

func TestWriteSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	writeSSE(c, "testevent", map[string]string{"key": "value"})

	body := w.Body.String()
	if !strings.Contains(body, "event: testevent") {
		t.Errorf("expected SSE event, got: %s", body)
	}
	if !strings.Contains(body, `"key":"value"`) {
		t.Errorf("expected JSON data, got: %s", body)
	}
}

func TestBaseURL_Configured(t *testing.T) {
	if got := baseURL(nil, "https://example.com"); got != "https://example.com" {
		t.Errorf("got %q, want 'https://example.com'", got)
	}
}

func TestBaseURL_OriginHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Request.Header.Set("Origin", "https://custom.origin")

	got := baseURL(c, "")
	if got != "https://custom.origin" {
		t.Errorf("got %q, want 'https://custom.origin'", got)
	}
}

func TestBaseURL_Fallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	got := baseURL(c, "")
	if !strings.HasPrefix(got, "http") {
		t.Errorf("expected http URL, got %q", got)
	}
}

func TestNormalizeKind(t *testing.T) {
	if normalizeKind("audio") != "audio" || normalizeKind("video") != "video" {
		t.Error("audio/video must pass through")
	}
	if normalizeKind("garbage") != "" || normalizeKind("") != "" {
		t.Error("non-whitelisted kind must normalize to empty")
	}
}

func TestLyricsGetGuards(t *testing.T) {
	w, c := ctxFor("GET", "/api/lyrics")
	LyricsGet(nil)(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("nil client must be 503, got %d", w.Code)
	}
}

func TestLyricsGetMissingTitle(t *testing.T) {
	w, c := ctxFor("GET", "/api/lyrics") // no title param
	LyricsGet(lyrics.New())(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing title must be 400, got %d", w.Code)
	}
}

func TestErrStr_WithError(t *testing.T) {
	if got := errStr(fmt.Errorf("some error")); got != "some error" {
		t.Errorf("got %q, want 'some error'", got)
	}
}

func ctxFor(method, target string) (*httptest.ResponseRecorder, *gin.Context) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, nil)
	return w, c
}
