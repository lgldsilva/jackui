package streamer

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// closeHookStorage wraps the file storage so a test can observe the moment
// anacrolix closes a torrent's storage, which Torrent.Drop waits for.
type closeHookStorage struct {
	storage.ClientImpl
	onClose func()
}

func (c closeHookStorage) OpenTorrent(ctx context.Context, info *metainfo.Info, h metainfo.Hash) (storage.TorrentImpl, error) {
	ti, err := c.ClientImpl.OpenTorrent(ctx, info, h)
	if err != nil {
		return ti, err
	}
	inner := ti.Close
	ti.Close = func() error {
		c.onClose()
		if inner != nil {
			return inner()
		}
		return nil
	}
	return ti, nil
}

// Drop blocks on the client lock and on the storage close (mmap unmap / file
// close). Holding s.mu across it stalled every streaming Read, which takes
// s.mu in bumpAccess, so the GC must release the lock before dropping.
func TestStreamer_DropIdleTorrents_DropsOutsideLock(t *testing.T) {
	s := NewForTesting()
	s.cfg.IdleTimeout = time.Hour

	var closes, heldDuringClose atomic.Int32
	tor := newTestTorrentWith(t, "locked", func(cfg *torrent.ClientConfig) {
		cfg.DefaultStorage = closeHookStorage{
			ClientImpl: storage.NewFile(cfg.DataDir),
			onClose: func() {
				closes.Add(1)
				if !s.mu.TryLock() {
					heldDuringClose.Add(1)
					return
				}
				s.mu.Unlock()
			},
		}
	})
	h := metainfo.Hash{0x09}
	s.mu.Lock()
	s.active[h] = &entry{t: tor, lastAccess: time.Now().Add(-2 * time.Hour)}
	s.mu.Unlock()

	dropped := s.dropIdleTorrents(time.Now())

	if len(dropped) != 1 || dropped[0] != h {
		t.Fatalf("dropped = %v, want [%v]", dropped, h)
	}
	if closes.Load() != 1 {
		t.Fatalf("storage closes = %d, want 1 (Drop must have run)", closes.Load())
	}
	if heldDuringClose.Load() != 0 {
		t.Fatal("s.mu was held while Torrent.Drop closed the storage")
	}
}
