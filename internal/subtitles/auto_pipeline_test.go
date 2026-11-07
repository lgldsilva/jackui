package subtitles

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/tmdb"
)

// End-to-end behavior of the AutoFetcher lifecycle (StartTorrent / StartLocal /
// Status / Cancel / RefineOnComplete) over the in-memory SQL fake and the
// OpenSubtitles httptest server — runnable WITHOUT PostgreSQL, so the
// pre-push gate and PR CI (JACKUI_TEST_DATABASE_URL unset, PRs have no
// Postgres service) exercise the real pipeline. The DB-gated acceptance tests
// in auto_test.go re-run the same lifecycle on real Postgres when available.

// pipeFixture is the no-Postgres sibling of autoFixture: same OpenSubtitles
// test server and fakes, but persistence goes through fakeLinksStore.
type pipeFixture struct {
	a       *AutoFetcher
	c       *Client
	links   *LinksStore
	fdb     *fakeLinksDB
	tmdb    *fakeTMDB
	server  *autoTestServer
	meta    *fakeTorrent
	hash    metainfo.Hash
	key     string
	langs   *string
	autoPtr *bool
}

func newPipeFixture(t *testing.T, search func(w http.ResponseWriter, r *http.Request)) *pipeFixture {
	t.Helper()
	links, fdb := newFakeLinksStore(t)
	f := &pipeFixture{links: links, fdb: fdb, tmdb: &fakeTMDB{}}
	f.server = newAutoServer(t, search)
	f.c = testSubClient(t, f.server.srv, "testkey", "", "", t.TempDir())
	f.hash = metainfo.HashBytes([]byte(t.Name()))
	f.key = f.hash.HexString()
	f.meta = &fakeTorrent{name: "Show.S01E02.1080p.WEB-DL-GRP", hashFn: instantHash}
	langs, auto := "pt-BR,pt", true
	f.langs, f.autoPtr = &langs, &auto
	f.a = NewAutoFetcher(f.c, links, f.tmdb, func() (string, bool) { return *f.langs, *f.autoPtr })
	return f
}

// waitForFakeStatus polls until the item leaves the in-flight state.
func waitForFakeStatus(t *testing.T, f *pipeFixture, idx int) AutoStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := f.a.Status(context.Background(), f.key, idx); st.Status != StatusPending || !f.a.busy(fetchKey{f.key, idx}) {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the fetch to settle")
	return AutoStatus{}
}

func TestAutoPipeStartTorrentHashPath(t *testing.T) {
	f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	f.a.StartTorrent(f.meta, f.hash, 0)
	st := waitForFakeStatus(t, f, 0)

	if st.Status != StatusReady {
		t.Fatalf("status = %q (%s), want ready", st.Status, st.Reason)
	}
	if st.FileID != "777" || st.Language != "pt-BR" || st.VTTPath != f.c.CachePath("777") {
		t.Errorf("outcome = %+v, want the picked 777/pt-BR with the cache path", st)
	}
	row := f.fdb.snapshot(f.key, 0)
	if row == nil || row.Source != SourceHash || !row.HashRefined || row.Languages != "pt-BR,pt" {
		t.Errorf("persisted row = %+v, want ready via hash with the settings languages", row)
	}
	q := f.server.query(0)
	if q.Get("moviehash") != "abcdef0123456789" || q.Get("moviebytesize") != "70000" ||
		q.Get("languages") != "pt-BR,pt" {
		t.Errorf("hash search params = %v", q)
	}
	if _, searches := f.server.totals(); searches != 1 {
		t.Errorf("searches = %d, want 1", searches)
	}
}

func TestAutoPipeStartTorrentCascadesToTier2(t *testing.T) {
	f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	f.a.hashBudget = 40 * time.Millisecond
	f.meta.hashFn = blockingHash
	f.tmdb.match = &tmdb.Match{ImdbID: "tt0117731", Title: "Trainspotting"}

	f.a.StartTorrent(f.meta, f.hash, 0)
	st := waitForFakeStatus(t, f, 0)
	if st.Status != StatusReady {
		t.Fatalf("status = %q (%s), want ready after the cascade", st.Status, st.Reason)
	}
	row := f.fdb.snapshot(f.key, 0)
	if row.Source != SourceIMDB || row.HashRefined {
		t.Errorf("row = %+v, want an imdb-source ready row without hash_refined", row)
	}
	if q := f.server.query(0); q.Get("imdb_id") != "0117731" || q.Get("query") != "Trainspotting" {
		t.Errorf("tier-2 search params = %v", q)
	}
}

