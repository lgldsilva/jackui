package streamer

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// Through a real anacrolix client: a relocated file that matches the metainfo
// verifies complete (seedable) and a foreign one is left untouched.
func TestRelocatedStorage_RealClientVerify(t *testing.T) {
	want := bytes.Repeat([]byte("0123456789abcdef"), 64) // 1 KiB, 64 pieces of 16
	for _, tc := range []struct {
		name     string
		onDisk   []byte
		complete bool
	}{
		{"matching", want, true},
		{"foreign", bytes.Repeat([]byte("Z"), len(want)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, info, real := relocatedFixture(t, tc.onDisk, want)
			ib, err := bencode.Marshal(info)
			if err != nil {
				t.Fatal(err)
			}
			mi := &metainfo.MetaInfo{InfoBytes: ib}

			cfg := torrent.NewDefaultClientConfig()
			cfg.DataDir = s.cfg.DataDir
			cfg.NoDHT, cfg.DisableTrackers, cfg.ListenPort = true, true, 0
			cl, err := torrent.NewClient(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer cl.Close()

			spec := torrent.TorrentSpecFromMetaInfo(mi)
			spec.Storage = s.relocatedStorage(info, mi.HashInfoBytes())
			tor, _, err := cl.AddTorrentSpec(spec)
			if err != nil {
				t.Fatal(err)
			}
			<-tor.GotInfo()
			tor.VerifyData()
			deadline := time.Now().Add(10 * time.Second)
			for tor.BytesMissing() != 0 && tc.complete && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if got := tor.BytesMissing() == 0; got != tc.complete {
				t.Fatalf("complete = %v (missing %d), want %v", got, tor.BytesMissing(), tc.complete)
			}
			tor.DownloadAll() // would request + write foreign pieces without the lock
			after, _ := os.ReadFile(real)
			if !bytes.Equal(after, tc.onDisk) {
				ents, _ := os.ReadDir(filepath.Dir(real))
				for _, e := range ents {
					fi, _ := e.Info()
					t.Logf("dir: %s %d", e.Name(), fi.Size())
				}
				t.Fatalf("relocated file content changed: len %d", len(after))
			}
		})
	}
}
