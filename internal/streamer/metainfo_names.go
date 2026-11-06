package streamer

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

// loadMetainfoFile is a seam so tests can count parses.
var loadMetainfoFile = metainfo.LoadFromFile

// metainfoNameCache memoizes the info.Name → infohash mapping of every
// .torrent in metainfoDir, keyed by file name and invalidated by (size,
// mtime). Stats() runs every 10s (metrics worker) and on each gc tick; without
// this it re-read, bencode-decoded and SHA-1-hashed every file per call, which
// with ~450 files pinned up to 1.7 cores per tick in production.
//
// The zero value is ready to use. Safe for concurrent callers (metrics worker
// and gcLoop both reach Stats()).
type metainfoNameCache struct {
	mu      sync.Mutex
	entries map[string]metainfoNameEntry
}

type metainfoNameEntry struct {
	size    int64
	modTime time.Time
	name    string // "" when the file failed to parse (negative cache)
	hash    string
}

// augmentNameToHashFromMetainfo fills nameToHash with mappings from persisted
// .torrent files, never overriding names already present (active torrents win).
func (s *Streamer) augmentNameToHashFromMetainfo(nameToHash map[string]string) {
	if s.metainfoDir == "" {
		return
	}
	ents, err := os.ReadDir(s.metainfoDir)
	if err != nil {
		return
	}
	s.miNames.mu.Lock()
	defer s.miNames.mu.Unlock()
	if s.miNames.entries == nil {
		s.miNames.entries = make(map[string]metainfoNameEntry, len(ents))
	}
	seen := make(map[string]struct{}, len(ents))
	for _, de := range ents {
		e, ok := s.miNames.resolve(s.metainfoDir, de)
		if !ok {
			continue
		}
		seen[de.Name()] = struct{}{}
		if _, exists := nameToHash[e.name]; e.name != "" && !exists {
			nameToHash[e.name] = e.hash
		}
	}
	s.miNames.prune(seen)
}

// prune drops entries whose .torrent is gone from disk. Caller holds c.mu.
func (c *metainfoNameCache) prune(seen map[string]struct{}) {
	for fn := range c.entries {
		if _, ok := seen[fn]; !ok {
			delete(c.entries, fn)
		}
	}
}

// resolve returns the cached entry for de, re-parsing only when the file is
// new or its size/mtime changed. ok=false for non-.torrent entries and files
// that vanished between ReadDir and Info. Caller holds c.mu.
func (c *metainfoNameCache) resolve(dir string, de os.DirEntry) (metainfoNameEntry, bool) {
	if de.IsDir() || !strings.HasSuffix(de.Name(), ".torrent") {
		return metainfoNameEntry{}, false
	}
	fi, err := de.Info()
	if err != nil {
		return metainfoNameEntry{}, false
	}
	if e, ok := c.entries[de.Name()]; ok && e.size == fi.Size() && e.modTime.Equal(fi.ModTime()) {
		return e, true
	}
	e := metainfoNameEntry{size: fi.Size(), modTime: fi.ModTime()}
	e.name, e.hash = parseMetainfoName(filepath.Join(dir, de.Name()))
	c.entries[de.Name()] = e
	return e, true
}

// parseMetainfoName returns ("", "") when the file is unreadable or has no name.
func parseMetainfoName(path string) (name, hash string) {
	mi, err := loadMetainfoFile(path)
	if err != nil {
		return "", ""
	}
	info, err := mi.UnmarshalInfo()
	if err != nil || info.Name == "" {
		return "", ""
	}
	return info.Name, mi.HashInfoBytes().HexString()
}
