package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// bodyLimitRouter mounts BodyLimit with one exempt upload path; the handler
// reads the whole body so any mid-read cap failure surfaces as a 500.
func bodyLimitRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(BodyLimit(DefaultJSONBodyCap, func(path string) bool {
		return path == "/api/local/upload"
	}))
	readAll := func(c *gin.Context) {
		if _, err := io.ReadAll(c.Request.Body); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.Status(http.StatusOK)
	}
	r.POST("/api/thing", readAll)
	r.POST("/api/local/upload", readAll)
	return r
}

func TestBodyLimit_OversizedContentLength(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/thing", bytes.NewReader(make([]byte, DefaultJSONBodyCap+1)))
	bodyLimitRouter().ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
}

func TestBodyLimit_NormalBody(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/thing", strings.NewReader(`{"magnet":"magnet:?xt=urn:btih:abc"}`))
	bodyLimitRouter().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
}

func TestBodyLimit_ExemptRouteNotCapped(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/local/upload", bytes.NewReader(make([]byte, DefaultJSONBodyCap+1)))
	bodyLimitRouter().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("exempt route status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
}

// TestBodyLimit_ChunkedOverCap: a lied/absent Content-Length can't shortcut the
// cap — the MaxBytesReader fails the body read mid-handler.
func TestBodyLimit_ChunkedOverCap(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/thing", io.NopCloser(strings.NewReader(strings.Repeat("x", DefaultJSONBodyCap+1))))
	req.ContentLength = -1 // force the chunked path: no declared length
	bodyLimitRouter().ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "request body too large") {
		t.Fatalf("status = %d body = %q, want capped-read failure", w.Code, w.Body.String())
	}
}
