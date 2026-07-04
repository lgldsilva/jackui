package httpshared

// HTTP/MIME/cache and error-message constants shared by package handlers and
// its handlers/local subpackage. They live here (not in package handlers) so
// local can reference them without importing handlers, which would re-create
// the import cycle. Constants used by only one side stay in their own package.
const (
	CacheControl        = "Cache-Control"
	ContentType         = "Content-Type"
	HeaderAuthorization = "Authorization"

	MIMEJPEG    = "image/jpeg"
	MIMEMPEGURL = "application/vnd.apple.mpegurl"
	MIMEVTT     = "text/vtt; charset=utf-8"

	CachePublicDay  = "public, max-age=86400"
	CachePublicYear = "public, max-age=31536000"
	CacheNoStore    = "no-store"
	CacheImmutable  = "public, max-age=86400, immutable"

	ErrFileNotFound       = "file not found"
	ErrPathIsDir          = "path is a directory"
	ErrInvalidData        = "dados inválidos"
	ErrSharedDirNotConfig = "JACKUI_SHARED_DIR não configurado"
)
