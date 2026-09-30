package streamer

import (
	"bytes"
	"os"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// SeedActiveForTesting registers a minimal info-complete torrent as an ACTIVE
// entry with the given lastAccess, so tests in other packages (handlers,
// transmissionrpc) can exercise the pause/drop guards without a real swarm.
// Mirrors the entry shape the streamer itself creates in registerTorrent.
//
// The returned cleanup func releases the throwaway torrent client; callers MUST
// defer it. Test scaffolding only — same family as NewForTesting.
func (s *Streamer) SeedActiveForTesting(name string, lastAccess time.Time) (metainfo.Hash, func()) {
	const piece = 1 << 14
	data := bytes.Repeat([]byte("z"), piece)
	pieceHash := metainfo.HashBytes(data)
	info := metainfo.Info{
		Name:        name,
		PieceLength: piece,
		Files:       []metainfo.FileInfo{{Path: []string{"file.bin"}, Length: 4}},
		Pieces:      pieceHash[:],
	}
	infoBytes, err := bencode.Marshal(info)
	mustFixture("bencode.Marshal", err)
	spec, err := torrent.TorrentSpecFromMetaInfoErr(&metainfo.MetaInfo{InfoBytes: infoBytes})
	mustFixture("TorrentSpecFromMetaInfoErr", err)
	dataDir, err := os.MkdirTemp("", "jackui-seedactive-*")
	mustFixture("MkdirTemp", err)
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dataDir
	cfg.NoDHT = true
	cfg.DisableTrackers = true
	cfg.DisableTCP = true
	cfg.DisableUTP = true
	cfg.ListenPort = 0
	cl, err := torrent.NewClient(cfg)
	mustFixture("torrent.NewClient", err)
	tor, _, err := cl.AddTorrentSpec(spec)
	mustFixture("AddTorrentSpec", err)

	h := tor.InfoHash()
	s.mu.Lock()
	s.active[h] = &entry{t: tor, lastAccess: lastAccess}
	s.mu.Unlock()

	return h, func() {
		_ = cl.Close()
		_ = os.RemoveAll(dataDir)
	}
}

// mustFixture aborts the test binary when a SeedActiveForTesting step fails.
// The fixture has no *testing.T to report to (it is called from other
// packages), and a half-built fixture would only produce confusing downstream
// assertions — failing loudly at the broken step is the honest outcome.
func mustFixture(step string, err error) {
	if err != nil {
		panic("SeedActiveForTesting: " + step + ": " + err.Error())
	}
}
