package downloads

import (
	"context"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/streamer"
)

func TestEnsureActiveUsesConfiguredDownloadStorage(t *testing.T) {
	store := dlwNewStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	d := Download{UserID: 1, InfoHash: "invalid", Magnet: "m:invalid"}

	cacheWorker := NewWorker(WorkerConfig{
		Store:    store,
		Streamer: streamer.NewForTesting(),
		DataDir:  t.TempDir(),
	})
	if _, err := cacheWorker.ensureActive(ctx, d, d.Magnet); err == nil {
		t.Fatal("cache-backed EnsureActive should reject the invalid source")
	}

	bulkWorker := NewWorker(WorkerConfig{
		Store:       store,
		Streamer:    streamer.NewForTesting(),
		DataDir:     t.TempDir(),
		DownloadDir: t.TempDir(),
	})
	if _, err := bulkWorker.ensureActive(ctx, d, d.Magnet); err == nil {
		t.Fatal("download-dir EnsureActive should reject the invalid source")
	}
}