func TestAutoPipeStartTorrentNilMetaNoOp(t *testing.T) {
	f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	f.a.StartTorrent(nil, f.hash, 0)
	time.Sleep(100 * time.Millisecond)
	if total, _ := f.server.totals(); total != 0 {
		t.Errorf("HTTP requests = %d, want 0", total)
	}
	if ex := f.fdb.recordedExecs(); len(ex) != 0 {
		t.Errorf("nil TorrentMeta issued writes: %+v", ex)
	}
}

func TestAutoPipeStartLocalTable(t *testing.T) {
	t.Run("complete file hashes directly", func(t *testing.T) {
		f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, searchPayload("pt-BR", 777))
		})
		dir := t.TempDir()
		path := filepath.Join(dir, "Movie.Local.2024.mkv")
		if err := os.WriteFile(path, make([]byte, 128<<10), 0o600); err != nil {
			t.Fatalf("write file: %v", err)
		}
		f.a.StartLocal("local:"+f.key, 0, "Movie.Local.2024.mkv", path)
		f.key = "local:" + f.key
		st := waitForFakeStatus(t, f, 0)
		if st.Status != StatusReady {
			t.Fatalf("status = %q (%s), want ready via the file hash", st.Status, st.Reason)
		}
		q := f.server.query(0)
		want, err := expectedLocalHash(path)
		if err != nil {
			t.Fatalf("expectedLocalHash: %v", err)
		}
		if q.Get("moviehash") != want || q.Get("moviebytesize") != "131072" {
			t.Errorf("local hash search params = %v, want moviehash %q", q, want)
		}
		if row := f.fdb.snapshot(f.key, 0); row.Source != SourceHash {
			t.Errorf("source = %q, want hash", row.Source)
		}
	})

	t.Run("unreadable file falls back to the title query", func(t *testing.T) {
		f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, searchPayload("pt-BR", 777))
		})
		f.a.StartLocal("local:gone", 0, "Missing.File.2023.mkv", filepath.Join(t.TempDir(), "nope.mkv"))
		f.key = "local:gone"
		st := waitForFakeStatus(t, f, 0)
		if st.Status != StatusReady {
			t.Fatalf("status = %q (%s), want ready via the query fallback", st.Status, st.Reason)
		}
		if q := f.server.query(0); q.Get("query") != "Missing.File.2023" {
			t.Errorf("query = %q, want the file base name", q.Get("query"))
		}
	})

	t.Run("small and empty paths fall back too", func(t *testing.T) {
		f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, searchPayload("pt-BR", 777))
		})
		small := filepath.Join(t.TempDir(), "small.mkv")
		if err := os.WriteFile(small, make([]byte, 1024), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		for i, path := range []string{small, ""} {
			f.a.StartLocal("local:small"+string(rune('a'+i)), i, "Small.File.2024.mkv", path)
			key := "local:small" + string(rune('a'+i))
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if row := f.fdb.snapshot(key, int64(i)); row != nil && row.Status == StatusReady {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if row := f.fdb.snapshot(key, int64(i)); row == nil || row.Source != SourceQuery {
				t.Errorf("path %q: row = %+v, want a ready query-source row", path, row)
			}
		}
	})

	t.Run("empty hash key is a no-op", func(t *testing.T) {
		f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, searchPayload("pt-BR", 777))
		})
		f.a.StartLocal("", 0, "Movie.mkv", "/tmp/movie.mkv")
		time.Sleep(100 * time.Millisecond)
		if total, _ := f.server.totals(); total != 0 {
			t.Errorf("HTTP requests = %d, want 0 for an empty key", total)
		}
		if ex := f.fdb.recordedExecs(); len(ex) != 0 {
			t.Errorf("empty key issued writes: %+v", ex)
		}
	})
}

