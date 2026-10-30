package subtitles

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/tmdb"
)

// --- fakes ---

// fakeTorrent is a scriptable TorrentMeta double.
type fakeTorrent struct {
	name        string
	complete    bool
	hashFn      func(ctx context.Context, h metainfo.Hash, idx int) (streamer.HashResult, error)
	entered     chan struct{}
	enteredOnce sync.Once
	canceled    chan struct{}
	cancelOnce  sync.Once
}

func (f *fakeTorrent) Name(_ metainfo.Hash, _ int) string   { return f.name }
func (f *fakeTorrent) Complete(_ metainfo.Hash, _ int) bool { return f.complete }
func (f *fakeTorrent) OSHash(ctx context.Context, h metainfo.Hash, idx int) (streamer.HashResult, error) {
	if f.entered != nil {
		f.enteredOnce.Do(func() { close(f.entered) })
	}
	if f.hashFn == nil {
		return streamer.HashResult{Hash: "deadbeefdeadbeef", Size: 1 << 16}, nil
	}
	res, err := f.hashFn(ctx, h, idx)
	if ctx.Err() != nil && f.canceled != nil {
		f.cancelOnce.Do(func() { close(f.canceled) })
	}
	return res, err
}

// blockingHash simulates an active torrent whose hash never completes before
// the tier-1 budget (the cascade must degrade to tier 2, never fail).
func blockingHash(ctx context.Context, _ metainfo.Hash, _ int) (streamer.HashResult, error) {
	<-ctx.Done()
	return streamer.HashResult{}, ctx.Err()
}

func instantHash(_ context.Context, _ metainfo.Hash, _ int) (streamer.HashResult, error) {
	return streamer.HashResult{Hash: "abcdef0123456789", Size: 70000}, nil
}

// fakeTMDB is a scriptable MatchResolver double.
type fakeTMDB struct {
	match *tmdb.Match
	err   error
	calls int32
}

func (f *fakeTMDB) Match(context.Context, string) (*tmdb.Match, error) {
	atomic.AddInt32(&f.calls, 1)
	return f.match, f.err
}

// --- OpenSubtitles test server with a request counter ---

type autoTestServer struct {
	srv      *httptest.Server
	mu       sync.Mutex
	total    int
	searches int
	queries  []url.Values
}

func (s *autoTestServer) totals() (total, searches int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total, s.searches
}

func (s *autoTestServer) query(i int) url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queries[i]
}

