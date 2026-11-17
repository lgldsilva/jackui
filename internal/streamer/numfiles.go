package streamer

import "github.com/anacrolix/torrent/metainfo"

// NumFiles returns how many files an active torrent has; ok=false when the
// torrent is not active. For callers that only need to bounds-check a file
// index: Get builds the full TorrentInfo, reading completion and priority of
// EVERY file under the anacrolix client lock.
func (s *Streamer) NumFiles(hash metainfo.Hash) (n int, ok bool) {
	s.mu.Lock()
	e, exists := s.active[hash]
	s.mu.Unlock()
	if !exists || e.t.Info() == nil {
		return 0, false
	}
	return len(e.t.Files()), true
}
