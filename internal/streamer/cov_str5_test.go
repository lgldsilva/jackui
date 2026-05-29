package streamer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

// str5NewFavorites opens a fresh, throwaway FavoritesStore for a test.
func str5NewFavorites(t *testing.T) *FavoritesStore {
	t.Helper()
	f, err := NewFavorites(filepath.Join(t.TempDir(), "str5-fav.db"))
	if err != nil {
		t.Fatalf("NewFavorites: %v", err)
	}
	t.Cleanup(f.Close)
	return f
}

// str5NewCache opens a fresh, throwaway MetadataCache for a test.
func str5NewCache(t *testing.T) *MetadataCache {
	t.Helper()
	mc, err := NewMetadataCache(filepath.Join(t.TempDir(), "str5-meta.db"))
	if err != nil {
		t.Fatalf("NewMetadataCache: %v", err)
	}
	t.Cleanup(func() { _ = mc.Close() })
	return mc
}

// ───── enforceCacheLimit: actual eviction of an over-limit, non-favorite entry ─────

func Test_str5_EnforceCacheLimit_EvictsOldestInactive(t *testing.T) {
	dir := t.TempDir()
	// A single non-favorite entry far above the (tiny) limit must be evicted.
	big := make([]byte, 128*1024)
	if err := os.WriteFile(filepath.Join(dir, "str5-evictme"), big, 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewForTesting()
	s.cfg.DataDir = dir
	s.cfg.MaxCacheSize = 1 // anything on disk is over the limit

	s.enforceCacheLimit()

	if _, err := os.Stat(filepath.Join(dir, "str5-evictme")); !os.IsNotExist(err) {
		t.Errorf("expected over-limit non-favorite entry to be evicted, stat err=%v", err)
	}
}

func Test_str5_EnforceCacheLimit_SkipsDownloadProtected(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, 128*1024)
	if err := os.WriteFile(filepath.Join(dir, "str5-dl"), big, 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewForTesting()
	s.cfg.DataDir = dir
	s.cfg.MaxCacheSize = 1
	s.RegisterDownload("str5-dl") // protected: must survive eviction

	s.enforceCacheLimit()

	if _, err := os.Stat(filepath.Join(dir, "str5-dl")); os.IsNotExist(err) {
		t.Error("download-protected entry was evicted despite protection")
	}
}

// ───── ClearAll: removes plain entries, keeps favorites and dot-prefixed files ─────

func Test_str5_ClearAll_KeepsFavoritesAndDotfiles(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("str5-plain")
	mustWrite("str5-fav")
	mustWrite(".favorites.db") // dot-prefixed bookkeeping → skipped

	s := NewForTesting()
	s.cfg.DataDir = dir
	f := str5NewFavorites(t)
	if err := f.Add("str5-fav", "hash5", "magnet:?xt=urn:btih:hash5", "manual", 1); err != nil {
		t.Fatal(err)
	}
	s.SetFavorites(f)

	if err := s.ClearAll(); err != nil {
		t.Fatalf("ClearAll: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "str5-plain")); !os.IsNotExist(err) {
		t.Error("expected plain entry to be cleared")
	}
	if _, err := os.Stat(filepath.Join(dir, "str5-fav")); os.IsNotExist(err) {
		t.Error("favorite entry was cleared despite protection")
	}
	if _, err := os.Stat(filepath.Join(dir, ".favorites.db")); os.IsNotExist(err) {
		t.Error("dot-prefixed file was cleared despite being skipped")
	}
}

// ───── ClearEntry: happy path removes; favorite refused ─────

func Test_str5_ClearEntry_RemovesPlain(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "str5-gone"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewForTesting()
	s.cfg.DataDir = dir

	if err := s.ClearEntry("str5-gone"); err != nil {
		t.Fatalf("ClearEntry: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "str5-gone")); !os.IsNotExist(err) {
		t.Error("expected entry to be removed")
	}
}

func Test_str5_ClearEntry_RefusesFavorite(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "str5-keep"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewForTesting()
	s.cfg.DataDir = dir
	f := str5NewFavorites(t)
	if err := f.Add("str5-keep", "h", "", "manual", 1); err != nil {
		t.Fatal(err)
	}
	s.SetFavorites(f)

	if err := s.ClearEntry("str5-keep"); err == nil {
		t.Error("expected ClearEntry to refuse a favorite")
	}
	if _, err := os.Stat(filepath.Join(dir, "str5-keep")); os.IsNotExist(err) {
		t.Error("favorite entry was removed despite refusal")
	}
}

// ───── SaveArtBytes: MkdirAll failure when DataDir is itself a regular file ─────

func Test_str5_SaveArtBytes_MkdirError(t *testing.T) {
	dir := t.TempDir()
	// Make DataDir a file so MkdirAll(DataDir/.art) fails.
	fileAsDataDir := filepath.Join(dir, "str5-notadir")
	if err := os.WriteFile(fileAsDataDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewForTesting()
	s.cfg.DataDir = fileAsDataDir

	if _, err := s.SaveArtBytes(metainfo.Hash{0x5a}, []byte("data")); err == nil {
		t.Error("expected SaveArtBytes to error when .art dir cannot be created")
	}
}

func Test_str5_SaveArtBytes_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewForTesting()
	s.cfg.DataDir = dir

	var h metainfo.Hash
	if err := h.FromHexString("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"); err != nil {
		t.Fatal(err)
	}
	rel, err := s.SaveArtBytes(h, []byte("str5-art-bytes"))
	if err != nil {
		t.Fatalf("SaveArtBytes: %v", err)
	}
	got, err := s.ReadArtBytes(rel)
	if err != nil {
		t.Fatalf("ReadArtBytes: %v", err)
	}
	if string(got) != "str5-art-bytes" {
		t.Errorf("round-trip mismatch: got %q", got)
	}
}

// ───── Constructor error paths: opening a DB inside a non-existent directory ─────

func Test_str5_NewFavorites_OpenError(t *testing.T) {
	// modernc.org/sqlite errors when the parent directory does not exist.
	bad := filepath.Join(t.TempDir(), "str5-missing-dir", "fav.db")
	if _, err := NewFavorites(bad); err == nil {
		t.Error("expected NewFavorites to error for unwritable path")
	}
}

func Test_str5_NewMetadataCache_OpenError(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "str5-missing-dir", "meta.db")
	if _, err := NewMetadataCache(bad); err == nil {
		t.Error("expected NewMetadataCache to error for unwritable path")
	}
}

// ───── Favorites folder mutators: nil-store no-op paths ─────

func Test_str5_Favorites_NilStore_FolderMutators(t *testing.T) {
	var f *FavoritesStore // nil receiver

	if _, err := f.CreateFolder(1, "x", nil); err == nil {
		t.Error("expected CreateFolder on nil store to error")
	}
	if err := f.RenameFolder(1, 1, "x"); err != nil {
		t.Errorf("RenameFolder nil store should no-op, got %v", err)
	}
	if err := f.MoveFolder(1, 1, nil); err != nil {
		t.Errorf("MoveFolder nil store should no-op, got %v", err)
	}
	if err := f.DeleteFolder(1, 1); err != nil {
		t.Errorf("DeleteFolder nil store should no-op, got %v", err)
	}
	if err := f.MoveFavoriteToFolder(1, "x", nil); err != nil {
		t.Errorf("MoveFavoriteToFolder nil store should no-op, got %v", err)
	}
}

// ───── Favorites: CreateFolder under a parent then move favorite into it ─────

func Test_str5_Favorites_NestedFolder_AndMoveFavorite(t *testing.T) {
	f := str5NewFavorites(t)

	root, err := f.CreateFolder(7, "str5-root", nil)
	if err != nil {
		t.Fatalf("CreateFolder root: %v", err)
	}
	child, err := f.CreateFolder(7, "str5-child", &root.ID)
	if err != nil {
		t.Fatalf("CreateFolder child: %v", err)
	}
	if child.ParentID == nil || *child.ParentID != root.ID {
		t.Errorf("child.ParentID = %v, want %d", child.ParentID, root.ID)
	}

	if err := f.Add("str5-item", "hh", "", "manual", 7); err != nil {
		t.Fatal(err)
	}
	if err := f.MoveFavoriteToFolder(7, "str5-item", &child.ID); err != nil {
		t.Fatalf("MoveFavoriteToFolder: %v", err)
	}

	list, err := f.List(7, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var found bool
	for _, fav := range list {
		if fav.Name == "str5-item" {
			found = true
			if fav.FolderID == nil || *fav.FolderID != child.ID {
				t.Errorf("FolderID = %v, want %d", fav.FolderID, child.ID)
			}
		}
	}
	if !found {
		t.Error("favorite str5-item not found in list")
	}
}

func Test_str5_Favorites_MoveFolder_ValidReparent(t *testing.T) {
	f := str5NewFavorites(t)

	a, err := f.CreateFolder(9, "str5-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.CreateFolder(9, "str5-b", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Move b under a — a valid, acyclic reparent (walks the parent chain of a).
	if err := f.MoveFolder(9, b.ID, &a.ID); err != nil {
		t.Fatalf("MoveFolder: %v", err)
	}
	got, err := f.GetFolder(9, b.ID)
	if err != nil {
		t.Fatalf("GetFolder: %v", err)
	}
	if got.ParentID == nil || *got.ParentID != a.ID {
		t.Errorf("ParentID = %v, want %d", got.ParentID, a.ID)
	}
}

// ───── MetadataCache: Set with files then Get returns the full snapshot ─────

func Test_str5_MetadataCache_SetWithFiles_RoundTrip(t *testing.T) {
	mc := str5NewCache(t)

	info := &TorrentInfo{
		InfoHash:    "str5hash",
		Name:        "str5 Movie",
		TotalSize:   2048,
		PrimaryFile: 1,
		Files: []FileInfo{
			{Index: 0, Path: "readme.txt", Size: 100, IsVideo: false},
			{Index: 1, Path: "movie.mkv", Size: 1948, IsVideo: true},
		},
	}
	if err := mc.Set(info); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got := mc.Get("str5hash")
	if got == nil {
		t.Fatal("Get returned nil after Set")
	}
	if got.Name != "str5 Movie" || got.TotalSize != 2048 || got.PrimaryFile != 1 {
		t.Errorf("snapshot mismatch: %+v", got)
	}
	if len(got.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(got.Files))
	}
	if got.Files[1].Path != "movie.mkv" || !got.Files[1].IsVideo {
		t.Errorf("file[1] mismatch: %+v", got.Files[1])
	}
	if got.CachedAt.IsZero() {
		t.Error("CachedAt should be populated")
	}
}

// ───── MetadataCache: Set upsert overwrites a prior snapshot for the same hash ─────

func Test_str5_MetadataCache_Set_Upsert(t *testing.T) {
	mc := str5NewCache(t)

	if err := mc.Set(&TorrentInfo{InfoHash: "str5up", Name: "old", TotalSize: 1, PrimaryFile: 0}); err != nil {
		t.Fatal(err)
	}
	if err := mc.Set(&TorrentInfo{InfoHash: "str5up", Name: "new", TotalSize: 99, PrimaryFile: -1}); err != nil {
		t.Fatal(err)
	}
	got := mc.Get("str5up")
	if got == nil || got.Name != "new" || got.TotalSize != 99 {
		t.Errorf("upsert did not overwrite: %+v", got)
	}
}

// ───── MetadataCache: SetArt then GetArt for a byte-backed (torrent) source ─────

func Test_str5_MetadataCache_SetArt_TorrentSource(t *testing.T) {
	mc := str5NewCache(t)

	art := &CachedArt{Source: "torrent", Path: ".art/abc.jpg", TmdbID: 0}
	if err := mc.SetArt("str5art", art); err != nil {
		t.Fatalf("SetArt: %v", err)
	}
	got := mc.GetArt("str5art")
	if got == nil {
		t.Fatal("GetArt returned nil")
	}
	if got.Source != "torrent" || got.Path != ".art/abc.jpg" {
		t.Errorf("art mismatch: %+v", got)
	}
}

// ───── GlobalStats on an empty active set reports zero rates ─────

func Test_str5_GlobalStats_Empty(t *testing.T) {
	s := NewForTesting()
	g := s.GlobalStats()
	if g.ActiveTorrents != 0 || g.DownRate != 0 || g.UpRate != 0 {
		t.Errorf("expected zero global stats, got %+v", g)
	}
}

// ───── dirSizeAndMTime over a single regular file returns its size ─────

func Test_str5_DirSizeAndMTime_SingleFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "str5-file")
	payload := make([]byte, 4096)
	if err := os.WriteFile(p, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	size, mtime, err := dirSizeAndMTime(p)
	if err != nil {
		t.Fatalf("dirSizeAndMTime: %v", err)
	}
	if size <= 0 {
		t.Errorf("expected positive size, got %d", size)
	}
	if mtime.IsZero() {
		t.Error("expected non-zero mtime")
	}
}
