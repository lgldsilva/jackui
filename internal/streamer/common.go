package streamer

import "errors"

// ErrTorrentNotActive is the sentinel returned by streamer methods when the
// requested torrent isn't loaded in the active set. Handlers match it with
// errors.Is to map to HTTP 404; wrap with %w when adding context.
var ErrTorrentNotActive = errors.New("torrent not active")

// ErrTorrentViewerActive is returned by explicit teardown (DropSeed) when a
// player still holds a viewer lease on the torrent: dropping it would kill an
// ongoing playback. Handlers map it to HTTP 409 so the UI can tell the user to
// close the player first instead of pretending the stop succeeded.
var ErrTorrentViewerActive = errors.New("torrent em uso: existe um player aberto assistindo este torrent")

// ErrTorrentDownloadProtected is returned by explicit teardown (DropSeed) when
// the torrent is registered as an active background download: dropping it
// would pull the rug from under the downloads worker mid-transfer.
var ErrTorrentDownloadProtected = errors.New("torrent protegido: download em background em andamento")

// IsDropRefusal reports whether err is a refusal from an explicit teardown
// (DropSeed): the torrent is still alive because something holds it (player
// viewer lease, background download). Handlers map it to HTTP 409.
// ErrTorrentNotActive is NOT a refusal — it means idempotent success.
func IsDropRefusal(err error) bool {
	return errors.Is(err, ErrTorrentViewerActive) || errors.Is(err, ErrTorrentDownloadProtected)
}

const (
	ErrFileIndexOutOfRange = "file index out of range"
	ErrFavoritesUnavail    = "favorites store unavailable"
)
