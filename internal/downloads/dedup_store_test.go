package downloads

import "testing"

func TestCreateLinked_InsertsCompletedLinkedRow(t *testing.T) {
	s := newTestStore(t)
	d, err := s.CreateLinked(Download{
		UserID:    1,
		InfoHash:  "hashA",
		FileIndex: 0,
		Name:      "Movie",
		Magnet:    "magnet:?xt=urn:btih:hashA",
	}, "/mnt/storage/Downloads/Movie/movie.mkv", 4096)
	if err != nil {
		t.Fatalf("CreateLinked: %v", err)
	}
	if !d.Linked {
		t.Fatal("row must be marked linked")
	}
	if d.Status != StatusCompleted {
		t.Fatalf("status=%q want completed", d.Status)
	}
	if d.FilePath != "/mnt/storage/Downloads/Movie/movie.mkv" || d.FileSize != 4096 {
		t.Fatalf("path/size not persisted: %q %d", d.FilePath, d.FileSize)
	}
	// Round-trips the linked flag through Get/scanGeneric.
	got, err := s.Get(1, d.ID)
	if err != nil || !got.Linked {
		t.Fatalf("Get linked round-trip failed: linked=%v err=%v", got != nil && got.Linked, err)
	}
}

func TestCreateLinked_ConvertsExistingRow(t *testing.T) {
	s := newTestStore(t)
	// A normal queued download for the same (user, hash, index)...
	q, err := s.Create(Download{UserID: 1, InfoHash: "hashB", FileIndex: 2, Magnet: "magnet:?xt=urn:btih:hashB", Name: "X", FilePath: "X", FileSize: 10})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if q.Status != StatusQueued || q.Linked {
		t.Fatalf("precondition: want queued non-linked, got %q linked=%v", q.Status, q.Linked)
	}
	// ...is adopted in place as a linked completion.
	d, err := s.CreateLinked(Download{UserID: 1, InfoHash: "hashB", FileIndex: 2, Magnet: "magnet:?xt=urn:btih:hashB"}, "/mnt/gdrive/media/x.mkv", 99)
	if err != nil {
		t.Fatalf("CreateLinked: %v", err)
	}
	if d.ID != q.ID {
		t.Fatalf("expected the existing row converted in place: id %d != %d", d.ID, q.ID)
	}
	if !d.Linked || d.Status != StatusCompleted || d.FilePath != "/mnt/gdrive/media/x.mkv" {
		t.Fatalf("row not converted: linked=%v status=%q path=%q", d.Linked, d.Status, d.FilePath)
	}
}

func TestCreateLinked_Validation(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateLinked(Download{UserID: 1, InfoHash: "h", FileIndex: 0}, "", 10); err == nil {
		t.Error("empty externalPath must error")
	}
	if _, err := s.CreateLinked(Download{UserID: 1, InfoHash: "h", FileIndex: -1}, "/x", 10); err == nil {
		t.Error("file_index < 0 must error (whole-torrent/best-file can't be linked per-file)")
	}
	if _, err := s.CreateLinked(Download{UserID: 1, InfoHash: "", FileIndex: 0}, "/x", 10); err == nil {
		t.Error("empty infoHash must error")
	}
}

func TestCompletedBySize(t *testing.T) {
	s := newTestStore(t)
	// user 1: a completed file of size 5000 (the candidate we expect to find)...
	if _, err := s.CreateLinked(Download{UserID: 1, InfoHash: "h1", FileIndex: 0, Magnet: "m", Name: "A"}, "/lib/a.mkv", 5000); err != nil {
		t.Fatalf("seed completed: %v", err)
	}
	// ...a completed file of a DIFFERENT size (must not match)...
	if _, err := s.CreateLinked(Download{UserID: 1, InfoHash: "h2", FileIndex: 0, Magnet: "m", Name: "B"}, "/lib/b.mkv", 7777); err != nil {
		t.Fatalf("seed other size: %v", err)
	}
	// ...a QUEUED (not completed) row of the right size (must not match)...
	if _, err := s.Create(Download{UserID: 1, InfoHash: "h3", FileIndex: 0, Magnet: "m", Name: "C", FilePath: "c", FileSize: 5000}); err != nil {
		t.Fatalf("seed queued: %v", err)
	}
	// ...and ANOTHER user's completed file of the right size (privacy: must not leak).
	if _, err := s.CreateLinked(Download{UserID: 2, InfoHash: "h4", FileIndex: 0, Magnet: "m", Name: "D"}, "/lib/d.mkv", 5000); err != nil {
		t.Fatalf("seed other user: %v", err)
	}

	got, err := s.CompletedBySize(1, 5000)
	if err != nil {
		t.Fatalf("CompletedBySize: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly 1 candidate, got %d: %+v", len(got), got)
	}
	if got[0].FilePath != "/lib/a.mkv" {
		t.Fatalf("wrong candidate: %q", got[0].FilePath)
	}
	if n, _ := s.CompletedBySize(1, 0); n != nil {
		t.Fatalf("size 0 must return nil, got %+v", n)
	}
}

func TestRefCountPath(t *testing.T) {
	s := newTestStore(t)
	const p = "/mnt/storage/Downloads/shared.mkv"
	if n, _ := s.RefCountPath(p); n != 0 {
		t.Fatalf("empty store: want 0, got %d", n)
	}
	// Two different downloads adopting the SAME external file.
	if _, err := s.CreateLinked(Download{UserID: 1, InfoHash: "h1", FileIndex: 0, Magnet: "m"}, p, 10); err != nil {
		t.Fatalf("CreateLinked#1: %v", err)
	}
	if _, err := s.CreateLinked(Download{UserID: 1, InfoHash: "h2", FileIndex: 0, Magnet: "m"}, p, 10); err != nil {
		t.Fatalf("CreateLinked#2: %v", err)
	}
	if n, _ := s.RefCountPath(p); n != 2 {
		t.Fatalf("two rows on the same path: want 2, got %d", n)
	}
	if n, _ := s.RefCountPath("/somewhere/else.mkv"); n != 0 {
		t.Fatalf("unreferenced path: want 0, got %d", n)
	}
}
