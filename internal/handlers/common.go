package handlers

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
	// #nosec G101 -- false positive: error message constant, not a credential
	ErrPasskeysNotConfig = "passkeys not configured"
	// #nosec G101 -- false positive: error message constant, not a credential
	ErrPasskeysNotConfigF = "passkeys not configured (set JACKUI_BASE_URL)"

	MagnetPrefix = "magnet:?xt=urn:btih:"
)
