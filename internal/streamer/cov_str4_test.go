package streamer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/lgldsilva/jackui/internal/dbtest"
)

// cov_str4_test.go — additional coverage for utilities WITHOUT a real torrent:
// favorites store, metadata cache and probe.go. All identifiers carry the str4
// prefix and the test lives in the `streamer` package, so it accesses private
// fields (f.db / m.db) to forge error states the happy paths don't reach.

// ───── helpers str4 ─────

func str4NewFavorites(t *testing.T) *FavoritesStore {
	t.Helper()
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	f, err := NewFavorites(pool)
	if err != nil {
		t.Fatalf("str4 NewFavorites: %v", err)
	}
	t.Cleanup(f.Close)
	return f
}

func str4NewCache(t *testing.T) *MetadataCache {
	t.Helper()
	c, err := NewMetadataCache(dbtest.NewDB(t))
	if err != nil {
		t.Fatalf("str4 NewMetadataCache: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// ───── metadata cache ─────

// Get with corrupt files JSON falls into the Unmarshal-err path → returns nil (no panic).
func Test_str4_MetadataCache_Get_CorruptFilesJSON(t *testing.T) {
	c := str4NewCache(t)
	const hash = "str4corrupthash"
	if _, err := c.db.Exec(
		`INSERT INTO metadata(info_hash, name, total_size, files, primary_file) VALUES(?, ?, 0, ?, -1)`,
		hash, "x", "{not valid json",
	); err != nil {
		t.Fatalf("seed corrupt row: %v", err)
	}
	if got := c.Get(hash); got != nil {
		t.Fatalf("expected nil on corrupt files JSON, got %+v", got)
	}
}

// Get of a missing hash → nil (Scan errs).
func Test_str4_MetadataCache_Get_Missing(t *testing.T) {
	c := str4NewCache(t)
	if got := c.Get("str4nope"); got != nil {
		t.Fatalf("expected nil for missing hash, got %+v", got)
	}
}

// Set after Get proves the round-trip and covers the happy path of Set without a real torrent.
func Test_str4_MetadataCache_SetThenGet(t *testing.T) {
	c := str4NewCache(t)
	info := &TorrentInfo{
		InfoHash:    "str4sethash",
		Name:        "str4 name",
		TotalSize:   42,
		PrimaryFile: 0,
		Files: []FileInfo{
			{Index: 0, Path: "a.mkv", Size: 42, IsVideo: true},
		},
	}
	if err := c.Set(info); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got := c.Get("str4sethash")
	if got == nil || got.Name != "str4 name" || len(got.Files) != 1 || !got.Files[0].IsVideo {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

// GetSortMeta: nil receiver and empty list return an empty map (no panic).
func Test_str4_MetadataCache_GetSortMeta_Empty(t *testing.T) {
	var nilCache *MetadataCache
	if got := nilCache.GetSortMeta([]string{"x"}); len(got) != 0 {
		t.Fatalf("nil cache: expected empty map, got %+v", got)
	}
	c := str4NewCache(t)
	if got := c.GetSortMeta(nil); len(got) != 0 {
		t.Fatalf("empty hashes: expected empty map, got %+v", got)
	}
}

// GetSortMeta: brings total_size + health_seeders only for the present hashes; a
// never-probed hash keeps seeders=-1 (default) and missing ones stay out of the map.
func Test_str4_MetadataCache_GetSortMeta_Batch(t *testing.T) {
	c := str4NewCache(t)
	if err := c.Set(&TorrentInfo{InfoHash: "str4sortA", Name: "A", TotalSize: 100, PrimaryFile: -1}); err != nil {
		t.Fatalf("Set A: %v", err)
	}
	if err := c.SetHealth("str4sortA", 7, 3); err != nil {
		t.Fatalf("SetHealth A: %v", err)
	}
	// B: metadata only, never probed → seeders stays -1 (column default).
	if err := c.Set(&TorrentInfo{InfoHash: "str4sortB", Name: "B", TotalSize: 50, PrimaryFile: -1}); err != nil {
		t.Fatalf("Set B: %v", err)
	}
	got := c.GetSortMeta([]string{"str4sortA", "str4sortB", "str4sortMissing"})
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d (%+v)", len(got), got)
	}
	if got["str4sortA"].TotalSize != 100 || got["str4sortA"].Seeders != 7 {
		t.Fatalf("A: %+v", got["str4sortA"])
	}
	if got["str4sortB"].TotalSize != 50 || got["str4sortB"].Seeders != -1 {
		t.Fatalf("B (never probed): %+v", got["str4sortB"])
	}
	if _, ok := got["str4sortMissing"]; ok {
		t.Fatalf("missing hash should not be in the map")
	}
}

// GetHealth: row exists (via SetArt) but health_checked_at was never written →
// nil ("row exists but health never probed" branch).
func Test_str4_MetadataCache_GetHealth_NeverProbed(t *testing.T) {
	c := str4NewCache(t)
	const hash = "str4healthnone"
	if err := c.SetArt(hash, &CachedArt{Source: "tmdb", PosterURL: "http://x/y.jpg"}); err != nil {
		t.Fatalf("SetArt: %v", err)
	}
	if got := c.GetHealth(hash); got != nil {
		t.Fatalf("expected nil health for never-probed row, got %+v", got)
	}
}

// GetHealth of a missing hash → nil.
func Test_str4_MetadataCache_GetHealth_Missing(t *testing.T) {
	c := str4NewCache(t)
	if got := c.GetHealth("str4missinghealth"); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

// SetHealth + GetHealth happy path with seeders>0 → Available=true.
func Test_str4_MetadataCache_SetHealth_RoundTrip(t *testing.T) {
	c := str4NewCache(t)
	const hash = "str4healthok"
	if err := c.SetHealth(hash, 5, 2); err != nil {
		t.Fatalf("SetHealth: %v", err)
	}
	got := c.GetHealth(hash)
	if got == nil || got.Seeders != 5 || got.Peers != 2 || !got.Available {
		t.Fatalf("health round-trip mismatch: %+v", got)
	}
}

// GetArt: row exists but art_source is empty → nil ("art never resolved" branch).
func Test_str4_MetadataCache_GetArt_EmptySource(t *testing.T) {
	c := str4NewCache(t)
	const hash = "str4artnone"
	if err := c.SetHealth(hash, 1, 1); err != nil { // creates row without art_source
		t.Fatalf("SetHealth: %v", err)
	}
	if got := c.GetArt(hash); got != nil {
		t.Fatalf("expected nil art for unresolved row, got %+v", got)
	}
}

// Nil-receiver: all methods must be safe no-ops.
func Test_str4_MetadataCache_NilReceiver(t *testing.T) {
	var c *MetadataCache
	if c.Get("h") != nil {
		t.Error("nil Get should be nil")
	}
	if c.GetArt("h") != nil {
		t.Error("nil GetArt should be nil")
	}
	if c.GetHealth("h") != nil {
		t.Error("nil GetHealth should be nil")
	}
	if err := c.Set(&TorrentInfo{}); err != nil {
		t.Errorf("nil Set: %v", err)
	}
	if err := c.SetArt("h", &CachedArt{}); err != nil {
		t.Errorf("nil SetArt: %v", err)
	}
	if err := c.SetHealth("h", 0, 0); err != nil {
		t.Errorf("nil SetHealth: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("nil Close: %v", err)
	}
}

// DefaultMetadataCachePath composes the default path.
func Test_str4_DefaultMetadataCachePath(t *testing.T) {
	got := DefaultMetadataCachePath("/data")
	if got != filepath.Join("/data", ".metadata-cache.db") {
		t.Fatalf("unexpected path: %q", got)
	}
}

// ArtSourceRank covers web + default in addition to the already-tested ones.
func Test_str4_ArtSourceRank(t *testing.T) {
	cases := map[string]int{"torrent": 4, "tmdb": 3, "web": 2, "frame": 1, "": 0, "bogus": 0}
	for src, want := range cases {
		if got := ArtSourceRank(src); got != want {
			t.Errorf("ArtSourceRank(%q) = %d, want %d", src, got, want)
		}
	}
}

// ───── favorites ─────

// List on a nil store → ErrFavoritesUnavail error.
func Test_str4_Favorites_List_NilStore(t *testing.T) {
	var f *FavoritesStore
	if _, err := f.List(0, false, false); err == nil || err.Error() != ErrFavoritesUnavail {
		t.Fatalf("expected %q, got %v", ErrFavoritesUnavail, err)
	}
}

// List with includeAll returns favorites from all users.
func Test_str4_Favorites_List_IncludeAll(t *testing.T) {
	f := str4NewFavorites(t)
	if err := f.Add("a", "h1", "magnet:?xt=urn:btih:h1", "manual", 1); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := f.Add("b", "h2", "", "manual", 2); err != nil {
		t.Fatalf("Add: %v", err)
	}
	all, err := f.List(0, true, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 favorites, got %d", len(all))
	}
}

// ListFolders on nil store → (nil, nil).
func Test_str4_Favorites_ListFolders_NilStore(t *testing.T) {
	var f *FavoritesStore
	got, err := f.ListFolders(1, false)
	if err != nil || got != nil {
		t.Fatalf("nil ListFolders: got=%v err=%v", got, err)
	}
}

// ListFolders returns a tree with a subfolder — exercises the scan + valid parent_id.
func Test_str4_Favorites_ListFolders_WithTree(t *testing.T) {
	f := str4NewFavorites(t)
	root, err := f.CreateFolder(1, "root", nil, false)
	if err != nil {
		t.Fatalf("CreateFolder root: %v", err)
	}
	if _, err := f.CreateFolder(1, "child", &root.ID, false); err != nil {
		t.Fatalf("CreateFolder child: %v", err)
	}
	folders, err := f.ListFolders(1, false)
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	if len(folders) != 2 {
		t.Fatalf("expected 2 folders, got %d", len(folders))
	}
	var sawChild bool
	for _, fl := range folders {
		if fl.Name == "child" {
			if fl.ParentID == nil || *fl.ParentID != root.ID {
				t.Errorf("child parent = %v, want %d", fl.ParentID, root.ID)
			}
			sawChild = true
		}
	}
	if !sawChild {
		t.Error("did not see child folder")
	}
}

// CreateFolder on nil store → error.
func Test_str4_Favorites_CreateFolder_NilStore(t *testing.T) {
	var f *FavoritesStore
	if _, err := f.CreateFolder(1, "x", nil, false); err == nil {
		t.Fatal("expected error from nil-store CreateFolder")
	}
}

// MoveFolder: moving a folder into its own descendant must be rejected (the
// parent-chain walk detects the cycle).
func Test_str4_Favorites_MoveFolder_RejectsCycle(t *testing.T) {
	f := str4NewFavorites(t)
	parent, err := f.CreateFolder(7, "parent", nil, false)
	if err != nil {
		t.Fatalf("CreateFolder parent: %v", err)
	}
	child, err := f.CreateFolder(7, "child", &parent.ID, false)
	if err != nil {
		t.Fatalf("CreateFolder child: %v", err)
	}
	// Move `parent` into `child` (its descendant) → cycle.
	if err := f.MoveFolder(7, parent.ID, &child.ID); err == nil {
		t.Fatal("expected cycle rejection moving parent into its child")
	}
	// Confirm nothing changed: parent is still root.
	got, err := f.GetFolder(7, parent.ID)
	if err != nil {
		t.Fatalf("GetFolder: %v", err)
	}
	if got.ParentID != nil {
		t.Errorf("parent should remain root, got parentID=%v", got.ParentID)
	}
}

// MoveFolder to root (newParent nil) is valid and doesn't trigger the cycle check.
func Test_str4_Favorites_MoveFolder_ToRoot(t *testing.T) {
	f := str4NewFavorites(t)
	parent, err := f.CreateFolder(8, "parent", nil, false)
	if err != nil {
		t.Fatalf("CreateFolder parent: %v", err)
	}
	child, err := f.CreateFolder(8, "child", &parent.ID, false)
	if err != nil {
		t.Fatalf("CreateFolder child: %v", err)
	}
	if err := f.MoveFolder(8, child.ID, nil); err != nil {
		t.Fatalf("MoveFolder to root: %v", err)
	}
	got, err := f.GetFolder(8, child.ID)
	if err != nil {
		t.Fatalf("GetFolder: %v", err)
	}
	if got.ParentID != nil {
		t.Errorf("child should be root after move, got parentID=%v", got.ParentID)
	}
}

// MoveFolder on nil store → nil no-op.
func Test_str4_Favorites_MoveFolder_NilStore(t *testing.T) {
	var f *FavoritesStore
	if err := f.MoveFolder(1, 1, nil); err != nil {
		t.Fatalf("nil MoveFolder: %v", err)
	}
}

// HashSetForUser on nil store → empty map without error (nil branch).
func Test_str4_Favorites_HashSetForUser_NilStore(t *testing.T) {
	var f *FavoritesStore
	set, err := f.HashSetForUser(1, false)
	if err != nil || len(set) != 0 {
		t.Fatalf("nil HashSetForUser: set=%v err=%v", set, err)
	}
}

// DefaultFavoritesPath composes the default path.
func Test_str4_DefaultFavoritesPath(t *testing.T) {
	got := DefaultFavoritesPath("/data")
	if got != filepath.Join("/data", ".favorites.db") {
		t.Fatalf("unexpected path: %q", got)
	}
}

// MoveFavoriteToFolder: path with non-nil folderID (assigns to a folder) and
// then back to root (nil) — covers both interface{} branches.
func Test_str4_Favorites_MoveFavoriteToFolder(t *testing.T) {
	f := str4NewFavorites(t)
	if err := f.Add("movie", "h9", "", "manual", 3); err != nil {
		t.Fatalf("Add: %v", err)
	}
	folder, err := f.CreateFolder(3, "box", nil, false)
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if err := f.MoveFavoriteToFolder(3, "movie", &folder.ID); err != nil {
		t.Fatalf("MoveFavoriteToFolder into folder: %v", err)
	}
	favs, err := f.List(3, false, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(favs) != 1 || favs[0].FolderID == nil || *favs[0].FolderID != folder.ID {
		t.Fatalf("expected favorite inside folder %d, got %+v", folder.ID, favs)
	}
	if err := f.MoveFavoriteToFolder(3, "movie", nil); err != nil {
		t.Fatalf("MoveFavoriteToFolder to root: %v", err)
	}
	favs, _ = f.List(3, false, false)
	if len(favs) != 1 || favs[0].FolderID != nil {
		t.Fatalf("expected favorite back at root, got %+v", favs)
	}
}

// MoveFavoriteToFolder on nil store → no-op.
func Test_str4_Favorites_MoveFavoriteToFolder_NilStore(t *testing.T) {
	var f *FavoritesStore
	if err := f.MoveFavoriteToFolder(1, "x", nil); err != nil {
		t.Fatalf("nil MoveFavoriteToFolder: %v", err)
	}
}

// RenameFolder / DeleteFolder on nil store → no-ops.
func Test_str4_Favorites_FolderMutators_NilStore(t *testing.T) {
	var f *FavoritesStore
	if err := f.RenameFolder(1, 1, "x"); err != nil {
		t.Fatalf("nil RenameFolder: %v", err)
	}
	if err := f.DeleteFolder(1, 1); err != nil {
		t.Fatalf("nil DeleteFolder: %v", err)
	}
}

// ───── probe.go ─────

// resolveProbeInput with a resolver returning a hit → plain input (no stdin/closeFn).
func Test_str4_ResolveProbeInput_ResolverHit(t *testing.T) {
	s := NewForTesting()
	s.SetFilePathResolver(func(_ metainfo.Hash, _ int) (string, bool) {
		return "/str4/path.mkv", true
	})
	pi, err := s.resolveProbeInput(metainfo.Hash{}, 0, 1024)
	if err != nil {
		t.Fatalf("resolveProbeInput: %v", err)
	}
	if pi.input != "/str4/path.mkv" || pi.stdin != nil || pi.closeFn != nil {
		t.Fatalf("unexpected probeInput: %+v", pi)
	}
}

// resolveProbeInput: resolver present but miss → falls through to the active lookup
// and, since nothing is active, returns ErrTorrentNotActive.
func Test_str4_ResolveProbeInput_ResolverMiss_NotActive(t *testing.T) {
	s := NewForTesting()
	s.SetFilePathResolver(func(_ metainfo.Hash, _ int) (string, bool) {
		return "", false
	})
	if _, err := s.resolveProbeInput(metainfo.HashBytes([]byte("str4")), 0, 1024); err == nil || !errors.Is(err, ErrTorrentNotActive) {
		t.Fatalf("expected %q, got %v", ErrTorrentNotActive, err)
	}
}

// Probe with no active torrent and no resolver → ErrTorrentNotActive.
func Test_str4_Probe_NotActive(t *testing.T) {
	s := NewForTesting()
	if _, err := s.Probe(context.Background(), metainfo.HashBytes([]byte("str4probe")), 0); err == nil || !errors.Is(err, ErrTorrentNotActive) {
		t.Fatalf("expected %q, got %v", ErrTorrentNotActive, err)
	}
}

// ExtractSubtitle via a resolver pointing at a non-media file: the resolver path
// is exercised and ffmpeg fails (no subtitle stream) → error.
func Test_str4_ExtractSubtitle_ResolverNonMedia(t *testing.T) {
	dir := t.TempDir()
	notMedia := filepath.Join(dir, "str4.txt")
	if err := os.WriteFile(notMedia, []byte("not a media file at all"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := NewForTesting()
	s.cfg.DataDir = dir
	s.SetFilePathResolver(func(_ metainfo.Hash, _ int) (string, bool) {
		return notMedia, true
	})
	if _, err := s.ExtractSubtitle(context.Background(), metainfo.Hash{}, 0, 0); err == nil {
		t.Fatal("expected error extracting subtitle from non-media file")
	}
}

// ExtractSubtitle without a resolver and no active torrent → ErrTorrentNotActive.
func Test_str4_ExtractSubtitle_NotActive(t *testing.T) {
	s := NewForTesting()
	if _, err := s.ExtractSubtitle(context.Background(), metainfo.HashBytes([]byte("str4sub")), 0, 0); err == nil || !errors.Is(err, ErrTorrentNotActive) {
		t.Fatalf("expected %q, got %v", ErrTorrentNotActive, err)
	}
}

// parseProbeOutput with multichannel audio + image subtitle (PGS) fills Channels
// and sets Image — field mapping paths.
func Test_str4_ParseProbeOutput_AudioAndImageSub(t *testing.T) {
	const out = `{
		"streams": [
			{"index":0,"codec_type":"audio","codec_name":"ac3","channels":6,
			 "tags":{"language":"por","title":"Dublado"},"disposition":{"default":1,"forced":0}},
			{"index":1,"codec_type":"subtitle","codec_name":"hdmv_pgs_subtitle",
			 "tags":{"language":"eng"},"disposition":{"default":0,"forced":1}}
		],
		"format":{"duration":"3600.0"}
	}`
	res, err := parseProbeOutput([]byte(out))
	if err != nil {
		t.Fatalf("parseProbeOutput: %v", err)
	}
	if len(res.Audio) != 1 || res.Audio[0].Channels != 6 || res.Audio[0].Language != "por" || res.Audio[0].Title != "Dublado" || !res.Audio[0].Default {
		t.Fatalf("audio track mismatch: %+v", res.Audio)
	}
	if len(res.Subtitles) != 1 || !res.Subtitles[0].Image || !res.Subtitles[0].Forced {
		t.Fatalf("subtitle track mismatch: %+v", res.Subtitles)
	}
	if res.DurationSec != 3600.0 {
		t.Fatalf("duration = %f, want 3600", res.DurationSec)
	}
}
