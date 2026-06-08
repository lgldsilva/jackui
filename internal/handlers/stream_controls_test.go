package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// streamRouter wires every Transmission-style endpoint to a fresh streamer
// stub. We deliberately avoid streamer.New() because that opens a UDP socket on
// :42069 which collides between parallel test packages and a running dev
// server. The handlers we test here only touch the active map and the
// limiters, both of which streamer.NewForTesting initializes for us.
func streamRouter(t *testing.T) (*gin.Engine, *streamer.Streamer) {
	t.Helper()
	s := streamer.NewForTesting()

	r := gin.New()
	r.GET("/api/stream/active", StreamActive(s))
	r.POST("/api/stream/active/pause", StreamPauseAll(s))
	r.POST("/api/stream/active/resume", StreamResumeAll(s))
	r.GET("/api/stream/limits", StreamGetLimits(s))
	r.POST("/api/stream/limits", StreamSetLimits(s))
	r.POST("/api/stream/:hash/pause", StreamPause(s))
	r.POST("/api/stream/:hash/resume", StreamResume(s))
	r.POST("/api/stream/:hash/priority", StreamSetPriority(s))
	return r, s
}

func TestStreamActive_EmptyList(t *testing.T) {
	r, _ := streamRouter(t)
	req := httptest.NewRequest("GET", "/api/stream/active", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got []any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty list, got %d", len(got))
	}
}

func TestStreamPause_NotActive_Returns404(t *testing.T) {
	r, _ := streamRouter(t)
	// 40-char hex hash that isn't loaded
	req := httptest.NewRequest("POST", "/api/stream/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/pause", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

func TestStreamResume_NotActive_Returns404(t *testing.T) {
	r, _ := streamRouter(t)
	req := httptest.NewRequest("POST", "/api/stream/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/resume", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestStreamPause_BadHash_Returns400(t *testing.T) {
	r, _ := streamRouter(t)
	req := httptest.NewRequest("POST", "/api/stream/nothex/pause", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestStreamSetPriority_BadValue(t *testing.T) {
	r, _ := streamRouter(t)
	body := []byte(`{"priority":"bogus"}`)
	req := httptest.NewRequest("POST", "/api/stream/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/priority", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (invalid label)", w.Code)
	}
}

func TestStreamSetPriority_Accepted(t *testing.T) {
	r, _ := streamRouter(t)
	// Even if torrent isn't active, validating label happens AFTER the lookup
	// so we expect 404, not 400.
	for _, label := range []string{"low", "normal", "high"} {
		body := []byte(`{"priority":"` + label + `"}`)
		req := httptest.NewRequest("POST", "/api/stream/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/priority", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		// Not active -> 400 from the streamer error (wrapped). Either 400/404 is
		// acceptable here; what we're checking is that the *label* parsed.
		if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
			t.Errorf("label=%s: status=%d, body=%s", label, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "invalid priority") {
			t.Errorf("label=%s: should have parsed; body=%s", label, w.Body.String())
		}
	}
}

func TestStreamLimits_GetSet(t *testing.T) {
	r, _ := streamRouter(t)

	// Initial: unlimited (0/0)
	req := httptest.NewRequest("GET", "/api/stream/limits", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET status=%d", w.Code)
	}
	var got map[string]int64
	json.Unmarshal(w.Body.Bytes(), &got)
	if got["down"] != 0 || got["up"] != 0 {
		t.Errorf("initial limits = %v, want {0,0}", got)
	}

	// POST 1MB/s down, 512KB/s up
	body := []byte(`{"down":1048576,"up":524288}`)
	req = httptest.NewRequest("POST", "/api/stream/limits", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST status=%d, body=%s", w.Code, w.Body.String())
	}

	// Verify GET returns the new values
	req = httptest.NewRequest("GET", "/api/stream/limits", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	json.Unmarshal(w.Body.Bytes(), &got)
	if got["down"] != 1048576 || got["up"] != 524288 {
		t.Errorf("after POST limits = %v, want {1048576,524288}", got)
	}
}

func TestStreamLimits_NegativeRejected(t *testing.T) {
	r, _ := streamRouter(t)
	body := []byte(`{"down":-1,"up":0}`)
	req := httptest.NewRequest("POST", "/api/stream/limits", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestStreamActiveBulk_EmptyNoop(t *testing.T) {
	r, _ := streamRouter(t)
	// No active torrents — these should still 200 with count=0
	for _, path := range []string{"/api/stream/active/pause", "/api/stream/active/resume"} {
		req := httptest.NewRequest("POST", path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s status=%d", path, w.Code)
		}
	}
}
