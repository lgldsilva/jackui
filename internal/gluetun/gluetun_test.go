package gluetun

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestForwardedPort(t *testing.T) {
	t.Run("returns the forwarded port", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/openvpn/portforwarded" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`{"port": 49152}`))
		}))
		defer srv.Close()
		p, err := ForwardedPort(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p != 49152 {
			t.Errorf("port = %d, want 49152", p)
		}
	})

	t.Run("errors when no port forwarded yet", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"port": 0}`))
		}))
		defer srv.Close()
		if _, err := ForwardedPort(context.Background(), srv.URL); err == nil {
			t.Error("expected error for port 0")
		}
	})

	t.Run("errors when control server unreachable", func(t *testing.T) {
		if _, err := ForwardedPort(context.Background(), "http://127.0.0.1:1"); err == nil {
			t.Error("expected error for unreachable server")
		}
	})
}
