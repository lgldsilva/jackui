package imagesearch

import (
	"net/http"
	"time"

	"github.com/lgldsilva/jackui/internal/netguard"
)

// newHTTPClient builds the client every source shares. Result URLs are
// attacker-influenced (any indexed page can point an image at the LAN, the
// loopback or a metadata endpoint), so each dial — redirects included — goes
// through the SSRF guard. trustedAddr is the one host:port exempt from it:
// always "" in production, set only by tests that fake a search engine on
// loopback.
func newHTTPClient(trustedAddr string) *http.Client {
	tr := netguard.NewTransport(trustedAddr)
	// A custom DialContext turns off Go's automatic HTTP/2; keep it so the
	// search front-ends still see a browser-like client.
	tr.ForceAttemptHTTP2 = true
	return &http.Client{Timeout: 12 * time.Second, Transport: tr}
}
