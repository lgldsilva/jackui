package handlers

const (
	CacheControl        = "Cache-Control"
	ContentType         = "Content-Type"
	HeaderAuthorization = "Authorization"
	HeaderContentDisp   = "Content-Disposition"

	MIMEJSON      = "application/json"
	MIMEMPEGURL   = "application/vnd.apple.mpegurl"
	MIMEOctet     = "application/octet-stream"
	MIMEJPEG      = "image/jpeg"
	MIMEVTT       = "text/vtt; charset=utf-8"

	CachePublicDay = "public, max-age=86400"
	CacheNoStore   = "no-store"
	CacheImmutable = "public, max-age=86400, immutable"

	ffHideBanner = "-hide_banner"
	ffLogLevel   = "-loglevel"
	pipe1        = "pipe:1"

	ErrInvalidID          = "invalid id"
	ErrFileNotFound       = "file not found"
	ErrNotFound           = "not found"
	ErrNameRequired       = "name is required"
	ErrPathIsDir          = "path is a directory"
	ErrQueryRequired      = "query parameter 'q' is required"
	ErrFileIdxOutOfRange  = "file index out of range"
	ErrTMDBDisabled       = "tmdb disabled"
	ErrPasskeysNotConfig  = "passkeys não configuradas"
	ErrInvalidData        = "dados inválidos"
	ErrPasskeysNotConfigF = "passkeys não configuradas (defina JACKUI_BASE_URL)"
)
