package streamer

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/lgldsilva/jackui/internal/diskutil"
)

// ─── Cache management ───────────────────────────────────────────────────────

// CacheEntry describes one item on disk in the cache directory.
type CacheEntry struct {
	Path       string    `json:"path"` // relative to DataDir
	Size       int64     `json:"size"`
	ModTime    time.Time `json:"modTime"`
	IsActive   bool      `json:"isActive"`   // currently being downloaded/seeded
	IsFavorite bool      `json:"isFavorite"` // protected from eviction
	// InfoHash is the torrent's hex-encoded SHA1 info hash. Populated when the
	// torrent is either active or has a persisted .torrent in metainfoDir.
	// Empty string when we can't resolve the hash — the UI hides Play in that case.
	InfoHash string `json:"infoHash,omitempty"`
}

// CacheStats summarizes disk usage of the streaming cache.
type CacheStats struct {
	DataDir   string       `json:"dataDir"`
	TotalSize int64        `json:"totalSize"`
	MaxSize   int64        `json:"maxSize"`   // 0 = unlimited
	NumActive int          `json:"numActive"` // currently loaded torrents
	Entries   []CacheEntry `json:"entries"`
	// Filesystem footprint of the disk hosting DataDir (0 = statfs unavailable).
	DiskFree  int64 `json:"diskFree"`
	DiskTotal int64 `json:"diskTotal"`
	// Lifetime LRU eviction counters (since process start).
	EvictedCount   int64      `json:"evictedCount"`
	EvictedBytes   int64      `json:"evictedBytes"`
	LastEvictionAt *time.Time `json:"lastEvictionAt,omitempty"`
}

// Stats walks the DataDir and returns disk usage stats.
// "Active" entries are torrents currently loaded in memory (likely being read).
func (s *Streamer) Stats() (*CacheStats, error) {
	st := &CacheStats{
		DataDir: s.cfg.DataDir,
		MaxSize: s.cfg.MaxCacheSize,
	}

	activeNames, nameToHash, numActive := s.buildActiveMaps()
	st.NumActive = numActive

	s.augmentNameToHashFromMetainfo(nameToHash)

	entries, err := os.ReadDir(s.cfg.DataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return nil, err
	}

	for _, ent := range entries {
		full := filepath.Join(s.cfg.DataDir, ent.Name())
		size, mtime, err := dirSizeAndMTime(full)
		if err != nil {
			continue
		}
		st.Entries = append(st.Entries, CacheEntry{
			Path:       ent.Name(),
			Size:       size,
			ModTime:    mtime,
			IsActive:   activeNames[ent.Name()],
			IsFavorite: s.favs != nil && s.favs.IsFavorite(ent.Name()),
			InfoHash:   nameToHash[ent.Name()],
		})
		st.TotalSize += size
	}

	sort.Slice(st.Entries, func(i, j int) bool {
		return st.Entries[i].ModTime.After(st.Entries[j].ModTime)
	})

	st.DiskFree, st.DiskTotal = diskutil.Usage(s.cfg.DataDir)

	s.evictMu.Lock()
	st.EvictedCount = s.evictedCount
	st.EvictedBytes = s.evictedBytes
	if !s.lastEvictionAt.IsZero() {
		last := s.lastEvictionAt
		st.LastEvictionAt = &last
	}
	s.evictMu.Unlock()

	return st, nil
}

func (s *Streamer) buildActiveMaps() (map[string]bool, map[string]string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	activeNames := make(map[string]bool, len(s.active))
	nameToHash := make(map[string]string, len(s.active))
	for h, e := range s.active {
		name := e.t.Name()
		activeNames[name] = true
		nameToHash[name] = h.HexString()
	}
	return activeNames, nameToHash, len(s.active)
}

func (s *Streamer) augmentNameToHashFromMetainfo(nameToHash map[string]string) {
	if s.metainfoDir == "" {
		return
	}
	mEnts, err := os.ReadDir(s.metainfoDir)
	if err != nil {
		return
	}
	for _, m := range mEnts {
		if m.IsDir() || !strings.HasSuffix(m.Name(), ".torrent") {
			continue
		}
		mi, err := metainfo.LoadFromFile(filepath.Join(s.metainfoDir, m.Name()))
		if err != nil {
			continue
		}
		info, err := mi.UnmarshalInfo()
		if err != nil || info.Name == "" {
			continue
		}
		if _, ok := nameToHash[info.Name]; !ok {
			nameToHash[info.Name] = mi.HashInfoBytes().HexString()
		}
	}
}

// ClearAll drops every active torrent and wipes the DataDir, *except* favorites.
// Favorites are preserved on disk; their active torrent is dropped but files remain.
func (s *Streamer) ClearAll() error {
	s.mu.Lock()
	for h, e := range s.active {
		e.t.Drop()
		delete(s.active, h)
	}
	s.mu.Unlock()

	entries, err := os.ReadDir(s.cfg.DataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, ent := range entries {
		// Skip favorites + internal bookkeeping files
		name := ent.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if s.favs != nil && s.favs.IsFavorite(name) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(s.cfg.DataDir, name))
	}
	return nil
}

