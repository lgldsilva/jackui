package main

import (
	"net/http"
	"strings"
)

// apiV1Prefix is the versioned API surface native clients pin to (issue #201).
// It is a transport-level alias: the wrapper rewrites it to the canonical
// /api path before the gin router runs, so every path-sensitive middleware
// (BodyLimit exemptions, Guest/Device restrictions, metrics route templates)
// keeps seeing exactly the paths it was built for. Rewriting at the net/http
// layer — not with a gin middleware — is what makes that possible: gin has
// already matched the route by the time gin middlewares run.
const apiV1Prefix = "/api/v1"

// WithAPIv1Alias wraps h so /api/v1/... requests are served by the /api/...
// routes. Query strings, headers and methods pass through untouched; only
// URL.Path changes. Non-API paths and look-alike prefixes (/api/v1foo) are
// never rewritten.
func WithAPIv1Alias(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rest, ok := strings.CutPrefix(r.URL.Path, apiV1Prefix); ok && (rest == "" || rest[0] == '/') {
			r.URL.Path = "/api" + rest
		}
		h.ServeHTTP(w, r)
	})
}
