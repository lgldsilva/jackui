package subtitles

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/tmdb"
)

// Direct unit tests for the AutoFetcher's pure helpers and tier builders.
// Lifecycle behavior (Start/Status/Cancel/Refine against HTTP + persistence)
// lives in auto_pipeline_test.go.

func TestAutoUnitClassifyPersisted(t *testing.T) {
	cases := []struct {
		name         string
		row          *SubtitleLink
		langs        string
		wantStop     bool
		wantReSearch bool
	}{
		{"nil row is a first fetch", nil, "pt-BR", false, false},
		{"failed is never retried", &SubtitleLink{Status: StatusFailed}, "pt-BR", true, false},
		{"ready same languages dedups to zero traffic",
			&SubtitleLink{Status: StatusReady, Languages: "pt-BR,pt"}, "pt-BR,pt", true, false},
		{"ready different languages re-searches",
			&SubtitleLink{Status: StatusReady, Languages: "pt-BR,pt"}, "en", false, true},
		{"pending crashed cycle is recovered",
			&SubtitleLink{Status: StatusPending, Languages: "pt-BR"}, "pt-BR", false, false},
		{"both empty language lists still dedup",
			&SubtitleLink{Status: StatusReady, Languages: ""}, "", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stop, reSearch := classifyPersisted(tc.row, tc.langs)
			if stop != tc.wantStop || reSearch != tc.wantReSearch {
				t.Errorf("classifyPersisted(%+v, %q) = (%v, %v), want (%v, %v)",
					tc.row, tc.langs, stop, reSearch, tc.wantStop, tc.wantReSearch)
			}
		})
	}
}

