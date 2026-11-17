package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/config"
)

const (
	testPeerAddr      = "203.0.113.7:40000"
	testPeerIP        = "203.0.113.7"
	testForwardedAddr = "198.51.100.23"
)

// clientIPThroughRouter builds the production router for cfg and reports what
// c.ClientIP() resolves to for a request from testPeerAddr that carries
// spoofable forwarding headers.
func clientIPThroughRouter(t *testing.T, cfg *config.Config) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := setupRouter(&appDeps{cfg: cfg})
	router.GET("/__test/client-ip", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })

	req := httptest.NewRequest(http.MethodGet, "/__test/client-ip", nil)
	req.RemoteAddr = testPeerAddr
	req.Header.Set("X-Forwarded-For", testForwardedAddr)
	req.Header.Set("X-Real-IP", testForwardedAddr)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

// Regression: gin trusts every proxy by default, so an anonymous client could
// pick its own ClientIP() via X-Forwarded-For and dodge the per-IP pairing cap.
func TestClientIPIgnoresForwardedHeadersByDefault(t *testing.T) {
	if got := clientIPThroughRouter(t, &config.Config{}); got != testPeerIP {
		t.Fatalf("ClientIP = %q, want socket peer %q (X-Forwarded-For must not be trusted)", got, testPeerIP)
	}
}

func TestClientIPHonoursForwardedHeadersFromTrustedProxy(t *testing.T) {
	cases := map[string][]string{
		"cidr":      {"203.0.113.0/24"},
		"single ip": {"10.0.0.1", testPeerIP},
	}
	for name, proxies := range cases {
		t.Run(name, func(t *testing.T) {
			got := clientIPThroughRouter(t, &config.Config{TrustedProxies: proxies})
			if got != testForwardedAddr {
				t.Fatalf("ClientIP = %q, want forwarded %q", got, testForwardedAddr)
			}
		})
	}
}

func TestClientIPIgnoresForwardedHeadersFromUntrustedPeer(t *testing.T) {
	got := clientIPThroughRouter(t, &config.Config{TrustedProxies: []string{"10.0.0.0/8"}})
	if got != testPeerIP {
		t.Fatalf("ClientIP = %q, want socket peer %q", got, testPeerIP)
	}
}

// An unparsable entry must fail closed: gin would otherwise keep trusting all.
func TestClientIPInvalidTrustedProxiesTrustsNone(t *testing.T) {
	got := clientIPThroughRouter(t, &config.Config{TrustedProxies: []string{"203.0.113.0/24", "not-an-ip"}})
	if got != testPeerIP {
		t.Fatalf("ClientIP = %q, want socket peer %q", got, testPeerIP)
	}
}
