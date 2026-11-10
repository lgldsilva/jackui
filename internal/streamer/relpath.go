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
	p, _, _ := s.cachedFileMeta(h, fileIdx)
	return p
}

// CachedFileSize returns the length the torrent's metadata declares for file
// fileIdx, from the same sources as FileRelPath (metadata cache, then the
// cached .torrent) and without activating the torrent. ok=false when the
// torrent or the index is unknown.
//
// Callers that bind an arbitrary on-disk file to (hash, fileIdx) — the dedup
// link — must check it: a size mismatch means the file is not that torrent
// file, and serving or seeding it as such would hand out (or let anacrolix
// write over) the wrong bytes.
func (s *Streamer) CachedFileSize(h metainfo.Hash, fileIdx int) (int64, bool) {
	_, size, ok := s.cachedFileMeta(h, fileIdx)
	return size, ok
}

// cachedFileMeta resolves (relPath, size) of file fileIdx from the persisted
// metadata cache, falling back to the cached .torrent.
func (s *Streamer) cachedFileMeta(h metainfo.Hash, fileIdx int) (string, int64, bool) {
	if s == nil || fileIdx < 0 {
		return "", 0, false
	}
	if cm := s.cache.Get(h.HexString()); cm != nil {
		for _, f := range cm.Files {
			if f.Index == fileIdx {
				return f.Path, f.Size, true
			}
		}
	}
	return s.fileMetaFromMetainfo(h, fileIdx)
}

// fileMetaFromMetainfo rebuilds File.Path() from a persisted .torrent:
// BestName joined with the file's BestPath — exactly how anacrolix constructs
// File.path — so the result matches the rel paths moveCompletedTree consumed.
func (s *Streamer) fileMetaFromMetainfo(h metainfo.Hash, fileIdx int) (string, int64, bool) {
	mi := s.loadCachedMetainfo(h)
	if mi == nil {
		return "", 0, false
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return "", 0, false
	}
	files := info.UpvertedFiles()
	if fileIdx >= len(files) {
		return "", 0, false
	}
	f := files[fileIdx]
	return strings.Join(append([]string{info.BestName()}, f.BestPath()...), "/"), f.Length, true
}
