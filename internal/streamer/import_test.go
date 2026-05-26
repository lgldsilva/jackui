package streamer

import (
	"os"
	"strings"
	"testing"
)

// TestParseMagnet covers the happy path + display-name fallback to hash.
func TestParseMagnet(t *testing.T) {
	s := &Streamer{}

	// Magnet WITH a display name.
	hash, name, err := s.ParseMagnet("magnet:?xt=urn:btih:bfb1741ecb8e7641158943545beb97c216158405&dn=Star+Wars")
	if err != nil {
		t.Fatalf("ParseMagnet: %v", err)
	}
	if hash != "bfb1741ecb8e7641158943545beb97c216158405" {
		t.Errorf("hash: got %q", hash)
	}
	if name != "Star Wars" {
		t.Errorf("name: want %q got %q", "Star Wars", name)
	}

	// Magnet WITHOUT a display name → falls back to the hash.
	hash2, name2, err := s.ParseMagnet("magnet:?xt=urn:btih:bfb1741ecb8e7641158943545beb97c216158405")
	if err != nil {
		t.Fatalf("ParseMagnet no-dn: %v", err)
	}
	if name2 != hash2 {
		t.Errorf("name fallback: want hash %q got %q", hash2, name2)
	}

	// Leading junk before the magnet: scheme is recovered.
	if _, _, err := s.ParseMagnet("  magnet:?xt=urn:btih:bfb1741ecb8e7641158943545beb97c216158405"); err != nil {
		t.Errorf("leading space should be tolerated: %v", err)
	}

	// Garbage → error.
	if _, _, err := s.ParseMagnet("not a magnet"); err == nil {
		t.Error("expected error for non-magnet input")
	}
}

// TestImportTorrentBytes builds a real .torrent in memory, imports it, and
// asserts the hash/name come back and the metainfo is persisted to disk.
func TestImportTorrentBytes(t *testing.T) {
	dir := t.TempDir()
	s := &Streamer{metainfoDir: dir}

	// Minimal valid single-file torrent, bencoded by hand. Top-level dict
	// { "info": { length, name, "piece length", pieces } }. pieces must be a
	// multiple of 20 bytes (one SHA-1 per piece).
	pieces := strings.Repeat("\x00", 20)
	info := "d6:lengthi1024e4:name9:movie.mp412:piece lengthi16384e6:pieces20:" + pieces + "e"
	torrent := "d4:info" + info + "e"

	hash, name, err := s.ImportTorrentBytes([]byte(torrent))
	if err != nil {
		t.Fatalf("ImportTorrentBytes: %v", err)
	}
	if name != "movie.mp4" {
		t.Errorf("name: want movie.mp4 got %q", name)
	}
	if len(hash) != 40 {
		t.Errorf("hash should be 40 hex chars, got %q", hash)
	}
	// Metainfo must be persisted for instant future playback — exactly one
	// .torrent file should now exist in the cache dir.
	entries, _ := os.ReadDir(dir)
	nTorrent := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".torrent") {
			nTorrent++
		}
	}
	if nTorrent != 1 {
		t.Errorf("expected 1 persisted .torrent, found %d", nTorrent)
	}

	// Corrupt input → clean error, no panic.
	if _, _, err := s.ImportTorrentBytes([]byte("not a torrent")); err == nil {
		t.Error("expected error for corrupt .torrent")
	}
}
