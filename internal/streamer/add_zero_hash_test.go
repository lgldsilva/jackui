package streamer

import (
	"context"
	"strings"
	"testing"
)

// TestAdd_ZeroInfoHashIsAnError guards #216: anacrolix's AddTorrentOpt panics
// on an all-zero v1 info hash with no v2 hash, so a magnet carrying one must be
// rejected as an ordinary error before it reaches the client.
func TestAdd_ZeroInfoHashIsAnError(t *testing.T) {
	s, _ := newRealStreamer(t)
	zero := strings.Repeat("0", 40)
	for _, magnet := range []string{
		"magnet:?xt=urn:btih:" + zero,
		"magnet:?xt=urn:btih:" + zero + "&dn=x&tr=udp%3A%2F%2Ftracker.invalid%3A1",
	} {
		info, err := s.Add(context.Background(), magnet)
		if err == nil || info != nil {
			t.Fatalf("Add(%q) = %v, %v; want nil info and an error", magnet, info, err)
		}
		if !strings.Contains(err.Error(), "zero info hash") {
			t.Fatalf("Add(%q) error = %q, want it to name the zero info hash", magnet, err)
		}
	}
	if _, err := s.AddForDownload(context.Background(), "magnet:?xt=urn:btih:"+zero, DownloadStorageSpec{BaseDir: t.TempDir()}); err == nil {
		t.Fatal("AddForDownload with a zero info hash must fail, not panic")
	}
}
