package streamer

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lgldsilva/jackui/internal/netguard"
)

// isBlockedFetchIP is the shared SSRF address policy (see internal/netguard).
func isBlockedFetchIP(ip net.IP) bool { return netguard.IsBlockedIP(ip) }

// JackettAddrFromURL returns the normalised host:port the SSRF guard trusts
// for a Jackett base URL, defaulting the port from the scheme.
func JackettAddrFromURL(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	return jackettAddrFromParsed(u)
}

func jackettAddrFromParsed(u *url.URL) (string, bool) {
	host := u.Hostname()
	if host == "" {
		return "", false
	}
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "http":
			port = "80"
		case "https":
			port = "443"
		default:
			return "", false
		}
	}
	return net.JoinHostPort(strings.ToLower(host), port), true
}

func newSSRFGuardedClient(jackettAddr string, capturedMagnet *string) *http.Client {
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: newSSRFTransport(jackettAddr),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return checkRedirect(req, via, capturedMagnet)
		},
	}
}

func newSSRFTransport(jackettAddr string) *http.Transport {
	return netguard.NewTransport(jackettAddr)
}

func ssrfDialContext(ctx context.Context, network, addr, jackettAddr string) (net.Conn, error) {
	return netguard.DialContext(ctx, network, addr, jackettAddr)
}

func checkRedirect(req *http.Request, via []*http.Request, capturedMagnet *string) error {
	if strings.HasPrefix(strings.ToLower(req.URL.String()), magnetPrefix) {
		*capturedMagnet = req.URL.String()
		return http.ErrUseLastResponse
	}
	if len(via) >= 10 {
		return fmt.Errorf("too many redirects")
	}
	return nil
}
