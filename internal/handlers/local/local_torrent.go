package local

import (
	"strings"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// purgeLinkedTorrents tears down the torrent(s) tied to a deleted local path:
// drops the active torrent, wipes its piece cache, clears the favorite, and
// removes the download row. Returns how many rows were removed. Best-effort —
// a failure on one step doesn't abort the others.
func purgeLinkedTorrents(dls *downloads.Store, s *streamer.Streamer, linked []downloads.Download) int {
	removed := 0
	for _, d := range linked {
		dropTorrentFromStreamer(s, d)
		if err := dls.Delete(d.UserID, d.ID); err == nil {
			removed++
		}
	}
	return removed
}

// dropTorrentFromStreamer tears down a single torrent's streamer-side state:
// drops the active torrent, wipes its piece cache, and clears the favorite.
// Best-effort — each step is independent.
func dropTorrentFromStreamer(s *streamer.Streamer, d downloads.Download) {
	if s == nil {
		return
	}
	if d.InfoHash != "" {
		var h metainfo.Hash
		if err := h.FromHexString(d.InfoHash); err == nil {
			s.Drop(h)
		}
	}
	if d.Name == "" {
		return
	}
	_ = s.ClearEntry(d.Name) // wipe pieces from the cache (DataDir/<name>)
	if favs := s.Favorites(); favs != nil {
		_ = favs.Remove(d.Name, d.UserID, true)
	}
}

// relinkMovedTorrents keeps the torrent↔file link intact after a local file or
// folder is moved/renamed (promote, reclassify or move-between-mounts). For every
// download whose on-disk path sat under oldAbs it rewrites file_path to the new
// location — so the FilePathResolver (and thus the next play) resolves the file
// where it now lives — and drops the active torrent so the streamer re-resolves +
// re-verifies pieces at the new path on the next access (~free when the file is
// already complete), instead of holding a stale descriptor on the old path. The
// piece cache and the favorite (both keyed by torrent name, not file_path) are
// left untouched. Best-effort: a failure on one row doesn't abort the rest.
func relinkMovedTorrents(dls *downloads.Store, s *streamer.Streamer, oldAbs, newAbs string) int {
	if dls == nil {
		return 0
	}
	linked, _ := dls.FindByPathPrefix(oldAbs)
	relinked := 0
	for _, d := range linked {
		newPath := newAbs + strings.TrimPrefix(d.FilePath, oldAbs)
		if err := dls.SetFilePath(d.UserID, d.ID, newPath); err != nil {
			continue
		}
		if s != nil && d.InfoHash != "" {
			var h metainfo.Hash
			if err := h.FromHexString(d.InfoHash); err == nil {
				s.Drop(h)
			}
		}
		relinked++
	}
	return relinked
}