// TestAutoPipeStatusReporting pins the Status decision table against seeded
// fake-store rows (no Postgres): ready rows stay reportable with auto-fetch
// off; lifecycle rows (pending/failed) report none while the feature is off;
// outcome fields only leak for ready rows.
func TestAutoPipeStatusReporting(t *testing.T) {
	ctx := context.Background()
	const key = "eeee0000eeee0000eeee0000eeee0000eeee0000"
	seed := func(f *pipeFixture, row *SubtitleLink) {
		if err := f.links.UpsertPending(ctx, key, 0, row.Languages); err != nil {
			t.Fatalf("UpsertPending: %v", err)
		}
		switch row.Status {
		case StatusReady:
			if err := f.links.MarkReady(ctx, key, 0, row.FileID, row.Language, row.Languages,
				row.HearingImpaired, row.Forced, row.VTTPath, row.Source); err != nil {
				t.Fatalf("MarkReady: %v", err)
			}
		case StatusFailed:
			if err := f.links.MarkFailed(ctx, key, 0, row.Reason); err != nil {
				t.Fatalf("MarkFailed: %v", err)
			}
		}
	}

	t.Run("nil fetcher and nil links report none", func(t *testing.T) {
		var nilFetcher *AutoFetcher
		if st := nilFetcher.Status(ctx, key, 0); st.Status != statusNone {
			t.Errorf("nil fetcher = %+v, want none", st)
		}
		if st := (&AutoFetcher{}).Status(ctx, key, 0); st.Status != statusNone {
			t.Errorf("links-less fetcher = %+v, want none", st)
		}
	})

	t.Run("ready row reports full outcome on and off", func(t *testing.T) {
		f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, searchPayload("pt-BR", 777))
		})
		seed(f, &SubtitleLink{Status: StatusReady, FileID: "777", Language: "pt-BR",
			Languages: "pt-BR,pt", HearingImpaired: true, Forced: true,
			VTTPath: "/cache/777.vtt", Source: SourceIMDB})

		st := f.a.Status(ctx, key, 0)
		if st.Status != StatusReady || st.FileID != "777" || st.Language != "pt-BR" ||
			!st.HearingImpaired || !st.Forced || st.VTTPath != "/cache/777.vtt" || st.Reason != "" {
			t.Errorf("ready status = %+v, want the full outcome block", st)
		}

		*f.autoPtr = false // fetched subtitles stay valid when auto-fetch turns off
		if st := f.a.Status(ctx, key, 0); st.Status != StatusReady || st.FileID != "777" {
			t.Errorf("ready with auto-fetch off = %+v, want the row kept", st)
		}
	})

	t.Run("failed row reports reason only when auto-fetch is on", func(t *testing.T) {
		f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, searchPayload("pt-BR", 777))
		})
		seed(f, &SubtitleLink{Status: StatusFailed, Reason: ReasonQuota, Languages: "pt-BR,pt",
			FileID: "stale", VTTPath: "/cache/stale.vtt"})

		st := f.a.Status(ctx, key, 0)
		if st.Status != StatusFailed || st.Reason != ReasonQuota {
			t.Errorf("failed status = %+v, want failed/quota", st)
		}
		if st.FileID != "" || st.VTTPath != "" || st.Language != "" || st.HearingImpaired || st.Forced {
			t.Errorf("failed row leaked outcome fields: %+v", st)
		}

		*f.autoPtr = false
		if st := f.a.Status(ctx, key, 0); st.Status != statusNone {
			t.Errorf("failed with auto-fetch off = %q, want none", st.Status)
		}
	})

	t.Run("pending and missing rows follow the same gate", func(t *testing.T) {
		f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, searchPayload("pt-BR", 777))
		})
		if err := f.links.UpsertPending(ctx, key, 0, "pt-BR,pt"); err != nil {
			t.Fatalf("UpsertPending: %v", err)
		}
		if st := f.a.Status(ctx, key, 0); st.Status != StatusPending {
			t.Errorf("pending status = %+v, want pending", st)
		}
		*f.autoPtr = false
		if st := f.a.Status(ctx, key, 0); st.Status != statusNone {
			t.Errorf("pending with auto-fetch off = %q, want none", st.Status)
		}
		if st := f.a.Status(ctx, key+"-absent", 0); st.Status != statusNone {
			t.Errorf("missing row = %q, want none", st.Status)
		}
	})
}

// TestAutoPipeCancelStopsHashPhase covers Cancel through the public API
// (including the cancel() dispatch inside the in-flight loop) and pins that an
// abandoned cycle persists NO failure — the pending row is recovered later.
func TestAutoPipeCancelStopsHashPhase(t *testing.T) {
	f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	f.a.hashBudget = 10 * time.Second // Cancel must beat the budget
	f.meta.hashFn = blockingHash
	f.meta.entered = make(chan struct{})
	f.meta.canceled = make(chan struct{})

	var nilFetcher *AutoFetcher
	nilFetcher.Cancel(f.key) // nil receiver → no-op
	f.a.Cancel(f.key)        // nothing in flight → no-op

	f.a.StartTorrent(f.meta, f.hash, 0)
	<-f.meta.entered
	if st := f.a.Status(context.Background(), f.key, 0); st.Status != StatusPending {
		t.Errorf("status while in flight = %+v, want pending", st)
	}
	f.a.Cancel(f.key)
	<-f.meta.canceled
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && f.a.busy(fetchKey{f.key, 0}) {
		time.Sleep(5 * time.Millisecond)
	}

	if total, _ := f.server.totals(); total != 0 {
		t.Errorf("OS traffic = %d, want 0 (cancel aborts before the search)", total)
	}
	row := f.fdb.snapshot(f.key, 0)
	if row == nil || row.Status != StatusPending || row.Reason != "" {
		t.Errorf("row after cancel = %+v, want the pending row without a failure", row)
	}
	if st := f.a.Status(context.Background(), f.key, 0); st.Status != StatusPending {
		t.Errorf("status after cancel = %+v, want the recoverable pending row", st)
	}
}

