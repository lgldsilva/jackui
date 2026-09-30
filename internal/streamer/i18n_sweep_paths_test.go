package streamer

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// Prefetch on an active torrent must validate the file index against the
// real file slice before touching anacrolix readers.
func TestPrefetch_FileIndexOutOfRange(t *testing.T) {
	s, hash := activeMultiPiece(t, make([]byte, 1<<14), 1<<14) // single-file torrent

	for _, idx := range []int{-1, 1, 42} {
		err := s.Prefetch(hash, idx)
		if err == nil || !strings.Contains(err.Error(), "out of range") {
			t.Errorf("Prefetch(idx=%d) err = %v, want out-of-range", idx, err)
		}
	}
}

// A magnet with no peers/trackers never resolves metadata: waitForMetadata
// must give up at the timeout instead of blocking the add pipeline forever.
func TestWaitForMetadata_Timeout(t *testing.T) {
	s, err := newTestStreamer(t, Config{DataDir: t.TempDir(), MetadataWait: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("newTestStreamer: %v", err)
	}
	t.Cleanup(s.Close)
	tor, err := s.client.AddMagnet("magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatalf("AddMagnet: %v", err)
	}

	err = waitForMetadata(context.Background(), tor, 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timeout waiting for torrent metadata") {
		t.Fatalf("err = %v, want metadata timeout", err)
	}
}

// A well-formed .torrent whose info value is not a dict parses as metainfo but
// fails UnmarshalInfo — ImportTorrentBytes must surface that, not the hash.
func TestImportTorrentBytes_UnreadableInfo(t *testing.T) {
	s := &Streamer{metainfoDir: t.TempDir()}
	infoBytes, err := bencode.Marshal("not-a-dict")
	if err != nil {
		t.Fatal(err)
	}
	var raw strings.Builder
	if err := (&metainfo.MetaInfo{InfoBytes: infoBytes}).Write(&raw); err != nil {
		t.Fatal(err)
	}

	_, _, err = s.ImportTorrentBytes([]byte(raw.String()))
	if err == nil || !strings.Contains(err.Error(), "unreadable .torrent metadata") {
		t.Fatalf("err = %v, want unreadable-metadata", err)
	}
}
