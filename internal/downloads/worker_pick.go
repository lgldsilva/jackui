package downloads

import (
	"strings"

	"github.com/anacrolix/torrent"
)

// SnapshotActiveCount is mostly diagnostic — returns the number of downloads
// currently being driven by the worker (matches store.ListActive() after the
// next tick).
func (w *Worker) SnapshotActiveCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.tracked)
}

// pickBestFile selects the best file to download from a torrent file list.
// It prefers the largest video/media file (by extension), falling back to
// the largest file overall. Returns -1 if the list is empty.
func pickBestFile(files []*torrent.File) int {
	if len(files) == 0 {
		return -1
	}
	videoExt := map[string]bool{
		".mkv": true, ".mp4": true, ".avi": true, ".mov": true,
		".wmv": true, ".flv": true, ".webm": true, ".m4v": true,
		".ts": true, ".m2ts": true,
	}
	audioExt := map[string]bool{
		".mp3": true, ".flac": true, ".wav": true, ".m4a": true,
		".aac": true, ".ogg": true, ".opus": true,
	}

	bestIdx := 0
	bestScore := int64(-1)

	for i, f := range files {
		p := strings.ToLower(f.Path())
		score := f.Length()

		// Video files get a massive boost so they always win.
		for ext := range videoExt {
			if strings.HasSuffix(p, ext) {
				score += 1 << 40 // 1TB boost — video trumps everything
				break
			}
		}
		// Audio files get a moderate boost.
		for ext := range audioExt {
			if strings.HasSuffix(p, ext) {
				score += 1 << 30 // 1GB boost — audio over generic data
				break
			}
		}

		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	return bestIdx
}