// newAutoServer routes the OS API surface: GET /api/v1/subtitles delegates to
// search (counted + query captured); POST /api/v1/download returns a link
// pointing back at the server; /files/* serves a tiny SRT body.
func newAutoServer(t *testing.T, search func(w http.ResponseWriter, r *http.Request)) *autoTestServer {
	t.Helper()
	ts := &autoTestServer{}
	ts.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts.mu.Lock()
		ts.total++
		isSearch := r.Method == http.MethodGet && r.URL.Path == "/api/v1/subtitles"
		if isSearch {
			ts.searches++
			ts.queries = append(ts.queries, r.URL.Query())
		}
		ts.mu.Unlock()
		if isSearch {
			search(w, r)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/download":
			var body struct {
				FileID string `json:"file_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"link": ts.srv.URL + "/files/" + body.FileID + ".srt"})
		case strings.HasPrefix(r.URL.Path, "/files/"):
			_, _ = w.Write([]byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.srv.Close)
	return ts
}

// searchPayload builds one OS search result with the given id/language.
func searchPayload(lang string, fileID int, fileNames ...string) map[string]any {
	if len(fileNames) == 0 {
		fileNames = []string{"Movie.2024." + lang + ".srt"}
	}
	files := make([]any, 0, len(fileNames))
	for _, n := range fileNames {
		files = append(files, map[string]any{"file_id": fileID, "file_name": n})
	}
	return map[string]any{"data": []any{map[string]any{"attributes": map[string]any{
		"language":           lang,
		"release":            "Rel.Name",
		"download_count":     42,
		"hearing_impaired":   false,
		"foreign_parts_only": false,
		"from_trusted":       true,
		"uploader":           map[string]any{"name": "up"},
		"files":              files,
	}}}}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// --- fixture ---

type autoFixture struct {
	a       *AutoFetcher
	c       *Client
	links   *LinksStore
	tmdb    *fakeTMDB
	server  *autoTestServer
	meta    *fakeTorrent
	hash    metainfo.Hash
	key     string
	langs   *string
	autoPtr *bool
}

func newAutoFixture(t *testing.T, search func(w http.ResponseWriter, r *http.Request)) *autoFixture {
	t.Helper()
	links, _ := newLinksStore(t)
	f := &autoFixture{links: links, tmdb: &fakeTMDB{}}
	f.server = newAutoServer(t, search)
	f.c = testSubClient(t, f.server.srv, "testkey", "", "", t.TempDir())
	f.hash = metainfo.HashBytes([]byte(t.Name()))
	f.key = f.hash.HexString()
	f.meta = &fakeTorrent{name: "Show.S01E02.1080p.WEB-DL-GRP"}
	langs, auto := "pt-BR,pt", true
	f.langs, f.autoPtr = &langs, &auto
	f.a = NewAutoFetcher(f.c, links, f.tmdb, func() (string, bool) { return *f.langs, *f.autoPtr })
	return f
}

// waitForStatus polls until the item leaves the in-flight/pending state.
func waitForStatus(t *testing.T, a *AutoFetcher, key string, idx int) AutoStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := a.Status(context.Background(), key, idx); st.Status != StatusPending {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for auto-fetch to settle")
	return AutoStatus{}
}

// waitForSource polls the persisted row until it reports the given source.
func waitForSource(t *testing.T, links *LinksStore, key string, idx int, source string) *SubtitleLink {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		row, err := links.Get(context.Background(), key, idx)
		if err == nil && row != nil && row.Source == source {
			return row
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for row source %q", source)
	return nil
}

// --- acceptance tests (all prefixed TestAuto for the -run filter) ---

func TestAutoHappyHashPath(t *testing.T) {
	f := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	f.meta.hashFn = instantHash
	f.a.StartTorrent(f.meta, f.hash, 0)
	st := waitForStatus(t, f.a, f.key, 0)

	if st.Status != StatusReady {
		t.Fatalf("status = %q (%s), want ready", st.Status, st.Reason)
	}
	if st.FileID != "777" || st.Language != "pt-BR" {
		t.Errorf("FileID/Language = %q/%q, want 777/pt-BR", st.FileID, st.Language)
	}
	if st.VTTPath != f.c.CachePath("777") {
		t.Errorf("VTTPath = %q, want %q", st.VTTPath, f.c.CachePath("777"))
	}
	if st.HearingImpaired || st.Forced {
		t.Errorf("flags = HI:%v forced:%v, want false/false", st.HearingImpaired, st.Forced)
	}
	row, err := f.links.Get(context.Background(), f.key, 0)
	if err != nil || row == nil {
		t.Fatalf("row: %v %v", row, err)
	}
	if row.Source != SourceHash || !row.HashRefined {
		t.Errorf("source/refined = %q/%v, want hash/true", row.Source, row.HashRefined)
	}
	if row.Languages != "pt-BR,pt" {
		t.Errorf("stored languages = %q", row.Languages)
	}
	q := f.server.query(0)
	if q.Get("moviehash") != "abcdef0123456789" || q.Get("moviebytesize") != "70000" {
		t.Errorf("hash search params missing: %v", q)
	}
	if q.Get("languages") != "pt-BR,pt" {
		t.Errorf("languages param = %q", q.Get("languages"))
	}
	total, searches := f.server.totals()
	if searches != 1 {
		t.Errorf("searches = %d, want 1", searches)
	}
	if total != 3 { // search + download meta + subtitle file
		t.Errorf("total OS requests = %d, want 3", total)
	}
}

func TestAutoHashTimeoutCascadesToIMDB(t *testing.T) {
	f := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	f.a.hashBudget = 40 * time.Millisecond
	f.meta.hashFn = blockingHash
	f.tmdb.match = &tmdb.Match{ImdbID: "tt0117731", Title: "Trainspotting", Year: 1996}

	f.a.StartTorrent(f.meta, f.hash, 0)
	st := waitForStatus(t, f.a, f.key, 0)

	if st.Status != StatusReady {
		t.Fatalf("status = %q (%s), want ready after cascade", st.Status, st.Reason)
	}
	row, _ := f.links.Get(context.Background(), f.key, 0)
	if row.Source != SourceIMDB {
		t.Errorf("source = %q, want imdb", row.Source)
	}
	if row.HashRefined {
		t.Error("hash_refined must not be set by an imdb search")
	}
	q := f.server.query(0)
	if q.Get("imdb_id") != "0117731" { // SearchAuto strips the "tt" prefix
		t.Errorf("imdb_id = %q, want 0117731", q.Get("imdb_id"))
	}
	if q.Get("query") != "Trainspotting" {
		t.Errorf("query = %q, want the TMDB title", q.Get("query"))
	}
	if q.Get("season_number") != "1" || q.Get("episode_number") != "2" {
		t.Errorf("season/episode = %q/%q, want 1/2", q.Get("season_number"), q.Get("episode_number"))
	}
	if _, searches := f.server.totals(); searches != 1 {
		t.Errorf("searches = %d, want 1 (hash timeout must not double-search)", searches)
	}
}

func TestAutoSmallFileFallsBackToQuery(t *testing.T) {
	f := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	// Files <64KiB cannot be OS-hashed: tier 1 must fail into tier 2, not fail.
	f.meta.hashFn = func(context.Context, metainfo.Hash, int) (streamer.HashResult, error) {
		return streamer.HashResult{}, errors.New("file smaller than 64KB; OS hash not applicable")
	}
	f.meta.name = "Some.Movie.2023.1080p.WEB-DL.mkv"
	f.tmdb.match, f.tmdb.err = nil, nil // TMDB resolves nothing: query-only

	f.a.StartTorrent(f.meta, f.hash, 0)
	st := waitForStatus(t, f.a, f.key, 0)

	if st.Status != StatusReady {
		t.Fatalf("status = %q (%s), want ready (small file is NOT a failure)", st.Status, st.Reason)
	}
	row, _ := f.links.Get(context.Background(), f.key, 0)
	if row.Source != SourceQuery {
		t.Errorf("source = %q, want query", row.Source)
	}
	q := f.server.query(0)
	if q.Get("query") != "Some.Movie.2023.1080p.WEB-DL" {
		t.Errorf("query = %q, want base name minus extension", q.Get("query"))
	}
	if q.Get("imdb_id") != "" {
		t.Errorf("imdb_id = %q, want unset without a TMDB match", q.Get("imdb_id"))
	}
}

func TestAutoTMDBErrorIsNotFatal(t *testing.T) {
	f := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt", 555))
	})
	f.meta.hashFn = func(context.Context, metainfo.Hash, int) (streamer.HashResult, error) {
		return streamer.HashResult{}, errors.New("no hash")
	}
	f.tmdb.err = errors.New("tmdb down")

	f.a.StartTorrent(f.meta, f.hash, 0)
	st := waitForStatus(t, f.a, f.key, 0)
	if st.Status != StatusReady {
		t.Fatalf("status = %q (%s), want ready query-only", st.Status, st.Reason)
	}
	row, _ := f.links.Get(context.Background(), f.key, 0)
	if row.Source != SourceQuery {
		t.Errorf("source = %q, want query when TMDB errors", row.Source)
	}
}

func TestAutoNoResultsGatesStaleOutcomeFields(t *testing.T) {
	f := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	f.a.StartTorrent(f.meta, f.hash, 0)
	if st := waitForStatus(t, f.a, f.key, 0); st.Status != StatusReady {
		t.Fatalf("phase1 status = %q", st.Status)
	}

	// Settings change + empty results: the re-search FAILS, and the working
	// ready decision survives untouched (audit §2 — flipping the row to failed
	// here permanently destroyed the only working subtitle link). Status may
	// legitimately keep reporting the ready outcome: it is the row's REAL
	// decision, not a stale leak.
	*f.langs = "en"
	f2 := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"data": []any{}})
	})
	f2.links = f.links
	f2.key = f.key
	f2.hash = f.hash
	f2.langs = f.langs
	f2.autoPtr = f.autoPtr
	f2.a = NewAutoFetcher(f2.c, f.links, f2.tmdb, func() (string, bool) { return *f2.langs, *f2.autoPtr })
	f2.a.StartTorrent(f2.meta, f2.hash, 0)

	st := waitForStatus(t, f2.a, f2.key, 0)
	if st.Status != StatusReady || st.FileID != "777" {
		t.Fatalf("status = %q/%q, want ready/777 (a failed re-search keeps the ready row)", st.Status, st.FileID)
	}
	row, _ := f.links.Get(context.Background(), f.key, 0)
	if row == nil || row.Status != StatusReady || row.FileID != "777" || row.Languages != "pt-BR,pt" {
		t.Fatalf("DB row should still be the working ready decision with old languages, got %+v", row)
	}

	// A FIRST fetch with no results still persists a failed row — and its
	// (empty) outcome fields must never leak through Status (review N2: the
	// outcome block is emitted only for ready rows).
	fresh := metainfo.HashBytes([]byte(t.Name() + "-fresh"))
	f2.a.StartTorrent(f2.meta, fresh, 0)
	st = waitForStatus(t, f2.a, fresh.HexString(), 0)
	if st.Status != StatusFailed || st.Reason != ReasonNoResults {
		t.Fatalf("first-fetch status/reason = %q/%q, want failed/no_results", st.Status, st.Reason)
	}
	if st.FileID != "" || st.VTTPath != "" || st.Language != "" {
		t.Errorf("stale outcome leaked: %+v", st)
	}
	row, _ = f.links.Get(context.Background(), fresh.HexString(), 0)
	if row == nil || row.Status != StatusFailed || row.Reason != ReasonNoResults {
		t.Errorf("first-fetch row = %+v, want failed/no_results", row)
	}

	// A failed (first-fetch) row is never retried: further Starts must issue
	// zero traffic.
	base, searches := f2.server.totals()
	f2.a.StartTorrent(f2.meta, fresh, 0)
	time.Sleep(150 * time.Millisecond)
	if total, s := f2.server.totals(); total != base || s != searches {
		t.Errorf("Start on failed row issued HTTP: total/searches = %d/%d, want %d/%d", total, s, base, searches)
	}
}

func TestAutoSearchQuotaTypedError(t *testing.T) {
	f := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"limit reached"}`, http.StatusTooManyRequests)
	})
	f.a.StartTorrent(f.meta, f.hash, 0)
	st := waitForStatus(t, f.a, f.key, 0)
	if st.Status != StatusFailed || st.Reason != ReasonQuota {
		t.Fatalf("status/reason = %q/%q, want failed/quota", st.Status, st.Reason)
	}

	// The typed sentinel must be observable at the client level too.
	_, err := f.c.SearchAuto(SearchOpts{Query: "x"})
	if !errors.Is(err, ErrQuota) {
		t.Errorf("SearchAuto error is not ErrQuota: %v", err)
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("original status text lost from wrap: %v", err)
	}
}

