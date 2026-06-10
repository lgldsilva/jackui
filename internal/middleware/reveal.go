package middleware

import "github.com/gin-gonic/gin"

// The "hidden curtain": a favourite folder can be marked hidden, and anything
// tied to it (by info_hash for torrents, by path for local files) is filtered
// out of every default listing — favourites, Continue Watching, downloads and
// the local browser. A session-only easter egg in the web UI flips the curtain
// open and tags requests with X-JackUI-Reveal-Hidden; handlers consult
// IsRevealHidden(c) and skip the filtering when it's set.
const (
	revealHeader = "X-JackUI-Reveal-Hidden"
	revealCtxKey = "reveal_hidden"
)

// RevealHidden reads X-JackUI-Reveal-Hidden from the request and tags the
// context when it's truthy. Mount on the /api group, alongside Incognito.
//
// EventSource can't set headers, so SSE/media callers pass ?revealHidden=1 in
// the query instead — accepted here for the same reason auth accepts ?token=.
func RevealHidden() gin.HandlerFunc {
	return func(c *gin.Context) {
		if isTruthy(c.GetHeader(revealHeader)) || isTruthy(c.Query("revealHidden")) {
			c.Set(revealCtxKey, true)
		}
		c.Next()
	}
}

// IsRevealHidden reports whether the current request opted into seeing hidden
// items. Defaults to false when the middleware isn't installed (unit tests).
func IsRevealHidden(c *gin.Context) bool {
	return c.GetBool(revealCtxKey)
}