// TestAutoPipeConcurrentStartDedup: two Starts for the same item must produce
// ONE fetch — the second start's begin() is rejected and its context torn down.
func TestAutoPipeConcurrentStartDedup(t *testing.T) {
	f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	f.a.hashBudget = 5 * time.Second
	f.meta.hashFn = blockingHash
	f.meta.entered = make(chan struct{})

	f.a.StartTorrent(f.meta, f.hash, 0)
	<-f.meta.entered // the first fetch owns the item
	f.a.StartTorrent(f.meta, f.hash, 0)
	time.Sleep(100 * time.Millisecond)

	if _, searches := f.server.totals(); searches != 0 {
		t.Errorf("searches = %d, want 0 (dedup must not search)", searches)
	}
	if !f.a.busy(fetchKey{f.key, 0}) {
		t.Error("the first fetch must still be in flight after the duplicate start")
	}
	f.a.Cancel(f.key)
}

func TestAutoPipeRefineOnCompleteGuards(t *testing.T) {
	f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	f.meta.complete = true

	f.a.RefineOnComplete(nil, f.hash, 0) // nil torrent → no-op
	f.meta.complete = false              // incomplete → no-op
	f.a.RefineOnComplete(f.meta, f.hash, 0)
	time.Sleep(100 * time.Millisecond)
	if total, _ := f.server.totals(); total != 0 {
		t.Errorf("guards issued HTTP: %d requests", total)
	}
	if ex := f.fdb.recordedExecs(); len(ex) != 0 {
		t.Errorf("guards issued writes: %+v", ex)
	}

	// auto-fetch off: refine is a no-op even with a complete file and a row.
	f.meta.complete = true
	if err := f.links.UpsertPending(context.Background(), f.key, 0, "pt-BR,pt"); err != nil {
		t.Fatalf("UpsertPending: %v", err)
	}
	*f.autoPtr = false
	f.a.RefineOnComplete(f.meta, f.hash, 0)
	time.Sleep(100 * time.Millisecond)
	if total, _ := f.server.totals(); total != 0 {
		t.Errorf("auto-fetch-off refine issued HTTP: %d requests", total)
	}
	if row := f.fdb.snapshot(f.key, 0); row.HashRefined {
		t.Errorf("auto-fetch-off refine marked hash_refined: %+v", row)
	}
}

