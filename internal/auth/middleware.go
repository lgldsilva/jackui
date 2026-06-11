package auth

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	ctxUserKey   = "auth.user"
	ctxClaimsKey = "auth.claims"
)

// Required is the Gin middleware that rejects requests without a valid Bearer token.
// On success, the parsed Claims are attached to the context and available via FromCtx.
// Media tokens (scope="media") only valem em rotas de mídia chamadas via
// ?token=; rejeitadas aqui mesmo que a assinatura seja válida.
func Required(tm *TokenManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := extractToken(c)
		if raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing access token"})
			return
		}
		claims, err := tm.ParseAccess(raw)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		if claims.Scope == ScopeMedia && !isMediaPath(c.Request.URL.Path) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "media token not accepted here"})
			return
		}
		c.Set(ctxClaimsKey, claims)
		c.Next()
	}
}

// Optional attaches claims if a valid token is present but never blocks.
// Useful for endpoints where behavior changes based on auth state (e.g., admin sees more).
// Aplica o mesmo gate de scope que Required pra evitar elevação de privilégio
// silenciosa via media token em rotas sensíveis.
func Optional(tm *TokenManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := extractToken(c)
		if raw != "" {
			if claims, err := tm.ParseAccess(raw); err == nil {
				if claims.Scope == ScopeMedia && !isMediaPath(c.Request.URL.Path) {
					c.Next()
					return
				}
				c.Set(ctxClaimsKey, claims)
			}
		}
		c.Next()
	}
}

// AdminOnly aborts with 403 unless the request was authenticated as an admin.
// Must be chained after Required.
func AdminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := ClaimsFromCtx(c)
		if !ok || claims.Role != RoleAdmin {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin only"})
			return
		}
		c.Next()
	}
}

// guestViewerPath matches the player's viewer-lease route ONLY for a real
// 40-hex infohash — a plain "/viewer" suffix check would also match
// DELETE /api/stream/favorite/viewer (a favourite named "viewer").
var guestViewerPath = regexp.MustCompile(`^/api/stream/[0-9a-fA-F]{40}/viewer$`)

// guestStreamAllowed lists the ONLY mutating /api/stream endpoints a guest may
// call — the ones playback itself needs. Everything else under /api/stream
// (cache clear, torrent drop, favourites, folders, import, limits,
// pause/resume, priority) is destructive or global and stays blocked for a
// read-only role. A bare prefix exception here once let guests wipe the whole
// piece cache and delete shared favourites.
func guestStreamAllowed(method, path string) bool {
	switch method {
	case http.MethodPost:
		return path == "/api/stream/add" ||
			path == "/api/stream/add-file" ||
			strings.HasPrefix(path, "/api/stream/prefetch/") ||
			(strings.HasPrefix(path, "/api/stream/art/") && strings.HasSuffix(path, "/resolve")) ||
			guestViewerPath.MatchString(path)
	case http.MethodDelete:
		// Closing the viewer lease on player exit.
		return guestViewerPath.MatchString(path)
	default:
		return false
	}
}

// guestAuthSelfAllowed lists the self-service account endpoints a guest may
// call with mutating methods: own password/email, own sessions and the media
// token playback needs. Exact paths only — /api/auth/users* (admin surface)
// must stay blocked, and AdminOnly remains the second fence there anyway.
func guestAuthSelfAllowed(method, path string) bool {
	switch method {
	case http.MethodPost:
		return path == "/api/auth/password" ||
			path == "/api/auth/email" ||
			path == "/api/auth/sessions" ||
			path == "/api/auth/sessions/revoke-others" ||
			path == "/api/auth/media-token"
	case http.MethodDelete:
		// DELETE /api/auth/sessions/:id — exactly one extra segment, so nothing
		// nested deeper (or under another root) can ride this exception.
		rest, ok := strings.CutPrefix(path, "/api/auth/sessions/")
		return ok && rest != "" && !strings.Contains(rest, "/")
	default:
		return false
	}
}

