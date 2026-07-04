package watchlist

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/jackett"
)

func mustCreate(t *testing.T, s *Store, userID int, p Params) {
	t.Helper()
	if _, err := s.Create(userID, p); err != nil {
		t.Fatal(err)
	}
}

func autoParams(query string, p Params) Params {
	p.Query = query
	p.AutoDownload = true
	return p
}

// ---------------------------------------------------------------------------
// MatchesFilters
// ---------------------------------------------------------------------------

func TestMatchesFilters(t *testing.T) {
	gb := int64(1024 * 1024 * 1024)
	cases := []struct {
		name  string
		wl    Watchlist
		title string
		size  int64
		want  bool
	}{
		{"no filters passes anything", Watchlist{}, "Some.Release.CAM", 50 * gb, true},
		{"min resolution met", Watchlist{MinResolution: "1080p"}, "Movie.2024.1080p.WEB-DL", gb, true},
		{"min resolution exceeded", Watchlist{MinResolution: "720p"}, "Movie.2160p.BluRay", gb, true},
		{"min resolution below", Watchlist{MinResolution: "1080p"}, "Movie.720p.HDTV", gb, false},
		{"unknown resolution rejected when filter set", Watchlist{MinResolution: "720p"}, "Movie.2024.CAM", gb, false},
		{"codec match", Watchlist{Codec: "x265"}, "Movie.1080p.HEVC", gb, true},
		{"codec mismatch", Watchlist{Codec: "x264"}, "Movie.1080p.x265", gb, false},
		{"codec unknown rejected when filter set", Watchlist{Codec: "av1"}, "Movie.1080p", gb, false},
		{"max size within", Watchlist{MaxSizeBytes: 5 * gb}, "Movie.1080p", 4 * gb, true},
		{"max size exceeded", Watchlist{MaxSizeBytes: 5 * gb}, "Movie.1080p", 6 * gb, false},
		{"all filters combined pass", Watchlist{MinResolution: "1080p", Codec: "x265", MaxSizeBytes: 10 * gb},
			"Show.S01E01.2160p.x265-GRP", 8 * gb, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.wl.MatchesFilters(tc.title, tc.size); got != tc.want {
				t.Fatalf("MatchesFilters(%q, %d) = %v, want %v", tc.title, tc.size, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Store: filter columns roundtrip + validation + re-baseline
// ---------------------------------------------------------------------------

func TestStore_AutoDownloadFieldsRoundtrip(t *testing.T) {
	s := newTestStore(t)
	w, err := s.Create(1, Params{
		Query: "q", MinSeeders: 2, AutoDownload: true,
		MinResolution: "1080P", MaxSizeBytes: 123, Codec: "X265",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !w.AutoDownload || w.MinResolution != "1080p" || w.MaxSizeBytes != 123 || w.Codec != "x265" {
		t.Fatalf("roundtrip mismatch: %+v", w)
	}
	lists, _ := s.List(1)
	if len(lists) != 1 || !lists[0].AutoDownload || lists[0].MinResolution != "1080p" {
		t.Fatalf("List lost fields: %+v", lists)
	}
	all, _ := s.ListAll()
	if len(all) != 1 || !all[0].AutoDownload || all[0].Codec != "x265" {
		t.Fatalf("ListAll lost fields: %+v", all)
	}
}

func TestStore_InvalidFilterValues(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create(1, Params{Query: "q", MinResolution: "900p"}); err == nil {
		t.Fatal("expected error for invalid minResolution")
	}
	if _, err := s.Create(1, Params{Query: "q", Codec: "mpeg2"}); err == nil {
		t.Fatal("expected error for invalid codec")
	}
	w, err := s.Create(1, Params{Query: "q", MaxSizeBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	if w.MaxSizeBytes != 0 {
		t.Fatalf("negative MaxSizeBytes should clamp to 0, got %d", w.MaxSizeBytes)
	}
}

func TestStore_UpdateQueryChangeResetsBaseline(t *testing.T) {
	s := newTestStore(t)
	w, _ := s.Create(1, params("old query", "", 1, ""))
	if err := s.MarkChecked(w.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Same query: last_checked survives.
	if err := s.Update(1, w.ID, params("old query", "", 3, "t")); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(1, w.ID)
	if got.LastChecked.IsZero() {
		t.Fatal("same-query update must keep last_checked")
	}
	// New query: re-baseline.
	if err := s.Update(1, w.ID, params("new query", "", 3, "t")); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(1, w.ID)
	if !got.LastChecked.IsZero() {
		t.Fatalf("query change must reset last_checked, got %v", got.LastChecked)
	}
}

func TestStore_MarkAutoDownloaded(t *testing.T) {
	s := newTestStore(t)
	w, _ := s.Create(1, params("q", "", 1, ""))
	if _, err := s.MarkSeen(w.ID, "hash1", "Title", "magnet:1", 5, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutoDownloaded(w.ID, "hash1"); err != nil {
		t.Fatal(err)
	}
	hits, err := s.Hits(1, w.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || !hits[0].AutoDownloaded {
		t.Fatalf("expected auto_downloaded hit, got %+v", hits)
	}
}

// ---------------------------------------------------------------------------
// Worker: auto-download flow
// ---------------------------------------------------------------------------

type recorderEnqueuer struct {
	calls []enqueueCall
	err   error
}

type enqueueCall struct {
	userID                          int
	infoHash, name, magnet, tracker string
}

func (e *recorderEnqueuer) EnqueueMagnet(userID int, infoHash, name, magnet, tracker string) error {
	e.calls = append(e.calls, enqueueCall{userID, infoHash, name, magnet, tracker})
	return e.err
}

func autoWorker(t *testing.T, s *Store, searcher *fakeSearcher) (*Worker, *recorderNotifier, *recorderEnqueuer) {
	t.Helper()
	notifier := &recorderNotifier{}
	enq := &recorderEnqueuer{}
	w := NewWorker(s, searcher, notifier, "topic", 15*time.Minute)
	w.SetEnqueuer(enq)
	return w, notifier, enq
}

func TestWorker_AutoDownload_SkipsBaselinePass(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, 7, autoParams("show 1080p", Params{MinSeeders: 1}))
	searcher := &fakeSearcher{results: []jackett.Result{
		{InfoHash: "aaa", Title: "Show.S01E01.1080p.x265", MagnetURI: "magnet:aaa", Seeders: 9, Size: 100},
	}}
	w, notifier, enq := autoWorker(t, s, searcher)
	w.RunOnce()
	if len(enq.calls) != 0 {
		t.Fatalf("baseline pass must not auto-download, got %d", len(enq.calls))
	}
	if len(notifier.notifications) != 0 {
		t.Fatalf("baseline pass must not notify (only seed 'seen'), got %d", len(notifier.notifications))
	}
	// Second pass: a NEW release shows up — now it auto-downloads.
	searcher.results = append(searcher.results,
		jackett.Result{InfoHash: "bbb", Title: "Show.S01E02.1080p.x265", MagnetURI: "magnet:bbb", Seeders: 5, Tracker: "trk", Size: 200})
	w.RunOnce()
	if len(enq.calls) != 1 {
		t.Fatalf("expected 1 auto-download, got %d", len(enq.calls))
	}
	c := enq.calls[0]
	if c.userID != 7 || c.infoHash != "bbb" || c.magnet != "magnet:bbb" || c.tracker != "trk" {
		t.Fatalf("enqueue call mismatch: %+v", c)
	}
	last := notifier.notifications[len(notifier.notifications)-1]
	if !strings.Contains(last.body, "fila de downloads") {
		t.Fatalf("auto-download notification should say so, got %q", last.body)
	}
	hits, _ := s.Hits(7, mustFirstID(t, s, 7), 10)
	var auto int
	for _, h := range hits {
		if h.AutoDownloaded {
			auto++
		}
	}
	if auto != 1 {
		t.Fatalf("expected exactly 1 hit flagged auto_downloaded, got %d", auto)
	}
}

func TestWorker_AutoDownload_RespectsFilters(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, 1, autoParams("movie", Params{MinSeeders: 1, MinResolution: "1080p"}))
	w, notifier, enq := autoWorker(t, s, &fakeSearcher{})
	w.RunOnce() // baseline on empty results
	w.searcher.(*fakeSearcher).results = []jackett.Result{
		{InfoHash: "low", Title: "Movie.720p.WEB", MagnetURI: "magnet:low", Seeders: 9, Size: 100},
	}
	w.RunOnce()
	if len(enq.calls) != 0 {
		t.Fatalf("filtered release must not enqueue, got %+v", enq.calls)
	}
	// It is still notified (auto-download is an upgrade, not a silencer).
	if len(notifier.notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifier.notifications))
	}
	if strings.Contains(notifier.notifications[0].body, "fila") {
		t.Fatalf("non-enqueued hit must not claim it was queued: %q", notifier.notifications[0].body)
	}
}

func TestWorker_AutoDownload_BudgetPerPass(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, 1, autoParams("q", Params{MinSeeders: 1}))
	w, _, enq := autoWorker(t, s, &fakeSearcher{})
	w.RunOnce() // baseline
	var burst []jackett.Result
	for i := 0; i < maxAutoPerPass+2; i++ {
		burst = append(burst, jackett.Result{
			InfoHash: fmt.Sprintf("h%d", i), Title: fmt.Sprintf("Q.E%02d.1080p", i),
			MagnetURI: fmt.Sprintf("magnet:%d", i), Seeders: 5, Size: 10,
		})
	}
	w.searcher.(*fakeSearcher).results = burst
	w.RunOnce()
	if len(enq.calls) != maxAutoPerPass {
		t.Fatalf("expected %d auto-downloads (budget), got %d", maxAutoPerPass, len(enq.calls))
	}
}

func TestWorker_AutoDownload_EnqueueErrorStillNotifies(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, 1, autoParams("q", Params{MinSeeders: 1}))
	w, notifier, enq := autoWorker(t, s, &fakeSearcher{})
	enq.err = errors.New("queue full")
	w.RunOnce() // baseline
	w.searcher.(*fakeSearcher).results = []jackett.Result{
		{InfoHash: "x", Title: "Q.1080p", MagnetURI: "magnet:x", Seeders: 5, Size: 10},
	}
	w.RunOnce()
	if len(notifier.notifications) != 1 {
		t.Fatalf("enqueue failure must still notify, got %d", len(notifier.notifications))
	}
	if strings.Contains(notifier.notifications[0].body, "fila") {
		t.Fatalf("failed enqueue must not claim success: %q", notifier.notifications[0].body)
	}
}

func TestWorker_AutoDownload_DisabledWatchlistNeverEnqueues(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, 1, params("q", "", 1, ""))
	w, _, enq := autoWorker(t, s, &fakeSearcher{})
	w.RunOnce() // baseline
	w.searcher.(*fakeSearcher).results = []jackett.Result{
		{InfoHash: "x", Title: "Q.1080p", MagnetURI: "magnet:x", Seeders: 5, Size: 10},
	}
	w.RunOnce()
	if len(enq.calls) != 0 {
		t.Fatalf("autoDownload=false must never enqueue, got %+v", enq.calls)
	}
}

func mustFirstID(t *testing.T, s *Store, userID int) int {
	t.Helper()
	lists, err := s.List(userID)
	if err != nil || len(lists) == 0 {
		t.Fatalf("List(%d): %v %v", userID, lists, err)
	}
	return lists[0].ID
}

// TestWorker_AggregatesHitsIntoOneNotification: a pass that turns up several new
// releases must emit ONE summary alert (naming the watch, listing the releases),
// not one alert per release — the fix for the create-time notification flood.
func TestWorker_AggregatesHitsIntoOneNotification(t *testing.T) {
	s := newTestStore(t)
	wl, _ := s.Create(1, params("Rick and Morty", "", 1, "topic"))
	primeChecked(t, s, wl.ID) // past the silent baseline
	searcher := &fakeSearcher{results: []jackett.Result{
		{InfoHash: "a", Title: "Rick.and.Morty.S01E01", MagnetURI: "magnet:a", Seeders: 5, Size: 100},
		{InfoHash: "b", Title: "Rick.and.Morty.S01E02", MagnetURI: "magnet:b", Seeders: 5, Size: 100},
		{InfoHash: "c", Title: "Rick.and.Morty.S01E03", MagnetURI: "magnet:c", Seeders: 5, Size: 100},
	}}
	notifier := &recorderNotifier{}
	un := &recorderUserNotifier{}
	w := NewWorker(s, searcher, notifier, "topic", 15*time.Minute)
	w.SetUserNotifier(un)
	w.RunOnce()
	if len(notifier.notifications) != 1 {
		t.Fatalf("3 hits must collapse into ONE ntfy notification, got %d", len(notifier.notifications))
	}
	if len(un.calls) != 1 {
		t.Fatalf("3 hits must collapse into ONE user notification, got %d", len(un.calls))
	}
	n := notifier.notifications[0]
	if !strings.Contains(n.title, "Rick and Morty") || !strings.Contains(n.title, "3") {
		t.Fatalf("summary title should name the watch and the count, got %q", n.title)
	}
	if n.magnet != "" {
		t.Fatalf("aggregated notification must carry no single magnet, got %q", n.magnet)
	}
	if !strings.Contains(n.body, "S01E01") || !strings.Contains(n.body, "S01E03") {
		t.Fatalf("summary body should list the releases, got %q", n.body)
	}
	// All three are recorded as seen, so a second identical pass stays silent.
	w.RunOnce()
	if len(notifier.notifications) != 1 {
		t.Fatalf("already-seen hits must not re-notify, got %d", len(notifier.notifications))
	}
}

// TestAggregateHits_TruncatesLongList: beyond maxHitList releases the summary
// lists the first few and collapses the rest into a "… e mais N" line rather
// than a wall of text.
func TestAggregateHits_TruncatesLongList(t *testing.T) {
	hits := make([]newHit, 0, maxHitList+3)
	for i := 0; i < maxHitList+3; i++ {
		hits = append(hits, newHit{title: fmt.Sprintf("Rel.%02d", i), seeders: 2, size: 10})
	}
	title, body, magnet := aggregateHits(&Watchlist{Query: "q"}, hits)
	if magnet != "" {
		t.Fatalf("multi-hit summary must carry no magnet, got %q", magnet)
	}
	if !strings.Contains(title, "9 novos") { // maxHitList(6)+3
		t.Fatalf("title should carry the total count, got %q", title)
	}
	if !strings.Contains(body, "… e mais 3") {
		t.Fatalf("body should collapse the overflow into '… e mais 3', got %q", body)
	}
	if strings.Contains(body, "Rel.07") {
		t.Fatalf("body must not spell out releases past the cap, got %q", body)
	}
}