// TestAutoPipeRefineUpgradesToHash drives a successful refinement through the
// public RefineOnComplete entry: the imdb decision becomes a frame-exact hash
// decision exactly once.
func TestAutoPipeRefineUpgradesToHash(t *testing.T) {
	f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	f.a.hashBudget = 40 * time.Millisecond
	f.meta.hashFn = blockingHash
	f.tmdb.match = &tmdb.Match{ImdbID: "tt0117731"}
	f.a.StartTorrent(f.meta, f.hash, 0)
	if st := waitForFakeStatus(t, f, 0); st.Status != StatusReady {
		t.Fatalf("phase1 = %q (%s)", st.Status, st.Reason)
	}
	if _, searches := f.server.totals(); searches != 1 {
		t.Fatalf("phase1 searches = %d", searches)
	}

	f.meta.complete = true
	f.meta.hashFn = instantHash
	f.a.RefineOnComplete(f.meta, f.hash, 0)
	deadline := time.Now().Add(5 * time.Second)
	var row *SubtitleLink
	for time.Now().Before(deadline) {
		if row = f.fdb.snapshot(f.key, 0); row != nil && row.Source == SourceHash {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if row == nil || row.Source != SourceHash || !row.HashRefined || row.FileID != "777" {
		t.Fatalf("refined row = %+v, want ready/777 via hash", row)
	}
	if _, searches := f.server.totals(); searches != 2 {
		t.Fatalf("post-refine searches = %d, want 2", searches)
	}

	// Completion events after the upgrade must not re-search.
	f.a.RefineOnComplete(f.meta, f.hash, 0)
	time.Sleep(150 * time.Millisecond)
	if _, searches := f.server.totals(); searches != 2 {
		t.Errorf("refine ran twice: searches = %d, want 2", searches)
	}
}

// TestAutoPipeRefineFailurePathsTable covers every refine failure branch: each
// keeps the old decision untouched, persists hash_refined, and never loops.
func TestAutoPipeRefineFailurePathsTable(t *testing.T) {
	cases := []struct {
		name              string
		hashFn            func(context.Context, metainfo.Hash, int) (streamer.HashResult, error)
		search            func(w http.ResponseWriter, r *http.Request)
		fetchBudget       time.Duration // 0 = default
		expectHashRefined bool
	}{
		{
			name: "hash unavailable",
			hashFn: func(context.Context, metainfo.Hash, int) (streamer.HashResult, error) {
				return streamer.HashResult{}, errors.New("file vanished")
			},
			expectHashRefined: true,
		},
		{
			name: "search error",
			hashFn: func(context.Context, metainfo.Hash, int) (streamer.HashResult, error) {
				return streamer.HashResult{Hash: "abcdef0123456789", Size: 70000}, nil
			},
			search: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "boom", http.StatusInternalServerError)
			},
			expectHashRefined: true,
		},
		{
			name: "no results",
			hashFn: func(context.Context, metainfo.Hash, int) (streamer.HashResult, error) {
				return streamer.HashResult{Hash: "abcdef0123456789", Size: 70000}, nil
			},
			search: func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, map[string]any{"data": []any{}})
			},
			expectHashRefined: true,
		},
		{
			name: "download error",
			hashFn: func(context.Context, metainfo.Hash, int) (streamer.HashResult, error) {
				return streamer.HashResult{Hash: "abcdef0123456789", Size: 70000}, nil
			},
			search: func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/api/v1/download" {
					http.Error(w, "quota", http.StatusNotAcceptable)
					return
				}
				writeJSON(w, searchPayload("pt-BR", 333))
			},
			expectHashRefined: true,
		},
		{
			name: "context cancelled after the search",
			hashFn: func(context.Context, metainfo.Hash, int) (streamer.HashResult, error) {
				return streamer.HashResult{Hash: "abcdef0123456789", Size: 70000}, nil
			},
			fetchBudget: 60 * time.Millisecond,
			search: func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(150 * time.Millisecond) // outlives the fetch budget
				writeJSON(w, searchPayload("pt-BR", 334))
			},
			// When the budget dies mid-refine, the failure flag itself cannot
			// be persisted (MarkRefined is best-effort over a dead context):
			// the old decision must survive WITHOUT hash_refined, so the next
			// completion event may try again.
			expectHashRefined: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, searchPayload("pt-BR", 777))
			})
			f.a.hashBudget = 40 * time.Millisecond
			f.meta.hashFn = blockingHash
			f.tmdb.match = &tmdb.Match{ImdbID: "tt0117731"}
			f.a.StartTorrent(f.meta, f.hash, 0)
			if st := waitForFakeStatus(t, f, 0); st.Status != StatusReady {
				t.Fatalf("phase1 = %q (%s)", st.Status, st.Reason)
			}
			before := f.fdb.snapshot(f.key, 0)

			if tc.search != nil {
				f.server.srv.Config.Handler = http.HandlerFunc(tc.search)
			}
			if tc.fetchBudget != 0 {
				f.a.fetchBudget = tc.fetchBudget
			}
			f.meta.complete = true
			f.meta.hashFn = adaptHashFn(tc.hashFn)
			f.a.RefineOnComplete(f.meta, f.hash, 0)

			// hash_refined is the failure path's last step: when expected, its
			// presence means the single attempt is consumed. When the context
			// died mid-refine, wait for the goroutine to settle and expect the
			// flag to be absent (best-effort persistence cannot write).
			deadline := time.Now().Add(5 * time.Second)
			var row *SubtitleLink
			for time.Now().Before(deadline) {
				row = f.fdb.snapshot(f.key, 0)
				if tc.expectHashRefined && row != nil && row.HashRefined {
					break
				}
				if !tc.expectHashRefined && !f.a.busy(fetchKey{f.key, 0}) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if row == nil {
				t.Fatalf("refine lost the row: %+v", row)
			}
			if row.HashRefined != tc.expectHashRefined {
				t.Fatalf("hash_refined = %v, want %v: %+v", row.HashRefined, tc.expectHashRefined, row)
			}
			if row.Status != StatusReady || row.FileID != "777" || row.Source != SourceIMDB ||
				row.Language != before.Language || row.VTTPath != before.VTTPath {
				t.Errorf("failed refine altered the old decision:\n got %+v\nwant %+v", row, before)
			}

			// No restart loop: a fresh fetcher (empty refined map) sees the
			// persisted flag and never re-attempts. When the context died
			// mid-refine the flag never persisted, so a restart legitimately
			// gets one more attempt — only assert the guard when it persisted.
			if !tc.expectHashRefined {
				return
			}
			execsBefore := len(f.fdb.recordedExecs())
			fresh := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, searchPayload("pt-BR", 999))
			})
			fresh.a = NewAutoFetcher(fresh.c, f.links, fresh.tmdb,
				func() (string, bool) { return *f.langs, *f.autoPtr })
			fresh.a.RefineOnComplete(f.meta, f.hash, 0)
			time.Sleep(150 * time.Millisecond)
			if _, searches := fresh.server.totals(); searches != 0 {
				t.Errorf("fresh process re-attempted the failed refine: %d searches", searches)
			}
			if ex := f.fdb.recordedExecs(); len(ex) != execsBefore {
				t.Errorf("fresh process wrote to the store: %d → %d execs", execsBefore, len(ex))
			}
		})
	}
}