// GuestRestrict blocks mutating methods (POST, DELETE, PUT, PATCH) for guests.
// Playback-only mutations under /api/stream are allowlisted via
// guestStreamAllowed; self-service account management via guestAuthSelfAllowed.
// /api/local/file is NOT exempt: its only mutating method
// is DELETE (LocalDelete), which a read-only guest must never reach. GET on any
// media route is already unaffected (it isn't a mutating method).
func GuestRestrict() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := ClaimsFromCtx(c)
		if !ok {
			c.Next()
			return
		}
		if claims.Role == RoleGuest {
			method := c.Request.Method
			if method == http.MethodPost || method == http.MethodDelete || method == http.MethodPut || method == http.MethodPatch {
				if guestStreamAllowed(method, c.Request.URL.Path) || guestAuthSelfAllowed(method, c.Request.URL.Path) {
					c.Next()
					return
				}
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "convidados não têm permissão para modificar recursos"})
				return
			}
		}
		c.Next()
	}
}

// ClaimsFromCtx retrieves the authenticated Claims previously attached by Required/Optional.
func ClaimsFromCtx(c *gin.Context) (*Claims, bool) {
	v, ok := c.Get(ctxClaimsKey)
	if !ok {
		return nil, false
	}
	claims, ok := v.(*Claims)
	return claims, ok
}

// UserIDFromCtx returns (userID, isAdmin, isAuthenticated). Use in handlers that filter by ownership.
func UserIDFromCtx(c *gin.Context) (int, bool, bool) {
	claims, ok := ClaimsFromCtx(c)
	if !ok {
		return 0, false, false
	}
	return claims.UserID, claims.Role == RoleAdmin, true
}

func extractToken(c *gin.Context) string {
	auth := c.GetHeader(HeaderAuthorization)
	if strings.HasPrefix(auth, BearerPrefix) {
		return strings.TrimPrefix(auth, BearerPrefix)
	}
	// Fallback: ?token=... in query — ONLY for media routes, where the element
	// loading the URL (<video src>/<track src>/<img>) can't set an Authorization
	// header. Everywhere else the JS client sends the header, so accepting the
	// token in the query there only widened the leak surface (it lands in the
	// gin access log and in downloaded .m3u files for sensitive endpoints like
	// /api/config and /api/download). Restrict it to /api/stream/*.
	if t := c.Query("token"); t != "" && isMediaPath(c.Request.URL.Path) {
		return t
	}
	return ""
}

// isMediaPath matches the routes loaded by browser primitives that CAN'T set an
// Authorization header, so they genuinely need the ?token= fallback:
//   - /api/stream/*            <video>/<track>/<img>: file, HLS, subtrack, thumb, art
//   - /api/subtitles/download/* external (OpenSubtitles) VTT loaded via <track>
//   - /api/local/file          local-filesystem file served to <video>
//   - /api/local/thumb         local-file frame preview loaded via <img>
//   - /api/local/hls/*         local-file HLS playlist + segments served to <video>
//   - /api/local/sidecar       local-file sidecar (.srt/.vtt) read by <track>
//   - /api/local/subtrack      local-file embedded subtitle extracted to VTT for <track>
//   - /api/search/stream       EventSource (SSE) search — EventSource has no way
//                              to set headers, so the token must ride the query.
//   - /api/preview/*           universal viewer: comic pages, EPUB chapter
//                              iframes and archive-inner images load via
//                              <img>/<iframe>, headerless by nature.
func isMediaPath(path string) bool {
	return strings.HasPrefix(path, "/api/stream/") ||
		strings.HasPrefix(path, "/api/preview/") ||
		strings.HasPrefix(path, "/api/subtitles/download/") ||
		strings.HasPrefix(path, "/api/local/file") ||
		strings.HasPrefix(path, "/api/local/thumb") ||
		strings.HasPrefix(path, "/api/local/hls/") ||
		strings.HasPrefix(path, "/api/local/sidecar") ||
		strings.HasPrefix(path, "/api/local/subtrack") ||
		strings.HasPrefix(path, "/api/search/stream")
}
