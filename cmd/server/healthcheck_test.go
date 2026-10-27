package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthcheckURL(t *testing.T) {
	if got := healthcheckURL(""); got != "http://127.0.0.1:8989/healthz" {
		t.Fatalf("default port: got %q", got)
	}
	if got := healthcheckURL("9000"); got != "http://127.0.0.1:9000/healthz" {
		t.Fatalf("custom port: got %q", got)
	}
}

func TestProbeHealth(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	if err := probeHealth(ok.URL, time.Second); err != nil {
		t.Fatalf("healthy server: %v", err)
	}

	degraded := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer degraded.Close()
	if err := probeHealth(degraded.URL, time.Second); err == nil {
		t.Fatal("503 must fail the probe")
	}

	degraded.Close()
	if err := probeHealth(degraded.URL, time.Second); err == nil {
		t.Fatal("unreachable server must fail the probe")
	}
}
