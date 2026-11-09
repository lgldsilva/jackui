package downloads

import "github.com/anacrolix/torrent/metainfo"

// Eviction protection is a SET keyed by torrent name in the streamer (one
// anacrolix handle per hash), while download rows are per user. Dropping the
// protection — or the torrent — because ONE row finished/aborted pulled the
// rug from under another user's row of the same torrent (dropped mid-download,
// LRU-evictable partial data). Every release goes through this check.

// protectionInUseLocked reports whether any OTHER row still needs the torrent:
// a tracked row with the same name or hash, or an init in flight for the hash.
// Caller holds w.mu.
func (w *Worker) protectionInUseLocked(name string, h metainfo.Hash, excludeID int) bool {
	for id, td := range w.tracked {
		if id == excludeID {
			continue
		}
		if (name != "" && td.name == name) || (h != (metainfo.Hash{}) && td.hash == h) {
			return true
		}
	}
	return w.pendingSiblingLocked(h, excludeID)
}

// releaseProtection drops the streamer's eviction protection for name unless
// another row still needs it.
func (w *Worker) releaseProtection(name string, h metainfo.Hash, excludeID int) {
	w.mu.Lock()
	inUse := w.protectionInUseLocked(name, h, excludeID)
	w.mu.Unlock()
	if !inUse {
		w.streamer.UnregisterDownload(name)
	}
}

// hashInUse reports whether a row other than excludeID still needs torrent h.
func (w *Worker) hashInUse(h metainfo.Hash, excludeID int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.protectionInUseLocked("", h, excludeID)
}

func parseHash(infoHash string) metainfo.Hash {
	var h metainfo.Hash
	if infoHash != "" {
		_ = h.FromHexString(infoHash)
	}
	return h
}