func TestAutoDownloadQuotaTypedError(t *testing.T) {
	f := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	// 406 is the OS download-endpoint quota-exhausted status.
	f.server.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/download" {
			ts := f.server
			ts.mu.Lock()
			ts.total++
			ts.mu.Unlock()
			http.Error(w, "quota exceeded", http.StatusNotAcceptable)
			return
		}
		writeJSON(w, searchPayload("pt-BR", 777))
	})

	f.a.StartTorrent(f.meta, f.hash, 0)
	st := waitForStatus(t, f.a, f.key, 0)
	if st.Status != StatusFailed || st.Reason != ReasonQuota {
		t.Fatalf("status/reason = %q/%q, want failed/quota (406 download)", st.Status, st.Reason)
	}
}

func TestAutoNoAPIKeyZeroHTTP(t *testing.T) {
	links, _ := newLinksStore(t)
	ts := newAutoServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	c := New("", "", "", t.TempDir()) // disabled client
	// Wire the disabled client to the fixture server so the zero-HTTP claim is
	// falsifiable: any regression that sends a request now shows up in the
	// counter (a bare New() would send it into the void).
	c.http = &http.Client{Transport: &subTestTransport{serverURL: ts.srv.URL}}
	hash := metainfo.HashBytes([]byte(t.Name()))
	key := hash.HexString()
	a := NewAutoFetcher(c, links, &fakeTMDB{}, func() (string, bool) { return "pt-BR,pt", true })

	a.StartTorrent(&fakeTorrent{name: "x"}, hash, 0)
	st := waitForStatus(t, a, key, 0)
	if st.Status != StatusFailed || st.Reason != ReasonNoAPIKey {
		t.Fatalf("status/reason = %q/%q, want failed/no_api_key", st.Status, st.Reason)
	}
	if total, _ := ts.totals(); total != 0 {
		t.Errorf("OS requests = %d, want 0 with a disabled client", total)
	}
	row, _ := links.Get(context.Background(), key, 0)
	if row == nil || row.Status != StatusFailed || row.Reason != ReasonNoAPIKey {
		t.Errorf("row = %+v, want failed/no_api_key", row)
	}
}

func TestAutoDedupSecondStartZeroHTTP(t *testing.T) {
	f := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	f.a.StartTorrent(f.meta, f.hash, 0)
	if st := waitForStatus(t, f.a, f.key, 0); st.Status != StatusReady {
		t.Fatalf("status = %q (%s)", st.Status, st.Reason)
	}
	base, searches := f.server.totals()
	if searches != 1 {
		t.Fatalf("searches = %d, want 1", searches)
	}

	// RF7: a ready row means the second Start issues zero OS traffic.
	f.a.StartTorrent(f.meta, f.hash, 0)
	time.Sleep(150 * time.Millisecond)
	total, searches := f.server.totals()
	if total != base || searches != 1 {
		t.Errorf("after second Start: total/searches = %d/%d, want %d/1", total, searches, base)
	}
	if st := f.a.Status(context.Background(), f.key, 0); st.Status != StatusReady {
		t.Errorf("status = %q, want ready", st.Status)
	}
}

func TestAutoLanguagesChangeResearcheExactlyOnce(t *testing.T) {
	var fileID atomic.Int64
	fileID.Store(9000)
	var f *autoFixture
	f = newAutoFixture(t, func(w http.ResponseWriter, r *http.Request) {
		id := int(fileID.Add(1))
		// Emit the first configured language, like a per-language OS result.
		lang := strings.Split(r.URL.Query().Get("languages"), ",")[0]
		if lang == "" {
			lang = "pt-BR"
		}
		writeJSON(w, searchPayload(lang, id))
	})

	f.a.StartTorrent(f.meta, f.hash, 0)
	st := waitForStatus(t, f.a, f.key, 0)
	if st.Status != StatusReady || st.Language != "pt-BR" {
		t.Fatalf("phase1 = %q/%q, want ready/pt-BR", st.Status, st.Language)
	}
	if _, searches := f.server.totals(); searches != 1 {
		t.Fatalf("phase1 searches = %d", searches)
	}

	// Settings change: exactly ONE re-search, converging on the new list.
	*f.langs = "en"
	f.a.StartTorrent(f.meta, f.hash, 0)
	st = waitForStatus(t, f.a, f.key, 0)
	if st.Status != StatusReady || st.Language != "en" {
		t.Fatalf("phase2 = %q/%q, want ready/en", st.Status, st.Language)
	}
	if _, searches := f.server.totals(); searches != 2 {
		t.Fatalf("phase2 searches = %d, want exactly 2", searches)
	}
	row, _ := f.links.Get(context.Background(), f.key, 0)
	if row.Languages != "en" || row.FileID != "9002" {
		t.Errorf("row languages/fileID = %q/%q, want en/9002", row.Languages, row.FileID)
	}

	// Converged: further Starts are free.
	f.a.StartTorrent(f.meta, f.hash, 0)
	time.Sleep(150 * time.Millisecond)
	if _, searches := f.server.totals(); searches != 2 {
		t.Errorf("post-convergence searches = %d, want 2", searches)
	}
}