// TestAutoPipeRefineInMemoryGuardAndSkips covers the remaining refine
// short-circuits directly: already-hash rows, already-refined rows, the
// in-memory guard, and broken/missing persistence.
func TestAutoPipeRefineInMemoryGuardAndSkips(t *testing.T) {
	ctx := context.Background()
	const key = "ffff0000ffff0000ffff0000ffff0000ffff0000"
	f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	if err := f.links.UpsertPending(ctx, key, 0, "pt-BR,pt"); err != nil {
		t.Fatalf("UpsertPending: %v", err)
	}
	if err := f.links.MarkReady(ctx, key, 0, "777", "pt-BR", "pt-BR,pt", false, false,
		"/cache/777.vtt", SourceIMDB); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}
	f.meta.complete = true
	f.meta.hashFn = instantHash

	// links-less and client-less fetchers: quiet no-ops.
	(&AutoFetcher{client: f.c}).refine(ctx, f.meta, f.hash, 0, key, "pt-BR,pt")
	(&AutoFetcher{links: f.links}).refine(ctx, f.meta, f.hash, 0, key, "pt-BR,pt")
	disabled := NewAutoFetcher(New("", "", "", ""), f.links, nil, func() (string, bool) { return "pt-BR", true })
	disabled.refine(ctx, f.meta, f.hash, 0, key, "pt-BR,pt")
	if row := f.fdb.snapshot(key, 0); row.HashRefined {
		t.Error("guard refines flagged hash_refined")
	}

	// Broken / missing persistence: no refinement attempt, no flag.
	f.fdb.failQueries(errors.New("pool gone"))
	f.a.refine(ctx, f.meta, f.hash, 0, key, "pt-BR,pt")
	if row := f.fdb.snapshot(key, 0); row.HashRefined {
		t.Error("refine with broken persistence flagged hash_refined")
	}
	f.fdb.clearFailures()
	f.a.refine(ctx, f.meta, f.hash, 0, key+"-absent", "pt-BR,pt")

	// Already frame-exact: a hash row never re-refines.
	if err := f.links.MarkReady(ctx, key, 1, "778", "pt", "pt-BR,pt", false, false,
		"/cache/778.vtt", SourceHash); err != nil {
		t.Fatalf("MarkReady hash: %v", err)
	}
	f.a.refine(ctx, f.meta, f.hash, 1, key, "pt-BR,pt")
	if total, _ := f.server.totals(); total != 0 {
		t.Errorf("hash-row refine issued HTTP: %d", total)
	}

	// The persisted flag (on a query/imdb row) blocks a re-attempt.
	if err := f.links.UpsertPending(ctx, key, 2, "pt-BR,pt"); err != nil {
		t.Fatalf("UpsertPending: %v", err)
	}
	if err := f.links.MarkRefined(ctx, key, 2); err != nil {
		t.Fatalf("MarkRefined: %v", err)
	}
	f.a.refine(ctx, f.meta, f.hash, 2, key, "pt-BR,pt")
	if total, _ := f.server.totals(); total != 0 {
		t.Errorf("flagged-row refine issued HTTP: %d", total)
	}

	// The in-memory guard blocks a second attempt in this process.
	if err := f.links.UpsertPending(ctx, key, 3, "pt-BR,pt"); err != nil {
		t.Fatalf("UpsertPending: %v", err)
	}
	if err := f.links.MarkReady(ctx, key, 3, "779", "pt", "pt-BR,pt", false, false,
		"/cache/779.vtt", SourceQuery); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}
	f.a.refined[key+"#3"] = true
	f.a.refine(ctx, f.meta, f.hash, 3, key, "pt-BR,pt")
	if total, _ := f.server.totals(); total != 0 {
		t.Errorf("in-memory-guarded refine issued HTTP: %d", total)
	}
	if row := f.fdb.snapshot(key, 3); row.FileID != "779" {
		t.Errorf("guarded refine changed the row: %+v", row)
	}
}