func TestAutoUnitLanguagePriority(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"pt-BR,pt", []string{"pt-br", "pt"}},
		{" pt-BR , pt ,EN ", []string{"pt-br", "pt", "en"}},
		{"", nil},
		{",, ,", nil},
	}
	for _, tc := range cases {
		got := languagePriority(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("languagePriority(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("languagePriority(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

func TestAutoUnitLanguageRank(t *testing.T) {
	priority := []string{"pt-br", "pt"}
	if got := languageRank(priority, "PT"); got != 1 {
		t.Errorf("rank(PT) = %d, want 1 (case-insensitive)", got)
	}
	if got := languageRank(priority, "  pt-br "); got != 0 {
		t.Errorf("rank with padding = %d, want 0", got)
	}
	if got := languageRank(priority, "en"); got != len(priority) {
		t.Errorf("rank(unknown) = %d, want %d (after every configured)", got, len(priority))
	}
	if got := languageRank(nil, "pt"); got != 0 {
		t.Errorf("rank with empty priority = %d, want 0", got)
	}
}

func TestAutoUnitCleanVideoTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Movie.2024.1080p.mkv", "Movie.2024.1080p"},  // extension stripped, separators kept
		{"/data/dir/Show.S01E02.mp4", "Show.S01E02"},  // base name only
		{"  spaced.Release .avi  ", "spaced.Release"}, // outer whitespace trimmed
		{"NoExtension", "NoExtension"},                // nothing to strip
		{".mkv", ""},                                  // whole name is the extension
		{"", ""},                                      // empty in, empty out
	}
	for _, tc := range cases {
		if got := cleanVideoTitle(tc.in); got != tc.want {
			t.Errorf("cleanVideoTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestAutoUnitSubtitleAppliable pins the auto-apply format gate: only SRT/VTT
// convert cleanly to VTT, the PRIMARY format decides when known, and an
// unknown primary falls back to the Formats union.
func TestAutoUnitSubtitleAppliable(t *testing.T) {
	cases := []struct {
		name string
		sub  Subtitle
		want bool
	}{
		{"no formats at all", Subtitle{ID: "1"}, true},
		{"empty formats slice", Subtitle{ID: "1", Formats: []string{}}, true},
		{"srt primary", Subtitle{ID: "1", PrimaryFormat: ".srt"}, true},
		{"vtt primary", Subtitle{ID: "1", PrimaryFormat: ".vtt"}, true},
		{"ass primary rejected even with srt in union",
			Subtitle{ID: "1", PrimaryFormat: ".ass", Formats: []string{".ass", ".srt"}}, false},
		{"ssa primary rejected", Subtitle{ID: "1", PrimaryFormat: ".ssa"}, false},
		{"sub primary rejected", Subtitle{ID: "1", PrimaryFormat: ".sub"}, false},
		{"unknown primary, union has srt", Subtitle{ID: "1", Formats: []string{".srt"}}, true},
		{"unknown primary, union has vtt", Subtitle{ID: "1", Formats: []string{".srt", ".vtt"}}, true},
		{"unknown primary, union ass-only", Subtitle{ID: "1", Formats: []string{".ass"}}, false},
		{"union case-folded and trimmed", Subtitle{ID: "1", Formats: []string{" .SRT "}}, true},
		{"blank union entries ignored", Subtitle{ID: "1", Formats: []string{"", "."}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := subtitleAutoAppliable(tc.sub); got != tc.want {
				t.Errorf("subtitleAutoAppliable(%+v) = %v, want %v", tc.sub, got, tc.want)
			}
		})
	}
}

func TestAutoUnitSettingSnapshot(t *testing.T) {
	var nilFetcher *AutoFetcher
	if langs, ok := nilFetcher.settingSnapshot(); langs != "" || ok {
		t.Errorf("nil fetcher snapshot = (%q, %v), want (\"\", false)", langs, ok)
	}
	if langs, ok := (&AutoFetcher{}).settingSnapshot(); langs != "" || ok {
		t.Errorf("no-closure snapshot = (%q, %v), want (\"\", false)", langs, ok)
	}
	a := NewAutoFetcher(nil, nil, nil, func() (string, bool) { return "  pt-BR , pt  ", true })
	if langs, ok := a.settingSnapshot(); langs != "pt-BR , pt" || !ok {
		t.Errorf("closure snapshot = (%q, %v), want trimmed langs", langs, ok)
	}
}

func TestAutoUnitTier1Torrent(t *testing.T) {
	a := NewAutoFetcher(nil, nil, nil, func() (string, bool) { return "pt-BR", true })
	ctx := context.Background()
	h := metainfo.HashBytes([]byte("tier1"))

	if _, ok := a.tier1Torrent(ctx, nil, h, 0, "pt-BR"); ok {
		t.Error("nil TorrentMeta must not produce a hash search")
	}
	cases := []struct {
		name   string
		hashFn func(context.Context, metainfo.Hash, int) (streamer.HashResult, error)
	}{
		{"hash compute error", func(context.Context, metainfo.Hash, int) (streamer.HashResult, error) {
			return streamer.HashResult{}, errors.New("file smaller than 64KB")
		}},
		{"empty hash", func(context.Context, metainfo.Hash, int) (streamer.HashResult, error) {
			return streamer.HashResult{Hash: "", Size: 1 << 16}, nil
		}},
		{"non-positive size", func(context.Context, metainfo.Hash, int) (streamer.HashResult, error) {
			return streamer.HashResult{Hash: "abc", Size: 0}, nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &fakeTorrent{hashFn: tc.hashFn}
			if opts, ok := a.tier1Torrent(ctx, s, h, 0, "pt-BR"); ok {
				t.Errorf("got SearchOpts %+v, want fall-through to tier 2", opts)
			}
		})
	}

	s := &fakeTorrent{hashFn: instantHash}
	opts, ok := a.tier1Torrent(ctx, s, h, 0, "pt-BR,pt")
	if !ok || opts.MovieHash != "abcdef0123456789" || opts.MovieBytesize != 70000 || opts.Languages != "pt-BR,pt" {
		t.Errorf("success opts = %+v (ok=%v), want the computed hash search", opts, ok)
	}
}

func TestAutoUnitTier1Local(t *testing.T) {
	a := NewAutoFetcher(nil, nil, nil, func() (string, bool) { return "pt-BR", true })

	if _, ok := a.tier1Local("", "pt-BR"); ok {
		t.Error("empty path must fall through")
	}
	if _, ok := a.tier1Local(filepath.Join(t.TempDir(), "missing.mkv"), "pt-BR"); ok {
		t.Error("missing file must fall through")
	}

	dir := t.TempDir()
	small := filepath.Join(dir, "small.mkv")
	if err := os.WriteFile(small, make([]byte, 1024), 0o600); err != nil {
		t.Fatalf("write small file: %v", err)
	}
	if _, ok := a.tier1Local(small, "pt-BR"); ok {
		t.Error("a <64KiB file cannot be OS-hashed and must fall through")
	}

	big := filepath.Join(dir, "big.mkv")
	if err := os.WriteFile(big, make([]byte, 128<<10), 0o600); err != nil {
		t.Fatalf("write big file: %v", err)
	}
	opts, ok := a.tier1Local(big, "pt-BR,pt")
	want, err := expectedLocalHash(big)
	if err != nil {
		t.Fatalf("expectedLocalHash: %v", err)
	}
	if !ok || opts.MovieHash != want || opts.MovieBytesize != 128<<10 || opts.Languages != "pt-BR,pt" {
		t.Errorf("opts = %+v (ok=%v), want hash %q size %d", opts, ok, want, 128<<10)
	}
}

func TestAutoUnitTier2(t *testing.T) {
	const name = "Show.S01E02.1080p.WEB-DL-GRP"
	const langs = "pt-BR,pt"

	// nil resolver: query-only with the parsed season/episode.
	a := NewAutoFetcher(nil, nil, nil, func() (string, bool) { return langs, true })
	opts, source := a.tier2(context.Background(), name, langs)
	if source != SourceQuery || opts.Query != "Show.S01E02.1080p" ||
		opts.Season != 1 || opts.Episode != 2 || opts.Languages != langs {
		t.Errorf("nil-tmdb tier2 = %+v/%q, want cleaned query with S1E2", opts, source)
	}

	// TMDB error and nil match are both non-fatal query-only searches.
	for _, tc := range []struct {
		name string
		tmdb *fakeTMDB
	}{
		{"tmdb error", &fakeTMDB{err: errors.New("tmdb down")}},
		{"no match", &fakeTMDB{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := NewAutoFetcher(nil, nil, tc.tmdb, func() (string, bool) { return langs, true })
			opts, source := a.tier2(context.Background(), name, langs)
			if source != SourceQuery || opts.Query != "Show.S01E02.1080p" {
				t.Errorf("tier2 = %+v/%q, want the raw cleaned query", opts, source)
			}
		})
	}

	// A resolved title replaces the query; an IMDB id scopes the search.
	a = NewAutoFetcher(nil, nil, &fakeTMDB{match: &tmdb.Match{Title: "Trainspotting", ImdbID: "tt0117731"}},
		func() (string, bool) { return langs, true })
	opts, source = a.tier2(context.Background(), name, langs)
	if source != SourceIMDB || opts.Query != "Trainspotting" || opts.IMDB != "tt0117731" || opts.Season != 1 || opts.Episode != 2 {
		t.Errorf("imdb tier2 = %+v/%q", opts, source)
	}

	// Title-only match keeps the query source.
	a = NewAutoFetcher(nil, nil, &fakeTMDB{match: &tmdb.Match{Title: "Trainspotting"}},
		func() (string, bool) { return langs, true })
	opts, source = a.tier2(context.Background(), name, langs)
	if source != SourceQuery || opts.Query != "Trainspotting" || opts.IMDB != "" {
		t.Errorf("title-only tier2 = %+v/%q", opts, source)
	}

	// IMDB-only match scopes the search but keeps the cleaned file name.
	a = NewAutoFetcher(nil, nil, &fakeTMDB{match: &tmdb.Match{ImdbID: "tt0117731"}},
		func() (string, bool) { return langs, true })
	opts, source = a.tier2(context.Background(), name, langs)
	if source != SourceIMDB || opts.Query != "Show.S01E02.1080p" || opts.IMDB != "tt0117731" {
		t.Errorf("imdb-only tier2 = %+v/%q", opts, source)
	}
}

func TestAutoUnitMarkFailureReasons(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name       string
		err        error
		wantReason string
	}{
		{"quota sentinel", fmt.Errorf("search: %w", ErrQuota), ReasonQuota},
		{"disabled sentinel", fmt.Errorf("search: %w", ErrDisabled), ReasonNoAPIKey},
		{"unclassified error", errors.New("connection reset"), ReasonError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, fdb := newFakeLinksStore(t)
			a := NewAutoFetcher(nil, s, nil, func() (string, bool) { return "pt-BR", true })
			a.markFailure(ctx, "key", 0, tc.err)
			row := fdb.snapshot("key", 0)
			if row == nil || row.Status != StatusFailed || row.Reason != tc.wantReason {
				t.Errorf("row = %+v, want failed with reason %q", row, tc.wantReason)
			}
		})
	}
}

func TestAutoUnitMarkRefineFailureFlagsRow(t *testing.T) {
	ctx := context.Background()
	s, fdb := newFakeLinksStore(t)
	a := NewAutoFetcher(nil, s, nil, func() (string, bool) { return "pt-BR", true })
	if err := s.UpsertPending(ctx, "key", 1, "pt-BR"); err != nil {
		t.Fatalf("UpsertPending: %v", err)
	}
	a.markRefineFailure(ctx, "key", 1)
	row := fdb.snapshot("key", 1)
	if row == nil || !row.HashRefined || row.Status != StatusPending {
		t.Errorf("row = %+v, want the untouched row with hash_refined set", row)
	}
}

func TestAutoUnitWritePending(t *testing.T) {
	ctx := context.Background()
	const langs = "pt-BR,pt"

	t.Run("reSearch never writes pending", func(t *testing.T) {
		s, fdb := newFakeLinksStore(t)
		a := NewAutoFetcher(nil, s, nil, func() (string, bool) { return langs, true })
		if !a.writePending(ctx, "key", 0, langs, true) {
			t.Fatal("reSearch must proceed without a pending write")
		}
		if ex := fdb.recordedExecs(); len(ex) != 0 {
			t.Errorf("reSearch issued writes: %+v", ex)
		}
	})

	t.Run("persistence failure aborts the cycle", func(t *testing.T) {
		s, fdb := newFakeLinksStore(t)
		a := NewAutoFetcher(nil, s, nil, func() (string, bool) { return langs, true })
		fdb.failExecs(errors.New("pool gone"))
		if a.writePending(ctx, "key", 0, langs, false) {
			t.Error("writePending must fail when UpsertPending fails")
		}
	})

	t.Run("success persists a pending row", func(t *testing.T) {
		s, fdb := newFakeLinksStore(t)
		a := NewAutoFetcher(nil, s, nil, func() (string, bool) { return langs, true })
		if !a.writePending(ctx, "key", 0, langs, false) {
			t.Fatal("writePending should proceed on a healthy store")
		}
		row := fdb.snapshot("key", 0)
		if row == nil || row.Status != StatusPending || row.Languages != langs {
			t.Errorf("row = %+v, want pending with the settings languages", row)
		}
	})

	t.Run("cancelled between writes leaves the pending row", func(t *testing.T) {
		s, fdb := newFakeLinksStore(t)
		a := NewAutoFetcher(nil, s, nil, func() (string, bool) { return langs, true })
		cctx, cancel := context.WithCancel(ctx)
		defer cancel()
		// Cancel inside the driver exec: database/sql refuses an Exec whose
		// context is already done, so this is the only way to observe the
		// "write succeeded, then ctx died" window the guard exists for.
		fdb.setExecHook(cancel)
		if a.writePending(cctx, "key", 0, langs, false) {
			t.Error("a context cancelled between writes must abort the cycle")
		}
		fdb.setExecHook(nil)
		row := fdb.snapshot("key", 0)
		if row == nil || row.Status != StatusPending {
			t.Errorf("row = %+v, want the pending row left for the next Start", row)
		}
	})
}

func TestAutoUnitBeginEndBusy(t *testing.T) {
	a := NewAutoFetcher(nil, nil, nil, func() (string, bool) { return "pt-BR", true })
	k := fetchKey{"key", 0}
	noop := func() {}

	if !a.begin(k, noop) {
		t.Fatal("first begin must register the item")
	}
	if a.begin(k, noop) {
		t.Error("second begin for the same item must be rejected (dedup)")
	}
	if !a.busy(k) {
		t.Error("item must report busy while registered")
	}
	if a.busy(fetchKey{"other", 0}) {
		t.Error("unrelated item must not report busy")
	}
	a.end(k)
	if a.busy(k) {
		t.Error("item must not report busy after end")
	}
}

func TestAutoUnitStartGuards(t *testing.T) {
	s, fdb := newFakeLinksStore(t)
	a := NewAutoFetcher(nil, s, nil, func() (string, bool) { return "pt-BR", true })

	a.start(fetchKey{"k", 0}, nil) // nil work → no-op
	var nilFetcher *AutoFetcher
	nilFetcher.start(fetchKey{"k", 0}, func(context.Context, string) {}) // nil receiver → no-op
	off := NewAutoFetcher(nil, s, nil, func() (string, bool) { return "pt-BR", false })
	off.start(fetchKey{"k", 0}, func(context.Context, string) {}) // auto-fetch off → no-op
	if ex := fdb.recordedExecs(); len(ex) != 0 {
		t.Errorf("guarded starts issued writes: %+v", ex)
	}
	if a.busy(fetchKey{"k", 0}) {
		t.Error("guarded starts must not register in-flight work")
	}
}

func TestAutoUnitRunGuards(t *testing.T) {
	ctx := context.Background()
	s, fdb := newFakeLinksStore(t)

	// Missing links/client/resolve: quiet no-op, zero persistence, zero HTTP.
	zero := &AutoFetcher{}
	zero.run(ctx, "k", 0, "pt-BR", func(context.Context) (SearchOpts, string) {
		t.Error("resolve must not be called without links+client")
		return SearchOpts{}, SourceQuery
	})
	if ex := fdb.recordedExecs(); len(ex) != 0 {
		t.Errorf("guard run issued writes: %+v", ex)
	}

	// Broken persistence: the cycle aborts before any decision is made.
	boom := errors.New("pool gone")
	fdb.failQueries(boom)
	a := NewAutoFetcher(nil, s, nil, func() (string, bool) { return "pt-BR", true })
	a.run(ctx, "k", 0, "pt-BR", func(context.Context) (SearchOpts, string) {
		t.Error("an undecidable persisted state must never reach the search")
		return SearchOpts{}, SourceQuery
	})
	fdb.clearFailures()
	if ex := fdb.recordedExecs(); len(ex) != 0 {
		t.Errorf("run on broken persistence issued writes: %+v", ex)
	}
}

// TestAutoUnitRunDedupLadder drives run() through the persisted-decision
// ladder end-to-end against the fake store: failed rows and converged ready
// rows stop with zero writes, a disabled client only fails a FRESH cycle, and
// a stale pending row is recovered into a ready one.
func TestAutoUnitRunDedupLadder(t *testing.T) {
	ctx := context.Background()
	const key = "cccc0000cccc0000cccc0000cccc0000cccc0000"
	const langs = "pt-BR,pt"
	newFetcher := func(s *LinksStore, enabled bool) *AutoFetcher {
		c := New("", "", "", "")
		if enabled {
			c.apiKey = "k"
		}
		return NewAutoFetcher(c, s, nil, func() (string, bool) { return langs, true })
	}
	resolve := func(context.Context) (SearchOpts, string) {
		return SearchOpts{Query: "x", Languages: langs}, SourceQuery
	}

	t.Run("failed row stops with zero writes", func(t *testing.T) {
		s, fdb := newFakeLinksStore(t)
		if err := s.MarkFailed(ctx, key, 0, ReasonQuota); err != nil {
			t.Fatalf("seed: %v", err)
		}
		newFetcher(s, true).run(ctx, key, 0, langs, resolve)
		if ex := fdb.recordedExecs(); len(ex) != 1 { // only the seeding MarkFailed
			t.Errorf("run on a failed row issued extra writes: %+v", ex)
		}
	})

	t.Run("converged ready row stops with zero writes", func(t *testing.T) {
		s, fdb := newFakeLinksStore(t)
		if err := s.MarkReady(ctx, key, 0, "1", "pt-BR", langs, false, false, "/v", SourceQuery); err != nil {
			t.Fatalf("seed: %v", err)
		}
		newFetcher(s, true).run(ctx, key, 0, langs, resolve)
		if ex := fdb.recordedExecs(); len(ex) != 1 {
			t.Errorf("run on a converged ready row issued extra writes: %+v", ex)
		}
	})

	t.Run("disabled client fails a fresh cycle without HTTP", func(t *testing.T) {
		s, fdb := newFakeLinksStore(t)
		newFetcher(s, false).run(ctx, key, 0, langs, resolve)
		row := fdb.snapshot(key, 0)
		if row == nil || row.Status != StatusFailed || row.Reason != ReasonNoAPIKey {
			t.Errorf("row = %+v, want failed/no_api_key with zero HTTP", row)
		}
	})

	t.Run("disabled client never destroys a ready row on re-search", func(t *testing.T) {
		s, fdb := newFakeLinksStore(t)
		if err := s.MarkReady(ctx, key, 0, "1", "pt-BR", "old", false, false, "/v", SourceIMDB); err != nil {
			t.Fatalf("seed: %v", err)
		}
		newFetcher(s, false).run(ctx, key, 0, langs, resolve) // langs differ → reSearch
		row := fdb.snapshot(key, 0)
		if row.Status != StatusReady || row.Reason != "" || row.Languages != "old" {
			t.Errorf("row = %+v, want the untouched ready decision", row)
		}
	})

	t.Run("stale pending row is recovered to ready", func(t *testing.T) {
		s, fdb := newFakeLinksStore(t)
		if err := s.UpsertPending(ctx, key, 0, langs); err != nil {
			t.Fatalf("seed: %v", err)
		}
		srv := newAutoServer(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, searchPayload("pt-BR", 4242))
		})
		c := testSubClient(t, srv.srv, "k", "", "", t.TempDir())
		a := NewAutoFetcher(c, s, nil, func() (string, bool) { return langs, true })
		a.run(ctx, key, 0, langs, resolve)
		row := fdb.snapshot(key, 0)
		if row == nil || row.Status != StatusReady || row.FileID != "4242" || row.Source != SourceQuery {
			t.Errorf("row = %+v, want ready/4242/query", row)
		}
	})
}

// TestAutoUnitRunSearchFailureClassifiesReason covers the fresh-cycle search
// failure path through run(): a 500 (non-quota) must persist failed/error
// after the client's retries give up.
func TestAutoUnitRunSearchFailureClassifiesReason(t *testing.T) {
	ctx := context.Background()
	const key = "dddd0000dddd0000dddd0000dddd0000dddd0000"
	s, fdb := newFakeLinksStore(t)
	srv := newAutoServer(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	c := testSubClient(t, srv.srv, "k", "", "", t.TempDir())
	a := NewAutoFetcher(c, s, nil, func() (string, bool) { return "pt-BR", true })
	a.run(ctx, key, 0, "pt-BR", func(context.Context) (SearchOpts, string) {
		return SearchOpts{Query: "x", Languages: "pt-BR"}, SourceQuery
	})
	row := fdb.snapshot(key, 0)
	if row == nil || row.Status != StatusFailed || row.Reason != ReasonError {
		t.Errorf("row = %+v, want failed/error", row)
	}
}