// TestAutoReSearchFailureKeepsReadyRow pins the NON-destructive settings-change
// re-search (second-model audit §2): a ready row whose stored languages no
// longer match the settings gets one new search, but a FAILED re-search (quota
// here) must leave the working decision untouched — still ready, OLD subtitle,
// OLD stored languages — while the success path still converges to the new
// list (pinned by TestAutoLanguagesChangeResearcheExactlyOnce). MarkFailed on
// this path would permanently destroy the only working subtitle link: failed
// rows are never retried, so the player would lose its subtitle even after the
// user reverted the setting.
func TestAutoReSearchFailureKeepsReadyRow(t *testing.T) {
	var searches atomic.Int32
	f := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		if searches.Add(1) > 1 {
			// The settings-change re-search hits the quota wall.
			http.Error(w, `{"limit reached"}`, http.StatusTooManyRequests)
			return
		}
		writeJSON(w, searchPayload("pt-BR", 777))
	})

	f.a.StartTorrent(f.meta, f.hash, 0)
	if st := waitForStatus(t, f.a, f.key, 0); st.Status != StatusReady || st.FileID != "777" {
		t.Fatalf("phase1 = %q/%q, want ready/777", st.Status, st.FileID)
	}

	// Settings change: the re-search fails on quota…
	*f.langs = "en"
	f.a.StartTorrent(f.meta, f.hash, 0)
	st := waitForStatus(t, f.a, f.key, 0)
	if st.Status != StatusReady || st.Reason != "" {
		t.Fatalf("after failed re-search = %q/%q, want ready with no failure reason", st.Status, st.Reason)
	}
	if st.FileID != "777" || st.Language != "pt-BR" || st.VTTPath != f.c.CachePath("777") {
		t.Errorf("outcome fields = %q/%q/%q, want the OLD subtitle 777/pt-BR", st.FileID, st.Language, st.VTTPath)
	}
	// The re-search must have actually run against the NEW setting (the client
	// retries 429s internally, so exact HTTP counts are retry-policy detail —
	// what matters is ≥1 re-search, queried with "en").
	_, httpSearches := f.server.totals()
	if httpSearches < 2 {
		t.Fatalf("searches = %d, want at least the initial + one re-search", httpSearches)
	}
	if got := f.server.query(0).Get("languages"); got != "pt-BR,pt" {
		t.Errorf("initial search languages = %q, want pt-BR,pt", got)
	}
	if got := f.server.query(httpSearches - 1).Get("languages"); got != "en" {
		t.Errorf("re-search languages = %q, want the NEW setting en", got)
	}

	// …and the persisted row is still the working ready decision with the OLD
	// languages: reverting the settings dedups straight back to it (RF7).
	row, err := f.links.Get(context.Background(), f.key, 0)
	if err != nil || row == nil {
		t.Fatalf("row: %v %v", row, err)
	}
	if row.Status != StatusReady || row.FileID != "777" || row.Languages != "pt-BR,pt" || row.Reason != "" {
		t.Errorf("row = %+v, want ready/777 with old languages pt-BR,pt", row)
	}

	// A kept differing setting re-attempts once per explicit Start (a new play
	// event — status polls NEVER kick a ready row), and every failed attempt
	// still keeps the row intact: never failed, never clobbered with pending.
	_, searchesBefore := f.server.totals()
	f.a.StartTorrent(f.meta, f.hash, 0)
	st = waitForStatus(t, f.a, f.key, 0)
	if st.Status != StatusReady || st.FileID != "777" {
		t.Fatalf("second failed re-search = %q/%q, want still ready/777", st.Status, st.FileID)
	}
	_, searchesAfter := f.server.totals()
	if searchesAfter <= searchesBefore {
		t.Errorf("searches = %d, want > %d (each explicit Start re-attempts the differing setting)", searchesAfter, searchesBefore)
	}
}

// TestAutoReSearchDisabledClientKeepsReadyRow: the same preservation rule when
// the re-search cannot even run — removing the OpenSubtitles API key must not
// destroy working ready rows with a no_api_key failure.
func TestAutoReSearchDisabledClientKeepsReadyRow(t *testing.T) {
	f := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	f.a.StartTorrent(f.meta, f.hash, 0)
	if st := waitForStatus(t, f.a, f.key, 0); st.Status != StatusReady {
		t.Fatalf("phase1 = %q (%s)", st.Status, st.Reason)
	}

	// The API key disappears, then the settings change requests a re-search.
	f.a.client = New("", "", "", t.TempDir())
	*f.langs = "en"
	f.a.StartTorrent(f.meta, f.hash, 0)
	st := waitForStatus(t, f.a, f.key, 0)
	if st.Status != StatusReady || st.FileID != "777" {
		t.Fatalf("after disabled-client re-search = %q/%q, want still ready/777", st.Status, st.FileID)
	}
	row, _ := f.links.Get(context.Background(), f.key, 0)
	if row == nil || row.Status != StatusReady || row.Reason != "" || row.Languages != "pt-BR,pt" {
		t.Errorf("row = %+v, want untouched ready row with old languages (reason must not be %q)", row, ReasonNoAPIKey)
	}
}

func TestAutoCancelStopsHashPhase(t *testing.T) {
	f := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	f.a.hashBudget = 10 * time.Second // Cancel must beat the budget
	f.meta.hashFn = blockingHash
	f.meta.entered = make(chan struct{})
	f.meta.canceled = make(chan struct{})

	f.a.StartTorrent(f.meta, f.hash, 0)
	<-f.meta.entered
	if total, _ := f.server.totals(); total != 0 {
		t.Fatalf("OS traffic during hash phase = %d, want 0", total)
	}
	f.a.Cancel(f.key)
	<-f.meta.canceled
	time.Sleep(100 * time.Millisecond) // let the goroutine settle

	if total, _ := f.server.totals(); total != 0 {
		t.Errorf("OS traffic after cancel = %d, want 0", total)
	}
	// The abandoned cycle did NOT persist a failure; the pending row from
	// UpsertPending is recovered by the next Start.
	if st := f.a.Status(context.Background(), f.key, 0); st.Status != StatusPending {
		t.Errorf("status after cancel = %q (%q), want pending", st.Status, st.Reason)
	}
}

func TestAutoStalePendingRecoveredByStart(t *testing.T) {
	f := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	// Simulate a crashed cycle from a previous process.
	if err := f.links.UpsertPending(context.Background(), f.key, 0, "pt-BR,pt"); err != nil {
		t.Fatalf("UpsertPending: %v", err)
	}
	f.a.StartTorrent(f.meta, f.hash, 0)
	if st := waitForStatus(t, f.a, f.key, 0); st.Status != StatusReady {
		t.Fatalf("status = %q (%s), want ready after recovery", st.Status, st.Reason)
	}
}

func TestAutoRefineUpgradesOnce(t *testing.T) {
	f := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	f.a.hashBudget = 30 * time.Millisecond
	f.meta.hashFn = blockingHash // torrent still active: tier 2
	f.tmdb.match = &tmdb.Match{ImdbID: "tt0117731", Title: "Trainspotting"}
	f.a.StartTorrent(f.meta, f.hash, 0)
	if st := waitForStatus(t, f.a, f.key, 0); st.Status != StatusReady {
		t.Fatalf("phase1 = %q (%s)", st.Status, st.Reason)
	}
	if _, searches := f.server.totals(); searches != 1 {
		t.Fatalf("phase1 searches = %d", searches)
	}

	// Download completes: the real hash now refines the imdb row ONCE.
	f.meta.complete = true
	f.meta.hashFn = instantHash
	f.a.RefineOnComplete(f.meta, f.hash, 0)
	row := waitForSource(t, f.links, f.key, 0, SourceHash)
	if !row.HashRefined {
		t.Error("hash_refined not set after a successful refinement")
	}
	if row.FileID != "777" || row.Language != "pt-BR" {
		t.Errorf("refined row = %q/%q", row.FileID, row.Language)
	}
	if _, searches := f.server.totals(); searches != 2 {
		t.Fatalf("post-refine searches = %d, want 2", searches)
	}

	// A second completion event must not re-search (source == hash).
	f.a.RefineOnComplete(f.meta, f.hash, 0)
	time.Sleep(150 * time.Millisecond)
	if _, searches := f.server.totals(); searches != 2 {
		t.Errorf("refine ran twice: searches = %d, want 2", searches)
	}
}

