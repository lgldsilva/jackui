package downloads

import (
	"context"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/streamer"
)

// resolveAndTrackDownload exercises the happy path: torrent resolves, metadata
// is present, target is picked, row is promoted into tracked, and the function
// returns the tracked download. Covers the extraction introduced to reduce
// S3776 in initDownload.
func TestWorker_ResolveAndTrackDownload_Promotes(t *testing.T) {
	store := dlwNewStore(t)
	s, err := streamer.New(streamer.Config{DataDir: t.TempDir(), ListenPort: 0})
	if err != nil {
		t.Fatalf("streamer.New: %v", err)
	}
	defer s.Close()

	tor := wholeSpecTorrent(t, "Movie", [][]string{
		{"Movie.1080p.mkv"},
	})
	hash := tor.InfoHash()

	// Make the torrent visible to the streamer's client using its metainfo.
	mi := tor.Metainfo()
	if _, err := s.Client().AddTorrent(&mi); err != nil {
		t.Fatalf("AddTorrent: %v", err)
	}
	st, ok := s.Client().Torrent(hash)
	if !ok {
		t.Fatal("streamer client does not have the torrent")
	}
	// wholeSpecTorrent already resolved metadata in its own client; the streamer's
	// copy may need a moment to see it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-st.GotInfo():
			goto metadataReady
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	t.Fatal("torrent metadata not resolved in streamer client")

metadataReady:
	d, err := store.Create(Download{
		UserID: 1, InfoHash: hash.HexString(), FileIndex: 0,
		Magnet: "magnet:?xt=urn:btih:" + hash.HexString(), Name: "Movie",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, _ = store.PromoteToDownloading(d.ID)

	w := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: s,
		DataDir:  t.TempDir(),
	})
	w.mu.Lock()
	w.pending[d.ID] = func() {}
	w.mu.Unlock()

	td, name, promoted := w.resolveAndTrackDownload(context.Background(), *d)
	if !promoted {
		t.Fatal("resolveAndTrackDownload should promote the download")
	}
	if td == nil || name != "Movie" {
		t.Fatalf("resolveAndTrackDownload = (%p, %q), want non-nil + Movie", td, name)
	}
	w.mu.Lock()
	_, tracked := w.tracked[d.ID]
	w.mu.Unlock()
	if !tracked {
		t.Fatal("download must appear in tracked after promotion")
	}
	if !s.IsDownloadProtected("Movie") {
		t.Fatal("streamer protection must be registered")
	}
}

// ensureActiveWithFallback short-circuits when the primary source resolves —
// no fallback attempt, no store write.
func TestWorker_EnsureActiveWithFallback_PrimarySuccess(t *testing.T) {
	store := dlwNewStore(t)
	s, err := streamer.New(streamer.Config{DataDir: t.TempDir(), ListenPort: 0})
	if err != nil {
		t.Fatalf("streamer.New: %v", err)
	}
	defer s.Close()

	tor := wholeSpecTorrent(t, "Doc", [][]string{{"doc.mkv"}})
	hash := tor.InfoHash()
	mi := tor.Metainfo()
	if _, err := s.Client().AddTorrent(&mi); err != nil {
		t.Fatalf("AddTorrent: %v", err)
	}
	st, ok := s.Client().Torrent(hash)
	if !ok {
		t.Fatal("streamer client does not have the torrent")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-st.GotInfo():
			goto ready
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	t.Fatal("metadata not resolved")

ready:
	d := Download{UserID: 1, InfoHash: hash.HexString(), Magnet: "magnet:?xt=urn:btih:" + hash.HexString()}
	w := NewWorker(WorkerConfig{Store: store, Streamer: s, DataDir: t.TempDir()})
	got, err := w.ensureActiveWithFallback(context.Background(), &d)
	if err != nil {
		t.Fatalf("ensureActiveWithFallback: %v", err)
	}
	if got != hash {
		t.Fatalf("ensureActiveWithFallback = %v, want %v", got, hash)
	}
	if d.ActiveMagnet != "" {
		t.Fatalf("ActiveMagnet should be empty on primary success, got %q", d.ActiveMagnet)
	}
}

// initDownload drives the full goroutine entry point (defer cleanup + progress
// snapshot) so the extraction's caller stays covered as well.
func TestWorker_InitDownload_PromotesAndCleansUp(t *testing.T) {
	store := dlwNewStore(t)
	s, err := streamer.New(streamer.Config{DataDir: t.TempDir(), ListenPort: 0})
	if err != nil {
		t.Fatalf("streamer.New: %v", err)
	}
	defer s.Close()

	tor := wholeSpecTorrent(t, "Series", [][]string{
		{"Series.S01E01.mkv"},
	})
	hash := tor.InfoHash()
	mi := tor.Metainfo()
	if _, err := s.Client().AddTorrent(&mi); err != nil {
		t.Fatalf("AddTorrent: %v", err)
	}
	st, ok := s.Client().Torrent(hash)
	if !ok {
		t.Fatal("streamer client does not have the torrent")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-st.GotInfo():
			goto metadataReady
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	t.Fatal("torrent metadata not resolved in streamer client")

metadataReady:
	d, err := store.Create(Download{
		UserID: 1, InfoHash: hash.HexString(), FileIndex: 0,
		Magnet: "magnet:?xt=urn:btih:" + hash.HexString(), Name: "Series",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, _ = store.PromoteToDownloading(d.ID)

	w := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: s,
		DataDir:  t.TempDir(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.mu.Lock()
	w.pending[d.ID] = cancel
	w.pendingHash[d.ID] = hash
	w.mu.Unlock()
	w.doneWG.Add(1)

	w.initDownload(ctx, *d)

	w.mu.Lock()
	_, tracked := w.tracked[d.ID]
	_, stillPending := w.pending[d.ID]
	_, tombstoned := w.removed[d.ID]
	w.mu.Unlock()
	if !tracked {
		t.Fatal("download must be tracked after initDownload")
	}
	if stillPending {
		t.Fatal("pending entry must be cleared by initDownload's defer")
	}
	if tombstoned {
		t.Fatal("initDownload should clear any tombstone for a live promotion")
	}
}
