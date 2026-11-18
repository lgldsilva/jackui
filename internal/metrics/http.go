package metrics

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// unmatchedRoute labels requests that matched no registered route (404/405,
// SPA fallback, scanners). Using the raw URL here would make the series count
// unbounded and leak paths/tokens into the metrics.
const unmatchedRoute = "unmatched"

var (
	httpRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "jackui_http_requests_total",
		Help: "HTTP requests by method, route template (Gin FullPath) and status class",
	}, []string{"method", "route", "status_class"})

	// Buckets span API calls (ms) up to long media responses; a full stream
	// that outlives 60s lands in +Inf, which is expected for /api/stream/*.
	httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "jackui_http_request_duration_seconds",
		Help:    "HTTP request latency by method and route template (Gin FullPath)",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60},
	}, []string{"method", "route"})
)

var knownMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true, http.MethodOptions: true,
}

// HTTPMiddleware records request count and latency per route template. The
// route label is c.FullPath() (e.g. "/api/stream/:hash"), never the raw URL;
// unknown routes collapse to "unmatched" and unknown methods to "OTHER".
func HTTPMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = unmatchedRoute
		}
		method := c.Request.Method
		if !knownMethods[method] {
			method = "OTHER"
		}
		httpRequests.WithLabelValues(method, route, statusClass(c.Writer.Status())).Inc()
		httpDuration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
	}
}

func statusClass(code int) string {
	switch {
	case code >= 100 && code < 200:
		return "1xx"
	case code < 300 && code >= 200:
		return "2xx"
	case code < 400 && code >= 300:
		return "3xx"
	case code < 500 && code >= 400:
		return "4xx"
	case code < 600 && code >= 500:
		return "5xx"
	}
	return "other"
}
