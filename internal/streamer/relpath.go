package streamer

import (
	"strings"

	"github.com/anacrolix/torrent/metainfo"
)

// FileRelPath resolves the torrent-relative path of file fileIdx — the same
// shape anacrolix File.Path() returns ("<name>/<sub>/<file>" on multi-file
// torrents, "<name>" on single-file ones) — WITHOUT activating the torrent.
// Sources, in order: the persisted metadata cache (filled on every Add), then
// the cached .torrent on disk. Returns "" when neither knows the torrent.
//
// Why it exists: a whole-torrent download persists ONE completed row whose
// file_path is a directory; mapping a *file index* into that tree needs the
// in-torrent path, and resolving it from the live torrent would mean re-adding
// it to the swarm — the exact thing serving finished downloads from disk
// avoids (a dead swarm would block playback despite the bytes being local).
func (s *Streamer) FileRelPath(h metainfo.Hash, fileIdx int) string {
	if s == nil || fileIdx < 0 {
		return ""
	}
	if cm := s.cache.Get(h.HexString()); cm != nil {
		for _, f := range cm.Files {
			if f.Index == fileIdx {
				return f.Path
			}
		}
	}
	return s.fileRelPathFromMetainfo(h, fileIdx)
}

// fileRelPathFromMetainfo rebuilds File.Path() from a persisted .torrent:
// BestName joined with the file's BestPath — exactly how anacrolix constructs
// File.path — so the result matches the rel paths moveCompletedTree consumed.
func (s *Streamer) fileRelPathFromMetainfo(h metainfo.Hash, fileIdx int) string {
	mi := s.loadCachedMetainfo(h)
	if mi == nil {
		return ""
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return ""
	}
	files := info.UpvertedFiles()
	if fileIdx >= len(files) {
		return ""
	}
	return strings.Join(append([]string{info.BestName()}, files[fileIdx].BestPath()...), "/")
}
