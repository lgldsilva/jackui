package streamer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// writeCachedMetainfo writes a .torrent (with the given announce URLs) to the
// streamer's metainfo cache for hash h, so loadCachedMetainfo can read it back.
func writeCachedMetainfo(t *testing.T, dir string, h metainfo.Hash, announces []string) {
	t.Helper()
	info := metainfo.Info{Name: "T", PieceLength: 1 << 14, Files: []metainfo.FileInfo{{Length: 10, Path: []string{"a.mkv"}}}}
	ib, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	tiers := make([][]string, 0, len(announces))
	for _, a := range announces {
		tiers = append(tiers, []string{a})
	}
	mi := metainfo.MetaInfo{InfoBytes: ib, AnnounceList: tiers}
	f, err := os.Create(filepath.Join(dir, h.HexString()+".torrent"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	if err := mi.Write(f); err != nil {
		t.Fatalf("write metainfo: %v", err)
	}
}

// Evidence for the boot-race bug: relocatedStorage NEEDS the file-path resolver,
// so resumeSeeding must wait for HasFilePathResolver — otherwise a resumed seed
// races ahead, relocatedStorage returns nil, and it falls back to the empty cache
// storage (showing 0%). This pins the gate resumeSeeding polls.
func TestHasFilePathResolver(t *testing.T) {
	s := &Streamer{}
	if s.HasFilePathResolver() {
		t.Fatal("expected false before SetFilePathResolver")
	}
	// Without the resolver, relocatedStorage must bail (the race's bad outcome).
	if s.relocatedStorage(twoFileInfo(), metainfo.Hash{}) != nil {
		t.Fatal("relocatedStorage must be nil without a resolver")
	}
	s.SetFilePathResolver(func(metainfo.Hash, int) (string, bool) { return "", false })
	if !s.HasFilePathResolver() {
		t.Fatal("expected true after SetFilePathResolver")
	}
}

func TestMatchesSeedTrackerCached(t *testing.T) {
	dir := t.TempDir()
	var h metainfo.Hash
	h[0] = 0xab
	writeCachedMetainfo(t, dir, h, []string{
		"https://jackui.club/announce.php?passkey=xyz",
		"udp://tracker.openbittorrent.com:80/announce",
	})

	s := &Streamer{metainfoDir: dir, seedTrackers: []string{"jackui"}}
	if !s.MatchesSeedTrackerCached(h) {
		t.Fatal("expected match for jackui announce")
	}

	s.seedTrackers = []string{"outro-tracker"}
	if s.MatchesSeedTrackerCached(h) {
		t.Fatal("expected no match when seed-tracker absent from announce")
	}

	// Unknown hash → no cached metainfo → false.
	var other metainfo.Hash
	other[0] = 0x99
	s.seedTrackers = []string{"jackui"}
	if s.MatchesSeedTrackerCached(other) {
		t.Fatal("expected false when no cached metainfo exists")
	}
}

func twoFileInfo() *metainfo.Info {
	return &metainfo.Info{
		Name:        "MyTorrent",
		PieceLength: 1 << 14,
		Files: []metainfo.FileInfo{
			{Length: 10, Path: []string{"a.mkv"}},
			{Length: 20, Path: []string{"subdir", "b.mkv"}},
		},
	}
}

func TestFileIndexInInfo(t *testing.T) {
	info := twoFileInfo()
	files := info.UpvertedFiles()
	if got := fileIndexInInfo(info, &files[0]); got != 0 {
		t.Fatalf("file 0 index = %d, want 0", got)
	}
	if got := fileIndexInInfo(info, &files[1]); got != 1 {
		t.Fatalf("file 1 index = %d, want 1", got)
	}
	other := metainfo.FileInfo{Path: []string{"nope.mkv"}}
	if got := fileIndexInInfo(info, &other); got != -1 {
		t.Fatalf("unknown file index = %d, want -1", got)
	}
	if got := fileIndexInInfo(nil, &files[0]); got != -1 {
		t.Fatalf("nil info = %d, want -1", got)
	}
}

func TestRelocatedStorage_NilCases(t *testing.T) {
	dataDir := t.TempDir()
	info := twoFileInfo()
	var h metainfo.Hash

	// No resolver → nil.
	s := &Streamer{cfg: Config{DataDir: dataDir}}
	if s.relocatedStorage(info, h) != nil {
		t.Fatal("expected nil without a resolver")
	}

	// File 0 resolves UNDER DataDir → nil (default storage already handles it).
	inCache := filepath.Join(dataDir, "MyTorrent", "a.mkv")
	_ = os.MkdirAll(filepath.Dir(inCache), 0o755)
	_ = os.WriteFile(inCache, []byte("x"), 0o644)
	s.filePathResolver = func(metainfo.Hash, int) (string, bool) { return inCache, true }
	if s.relocatedStorage(info, h) != nil {
		t.Fatal("expected nil when file is under DataDir")
	}

	// File 0 doesn't exist → nil.
	s.filePathResolver = func(metainfo.Hash, int) (string, bool) {
		return filepath.Join(t.TempDir(), "ghost.mkv"), true
	}
	if s.relocatedStorage(info, h) != nil {
		t.Fatal("expected nil when resolved file doesn't exist")
	}
}

func TestRelocatedStorage_OutsideCache(t *testing.T) {
	dataDir := t.TempDir()
	bulk := t.TempDir() // a different root, outside DataDir
	real := filepath.Join(bulk, "MyTorrent", "a.mkv")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Streamer{
		cfg:              Config{DataDir: dataDir},
		filePathResolver: func(_ metainfo.Hash, idx int) (string, bool) { return real, true },
	}
	var h metainfo.Hash
	st := s.relocatedStorage(twoFileInfo(), h)
	if st == nil {
		t.Fatal("expected a relocated storage for a file outside DataDir")
	}
	if c, ok := st.(interface{ Close() error }); ok {
		_ = c.Close()
	}
}
