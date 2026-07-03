package streamer

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// isBlockedFetchIP reports whether an IP is off-limits for server-side fetches
// (SSRF protection): loopback, private RFC1918/ULA, link-local, and the
// unspecified address. .torrent URLs from indexers are public, so blocking
// these doesn't hurt legitimate use but stops a caller from making the server
// probe the internal homelab network or metadata endpoints.
func isBlockedFetchIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

func newSSRFGuardedClient(jackettHost string, capturedMagnet *string) *http.Client {
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: newSSRFTransport(jackettHost),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return checkRedirect(req, via, capturedMagnet)
		},
	}
}

func newSSRFTransport(jackettHost string) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return ssrfDialContext(ctx, network, addr, jackettHost)
		},
	}
}

func ssrfDialContext(ctx context.Context, network, addr, jackettHost string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	trusted := jackettHost != "" && host == jackettHost
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if !trusted {
		for _, ip := range ips {
			if isBlockedFetchIP(ip.IP) {
				return nil, fmt.Errorf("refusing to fetch from non-public address %s", ip.IP)
			}
		}
	}
	d := net.Dialer{}
	return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
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
