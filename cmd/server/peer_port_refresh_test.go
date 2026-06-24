package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/streamer"
)

func doRefresh(t *testing.T, ctrlURL, authHeader string, restart chan struct{}) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/r", peerPortRefreshHandler(ctrlURL, streamer.NewForTesting(), restart))
	req := httptest.NewRequest("POST", "/r", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestPeerPortRefresh_NoTokenConfigured(t *testing.T) {
	t.Setenv("JACKUI_CONTROL_TOKEN", "")
	w := doRefresh(t, "", "", nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (token not configured)", w.Code)
	}
}

func TestPeerPortRefresh_WrongToken(t *testing.T) {
	t.Setenv("JACKUI_CONTROL_TOKEN", "s3cret")
	w := doRefresh(t, "", "Bearer nope", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestPeerPortRefresh_NotBehindGluetun(t *testing.T) {
	t.Setenv("JACKUI_CONTROL_TOKEN", "s3cret")
	w := doRefresh(t, "", "Bearer s3cret", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

// Correct token + gluetun reporting a port different from the streamer's (0 for
// NewForTesting) → triggers a restart signal and reports changed.
func TestPeerPortRefresh_ChangedTriggersRestart(t *testing.T) {
	t.Setenv("JACKUI_CONTROL_TOKEN", "s3cret")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"port": 60053}`))
	}))
	defer srv.Close()
	restart := make(chan struct{}, 1)
	w := doRefresh(t, srv.URL, "Bearer s3cret", restart)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body.String())
	}
	select {
	case <-restart:
		// expected: a restart was signaled because 60053 != 0
	default:
		t.Fatal("expected a restart signal on port change")
	}
}