func TestAutoRefineFailureKeepsRowOnce(t *testing.T) {
	f := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	f.a.hashBudget = 30 * time.Millisecond
	f.meta.hashFn = blockingHash
	f.tmdb.match = &tmdb.Match{ImdbID: "tt0117731"}
	f.a.StartTorrent(f.meta, f.hash, 0)
	if st := waitForStatus(t, f.a, f.key, 0); st.Status != StatusReady {
		t.Fatalf("phase1 = %q (%s)", st.Status, st.Reason)
	}

	// The hash search finds nothing: the old decision must survive untouched,
	// and repeated completion events must not loop (in-memory refined guard).
	empty := map[string]any{"data": []any{}}
	fail := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, empty)
	})
	fail.links, fail.key, fail.hash = f.links, f.key, f.hash
	fail.a = NewAutoFetcher(fail.c, f.links, fail.tmdb, func() (string, bool) { return *f.langs, *f.autoPtr })

	f.meta.complete = true
	f.meta.hashFn = instantHash
	// Refine through the EMPTY-server fetcher so the hash search finds nothing.
	fail.a.RefineOnComplete(f.meta, f.hash, 0)
	time.Sleep(250 * time.Millisecond) // settle the single refine attempt

	row, _ := f.links.Get(context.Background(), f.key, 0)
	if row.Source != SourceIMDB || row.Status != StatusReady || row.FileID != "777" {
		t.Fatalf("row after failed refine = %+v, want the untouched imdb row", row)
	}
	if !row.HashRefined {
		t.Error("failed refine must persist hash_refined (restart loop guard)")
	}
	// The failed refine consumed its single attempt on the empty server; the
	// phase-1 server saw only the original tier-2 search.
	if _, sFail := fail.server.totals(); sFail != 1 {
		t.Errorf("refine searches = %d, want exactly 1 (no retry loop)", sFail)
	}
	if _, sOrig := f.server.totals(); sOrig != 1 {
		t.Errorf("original server searches = %d, want 1", sOrig)
	}
	// Refinement guard: repeat completion events must not re-attempt.
	fail.a.RefineOnComplete(f.meta, f.hash, 0)
	fail.a.RefineOnComplete(f.meta, f.hash, 0)
	time.Sleep(200 * time.Millisecond)
	if _, sFail := fail.server.totals(); sFail != 1 {
		t.Errorf("refine searches after repeats = %d, want 1 (in-memory guard)", sFail)
	}

	// The PERSISTED flag (not just the in-memory guard) stops a fresh process
	// — a new AutoFetcher with an empty refined map — from re-attempting the
	// failed refinement across restarts.
	fresh := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 999))
	})
	fresh.a = NewAutoFetcher(fresh.c, f.links, fresh.tmdb, func() (string, bool) { return *f.langs, *f.autoPtr })
	fresh.a.RefineOnComplete(f.meta, f.hash, 0)
	time.Sleep(200 * time.Millisecond)
	if _, sFresh := fresh.server.totals(); sFresh != 0 {
		t.Errorf("fresh process re-attempted failed refine: searches = %d, want 0 (persisted hash_refined)", sFresh)
	}
}

func TestAutoRefineSearchErrorKeepsRow(t *testing.T) {
	f := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	f.a.hashBudget = 30 * time.Millisecond
	f.meta.hashFn = blockingHash
	f.tmdb.match = &tmdb.Match{ImdbID: "tt0117731"}
	f.a.StartTorrent(f.meta, f.hash, 0)
	if st := waitForStatus(t, f.a, f.key, 0); st.Status != StatusReady {
		t.Fatalf("phase1 = %q (%s)", st.Status, st.Reason)
	}

	serverError := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	serverError.links, serverError.key = f.links, f.key
	serverError.a = NewAutoFetcher(serverError.c, f.links, serverError.tmdb,
		func() (string, bool) { return *f.langs, *f.autoPtr })

	f.meta.complete = true
	f.meta.hashFn = instantHash
	serverError.a.RefineOnComplete(f.meta, f.hash, 0)
	// The 500 is retried by httpretry (3 attempts with backoff) before the
	// refine gives up — settle on the persisted flag instead of a fixed sleep
	// (MarkRefined is the failure path's last step, so the flag means done).
	row := waitForHashRefined(t, f.links, f.key, 0)
	if row.Source != SourceIMDB || row.Status != StatusReady {
		t.Fatalf("row after refine error = %+v, want untouched imdb row", row)
	}
	// No restart loop: once the flag is persisted the traffic count is final.
	total, searches := serverError.server.totals()
	time.Sleep(300 * time.Millisecond)
	if total2, s2 := serverError.server.totals(); total2 != total || s2 != searches {
		t.Errorf("refine traffic kept growing: %d/%d → %d/%d (total/searches)", total, searches, total2, s2)
	}
}

// waitForHashRefined polls the persisted row until hash_refined is set.
func waitForHashRefined(t *testing.T, links *LinksStore, key string, idx int) *SubtitleLink {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		row, err := links.Get(context.Background(), key, idx)
		if err == nil && row != nil && row.HashRefined {
			return row
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for hash_refined on %s#%d", key, idx)
	return nil
}

// TestAutoRefineFailurePathsMarkRefined covers the two refine failure paths
// not exercised by the no-results/search-error tests: the hash becoming
// unavailable, and the download failing after a successful hash search. Every
// failure must persist hash_refined while keeping the old decision.
func TestAutoRefineFailurePathsMarkRefined(t *testing.T) {
	f := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	f.a.hashBudget = 30 * time.Millisecond
	f.meta.hashFn = blockingHash // tier 2 while the torrent is active
	f.tmdb.match = &tmdb.Match{ImdbID: "tt0117731"}
	f.a.StartTorrent(f.meta, f.hash, 0)
	if st := waitForStatus(t, f.a, f.key, 0); st.Status != StatusReady {
		t.Fatalf("phase1 = %q (%s)", st.Status, st.Reason)
	}

	// (a) Hash unavailable at refine time: no HTTP at all, flag persisted.
	f.meta.complete = true
	f.meta.hashFn = func(context.Context, metainfo.Hash, int) (streamer.HashResult, error) {
		return streamer.HashResult{}, errors.New("file vanished")
	}
	f.a.RefineOnComplete(f.meta, f.hash, 0)
	row := waitForHashRefined(t, f.links, f.key, 0)
	if row.Source != SourceIMDB || row.Status != StatusReady || row.FileID != "777" {
		t.Errorf("row after hash-unavailable refine = %+v, want the untouched imdb row", row)
	}
	if _, searches := f.server.totals(); searches != 1 {
		t.Errorf("searches = %d, want 1 (refine made no HTTP)", searches)
	}

	// (b) Download fails after a successful hash search: a second item, its
	// own ready imdb row, refined through a fixture whose download quota-fails.
	hash2 := metainfo.HashBytes([]byte(t.Name() + "-dl"))
	key2 := hash2.HexString()
	f.meta.hashFn = blockingHash
	f.a.StartTorrent(f.meta, hash2, 0)
	if st := waitForStatus(t, f.a, key2, 0); st.Status != StatusReady {
		t.Fatalf("phase1 (item 2) = %q (%s)", st.Status, st.Reason)
	}
	dlFail := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 333))
	})
	// The fixture wrapper owns the /api/v1/download route, so the 406 must be
	// intercepted at the handler level: the download meta fails while search
	// traffic keeps flowing through the counted wrapper.
	base := dlFail.server.srv.Config.Handler
	dlFail.server.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/download" {
			dlFail.server.mu.Lock()
			dlFail.server.total++
			dlFail.server.mu.Unlock()
			http.Error(w, "quota exceeded", http.StatusNotAcceptable)
			return
		}
		base.ServeHTTP(w, r)
	})
	dlFail.links, dlFail.key, dlFail.hash = f.links, key2, hash2
	dlFail.a = NewAutoFetcher(dlFail.c, f.links, dlFail.tmdb, func() (string, bool) { return *f.langs, *f.autoPtr })

	f.meta.hashFn = instantHash
	dlFail.a.RefineOnComplete(f.meta, hash2, 0)
	row2 := waitForHashRefined(t, f.links, key2, 0)
	if row2.Source != SourceIMDB || row2.Status != StatusReady || row2.FileID != "777" {
		t.Errorf("row after download-fail refine = %+v, want the untouched imdb row", row2)
	}
	total, searches := dlFail.server.totals()
	if searches != 1 || total != 2 { // search + rejected download meta, nothing else
		t.Errorf("dl-fail refine traffic = %d/%d (total/searches), want 2/1", total, searches)
	}
}

