package streamer

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// relocatedStorage builds a per-torrent file storage whose paths resolve to each
// internal file's REAL on-disk location via the FilePathResolver. It lets
// anacrolix verify and SEED a download wherever its files actually live — e.g. a
// completed download moved out of the small SSD cache to bulk storage — instead
// of looking only under DataDir and re-downloading everything. Works for
// multi-file torrents (each file resolved by index) and survives renames (the
// resolver returns the actual path). Each torrent gets its own storage, so
// different torrents can live in different places.
//
// Returns nil — caller falls back to the default DataDir storage — when there's
// no resolver, the layout can't be resolved, or file 0 still lives under DataDir
// (where the default storage already points). Only relocations OUTSIDE DataDir
// switch to this storage, so the normal streaming/download paths are unchanged.
func (s *Streamer) relocatedStorage(info *metainfo.Info, hash metainfo.Hash) storage.ClientImpl {
	if s.filePathResolver == nil || info == nil {
		return nil
	}
	if len(info.UpvertedFiles()) == 0 {
		return nil
	}
	p0, ok := s.filePathResolver(hash, 0)
	if !ok || p0 == "" {
		return nil
	}
	if st, err := os.Stat(p0); err != nil || st.IsDir() {
		return nil
	}
	absData, _ := filepath.Abs(s.cfg.DataDir)
	abs0, _ := filepath.Abs(p0)
	if abs0 == absData || strings.HasPrefix(abs0, absData+string(os.PathSeparator)) {
		return nil // still under the cache — default storage already handles it
	}

	return storage.NewFileOpts(storage.NewFileClientOpts{
		// ClientBaseDir only roots the FilePathMaker fallback below.
		ClientBaseDir: s.cfg.DataDir,
		// In-memory completion (not a DB under DataDir): the default client storage
		// already owns a completion DB there, and opening a second one at the same
		// path would lock/conflict. In-memory means anacrolix re-verifies the real
		// file on add (hash-check marks complete pieces → seeds, never re-downloads)
		// and again after a restart — exactly what we want for a relocated seed.
		PieceCompletion: storage.NewMapPieceCompletion(),
		// Root the torrent at "/" so each file's absolute real path is used
		// verbatim (joined under "/", it stays a valid sub-path).
		TorrentDirMaker: func(string, *metainfo.Info, metainfo.Hash) string { return string(os.PathSeparator) },
		FilePathMaker: func(o storage.FilePathMakerOpts) string {
			if idx := fileIndexInInfo(o.Info, o.File); idx >= 0 {
				if p, ok := s.filePathResolver(hash, idx); ok && p != "" {
					return strings.TrimPrefix(p, string(os.PathSeparator))
				}
			}
			// Unresolved file (shouldn't happen — file 0 resolved): fall back to the
			// default layout under DataDir so we never write outside a known root.
			parts := make([]string, 0, len(o.File.BestPath())+2)
			parts = append(parts, strings.TrimPrefix(s.cfg.DataDir, string(os.PathSeparator)))
			if o.Info.BestName() != metainfo.NoName {
				parts = append(parts, o.Info.BestName())
			}
			parts = append(parts, o.File.BestPath()...)
			return filepath.Join(parts...)
		},
	})
}

// MatchesSeedTrackerCached reports whether the torrent's CACHED metainfo announce
// matches a configured seed-tracker, without activating the torrent. Used at boot
// to decide which completed downloads to auto-reactivate for seeding. Returns
// false when no metainfo is cached or no seed-trackers are configured.
func (s *Streamer) MatchesSeedTrackerCached(hash metainfo.Hash) bool {
	cached := s.loadCachedMetainfo(hash)
	if cached == nil {
		return false
	}
	var anns []string
	for _, tier := range cached.UpvertedAnnounceList() {
		anns = append(anns, tier...)
	}
	s.mu.Lock()
	trackers := s.seedTrackers
	s.mu.Unlock()
	return matchesSeedTracker(anns, trackers)
}

// fileIndexInInfo returns the index of file within info.UpvertedFiles(), matched
// by in-torrent path; -1 if not found.
func fileIndexInInfo(info *metainfo.Info, file *metainfo.FileInfo) int {
	if info == nil || file == nil {
		return -1
	}
	target := filepath.Join(file.BestPath()...)
	for i, f := range info.UpvertedFiles() {
		if filepath.Join(f.BestPath()...) == target {
			return i
		}
	}
	return -1
}
