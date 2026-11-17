// Package netguard holds the SSRF dial guard shared by every server-side fetch
// of a URL the server did not choose (indexer .torrent links, scraped image
// URLs). It lives outside internal/streamer so light packages can use it
// without importing the torrent stack.
package netguard

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// IsBlockedIP reports whether an IP is off-limits for server-side fetches
// (SSRF protection): loopback, private RFC1918/ULA, link-local, and the
// unspecified address. The fetched URLs are public, so blocking these doesn't
// hurt legitimate use but stops a caller from making the server probe the
// internal homelab network or metadata endpoints.
func IsBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// sameAddr compares two host:port strings case-insensitively on the host.
func sameAddr(a, b string) bool {
	ah, ap, err1 := net.SplitHostPort(a)
	bh, bp, err2 := net.SplitHostPort(b)
	return err1 == nil && err2 == nil && ap == bp && strings.EqualFold(ah, bh)
}

// NewTransport returns a transport whose every dial goes through DialContext,
// so redirects are re-validated too. trustedAddr is the single host:port
// allowed to resolve to a non-public address ("" = none).
func NewTransport(trustedAddr string) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return DialContext(ctx, network, addr, trustedAddr)
		},
	}
}

// DialContext resolves addr, refuses it when any resolved IP is non-public
// (unless addr is exactly trustedAddr), and dials the IP it validated so a DNS
// rebind between check and connect can't swap the target.
func DialContext(ctx context.Context, network, addr, trustedAddr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	trusted := trustedAddr != "" && sameAddr(addr, trustedAddr)
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if !trusted {
		for _, ip := range ips {
			if IsBlockedIP(ip.IP) {
				return nil, fmt.Errorf("refusing to fetch from non-public address %s", ip.IP)
			}
		}
	}
	d := net.Dialer{}
	return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
}