func TestAutoRefineIgnoresIncompleteAndDisabled(t *testing.T) {
	f := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	f.meta.complete = false
	f.a.RefineOnComplete(f.meta, f.hash, 0)
	time.Sleep(100 * time.Millisecond)
	if total, _ := f.server.totals(); total != 0 {
		t.Errorf("traffic = %d, want 0 for an incomplete file", total)
	}

	// No row at all: nothing to refine.
	f.meta.complete = true
	f.a.RefineOnComplete(f.meta, f.hash, 0)
	time.Sleep(100 * time.Millisecond)
	if total, _ := f.server.totals(); total != 0 {
		t.Errorf("traffic = %d, want 0 without a row", total)
	}

	// auto-fetch off: refine is a no-op like Start.
	*f.autoPtr = false
	if err := f.links.UpsertPending(context.Background(), f.key, 0, "pt-BR,pt"); err != nil {
		t.Fatalf("UpsertPending: %v", err)
	}
	f.a.RefineOnComplete(f.meta, f.hash, 0)
	time.Sleep(100 * time.Millisecond)
	if total, _ := f.server.totals(); total != 0 {
		t.Errorf("traffic = %d, want 0 with auto-fetch off", total)
	}
}

func TestAutoStatusReadySurvivesAutoFetchOff(t *testing.T) {
	f := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	f.a.StartTorrent(f.meta, f.hash, 0)
	if st := waitForStatus(t, f.a, f.key, 0); st.Status != StatusReady {
		t.Fatalf("phase1 = %q (%s)", st.Status, st.Reason)
	}

	// Decision (review finding 4): already-fetched subtitles stay valid when
	// auto-fetch is turned OFF — the ready row and its outcome fields keep
	// being reported, because RF7 persistence makes no calls. Only new
	// fetches are gated by auto-fetch.
	*f.autoPtr = false
	st := f.a.Status(context.Background(), f.key, 0)
	if st.Status != StatusReady || st.FileID != "777" || st.VTTPath != f.c.CachePath("777") {
		t.Errorf("status with auto-fetch off = %+v, want the ready row intact", st)
	}

	// Everything that belongs to an ACTIVE fetch lifecycle reports none while
	// the feature is off: a failed row, and a missing row.
	ctx := context.Background()
	if err := f.links.MarkFailed(ctx, f.key+"-failed", 0, ReasonNoResults); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if st := f.a.Status(ctx, f.key+"-failed", 0); st.Status != statusNone {
		t.Errorf("failed row + auto-fetch off = %q, want none", st.Status)
	}
	if st := f.a.Status(ctx, f.key+"-absent", 0); st.Status != statusNone {
		t.Errorf("no row + auto-fetch off = %q, want none", st.Status)
	}
}

func TestAutoStartDisabledNoOp(t *testing.T) {
	f := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	*f.autoPtr = false
	f.a.StartTorrent(f.meta, f.hash, 0)
	f.a.StartLocal("local:xyz", 0, "Movie.2024.mkv", "/nonexistent")
	time.Sleep(150 * time.Millisecond)
	if total, _ := f.server.totals(); total != 0 {
		t.Errorf("traffic = %d, want 0 when auto-fetch is off", total)
	}
	if st := f.a.Status(context.Background(), f.key, 0); st.Status != statusNone {
		t.Errorf("status = %q, want none when auto-fetch is off", st.Status)
	}
	row, _ := f.links.Get(context.Background(), f.key, 0)
	if row != nil {
		t.Errorf("row = %+v, want none when auto-fetch is off", row)
	}
}

func TestAutoLocalCompleteFileHashesDirectly(t *testing.T) {
	f := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	dir := t.TempDir()
	path := filepath.Join(dir, "Movie.Local.2024.mkv")
	buf := make([]byte, 128<<10) // >64KiB so the OS hash applies
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	f.a.StartLocal("local:"+f.key, 0, "Movie.Local.2024.mkv", path)
	st := waitForStatus(t, f.a, "local:"+f.key, 0)
	if st.Status != StatusReady {
		t.Fatalf("status = %q (%s), want ready", st.Status, st.Reason)
	}
	q := f.server.query(0)
	want, err := expectedLocalHash(path)
	if err != nil {
		t.Fatalf("expected hash: %v", err)
	}
	if q.Get("moviehash") != want {
		t.Errorf("moviehash = %q, want %q", q.Get("moviehash"), want)
	}
	row, _ := f.links.Get(context.Background(), "local:"+f.key, 0)
	if row == nil || row.Source != SourceHash {
		t.Errorf("source = %v, want hash", row)
	}
}

func TestAutoLocalUnreadableFallsToQuery(t *testing.T) {
	f := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	f.meta.name = "Missing.File.2023.mkv"
	f.a.StartLocal("local:gone", 0, f.meta.name, filepath.Join(t.TempDir(), "nope.mkv"))
	st := waitForStatus(t, f.a, "local:gone", 0)
	if st.Status != StatusReady {
		t.Fatalf("status = %q (%s), want ready via query fallback", st.Status, st.Reason)
	}
	q := f.server.query(0)
	if q.Get("query") != "Missing.File.2023" {
		t.Errorf("query = %q, want the file base name", q.Get("query"))
	}
}

