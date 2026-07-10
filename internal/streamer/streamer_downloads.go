package streamer

import "strings"

// RegisterDownload protects `name` from eviction — the downloads worker is
// pulling it from the swarm and the user hasn't watched it yet. Must be called
// for every download the user adds to the download queue.
func (s *Streamer) RegisterDownload(name string) {
	if name == "" {
		return
	}
	s.mu.Lock()
	if s.downloads == nil {
		s.downloads = make(map[string]struct{})
	}
	s.downloads[name] = struct{}{}
	s.mu.Unlock()
}

// UnregisterDownload removes the eviction protection. Call after the
// download completes or is cancelled. Idempotent.
func (s *Streamer) UnregisterDownload(name string) {
	s.mu.Lock()
	delete(s.downloads, name)
	s.mu.Unlock()
}

// IsDownloadProtected reports whether `name` is currently in the download
// protection set. Used by tests + the cache eviction code.
//
// anacrolix grava arquivos SINGLE-FILE como "<name>.part" enquanto o download
// não terminou — ao mesmo tempo `t.Name()` (que o worker registra) NÃO inclui
// o sufixo. Sem essa tolerância o enforceCacheLimit passa "<name>.part" e
// consulta um set que só tem "<name>", então conclui que o arquivo NÃO está
// protegido e o LRU deleta o .part — anacrolix perde os pieces no disco e
// recomeça do zero. (Multi-file torrents não sofrem porque o entry é o
// diretório, cujo nome casa com t.Name() exatamente.)
func (s *Streamer) IsDownloadProtected(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.downloads[name]; ok {
		return true
	}
	if stripped := strings.TrimSuffix(name, ".part"); stripped != name {
		if _, ok := s.downloads[stripped]; ok {
			return true
		}
	}
	return false
}

// evictionBlocked reports whether a top-level DataDir entry `name` must be kept
// RIGHT NOW because it belongs to a currently-loaded torrent or a protected
// download. Re-checked under the lock immediately before deletion to close the
// TOCTOU window between Stats()'s snapshot (which drops the lock before walking
// the filesystem) and the actual RemoveAll: a stream that started in that gap
// is in s.active by now, and deleting its file would pull the rug out from under
// an in-flight HLS transcode ("torrent closed" → demux I/O error → segment 404).
// Favorites are intentionally not re-checked here — they were already filtered at
// snapshot time and a torrent rarely becomes a favorite within the eviction loop.
func (s *Streamer) evictionBlocked(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Match active torrents by their on-disk name (t.Name()), tolerating the
	// single-file ".part" suffix anacrolix uses while a download is in flight.
	stripped := strings.TrimSuffix(name, ".part")
	for _, e := range s.active {
		if tn := e.t.Name(); tn == name || tn == stripped {
			return true
		}
	}
	if _, ok := s.downloads[name]; ok {
		return true
	}
	if stripped != name {
		if _, ok := s.downloads[stripped]; ok {
			return true
		}
	}
	return false
}