// TestAutoPipeSearchDownloadPersistTable pins searchDownloadPersist directly,
// including the re-search variants where a failure must keep the prior ready
// row completely untouched.
func TestAutoPipeSearchDownloadPersistTable(t *testing.T) {
	const key = "aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111"
	const langs = "pt-BR,pt"
	resolve := func(context.Context) (SearchOpts, string) {
		return SearchOpts{Query: "x", Languages: langs}, SourceQuery
	}

	t.Run("success replaces the decision", func(t *testing.T) {
		f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, searchPayload("pt-BR", 777))
		})
		f.a.searchDownloadPersist(context.Background(), key, 0, langs, false, resolve)
		row := f.fdb.snapshot(key, 0)
		if row == nil || row.Status != StatusReady || row.FileID != "777" ||
			row.Source != SourceQuery || row.Languages != langs {
			t.Errorf("row = %+v, want ready/777 with the settings languages", row)
		}
	})

	t.Run("search failure on a fresh cycle persists no_results", func(t *testing.T) {
		f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, map[string]any{"data": []any{}})
		})
		f.a.searchDownloadPersist(context.Background(), key, 0, langs, false, resolve)
		row := f.fdb.snapshot(key, 0)
		if row == nil || row.Status != StatusFailed || row.Reason != ReasonNoResults {
			t.Errorf("row = %+v, want failed/no_results", row)
		}
	})

	t.Run("search failure on a re-search keeps the ready row", func(t *testing.T) {
		f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, map[string]any{"data": []any{}})
		})
		ctx := context.Background()
		if err := f.links.MarkReady(ctx, key, 0, "777", "pt-BR", "old", false, false,
			"/cache/777.vtt", SourceIMDB); err != nil {
			t.Fatalf("seed: %v", err)
		}
		f.a.searchDownloadPersist(ctx, key, 0, langs, true, resolve)
		row := f.fdb.snapshot(key, 0)
		if row.Status != StatusReady || row.FileID != "777" || row.Languages != "old" || row.Reason != "" {
			t.Errorf("failed re-search destroyed the row: %+v", row)
		}
	})

	t.Run("download failure on a fresh cycle persists error", func(t *testing.T) {
		f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, searchPayload("pt-BR", 777))
		})
		// The download route lives in the server wrapper, so the failure must
		// be intercepted at the handler level (search traffic keeps flowing).
		failDownload(t, f.server, http.StatusInternalServerError, "boom")
		f.a.searchDownloadPersist(context.Background(), key, 0, langs, false, resolve)
		row := f.fdb.snapshot(key, 0)
		if row == nil || row.Status != StatusFailed || row.Reason != ReasonError {
			t.Errorf("row = %+v, want failed/error", row)
		}
	})

	t.Run("download failure on a re-search keeps the ready row", func(t *testing.T) {
		f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, searchPayload("pt-BR", 777))
		})
		failDownload(t, f.server, http.StatusInternalServerError, "boom")
		ctx := context.Background()
		if err := f.links.MarkReady(ctx, key, 0, "777", "pt-BR", "old", false, false,
			"/cache/777.vtt", SourceIMDB); err != nil {
			t.Fatalf("seed: %v", err)
		}
		f.a.searchDownloadPersist(ctx, key, 0, langs, true, resolve)
		row := f.fdb.snapshot(key, 0)
		if row.Status != StatusReady || row.FileID != "777" || row.Languages != "old" {
			t.Errorf("failed re-search destroyed the row: %+v", row)
		}
	})

	t.Run("context cancelled by the resolver aborts before the search", func(t *testing.T) {
		f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, searchPayload("pt-BR", 777))
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		f.a.searchDownloadPersist(ctx, key, 0, langs, false, resolve)
		if row := f.fdb.snapshot(key, 0); row != nil {
			t.Errorf("cancelled cycle persisted a decision: %+v", row)
		}
	})

	t.Run("context cancelled during the search aborts before the download", func(t *testing.T) {
		f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(150 * time.Millisecond)
			writeJSON(w, searchPayload("pt-BR", 777))
		})
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(20 * time.Millisecond)
			cancel()
		}()
		// searchDownloadPersist never writes pending (that is run()'s job), so
		// the aborted cycle must persist NOTHING — no failed, no ready row.
		f.a.searchDownloadPersist(ctx, key, 0, langs, false, resolve)
		if row := f.fdb.snapshot(key, 0); row != nil {
			t.Errorf("aborted cycle persisted a decision: %+v", row)
		}
	})

	t.Run("re-search success converges to the new languages", func(t *testing.T) {
		f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, searchPayload("pt-BR", 888))
		})
		ctx := context.Background()
		if err := f.links.MarkReady(ctx, key, 0, "777", "pt-BR", "old", false, false,
			"/cache/777.vtt", SourceIMDB); err != nil {
			t.Fatalf("seed: %v", err)
		}
		f.a.searchDownloadPersist(ctx, key, 0, langs, true, resolve)
		row := f.fdb.snapshot(key, 0)
		if row.Status != StatusReady || row.FileID != "888" || row.Languages != langs {
			t.Errorf("converged row = %+v, want ready/888 with %q", row, langs)
		}
	})
}

