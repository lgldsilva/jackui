package streamer

import "errors"

// ErrTorrentNotActive is the sentinel returned by streamer methods when the
// requested torrent isn't loaded in the active set. Handlers match it with
// errors.Is to map to HTTP 404; wrap with %w when adding context.
var ErrTorrentNotActive = errors.New("torrent not active")

const (
	ErrFileIndexOutOfRange = "file index out of range"
	ErrFavoritesUnavail    = "favorites store unavailable"
)
