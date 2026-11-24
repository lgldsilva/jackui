// Package edgeproxy forwards requests from an edge node to the primary.
//
// An edge shares the primary's database but not its disks: downloads, their
// destinations, the Transfers dock and the local-files browser all refer to the
// primary's filesystem and are acted on by the primary's worker. The edge
// therefore relays those routes verbatim (method, path, query, body, auth
// headers) and streams the response back unbuffered so media Range reads work.
package edgeproxy

import (
	"errors"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
)

// HeaderForwardedBy marks a proxied request so a misconfigured primary that
// points back at itself (or at another edge) fails fast instead of looping.
const HeaderForwardedBy = "X-JackUI-Edge-Forwarded"

// New returns a gin handler that relays the request to primaryURL.
func New(primaryURL string) (gin.HandlerFunc, error) {
	target, err := url.Parse(primaryURL)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		return nil, errors.New("edgeproxy: primary URL must be an absolute http(s) URL")
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.SetXForwarded()
			r.Out.Host = target.Host
			r.Out.Header.Set(HeaderForwardedBy, "1")
		},
		// -1 flushes every write: media bodies and long JSON lists stream through
		// instead of being buffered on the edge.
		FlushInterval: -1,
		Transport: &http.Transport{
			Proxy: nil,
			// Above the primary's own slowest synchronous waits (local HLS
			// master up to 60s, duplicate/walk scans), so the primary's
			// diagnosis reaches the client instead of a bare 502.
			ResponseHeaderTimeout: 3 * time.Minute,
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConnsPerHost:   16,
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("edgeproxy: %s %s → primary failed: %v", r.Method, r.URL.Path, err)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"primary unavailable"}`))
		},
	}
	return func(c *gin.Context) {
		if c.GetHeader(HeaderForwardedBy) != "" {
			c.AbortWithStatusJSON(http.StatusLoopDetected, gin.H{"error": "edge proxy loop: primary URL points at an edge node"})
			return
		}
		rp.ServeHTTP(unwrapWriter{c.Writer}, c.Request)
		c.Abort()
	}, nil
}

// unwrapWriter hides gin's CloseNotify (which panics when the underlying writer
// lacks it) from ReverseProxy's legacy CloseNotifier probe; cancellation still
// flows through the request context, and Unwrap keeps Flush reachable for
// http.ResponseController.
type unwrapWriter struct{ w gin.ResponseWriter }

func (u unwrapWriter) Header() http.Header         { return u.w.Header() }
func (u unwrapWriter) Write(b []byte) (int, error) { return u.w.Write(b) }
func (u unwrapWriter) WriteHeader(code int)        { u.w.WriteHeader(code) }
func (u unwrapWriter) Unwrap() http.ResponseWriter { return u.w }