// TestAutoPipeDownloadQuotaClassified pins the 406 download-quota sentinel
// through the pipeline: the fresh cycle persists failed/quota.
func TestAutoPipeDownloadQuotaClassified(t *testing.T) {
	f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	failDownload(t, f.server, http.StatusNotAcceptable, "quota exceeded")
	f.a.StartTorrent(f.meta, f.hash, 0)
	st := waitForFakeStatus(t, f, 0)
	if st.Status != StatusFailed || st.Reason != ReasonQuota {
		t.Fatalf("status/reason = %q/%q, want failed/quota (406 download)", st.Status, st.Reason)
	}
	if row := f.fdb.snapshot(f.key, 0); row == nil || row.Reason != ReasonQuota {
		t.Errorf("row = %+v, want failed/quota", row)
	}
}

// TestAutoPipeNoAPIKeyZeroHTTP: a disabled client fails the cycle with
// zero OpenSubtitles traffic, observable on a wired-up server.
func TestAutoPipeNoAPIKeyZeroHTTP(t *testing.T) {
	f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	f.a.client = New("", "", "", t.TempDir())
	f.a.StartTorrent(f.meta, f.hash, 0)
	st := waitForFakeStatus(t, f, 0)
	if st.Status != StatusFailed || st.Reason != ReasonNoAPIKey {
		t.Fatalf("status/reason = %q/%q, want failed/no_api_key", st.Status, st.Reason)
	}
	if total, _ := f.server.totals(); total != 0 {
		t.Errorf("OS requests = %d, want 0 with a disabled client", total)
	}
}

// TestAutoPipeSearchQuotaTypedError keeps a client-level sentinel assertion
// outside the DB-gated suite.
func TestAutoPipeSearchQuotaTypedError(t *testing.T) {
	f := newPipeFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"limit reached"}`, http.StatusTooManyRequests)
	})
	if _, err := f.c.SearchAuto(SearchOpts{Query: "x"}); !errors.Is(err, ErrQuota) {
		t.Errorf("SearchAuto error = %v, want ErrQuota", err)
	}
}

// --- small adaptors for the failure-path table ---

// adaptHashFn converts a table hash function to the fakeTorrent signature.
func adaptHashFn(fn func(context.Context, metainfo.Hash, int) (streamer.HashResult, error)) func(context.Context, metainfo.Hash, int) (streamer.HashResult, error) {
	if fn == nil {
		return nil
	}
	return fn
}

// failDownload rewraps the fixture's server handler so every /api/v1/download
// request answers with the given status while search traffic keeps flowing
// through the counted wrapper (the wrapper owns the healthy download route).
func failDownload(t *testing.T, ts *autoTestServer, code int, body string) {
	t.Helper()
	base := ts.srv.Config.Handler
	ts.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/download" {
			http.Error(w, body, code)
			return
		}
		base.ServeHTTP(w, r)
	})
}
