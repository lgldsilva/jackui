package streamer

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// FileLayoutResolver resolves EVERY file of a torrent in one call. relPaths[i]
// is the torrent-relative path of file i (the shape FileRelPath returns); the
// result maps a file index to its real on-disk path and omits unresolved files.
type FileLayoutResolver func(hash metainfo.Hash, relPaths []string) map[int]string

// SetFileLayoutResolver registers the batch resolver. Wire it BEFORE
// SetFilePathResolver: HasFilePathResolver is the gate boot-time seeding polls.
func (s *Streamer) SetFileLayoutResolver(r FileLayoutResolver) {
	s.fileLayoutResolver = r
}

// relocatedLayout resolves the real path of every file ONCE per activation.
// anacrolix asks for each file's path separately while opening the torrent, and
// answering each through the per-file resolver costs a metadata decode of the
// whole file list plus up to two queries — quadratic on a large pack. The
// per-file resolver is only the fallback for a streamer wired without the batch
// one. Returns nil when file 0 does not resolve (nothing to relocate).
func (s *Streamer) relocatedLayout(info *metainfo.Info, hash metainfo.Hash) map[int]string {
	if info == nil {
		return nil
	}
	files := info.UpvertedFiles()
	if len(files) == 0 {
		return nil
	}
	if s.fileLayoutResolver != nil {
		return regularFilesOnly(s.fileLayoutResolver(hash, infoRelPaths(info, files)))
	}
	if s.filePathResolver == nil {
		return nil
	}
	layout := make(map[int]string, len(files))
	for i := range files {
		p, ok := s.filePathResolver(hash, i)
		if ok && p != "" {
			layout[i] = p
			continue
		}
		if i == 0 {
			return nil
		}
	}
	return layout
}

// infoRelPaths builds each file's torrent-relative path the way anacrolix builds
// File.Path() (BestName joined with BestPath), matching FileRelPath.
func infoRelPaths(info *metainfo.Info, files []metainfo.FileInfo) []string {
	name := info.BestName()
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = strings.Join(append([]string{name}, f.BestPath()...), "/")
	}
	return out
}

// regularFilesOnly drops entries that are not existing regular files, so a
// stale DB row never roots a torrent file at a missing path or a directory.
func regularFilesOnly(layout map[int]string) map[int]string {
	for i, p := range layout {
		if st, err := os.Stat(p); p == "" || err != nil || st.IsDir() {
			delete(layout, i)
		}
	}
	return layout
}

// fileIndexByPath maps each file's in-torrent path to its index in
// info.UpvertedFiles(); the first file wins on a duplicate path.
func fileIndexByPath(info *metainfo.Info) map[string]int {
	if info == nil {
		return nil
	}
	files := info.UpvertedFiles()
	byPath := make(map[string]int, len(files))
	for i, f := range files {
		key := filepath.Join(f.BestPath()...)
		if _, dup := byPath[key]; !dup {
			byPath[key] = i
		}
	}
	return byPath
}

// layoutFilePathMaker answers anacrolix's per-file path requests from the
// pre-resolved layout; onResolved (may be nil) is told which file indices were
// relocated so their pieces can be locked.
func (s *Streamer) layoutFilePathMaker(info *metainfo.Info, layout map[int]string, onResolved func(int)) storage.FilePathMaker {
	byPath := fileIndexByPath(info)
	return func(o storage.FilePathMakerOpts) string {
		if o.File != nil {
			if idx, known := byPath[filepath.Join(o.File.BestPath()...)]; known {
				if p := layout[idx]; p != "" {
					if onResolved != nil {
						onResolved(idx)
					}
					return strings.TrimPrefix(p, string(os.PathSeparator))
				}
			}
		}
		// Unresolved file: fall back to the default layout under DataDir so we
		// never write outside a known root.
		return defaultDataDirFilePath(s.cfg.DataDir, o)
	}
}
