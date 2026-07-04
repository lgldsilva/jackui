package handlers

// HTTP/cache/error constants shared with the handlers/local subpackage live in
// package httpshared to avoid an import cycle. The constants below are used
// only within package handlers.
const (
	HeaderContentDisp = "Content-Disposition"

	MIMEJSON  = "application/json"
	MIMEOctet = "application/octet-stream"

	ErrInvalidID          = "invalid id"
	ErrNotFound           = "not found"
	ErrNameRequired       = "name is required"
	ErrQueryRequired      = "query parameter 'q' is required"
	ErrFileIdxOutOfRange  = "file index out of range"
	ErrTMDBDisabled       = "tmdb disabled"
	ErrPasskeysNotConfig  = "passkeys não configuradas"
	ErrPasskeysNotConfigF = "passkeys não configuradas (defina JACKUI_BASE_URL)"

	MagnetPrefix = "magnet:?xt=urn:btih:"
)