// ClearEntry removes a specific cache entry from disk (by relative path).
// Refuses if the entry is favorited (use Favorites().Remove first).
// If the torrent is currently active, it is dropped first.
func (s *Streamer) ClearEntry(name string) error {
	if s.favs != nil && s.favs.IsFavorite(name) {
		return fmt.Errorf("entry %q é favorito — desfavorite antes de remover", name)
	}
	// Drop matching active torrent if any
	s.mu.Lock()
	for h, e := range s.active {
		if e.t.Name() == name {
			e.t.Drop()
			delete(s.active, h)
			break
		}
	}
	s.mu.Unlock()

	full := filepath.Join(s.cfg.DataDir, filepath.Clean(name))
	// Safety: refuse to delete outside DataDir
	abs, err := filepath.Abs(full)
	if err != nil {
		return err
	}
	dirAbs, err := filepath.Abs(s.cfg.DataDir)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(abs, dirAbs+string(os.PathSeparator)) && abs != dirAbs {
		return fmt.Errorf("invalid path")
	}
	return os.RemoveAll(full)
}

// enforceCacheLimit evicts oldest inactive entries until total size <= maxSize.
// Only inactive entries are touched — active torrents are protected.
func (s *Streamer) enforceCacheLimit() {
	if s.cfg.MaxCacheSize <= 0 {
		return
	}
	stats, err := s.Stats()
	if err != nil {
		return
	}
	if stats.TotalSize <= s.cfg.MaxCacheSize {
		return
	}

	// Sort oldest first (LRU based on mtime). Favorites, active torrents, and
	// in-flight background downloads are protected from eviction.
	inactive := make([]CacheEntry, 0, len(stats.Entries))
	for _, e := range stats.Entries {
		if !e.IsActive && !e.IsFavorite && !s.IsDownloadProtected(e.Path) {
			inactive = append(inactive, e)
		}
	}
	sort.Slice(inactive, func(i, j int) bool {
		return inactive[i].ModTime.Before(inactive[j].ModTime)
	})

	s.evictCandidates(inactive, stats.TotalSize)
}

// evictCandidates deletes entries oldest-first until total size drops to/below
// MaxCacheSize. `candidates` are the entries that looked evictable at snapshot
// time; each is re-checked with evictionBlocked under the lock right before
// removal, so one that became active in the gap is skipped instead of deleted.
func (s *Streamer) evictCandidates(candidates []CacheEntry, total int64) {
	current := total
	for _, e := range candidates {
		if current <= s.cfg.MaxCacheSize {
			break
		}
		// Re-check under the lock: a play may have started between the Stats()
		// snapshot and now, loading this entry into s.active. Deleting it then
		// would kill the file out from under an active HLS transcode.
		if s.evictionBlocked(e.Path) {
			continue
		}
		log.Printf("streamer: cache over %s, evicting %s (%s, mtime=%s)",
			fmtBytes(s.cfg.MaxCacheSize), e.Path, fmtBytes(e.Size), e.ModTime.Format(time.RFC3339))
		if err := os.RemoveAll(filepath.Join(s.cfg.DataDir, e.Path)); err == nil {
			current -= e.Size
			s.recordEviction(e.Size)
		}
	}
}

// recordEviction bumps the lifetime eviction counters surfaced by Stats().
func (s *Streamer) recordEviction(bytes int64) {
	s.evictMu.Lock()
	s.evictedCount++
	s.evictedBytes += bytes
	s.lastEvictionAt = time.Now()
	s.evictMu.Unlock()
}

// dirSizeAndMTime returns the *physical* bytes allocated on disk under a path
// (file or dir), plus the newest mtime.
//
// Why physical (not logical): anacrolix writes sparse files — it opens the
// target file and writes only the bytes for completed pieces. The file's
// logical size (info.Size()) is the **final torrent size**, but the actual
// blocks consumed on disk grow progressively as pieces arrive.
//
// For cache eviction and the UI "X / Y used" indicator, what the user cares
// about is the *real* footprint, not the logical placeholder. Reporting
// logical size makes a 10 GB torrent look "fully cached" the moment metadata
// is received, which is why the cache UI looked pre-allocated.
//
// We use the platform-specific allocated-block count when available (POSIX
// stat.Blocks * 512) and fall back to logical size on platforms where the
// syscall data isn't accessible.
func dirSizeAndMTime(path string) (int64, time.Time, error) {
	var size int64
	var mtime time.Time
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += physicalBytes(info)
		}
		if info.ModTime().After(mtime) {
			mtime = info.ModTime()
		}
		return nil
	})
	return size, mtime, err
}

func fmtBytes(n int64) string {
	const k = 1024
	if n < k {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v := float64(n) / k
	u := 0
	for v >= k && u < len(units)-1 {
		v /= k
		u++
	}
	return fmt.Sprintf("%.2f %s", v, units[u])
}