func TestAutoDBErrorsAbortQuietly(t *testing.T) {
	ts := newAutoServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	c := testSubClient(t, ts.srv, "testkey", "", "", t.TempDir())
	// A pool that cannot connect: every store call errors immediately.
	bad, err := sql.Open("pgx", "postgres://invalid:invalid@127.0.0.1:1/nope?sslmode=disable")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = bad.Close() })
	links, err := NewLinksStore(bad)
	if err != nil {
		t.Fatalf("NewLinksStore: %v", err)
	}
	hash := metainfo.HashBytes([]byte(t.Name()))
	key := hash.HexString()
	a := NewAutoFetcher(c, links, &fakeTMDB{}, func() (string, bool) { return "pt-BR,pt", true })

	a.StartTorrent(&fakeTorrent{name: "x"}, hash, 0)
	time.Sleep(150 * time.Millisecond)
	if total, _ := ts.totals(); total != 0 {
		t.Errorf("traffic = %d, want 0 when persistence is broken", total)
	}
	if st := a.Status(context.Background(), key, 0); st.Status != statusNone {
		t.Errorf("status = %q, want none on DB error", st.Status)
	}
}

// --- unit tests for the helpers added with the AutoFetcher ---

func TestAutoPickSubtitle(t *testing.T) {
	sub := func(id, lang string, hi, forced bool, formats ...string) Subtitle {
		return Subtitle{ID: id, Language: lang, HearingImp: hi, Forced: forced, Formats: formats}
	}
	cases := []struct {
		name  string
		in    []Subtitle
		langs string
		want  string // "" = nil
	}{
		{"empty", nil, "pt-BR,pt", ""},
		{"ass-only skipped", []Subtitle{sub("1", "pt-BR", false, false, ".ass")}, "pt-BR,pt", ""},
		{"ssa-only skipped", []Subtitle{sub("1", "pt-BR", false, false, ".ssa", ".ass")}, "pt-BR,pt", ""},
		{"srt over ass", []Subtitle{sub("1", "pt-BR", false, false, ".ass"), sub("2", "pt-BR", false, false, ".srt")}, "pt-BR,pt", "2"},
		{"unknown formats allowed", []Subtitle{sub("1", "pt-BR", false, false)}, "pt-BR,pt", "1"},
		{"vtt appliable", []Subtitle{sub("1", "pt-BR", false, false, ".vtt")}, "pt-BR,pt", "1"},
		{"language priority pt-BR first", []Subtitle{sub("1", "pt", false, false), sub("2", "pt-BR", false, false)}, "pt-BR,pt", "2"},
		{"language priority pt first", []Subtitle{sub("1", "pt-BR", false, false), sub("2", "pt", false, false)}, "pt", "2"},
		{"language rank dominates hearing-impaired", []Subtitle{sub("1", "pt-BR", true, false), sub("2", "pt", false, false)}, "pt-BR,pt", "1"},
		{"hearing-impaired deprioritized", []Subtitle{sub("1", "pt", true, false), sub("2", "pt", false, false)}, "pt", "2"},
		{"forced deprioritized", []Subtitle{sub("1", "pt", false, true), sub("2", "pt", false, false)}, "pt", "2"},
		{"unknown language ranks last", []Subtitle{sub("1", "en", false, false), sub("2", "pt-BR", false, false)}, "pt-BR,pt", "2"},
		{"case-insensitive language", []Subtitle{sub("1", "PT-br", false, false)}, "pt-BR", "1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pickSubtitle(tc.in, tc.langs)
			if tc.want == "" {
				if got != nil {
					t.Errorf("pick = %+v, want nil", got)
				}
				return
			}
			if got == nil || got.ID != tc.want {
				t.Errorf("pick = %+v, want id %q", got, tc.want)
			}
		})
	}
}

func TestAutoSubtitleFormatsPassthrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"data": []any{
			map[string]any{"attributes": map[string]any{
				"language": "pt-BR",
				"files": []any{
					map[string]any{"file_id": 1, "file_name": "A.B.eng.srt"},
					map[string]any{"file_id": 1, "file_name": "x.ASS"},
				},
			}},
			map[string]any{"attributes": map[string]any{
				"language": "pt",
				"files":    []any{map[string]any{"file_id": 2, "file_name": "y.SRT"}}, // dedup, case-folded
			}},
			map[string]any{"attributes": map[string]any{ // no file_name: unknown
				"language": "en",
				"files":    []any{map[string]any{"file_id": 3}},
			}},
		}})
	}))
	defer srv.Close()
	c := testSubClient(t, srv, "k", "", "", t.TempDir())
	res, err := c.SearchAuto(SearchOpts{Query: "x"})
	if err != nil {
		t.Fatalf("SearchAuto: %v", err)
	}
	if len(res) != 3 {
		t.Fatalf("results = %d, want 3", len(res))
	}
	if got := res[0].Formats; len(got) != 2 || got[0] != ".srt" || got[1] != ".ass" {
		t.Errorf("formats[0] = %v, want [.srt .ass]", got)
	}
	if got := res[0].PrimaryFormat; got != ".srt" {
		t.Errorf("PrimaryFormat[0] = %q, want .srt (Files[0], not the union)", got)
	}
	if got := res[1].Formats; len(got) != 1 || got[0] != ".srt" {
		t.Errorf("formats[1] = %v, want [.srt] (deduped)", got)
	}
	if got := res[1].PrimaryFormat; got != ".srt" {
		t.Errorf("PrimaryFormat[1] = %q, want .srt (case-folded from y.SRT)", got)
	}
	if got := res[2].Formats; len(got) != 0 {
		t.Errorf("formats[2] = %v, want empty (unknown)", got)
	}
	if got := res[2].PrimaryFormat; got != "" {
		t.Errorf("PrimaryFormat[2] = %q, want empty (no file_name on Files[0])", got)
	}
	if res[0].HearingImp || res[0].Forced {
		t.Errorf("default flags = HI:%v forced:%v, want false", res[0].HearingImp, res[0].Forced)
	}
}

