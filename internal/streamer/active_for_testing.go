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
// The returned close func releases the throwaway torrent client; callers MUST
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
	if err != nil {
		panic("SeedActiveForTesting: bencode.Marshal: " + err.Error())
	}
	spec, err := torrent.TorrentSpecFromMetaInfoErr(&metainfo.MetaInfo{InfoBytes: infoBytes})
	if err != nil {
		panic("SeedActiveForTesting: TorrentSpecFromMetaInfoErr: " + err.Error())
	}
	dataDir, err := os.MkdirTemp("", "jackui-seedactive-*")
	if err != nil {
		panic("SeedActiveForTesting: MkdirTemp: " + err.Error())
	}
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dataDir
	cfg.NoDHT = true
	cfg.DisableTrackers = true
	cfg.DisableTCP = true
	cfg.DisableUTP = true
	cfg.ListenPort = 0
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		os.RemoveAll(dataDir)
		panic("SeedActiveForTesting: torrent.NewClient: " + err.Error())
	}
	tor, _, err := cl.AddTorrentSpec(spec)
	if err != nil {
		cl.Close()
		os.RemoveAll(dataDir)
		panic("SeedActiveForTesting: AddTorrentSpec: " + err.Error())
	}

	h := tor.InfoHash()
	s.mu.Lock()
	s.active[h] = &entry{t: tor, lastAccess: lastAccess}
	s.mu.Unlock()

	return h, func() {
		cl.Close()
		os.RemoveAll(dataDir)
	}
}
