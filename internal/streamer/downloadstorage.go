package streamer

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// DownloadStorageSpec tells the add path to write a torrent's data DIRECTLY to
// its final destination on bulk storage instead of the SSD piece cache.
//
//   - BaseDir is the PARENT directory already resolved by the downloads worker
//     (e.g. downloadDir/<username> or sharedDir/<category>) — WITHOUT the torrent
//     name segment. The name isn't known until metadata arrives, so the storage
//     appends it itself inside TorrentDirMaker (which runs post-metadata).
//   - Sanitize turns the real torrent name (t.Name()) into the final folder
//     segment. It is INJECTED (rather than imported) because the canonical
//     sanitizer lives in internal/downloads, which already imports this package —
//     importing it back would be a cycle. Passing it as a func keeps the path the
//     storage writes to byte-identical to the path the worker's completionDest
//     computes, so the move-on-completion is a no-op.
type DownloadStorageSpec struct {
	BaseDir  string
	Sanitize func(string) string
}

// downloadStorage builds a per-torrent file storage rooted at
// BaseDir/<Sanitize(name)>. The layout mirrors what the downloads worker expects
// at completion: single-file → BaseDir/<name>/<name>; multi-file →
// BaseDir/<name>/<internal/tree>. Piece completion is persistent (shared Bolt)
// so a restart doesn't re-hash large files on the slow bulk disk.
func (s *Streamer) downloadStorage(ds DownloadStorageSpec) storage.ClientImpl {
	return storage.NewFileOpts(storage.NewFileClientOpts{
		ClientBaseDir:   ds.BaseDir,
		PieceCompletion: s.dlPieceCompletion, // nil → NewFileOpts uses a default under BaseDir
		TorrentDirMaker: func(baseDir string, info *metainfo.Info, _ metainfo.Hash) string {
			return downloadTorrentDir(baseDir, info.BestName(), ds.Sanitize)
		},
		// File path relative to the TorrentDir. For a single-file torrent
		// BestPath() is EMPTY, so we fall back to the torrent name — landing the
		// file at BaseDir/<Sanitize(name)>/<name>, exactly what the worker's
		// completionDest+moveDownloadedFile produce (destDir/<base(relPath)>),
		// so the move-on-completion is a no-op. For multi-file it's the internal
		// tree (without the name root), matching wholeTorrentDest's stripped path.
		FilePathMaker: downloadFileRelPath,
	})
}

// downloadFileRelPath returns a torrent file's path relative to its TorrentDir.
// Single-file torrents have an empty BestPath() (the file IS the torrent), so we
// use the torrent name; multi-file torrents use their internal tree.
func downloadFileRelPath(o storage.FilePathMakerOpts) string {
	parts := o.File.BestPath()
	if len(parts) == 0 {
		return o.Info.BestName()
	}
	return filepath.Join(parts...)
}

// downloadTorrentDir computes the per-torrent destination directory:
// baseDir/<sanitize(name)>. Kept pure (no receiver) so it's unit-testable and so
// the worker can assert its completionDest matches byte-for-byte.
func downloadTorrentDir(baseDir, name string, sanitize func(string) string) string {
	seg := name
	if sanitize != nil {
		seg = sanitize(name)
	}
	return filepath.Join(baseDir, seg)
}

// EnsureActiveForDownload is EnsureActive for the download-to-bulk path: it adds
// the magnet writing data straight to ds.BaseDir on bulk storage and returns the
// info hash. Mirrors EnsureActive otherwise (metadata cache + favorites stay
// consistent via the shared Add pipeline).
func (s *Streamer) EnsureActiveForDownload(ctx context.Context, magnet string, ds DownloadStorageSpec) (metainfo.Hash, error) {
	info, err := s.AddForDownload(ctx, magnet, ds)
	if err != nil {
		return metainfo.Hash{}, err
	}
	var h metainfo.Hash
	if err := h.FromHexString(info.InfoHash); err != nil {
		return metainfo.Hash{}, fmt.Errorf("invalid hash from streamer.AddForDownload: %w", err)
	}
	return h, nil
}
