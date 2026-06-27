package handlers

import (
	"net"
	"testing"

	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// freePort grabs an ephemeral TCP port so the test streamer doesn't collide with
// the fixed default (51469), which may be held by a running instance.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// TestRelinkMovedTorrents verifies that moving/renaming a local file rewrites the
// linked download's file_path to the new location (exact file and files under a
// moved folder), while leaving unrelated downloads untouched. A nil streamer is
// fine — the Drop step is skipped.
func TestRelinkMovedTorrents(t *testing.T) {
	dls, err := downloads.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}
	defer dls.Close()

	oldDir := "/srv/cache/Filmes"
	// Exact file at oldDir, a file nested under oldDir, and an unrelated one.
	exact, _ := dls.Create(downloads.Download{UserID: 1, InfoHash: "h1", Magnet: "m1", FilePath: oldDir + "/movie.mkv", Name: "movie"})
	nested, _ := dls.Create(downloads.Download{UserID: 1, InfoHash: "h2", Magnet: "m2", FilePath: oldDir + "/Season 1/ep.mkv", Name: "ep"})
	other, _ := dls.Create(downloads.Download{UserID: 1, InfoHash: "h3", Magnet: "m3", FilePath: "/srv/cache/Series/x.mkv", Name: "x"})

	newDir := "/srv/shared/Filmes (2020)"
	if n := relinkMovedTorrents(dls, nil, oldDir, newDir); n != 2 {
		t.Fatalf("relinked count = %d, want 2", n)
	}

	paths := map[int]string{}
	list, err := dls.List(1)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range list {
		paths[d.ID] = d.FilePath
	}

	if got, want := paths[exact.ID], newDir+"/movie.mkv"; got != want {
		t.Errorf("exact file path = %q, want %q", got, want)
	}
	if got, want := paths[nested.ID], newDir+"/Season 1/ep.mkv"; got != want {
		t.Errorf("nested file path = %q, want %q", got, want)
	}
	if got, want := paths[other.ID], "/srv/cache/Series/x.mkv"; got != want {
		t.Errorf("unrelated file path changed to %q, want %q", got, want)
	}
}

// A nil store must be a no-op, not a panic.
func TestRelinkMovedTorrentsNilStore(t *testing.T) {
	if n := relinkMovedTorrents(nil, nil, "/a", "/b"); n != 0 {
		t.Fatalf("nil store: want 0, got %d", n)
	}
}

// With a real streamer, relink also drops the (inactive) torrent by info_hash —
// a no-op for a hash that isn't loaded, but it exercises the Drop path so a move
// never leaves a stale descriptor behind.
func TestRelinkMovedTorrentsDropsTorrent(t *testing.T) {
	s, err := streamer.New(streamer.Config{DataDir: t.TempDir(), ListenPort: freePort(t)})
	if err != nil {
		t.Skipf("streamer unavailable in this env: %v", err)
	}
	defer s.Close()

	dls, err := downloads.New(seededPool(t))
	if err != nil {
		t.Fatal(err)
	}
	defer dls.Close()

	hash := "0123456789abcdef0123456789abcdef01234567" // valid 40-hex, not loaded
	d, _ := dls.Create(downloads.Download{UserID: 1, InfoHash: hash, Magnet: "m", FilePath: "/old/f.mkv", Name: "f"})

	if n := relinkMovedTorrents(dls, s, "/old", "/new"); n != 1 {
		t.Fatalf("relinked = %d, want 1", n)
	}
	list, _ := dls.List(1)
	for _, row := range list {
		if row.ID == d.ID && row.FilePath != "/new/f.mkv" {
			t.Fatalf("file_path = %q, want /new/f.mkv", row.FilePath)
		}
	}
}
