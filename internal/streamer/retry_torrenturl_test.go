package streamer

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// TestAddFromTorrentURL_RetriesOn404 reproduces the user-reported bug: clicking a
// fresh card 404s the .torrent link because the indexer hasn't propagated it yet.
// addFromTorrentURL must retry the 404 and succeed once the link is live.
func TestAddFromTorrentURL_RetriesOn404(t *testing.T) {
	const piece = 1 << 14
	data := bytes.Repeat([]byte("z"), piece)
	ph := metainfo.HashBytes(data)
	info := metainfo.Info{Name: "retry-sample.bin", PieceLength: piece, Length: int64(len(data)), Pieces: ph[:]}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	body, err := bencode.Marshal(&metainfo.MetaInfo{InfoBytes: infoBytes})
	if err != nil {
		t.Fatalf("marshal metainfo: %v", err)
	}

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&hits, 1) < 3 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/x-bittorrent")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	// JackettHost makes the SSRF guard trust the loopback httptest server.
	s, err := newTestStreamer(t, Config{DataDir: t.TempDir(), JackettHost: u.Hostname()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	tor, err := s.addFromTorrentURL(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("addFromTorrentURL should retry the 404 and succeed: %v", err)
	}
	if tor == nil {
		t.Fatal("expected a non-nil torrent")
	}
	if hits != 3 {
		t.Fatalf("hits = %d, want 3 (two 404s then the .torrent)", hits)
	}
}
