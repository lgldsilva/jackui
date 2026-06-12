package streamer

import (
	"strings"

	"github.com/anacrolix/torrent/metainfo"
)

// FileRelPath resolves the torrent-relative path of file fileIdx — the same
// shape anacrolix File.Path() returns ("<name>/<sub>/<file>" on multi-file
// torrents, "<name>" on single-file ones). Sources, in order: the persisted
// metadata cache (filled on every Add), the cached .torrent on disk, and finally
// an already-active torrent. The first two need no activation; the third only
// reads a torrent the caller already activated. Returns "" when none knows it.
//
// Why it exists: a whole-torrent download persists ONE completed row whose
// file_path is a directory; mapping a *file index* into that tree needs the
// in-torrent path, and resolving it purely from a dead swarm would block
// playback despite the bytes being local. The active-torrent fallback covers a
// finished pack whose cache/metainfo wasn't persisted: once it's reactivated for
// playback, the path resolves and it plays from disk instead of re-downloading.
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
	if rel := s.fileRelPathFromMetainfo(h, fileIdx); rel != "" {
		return rel
	}
	return s.fileRelPathFromActive(h, fileIdx)
}

// fileRelPathFromActive reads File.Path() straight off an ALREADY-ACTIVE torrent.
// It does NOT activate anything — it only helps once the caller has the torrent
// loaded (e.g. after Add during playback). Returns "" when the torrent isn't
// active or the index is out of range.
func (s *Streamer) fileRelPathFromActive(h metainfo.Hash, fileIdx int) string {
	s.mu.Lock()
	e, ok := s.active[h]
	s.mu.Unlock()
	if !ok {
		return ""
	}
	files := e.t.Files()
	if fileIdx < 0 || fileIdx >= len(files) {
		return ""
	}
	return files[fileIdx].Path()
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