// TestAutoPrimaryFormatGatesMixedResults pins review finding 3: the download
// delivers Files[0] (Subtitle.ID is Files[0].FileID), so a files[0]=.ass +
// files[1]=.srt result must NOT be auto-appliable even though the Formats
// union contains .srt — auto-applying it would convert the .ass into an
// invalid track.
func TestAutoPrimaryFormatGatesMixedResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("query") {
		case "mixed": // .ass first: the file the download would deliver
			writeJSON(w, searchPayload("pt-BR", 551, "A.B.eng.ass", "A.B.eng.srt"))
		case "srtonly":
			writeJSON(w, searchPayload("pt-BR", 552, "A.B.eng.srt"))
		case "srtfirst": // .srt primary with a trailing .ass alternative
			writeJSON(w, searchPayload("pt-BR", 553, "A.B.eng.srt", "A.B.eng.ass"))
		default: // extensionless Files[0] + a .srt alternative: unknown primary
			writeJSON(w, map[string]any{"data": []any{map[string]any{"attributes": map[string]any{
				"language": "pt-BR",
				"files": []any{
					map[string]any{"file_id": 554},
					map[string]any{"file_id": 554, "file_name": "B.eng.srt"},
				},
			}}}})
		}
	}))
	defer srv.Close()
	c := testSubClient(t, srv, "k", "", "", t.TempDir())

	res, err := c.SearchAuto(SearchOpts{Query: "mixed"})
	if err != nil || len(res) != 1 {
		t.Fatalf("mixed SearchAuto: %v (%d)", err, len(res))
	}
	if res[0].PrimaryFormat != ".ass" {
		t.Errorf("PrimaryFormat = %q, want .ass", res[0].PrimaryFormat)
	}
	if pick := pickSubtitle(res, "pt-BR,pt"); pick != nil {
		t.Errorf("mixed ass+srt result auto-applied: id %s (would download the .ass)", pick.ID)
	}

	res, err = c.SearchAuto(SearchOpts{Query: "srtonly"})
	if err != nil || len(res) != 1 {
		t.Fatalf("srtonly SearchAuto: %v (%d)", err, len(res))
	}
	if pick := pickSubtitle(res, "pt-BR,pt"); pick == nil || pick.ID != "552" {
		t.Errorf("srt-only result not appliable: %+v", pick)
	}

	res, err = c.SearchAuto(SearchOpts{Query: "srtfirst"})
	if err != nil || len(res) != 1 {
		t.Fatalf("srtfirst SearchAuto: %v (%d)", err, len(res))
	}
	if pick := pickSubtitle(res, "pt-BR,pt"); pick == nil || pick.ID != "553" {
		t.Errorf("srt-primary mixed result not appliable: %+v", pick)
	}

	// Unknown primary format keeps the legacy union rule: allowed.
	res, err = c.SearchAuto(SearchOpts{Query: "unknown"})
	if err != nil || len(res) != 1 {
		t.Fatalf("unknown SearchAuto: %v (%d)", err, len(res))
	}
	if res[0].PrimaryFormat != "" {
		t.Errorf("PrimaryFormat = %q, want empty (extensionless Files[0])", res[0].PrimaryFormat)
	}
	if pick := pickSubtitle(res, "pt-BR,pt"); pick == nil || pick.ID != "554" {
		t.Errorf("unknown primary not appliable via union rule: %+v", pick)
	}
}

func TestAutoSubtitleForcedFlagDecoded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"data": []any{map[string]any{"attributes": map[string]any{
			"language":           "pt-BR",
			"hearing_impaired":   true,
			"foreign_parts_only": true,
			"files":              []any{map[string]any{"file_id": 9}},
		}}}})
	}))
	defer srv.Close()
	c := testSubClient(t, srv, "k", "", "", t.TempDir())
	res, err := c.SearchAuto(SearchOpts{Query: "x"})
	if err != nil || len(res) != 1 {
		t.Fatalf("SearchAuto: %v (%d)", err, len(res))
	}
	if !res[0].Forced || !res[0].HearingImp {
		t.Errorf("forced/HI = %v/%v, want true/true", res[0].Forced, res[0].HearingImp)
	}
	// A forced HI result still loses to a clean same-language result.
	pick := pickSubtitle([]Subtitle{
		{ID: "9", Language: "pt-BR", HearingImp: true, Forced: true},
		{ID: "8", Language: "pt-BR"},
	}, "pt-BR")
	if pick.ID != "8" {
		t.Errorf("pick = %q, want 8 (clean result preferred)", pick.ID)
	}
}

// TestAutoNilSettingsAndGuardShortCircuits covers the defensive no-op guards
// (nil fetcher, nil TorrentMeta, empty local key, nil work/resolve, and a
// fetcher built without a settings closure). A settings-less fetcher behaves
// as "disabled, no languages": Start side effects are suppressed, yet a READY
// row stays reportable per the status decision.
func TestAutoNilSettingsAndGuardShortCircuits(t *testing.T) {
	f := newAutoFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, searchPayload("pt-BR", 777))
	})
	ctx := context.Background()
	if err := f.links.UpsertPending(ctx, f.key, 0, "pt-BR"); err != nil {
		t.Fatalf("UpsertPending: %v", err)
	}
	if err := f.links.MarkReady(ctx, f.key, 0, "777", "pt-BR", "pt-BR", false, false,
		f.c.CachePath("777"), SourceIMDB); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}

	a := NewAutoFetcher(f.c, f.links, f.tmdb, nil)     // no settings closure
	a.StartTorrent(f.meta, f.hash, 0)                  // settings nil → no fetch
	a.StartLocal("", 0, "Movie.mkv", "/tmp/movie.mkv") // empty key → no-op
	var nilFetcher *AutoFetcher
	nilFetcher.Cancel(f.key)           // nil receiver → no-op
	f.a.StartTorrent(nil, f.hash, 0)   // nil TorrentMeta → no-op
	f.a.start(fetchKey{f.key, 9}, nil) // nil work → no-op
	a.run(ctx, f.key, 0, "pt-BR", nil) // nil resolve → no-op
	zero := &AutoFetcher{}
	zero.run(ctx, f.key, 0, "", nil) // no links/client → no-op
	time.Sleep(150 * time.Millisecond)
	if total, _ := f.server.totals(); total != 0 {
		t.Errorf("guard paths issued HTTP: %d requests, want 0", total)
	}

	// A ready row is still reported without a settings closure (auto-fetch
	// "off" + ready ⇒ valid previously fetched subtitle).
	if st := a.Status(ctx, f.key, 0); st.Status != StatusReady || st.FileID != "777" {
		t.Errorf("status without settings closure = %+v, want the ready row", st)
	}
}

func TestAutoCachePathExported(t *testing.T) {
	dir := t.TempDir()
	c := New("k", "", "", dir)
	if got := c.CachePath("123"); got != filepath.Join(dir, "123.vtt") {
		t.Errorf("CachePath = %q, want %q", got, filepath.Join(dir, "123.vtt"))
	}
	if got := c.CachePath("../evil"); got != "" {
		t.Errorf("CachePath(non-numeric) = %q, want empty", got)
	}
	if got := New("k", "", "", "").CachePath("1"); got != "" {
		t.Errorf("CachePath without cache dir = %q, want empty", got)
	}
	var nilClient *Client
	if got := nilClient.CachePath("1"); got != "" {
		t.Errorf("nil client CachePath = %q, want empty", got)
	}
}

func TestAutoIsQuotaStatus(t *testing.T) {
	for code, want := range map[int]bool{
		http.StatusTooManyRequests:     true,
		http.StatusNotAcceptable:       true,
		http.StatusOK:                  false,
		http.StatusNotFound:            false,
		http.StatusInternalServerError: false,
	} {
		if got := isQuotaStatus(code); got != want {
			t.Errorf("isQuotaStatus(%d) = %v, want %v", code, got, want)
		}
	}
}

func TestAutoDisabledSentinels(t *testing.T) {
	c := New("", "", "", "")
	if _, err := c.SearchAuto(SearchOpts{Query: "x"}); !errors.Is(err, ErrDisabled) {
		t.Errorf("SearchAuto err = %v, want ErrDisabled", err)
	}
	if _, err := c.Download("123"); !errors.Is(err, ErrDisabled) {
		t.Errorf("Download err = %v, want ErrDisabled", err)
	}
}

// expectedLocalHash mirrors what tier1Local computes for a local file.
func expectedLocalHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	res, err := streamer.ComputeFileOSHash(f, fi.Size())
	if err != nil {
		return "", err
	}
	return res.Hash, nil
}
