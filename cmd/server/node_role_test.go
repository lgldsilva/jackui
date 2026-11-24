package main

import (
	"testing"

	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transfer"
)

func edgeTestDeps(t *testing.T) *appDeps {
	t.Helper()
	cfg := &config.Config{Node: config.NodeConfig{Role: config.NodeRoleEdge, PrimaryURL: "http://127.0.0.1:1"}}
	deps := &appDeps{
		cfg:             cfg,
		db:              dbtest.NewDB(t),
		streamSrv:       streamer.NewForTesting(),
		transferTracker: transfer.New(1),
	}
	// Not deps.runCleanup: its watchdog os.Exit(0)s after 20s, which would end
	// the test binary green on a hung worker.Stop.
	t.Cleanup(func() {
		for i := len(deps.cleanup) - 1; i >= 0; i-- {
			deps.cleanup[i]()
		}
	})
	return deps
}

// An edge shares the primary's DB: its downloads store must exist (handlers read
// it) but the worker, pending-transfer reconcile and completed-path resolver
// must not run, or two workers fight over the same queue rows.
func TestEdgeDownloadsStoreWithoutWorker(t *testing.T) {
	deps := edgeTestDeps(t)
	initDownloadsStore(deps)
	if deps.downloadsStore == nil {
		t.Fatal("downloads store should still be opened on edge")
	}
	if deps.downloadsWkr != nil {
		t.Fatal("downloads worker must not start on edge")
	}
	if deps.pendingTransfers != nil {
		t.Fatal("pending-transfer reconcile must not run on edge")
	}
	if deps.streamSrv.HasFilePathResolver() {
		t.Fatal("completed-path resolver points at the primary's disk; must not be wired on edge")
	}
	if downloadRemoverDep(deps) != nil {
		t.Fatal("downloadRemoverDep must be a true nil interface without a worker")
	}
}

func TestEdgeWatchlistStoreWithoutWorker(t *testing.T) {
	deps := edgeTestDeps(t)
	initWatchlistStore(deps)
	if deps.watchlistStore == nil {
		t.Fatal("watchlist store should still be opened on edge (CRUD)")
	}
	if deps.watchlistWkr != nil {
		t.Fatal("watchlist worker must not start on edge")
	}
	if watchlistKickerDep(deps) != nil {
		t.Fatal("watchlistKickerDep must be a true nil interface without a worker")
	}
}

// Primary keeps today's behaviour: the worker and resolver come up.
func TestPrimaryDownloadsStoreStartsWorker(t *testing.T) {
	deps := edgeTestDeps(t)
	deps.cfg.Node = config.NodeConfig{}
	deps.streamCfg = streamer.Config{DataDir: t.TempDir()}
	initDownloadsStore(deps)
	if deps.downloadsWkr == nil {
		t.Fatal("primary must start the downloads worker")
	}
	if !deps.streamSrv.HasFilePathResolver() {
		t.Fatal("primary must wire the completed-path resolver")
	}
}

func TestPrimaryWatchlistStartsWorker(t *testing.T) {
	deps := edgeTestDeps(t)
	deps.cfg.Node = config.NodeConfig{}
	initWatchlistStore(deps)
	if deps.watchlistWkr == nil {
		t.Fatal("primary must start the watchlist worker")
	}
	if watchlistKickerDep(deps) == nil {
		t.Fatal("kicker must be wired on primary")
	}
}

// Edge must not keep seed-tracker torrents alive in memory (passkey from 2 IPs).
func TestPrepareStreamConfigDropsSeedTrackersOnEdge(t *testing.T) {
	t.Setenv("JACKUI_GLUETUN_CONTROL_URL", "")
	t.Setenv("JACKUI_PEER_PORT", "")
	cfg := &config.Config{}
	cfg.Stream.SeedTrackers = []string{"tracker.example"}
	if sc := prepareStreamConfig(cfg, make(chan struct{}, 1)); len(sc.SeedTrackers) != 1 {
		t.Fatalf("primary seed trackers = %v", sc.SeedTrackers)
	}
	cfg.Node = config.NodeConfig{Role: config.NodeRoleEdge, PrimaryURL: "http://127.0.0.1:1"}
	if sc := prepareStreamConfig(cfg, make(chan struct{}, 1)); len(sc.SeedTrackers) != 0 {
		t.Fatalf("edge must not keep seeding, got %v", sc.SeedTrackers)
	}
}
