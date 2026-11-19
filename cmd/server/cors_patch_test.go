package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// Native clients (issue #201) send PATCH requests (favorites folders,
// library resume, playlists) and custom X-JackUI-* headers; the router-wide
// CORS policy must advertise both or browsers drop the request pre-flight.

func TestCORSAllowsPatchAndJackUIHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(corsMiddleware())
	r.PATCH("/api/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })

	req := httptest.NewRequest(http.MethodOptions, "/api/ping", nil)
	req.Header.Set("Origin", "https://other.example")
	req.Header.Set("Access-Control-Request-Method", "PATCH")
	req.Header.Set("Access-Control-Request-Headers", "x-jackui-device")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", w.Code)
	}
	methods := w.Header().Get("Access-Control-Allow-Methods")
	if !strings.Contains(methods, "PATCH") {
		t.Errorf("Access-Control-Allow-Methods = %q, want PATCH", methods)
	}
	headers := w.Header().Get("Access-Control-Allow-Headers")
	if !strings.Contains(strings.ToLower(headers), "x-jackui-*") {
		t.Errorf("Access-Control-Allow-Headers = %q, want X-JackUI-*", headers)
	}

	// The method itself must be usable cross-origin, not just advertised.
	patch := httptest.NewRequest(http.MethodPatch, "/api/ping", nil)
	patch.Header.Set("Origin", "https://other.example")
	pw := httptest.NewRecorder()
	r.ServeHTTP(pw, patch)
	if pw.Code != http.StatusOK || pw.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("PATCH = %d ACAO=%q, want 200/*", pw.Code, pw.Header().Get("Access-Control-Allow-Origin"))
	}
}
