package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Happy path: gluetun control server answers → resolvePeerPort returns the
// forwarded port (takes precedence over JACKUI_PEER_PORT).
func TestResolvePeerPort_GluetunForwardedPort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/openvpn/portforwarded" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"port": 60053}`))
	}))
	defer srv.Close()
	t.Setenv("JACKUI_GLUETUN_CONTROL_URL", srv.URL)
	t.Setenv("JACKUI_PEER_PORT", "12345") // must be ignored when gluetun answers

	if got := resolvePeerPort(); got != 60053 {
		t.Fatalf("resolvePeerPort() = %d, want 60053", got)
	}
}

// Boot race: the control server fails the first attempt, then succeeds.
// resolvePeerPort must retry (not fall back) and return the port.
func TestResolvePeerPort_RetriesUntilReady(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable) // gluetun still starting
			return
		}
		_, _ = w.Write([]byte(`{"port": 51820}`))
	}))
	defer srv.Close()
	t.Setenv("JACKUI_GLUETUN_CONTROL_URL", srv.URL)

	if got := resolvePeerPort(); got != 51820 {
		t.Fatalf("resolvePeerPort() = %d, want 51820 (should retry past the first failure)", got)
	}
	if atomic.LoadInt32(&hits) < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", hits)
	}
}

// No gluetun: a fixed JACKUI_PEER_PORT is honored.
func TestResolvePeerPort_FixedEnvFallback(t *testing.T) {
	t.Setenv("JACKUI_GLUETUN_CONTROL_URL", "")
	t.Setenv("JACKUI_PEER_PORT", "51469")
	if got := resolvePeerPort(); got != 51469 {
		t.Fatalf("resolvePeerPort() = %d, want 51469", got)
	}
}

// Nothing configured → 0 (streamer then uses its default).
func TestResolvePeerPort_NoneConfigured(t *testing.T) {
	t.Setenv("JACKUI_GLUETUN_CONTROL_URL", "")
	t.Setenv("JACKUI_PEER_PORT", "")
	if got := resolvePeerPort(); got != 0 {
		t.Fatalf("resolvePeerPort() = %d, want 0", got)
	}
}
