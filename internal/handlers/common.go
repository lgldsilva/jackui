package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
)

// HTTP/cache/error constants shared with the handlers/local subpackage live in
// package httpshared to avoid an import cycle. The constants below are used
// only within package handlers.
const (
	HeaderContentDisp = "Content-Disposition"

	MIMEJSON  = "application/json"
	MIMEOctet = "application/octet-stream"

	ErrInvalidID         = "invalid id"
	ErrNotFound          = "not found"
	ErrNameRequired      = "name is required"
	ErrQueryRequired     = "query parameter 'q' is required"
	ErrFileIdxOutOfRange = "file index out of range"
	ErrNotPausable       = "cannot pause a finished download"
	ErrTMDBDisabled      = "tmdb disabled"
	// User-facing error messages for the WebAuthn (passkey) endpoints.
	ErrWebAuthnNotConfig  = "passkeys not configured"
	ErrWebAuthnNotConfigF = "passkeys not configured (set JACKUI_BASE_URL)"

	MagnetPrefix = "magnet:?xt=urn:btih:"
)

// RespondStoreError writes the standard error response for a store-layer
// failure: the not-found sentinel maps to 404 with the shared "not found"
// body, anything else stays a 500. Sweep e2e 2026-11-06 §3.2.6: mutation
// handlers leaked 500 for missing rows while the GETs already answered 404.
func RespondStoreError(c *gin.Context, err error, notFound error) {
	if errors.Is(err, notFound) {
		httpshared.RespondErrorMessage(c, http.StatusNotFound, ErrNotFound)
		return
	}
	httpshared.RespondError(c, http.StatusInternalServerError, err)
}
