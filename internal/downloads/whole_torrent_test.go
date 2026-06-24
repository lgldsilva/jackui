package downloads

import (
	"context"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/lgldsilva/jackui/internal/streamer"
)

// waitForStatus polls the row until its status equals want or the deadline
// passes — the post-download move now runs OFF the tick in its own goroutine, so
// completion/failure is observed asynchronously.
func waitForStatus(t *testing.T, store *Store, userID, id int, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got, _ := store.Get(userID, id); got != nil && got.Status == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	got, _ := store.Get(userID, id)
	status := "<nil>"
	if got != nil {
		status = got.Status
	}
	t.Fatalf("status = %q after %s, want %q", status, timeout, want)
}

// ─── store: sentinel semantics ──────────────────────────────────────────────

func TestStore_WholeTorrentSentinel_RoundTrip(t *testing.T) {
	s := dlwNewStore(t)
	d, err := s.Create(Download{
		UserID: 1, InfoHash: "h1", FileIndex: FileIndexWholeTorrent,
		Magnet: "magnet:?xt=urn:btih:h1", Name: "Whole", FileSize: 5000,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if d.FileIndex != FileIndexWholeTorrent {
		t.Fatalf("FileIndex = %d, want %d", d.FileIndex, FileIndexWholeTorrent)
	}
	if !d.IsWholeTorrent() {
		t.Fatal("IsWholeTorrent must be true for the sentinel")
	}
	// The UNIQUE(user_id, info_hash, file_index) constraint dedupes: re-queueing
	// the whole torrent returns the SAME row (no second item in the queue).
	again, err := s.Create(Download{
		UserID: 1, InfoHash: "h1", FileIndex: FileIndexWholeTorrent,
		Magnet: "magnet:?xt=urn:btih:h1", Name: "Whole",
	})
	if err != nil {
		t.Fatalf("Create (again): %v", err)
	}
	if again.ID != d.ID {
		t.Fatalf("re-enqueue created a new row: %d != %d", again.ID, d.ID)
	}
	// A whole row and a per-file row for the same torrent can coexist (distinct
	// file_index values) — old clients keep working.
	perFile, err := s.Create(Download{
		UserID: 1, InfoHash: "h1", FileIndex: 0,
		Magnet: "magnet:?xt=urn:btih:h1", Name: "Whole",
	})
	if err != nil {
		t.Fatalf("Create per-file: %v", err)
	}
	if perFile.ID == d.ID {
		t.Fatal("per-file row must be a separate row from the whole row")
	}
	if perFile.IsWholeTorrent() {
		t.Fatal("per-file row must not report IsWholeTorrent")
	}
}

func TestStore_WholeTorrentSentinel_RejectsBelowMin(t *testing.T) {
	s := dlwNewStore(t)
	_, err := s.Create(Download{
		UserID: 1, InfoHash: "h2", FileIndex: -3,
		Magnet: "magnet:?xt=urn:btih:h2", Name: "Bad",
	})
	if err == nil {
		t.Fatal("expected error for fileIndex below the whole-torrent sentinel")
	}
}

func TestStore_WholeTorrentSentinel_AutoStaysDistinct(t *testing.T) {
	// -1 (auto-pick, Transmission RPC) and -2 (whole) must not collide.
	s := dlwNewStore(t)
	auto, err := s.Create(Download{
		UserID: 1, InfoHash: "h3", FileIndex: FileIndexAuto,
		Magnet: "magnet:?xt=urn:btih:h3", Name: "Auto",
	})
	if err != nil {
		t.Fatalf("Create auto: %v", err)
	}
	whole, err := s.Create(Download{
		UserID: 1, InfoHash: "h3", FileIndex: FileIndexWholeTorrent,
		Magnet: "magnet:?xt=urn:btih:h3", Name: "Whole",
	})
	if err != nil {
		t.Fatalf("Create whole: %v", err)
	}
	if auto.ID == whole.ID {
		t.Fatal("auto (-1) and whole (-2) rows must be distinct")
	}
	if auto.IsWholeTorrent() {
		t.Fatal("auto-pick row must not report IsWholeTorrent")
	}
}

// ─── worker: aggregate progress via the wholeTarget fake ────────────────────

// fakeWhole implements wholeTarget without a live anacrolix client.
type fakeWhole struct {
	completed   int64
	length      int64
	files       []*torrent.File
	downloadAll int // times DownloadAll was invoked
}

func (f *fakeWhole) BytesCompleted() int64  { return f.completed }
func (f *fakeWhole) Length() int64          { return f.length }
func (f *fakeWhole) Files() []*torrent.File { return f.files }
func (f *fakeWhole) DownloadAll()           { f.downloadAll++ }

func TestTrackedDL_Progress_WholeAggregates(t *testing.T) {
	td := &trackedDL{whole: &fakeWhole{completed: 1234, length: 5000}}
	completed, total, ok := td.progress()
	if !ok || completed != 1234 || total != 5000 {
		t.Fatalf("progress() = (%d, %d, %v), want (1234, 5000, true)", completed, total, ok)
	}
	// No target at all → not ok.
	if _, _, ok := (&trackedDL{}).progress(); ok {
		t.Fatal("progress() without file/whole must report ok=false")
	}
}

func TestInitTarget_WholeCallsDownloadAll(t *testing.T) {
	store := dlwNewStore(t)
	w := dlwNewWorker(t, store, t.TempDir(), "")
	d, err := store.Create(Download{
		UserID: 1, InfoHash: "wh", FileIndex: FileIndexWholeTorrent,
		Magnet: "magnet:?xt=urn:btih:wh", Name: "W",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	fake := &fakeWhole{length: 100}
	f, whole, ok := w.initTarget(d, metainfo.Hash{}, fake)
	if !ok {
		t.Fatal("initTarget must succeed for a whole-torrent row")
	}
	if f != nil {
		t.Fatal("whole-torrent init must not pick a single file")
	}
	if whole == nil {
		t.Fatal("whole-torrent init must return the aggregate target")
	}
	if fake.downloadAll != 1 {
		t.Fatalf("DownloadAll called %d times, want 1", fake.downloadAll)
	}
}

func TestSampleProgress_WholePersistsAggregate(t *testing.T) {
	store := dlwNewStore(t)
	w := dlwNewWorker(t, store, t.TempDir(), "")
	d, _ := store.Create(Download{
		UserID: 1, InfoHash: "wp", FileIndex: FileIndexWholeTorrent,
		Magnet: "magnet:?xt=urn:btih:wp", Name: "W", FileSize: 100,
	})
	td := &trackedDL{id: d.ID, userID: 1, whole: &fakeWhole{completed: 42, length: 100}}
	w.sampleProgress(*d, td)
	got, _ := store.Get(1, d.ID)
	if got.BytesDownloaded != 42 {
		t.Fatalf("BytesDownloaded = %d, want 42 (aggregate)", got.BytesDownloaded)
	}
}

func TestCheckCompletion_WholeIncompleteNoop(t *testing.T) {
	store := dlwNewStore(t)
	w := dlwNewWorker(t, store, t.TempDir(), t.TempDir())
	d, _ := store.Create(Download{
		UserID: 1, InfoHash: "wi", FileIndex: FileIndexWholeTorrent,
		Magnet: "magnet:?xt=urn:btih:wi", Name: "W", FileSize: 100,
	})
	td := &trackedDL{id: d.ID, userID: 1, name: "W", whole: &fakeWhole{completed: 50, length: 100}}
	w.checkCompletion(*d, td)
	got, _ := store.Get(1, d.ID)
	if got.Status == StatusCompleted {
		t.Fatal("incomplete whole torrent must not flip to completed")
	}
}

// ─── worker: completion moves the WHOLE tree preserving structure ───────────

// wholeSpecTorrent builds an info-complete MULTI-FILE torrent in a throwaway
// anacrolix client so td.whole.Files() yields real *torrent.File values.
func wholeSpecTorrent(t *testing.T, name string, paths [][]string) *torrent.Torrent {
	t.Helper()
	files := make([]metainfo.FileInfo, 0, len(paths))
	for _, p := range paths {
		files = append(files, metainfo.FileInfo{Path: p, Length: 4})
	}
	return wholeSpecTorrentFI(t, name, files)
}

// wholeSpecTorrentFI is wholeSpecTorrent taking raw metainfo.FileInfo values,
// for tests that need extended attrs (e.g. BEP 47 pad files).
func wholeSpecTorrentFI(t *testing.T, name string, files []metainfo.FileInfo) *torrent.Torrent {
	t.Helper()
	const piece = 1 << 14
	data := bytes.Repeat([]byte("z"), piece)
	pieceHash := metainfo.HashBytes(data)
	info := metainfo.Info{Name: name, PieceLength: piece, Files: files, Pieces: pieceHash[:]}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("bencode.Marshal: %v", err)
	}
	spec, err := torrent.TorrentSpecFromMetaInfoErr(&metainfo.MetaInfo{InfoBytes: infoBytes})
	if err != nil {
		t.Fatalf("TorrentSpecFromMetaInfoErr: %v", err)
	}
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = t.TempDir()
	cfg.NoDHT = true
	cfg.DisableTrackers = true
	cfg.DisableTCP = true
	cfg.DisableUTP = true
	cfg.ListenPort = 0
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatalf("torrent.NewClient: %v", err)
	}
	t.Cleanup(func() { cl.Close() })
	tor, _, err := cl.AddTorrentSpec(spec)
	if err != nil {
		t.Fatalf("AddTorrentSpec: %v", err)
	}
	return tor
}

func TestMoveCompletedTorrent_PreservesStructure(t *testing.T) {
	store := dlwNewStore(t)
	s := streamer.NewForTesting()
	dataDir := t.TempDir()
	downloadDir := t.TempDir()
	w := NewWorker(WorkerConfig{
		Store: store, Streamer: s, DataDir: dataDir, DownloadDir: downloadDir,
		ResolveUsername: func(int) string { return "alice" },
	})
	d, err := store.Create(Download{
		UserID: 1, InfoHash: "mt", FileIndex: FileIndexWholeTorrent,
		Magnet: "magnet:?xt=urn:btih:mt", Name: "Pack",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	tor := wholeSpecTorrent(t, "Pack", [][]string{
		{"Sub", "a.bin"},
		{"b.bin"}, // one finished as a leftover .part (storage didn't rename yet)
	})
	// Lay the finished bytes out in the cache the way anacrolix file storage does:
	// dataDir/<torrentName>/<path-in-torrent>.
	if err := os.MkdirAll(filepath.Join(dataDir, "Pack", "Sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "Pack", "Sub", "a.bin"), []byte("aaaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "Pack", "b.bin.part"), []byte("bbbb"), 0o644); err != nil {
		t.Fatal(err)
	}

	td := &trackedDL{id: d.ID, userID: 1, name: "Pack", whole: tor}
	destDir, err := w.moveCompletedTorrent(*d, td)
	if err != nil {
		t.Fatalf("moveCompletedTorrent: %v", err)
	}
	wantDir := filepath.Join(downloadDir, "alice", "Pack")
	if destDir != wantDir {
		t.Fatalf("destDir = %q, want %q", destDir, wantDir)
	}
	// Structure preserved; the .part landed under its final name.
	if !fileExists(filepath.Join(wantDir, "Sub", "a.bin")) {
		t.Error("Sub/a.bin should exist at the destination")
	}
	if !fileExists(filepath.Join(wantDir, "b.bin")) {
		t.Error("b.bin should exist at the destination (renamed from .part)")
	}
	got, _ := store.Get(1, d.ID)
	if got.FilePath != wantDir {
		t.Fatalf("file_path = %q, want the torrent dest dir %q", got.FilePath, wantDir)
	}
}

func TestMoveCompletedTree_IdempotentSkipsAlreadyMoved(t *testing.T) {
	dataDir := t.TempDir()
	destDir := filepath.Join(t.TempDir(), "T")
	// First file already at the destination (previous interrupted attempt);
	// second still in the cache.
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destDir, "done.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "T"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "T", "todo.bin"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := moveCompletedTree(context.Background(), dataDir, destDir, "T", []string{"T/done.bin", "T/todo.bin"}, nil, nil)
	if err != nil {
		t.Fatalf("moveCompletedTree: %v", err)
	}
	if !fileExists(filepath.Join(destDir, "todo.bin")) {
		t.Error("todo.bin should have been moved")
	}
	// Missing source AND missing destination → hard error (don't fake success).
	err = moveCompletedTree(context.Background(), dataDir, destDir, "T", []string{"T/ghost.bin"}, nil, nil)
	if err == nil {
		t.Fatal("expected error for a file missing from both cache and destination")
	}
}

func TestWholeTorrentDest_RejectsTraversal(t *testing.T) {
	if _, err := wholeTorrentDest("/dl/T", "T", "../../etc/passwd"); err == nil {
		t.Fatal("expected traversal rejection")
	}
	got, err := wholeTorrentDest("/dl/T", "T", "T/Sub/x.bin")
	if err != nil {
		t.Fatalf("wholeTorrentDest: %v", err)
	}
	if got != filepath.Join("/dl/T", "Sub", "x.bin") {
		t.Fatalf("dest = %q", got)
	}
	// Single-file torrents have no leading dir — path is kept as-is.
	got, err = wholeTorrentDest("/dl/T", "T", "movie.mkv")
	if err != nil {
		t.Fatalf("wholeTorrentDest single: %v", err)
	}
	if got != filepath.Join("/dl/T", "movie.mkv") {
		t.Fatalf("single dest = %q", got)
	}
}

func TestCheckCompletion_WholeCompleteMovesAndFinishes(t *testing.T) {
	store := dlwNewStore(t)
	s := streamer.NewForTesting()
	dataDir := t.TempDir()
	downloadDir := t.TempDir()
	w := NewWorker(WorkerConfig{Store: store, Streamer: s, DataDir: dataDir, DownloadDir: downloadDir})
	d, _ := store.Create(Download{
		UserID: 1, InfoHash: "wc", FileIndex: FileIndexWholeTorrent,
		Magnet: "magnet:?xt=urn:btih:wc", Name: "Pack2", FileSize: 4,
	})
	tor := wholeSpecTorrent(t, "Pack2", [][]string{{"only.bin"}})
	if err := os.MkdirAll(filepath.Join(dataDir, "Pack2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "Pack2", "only.bin"), []byte("zzzz"), 0o644); err != nil {
		t.Fatal(err)
	}
	// fakeWhole reports the aggregate as complete; Files() still needs the real
	// paths for the move, so wrap the real torrent's files.
	fake := &fakeWhole{completed: 4, length: 4, files: tor.Files()}
	td := &trackedDL{id: d.ID, userID: 1, name: "Pack2", whole: fake}
	w.moveBackoff = time.Millisecond
	w.tracked[d.ID] = td
	w.checkCompletion(*d, td)
	// checkCompletion untracks synchronously and runs the move off the tick.
	w.mu.Lock()
	_, still := w.tracked[d.ID]
	w.mu.Unlock()
	if still {
		t.Error("download handed to the move must be untracked")
	}
	waitForStatus(t, store, 1, d.ID, StatusCompleted, 2*time.Second)
	want := filepath.Join(downloadDir, "Pack2", "only.bin")
	if !fileExists(want) {
		t.Errorf("file should be moved to %q", want)
	}
	got, _ := store.Get(1, d.ID)
	if got.FilePath != filepath.Join(downloadDir, "Pack2") {
		t.Errorf("file_path = %q, want the torrent dest dir", got.FilePath)
	}
}

func TestInitTarget_PerFilePicksRequestedFile(t *testing.T) {
	store := dlwNewStore(t)
	w := dlwNewWorker(t, store, t.TempDir(), "")
	d, err := store.Create(Download{
		UserID: 1, InfoHash: "pf", FileIndex: 1,
		Magnet: "magnet:?xt=urn:btih:pf", Name: "P",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	tor := wholeSpecTorrent(t, "P", [][]string{{"a.bin"}, {"b.bin"}})
	// VerifyFile errors (torrent not active in the test streamer) — logged, not
	// fatal; the per-file branch must still resolve and mark the file wanted.
	f, whole, ok := w.initTarget(d, tor.InfoHash(), tor)
	if !ok {
		t.Fatal("initTarget must succeed for an in-bounds per-file row")
	}
	if whole != nil {
		t.Fatal("per-file init must not return an aggregate target")
	}
	if f == nil || !strings.HasSuffix(f.Path(), "b.bin") {
		t.Fatalf("initTarget picked %v, want file index 1 (b.bin)", f)
	}
}

func TestInitTarget_PerFileNoFilesFails(t *testing.T) {
	store := dlwNewStore(t)
	w := dlwNewWorker(t, store, t.TempDir(), "")
	d, err := store.Create(Download{
		UserID: 1, InfoHash: "nf", FileIndex: 0,
		Magnet: "magnet:?xt=urn:btih:nf", Name: "Empty",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	f, whole, ok := w.initTarget(d, metainfo.Hash{}, &fakeWhole{})
	if ok || f != nil || whole != nil {
		t.Fatal("initTarget must fail when the torrent has no files")
	}
	got, _ := store.Get(1, d.ID)
	if got.Status != StatusFailed {
		t.Fatalf("status = %q, want failed (no files in torrent)", got.Status)
	}
}

func TestPeerCount_NilSafe(t *testing.T) {
	if got := peerCount(nil); got != 0 {
		t.Fatalf("peerCount(nil) = %d, want 0", got)
	}
	tor := wholeSpecTorrent(t, "PC", [][]string{{"x.bin"}})
	if got := peerCount(tor); got != 0 {
		t.Fatalf("peerCount(idle torrent) = %d, want 0", got)
	}
}

func TestMoveCompletedTree_RejectsUnsafePath(t *testing.T) {
	err := moveCompletedTree(context.Background(), t.TempDir(), t.TempDir(), "T", []string{"../evil.bin"}, nil, nil)
	if err == nil {
		t.Fatal("expected traversal rejection to propagate")
	}
}

func TestCheckCompletion_WholeMoveErrorMarksFailed(t *testing.T) {
	store := dlwNewStore(t)
	w := dlwNewWorker(t, store, t.TempDir(), t.TempDir())
	d, _ := store.Create(Download{
		UserID: 1, InfoHash: "we", FileIndex: FileIndexWholeTorrent,
		Magnet: "magnet:?xt=urn:btih:we", Name: "PackErr", FileSize: 4,
	})
	tor := wholeSpecTorrent(t, "PackErr", [][]string{{"x.bin"}})
	// Aggregate says complete, but the bytes never landed in the cache NOR the
	// destination → the move keeps failing. After moveMaxAttempts the row must end
	// as `failed` with a captured error — NOT wedged at "100% downloading" forever
	// (the bug the user reported), and NOT a phantom `completed`.
	fake := &fakeWhole{completed: 4, length: 4, files: tor.Files()}
	td := &trackedDL{id: d.ID, userID: 1, name: "PackErr", whole: fake}
	w.moveBackoff = time.Millisecond
	w.tracked[d.ID] = td
	w.checkCompletion(*d, td)
	waitForStatus(t, store, 1, d.ID, StatusFailed, 2*time.Second)
	got, _ := store.Get(1, d.ID)
	if got.Error == "" {
		t.Fatal("a failed move must capture the error message")
	}
}

// ─── scheduler: the whole-torrent row takes exactly ONE active slot ─────────

func TestSchedule_WholeTorrentCountsAsOneActive(t *testing.T) {
	store := dlwNewStore(t)
	w := NewWorker(WorkerConfig{
		Store: store, Streamer: streamer.NewForTesting(), DataDir: t.TempDir(),
		Settings: func() QueueSettings {
			return QueueSettings{MaxActive: 2, StallThresholdMin: 30, MaxStalls: 3}
		},
	})
	whole, _ := store.Create(Download{
		UserID: 1, InfoHash: "s1", FileIndex: FileIndexWholeTorrent,
		Magnet: "magnet:?xt=urn:btih:s1", Name: "Whole",
	})
	one, _ := store.Create(Download{
		UserID: 1, InfoHash: "s2", FileIndex: 0,
		Magnet: "magnet:?xt=urn:btih:s2", Name: "One",
	})
	two, _ := store.Create(Download{
		UserID: 1, InfoHash: "s3", FileIndex: 0,
		Magnet: "magnet:?xt=urn:btih:s3", Name: "Two",
	})
	w.applySchedule(w.queueSettings())
	statuses := map[int]string{}
	for _, id := range []int{whole.ID, one.ID, two.ID} {
		got, _ := store.Get(1, id)
		statuses[id] = got.Status
	}
	active := 0
	for _, st := range statuses {
		if st == StatusDownloading {
			active++
		}
	}
	if active != 2 {
		t.Fatalf("active = %d, want exactly MaxActive=2 (whole torrent is ONE slot): %v", active, statuses)
	}
	if statuses[whole.ID] != StatusDownloading {
		t.Fatalf("the whole-torrent row (oldest) should hold one slot, got %q", statuses[whole.ID])
	}
}
