package watchlist

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/luizg/jackui/internal/jackett"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "watchlist.db")
	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestCreateAndGet(t *testing.T) {
	s := newTestStore(t)
	w, err := s.Create(1, "test query", "2000", 5, "mytopic")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if w.Query != "test query" || w.MinSeeders != 5 || w.NtfyTopic != "mytopic" {
		t.Fatalf("unexpected watchlist: %+v", w)
	}
	if w.ID == 0 {
		t.Fatal("expected non-zero ID")
	}
	got, err := s.Get(1, w.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Query != "test query" {
		t.Fatalf("query = %q", got.Query)
	}
}

func TestCreate_EmptyQuery(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Create(1, "", "", 0, "")
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestCreate_NegativeMinSeeders(t *testing.T) {
	s := newTestStore(t)
	w, err := s.Create(1, "query", "", -5, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if w.MinSeeders != 0 {
		t.Fatalf("MinSeeders = %d, want 0", w.MinSeeders)
	}
}

func TestUpdate(t *testing.T) {
	s := newTestStore(t)
	w, _ := s.Create(1, "old", "2000", 1, "")
	err := s.Update(1, w.ID, "new", "5000", 10, "newtopic")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := s.Get(1, w.ID)
	if got.Query != "new" || got.Category != "5000" || got.MinSeeders != 10 || got.NtfyTopic != "newtopic" {
		t.Fatalf("update not applied: %+v", got)
	}
}

func TestUpdate_EmptyQuery(t *testing.T) {
	s := newTestStore(t)
	w, _ := s.Create(1, "q", "", 1, "")
	err := s.Update(1, w.ID, "", "", 0, "")
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestUpdate_WrongUser(t *testing.T) {
	s := newTestStore(t)
	w, _ := s.Create(1, "q", "", 1, "")
	err := s.Update(2, w.ID, "new", "", 1, "")
	if err == nil || err.Error() != "watchlist n\u00e3o encontrada" {
		t.Fatalf("expected watchlist not found, got: %v", err)
	}
}

func TestDelete(t *testing.T) {
	s := newTestStore(t)
	w, _ := s.Create(1, "q", "", 1, "")
	if err := s.Delete(1, w.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(1, w.ID); err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestDelete_WrongUser(t *testing.T) {
	s := newTestStore(t)
	w, _ := s.Create(1, "q", "", 1, "")
	err := s.Delete(2, w.ID)
	if err == nil || err.Error() != "watchlist n\u00e3o encontrada" {
		t.Fatalf("expected watchlist not found, got: %v", err)
	}
}

func TestList(t *testing.T) {
	s := newTestStore(t)
	s.Create(1, "q1", "", 1, "")
	s.Create(1, "q2", "", 1, "")
	s.Create(2, "q3", "", 1, "")
	items, err := s.List(1)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items for user 1, got %d", len(items))
	}
}

func TestListAll(t *testing.T) {
	s := newTestStore(t)
	s.Create(1, "q1", "", 1, "")
	s.Create(2, "q2", "", 1, "")
	all, err := s.ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 items, got %d", len(all))
	}
}

func TestMarkSeen_NewHit(t *testing.T) {
	s := newTestStore(t)
	w, _ := s.Create(1, "q", "", 1, "")
	isNew, err := s.MarkSeen(w.ID, "hash1", "Test Movie", "magnet:abc", 10, 1000)
	if err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	if !isNew {
		t.Fatal("expected isNew=true for first sighting")
	}
	isNew, err = s.MarkSeen(w.ID, "hash1", "Test Movie", "magnet:abc", 10, 1000)
	if err != nil {
		t.Fatalf("MarkSeen duplicate: %v", err)
	}
	if isNew {
		t.Fatal("expected isNew=false for duplicate")
	}
}

func TestMarkChecked(t *testing.T) {
	s := newTestStore(t)
	w, _ := s.Create(1, "q", "", 1, "")
	if err := s.MarkChecked(w.ID); err != nil {
		t.Fatalf("MarkChecked: %v", err)
	}
	got, _ := s.Get(1, w.ID)
	if got.LastChecked.IsZero() {
		t.Fatal("expected LastChecked to be set")
	}
}

func TestHits(t *testing.T) {
	s := newTestStore(t)
	w, _ := s.Create(1, "q", "", 1, "")
	s.MarkSeen(w.ID, "h1", "T1", "m:1", 10, 100)
	s.MarkSeen(w.ID, "h2", "T2", "m:2", 5, 200)
	hits, err := s.Hits(1, w.ID, 10)
	if err != nil {
		t.Fatalf("Hits: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
}

func TestHits_WrongUser(t *testing.T) {
	s := newTestStore(t)
	w, _ := s.Create(1, "q", "", 1, "")
	_, err := s.Hits(2, w.ID, 10)
	if err == nil {
		t.Fatal("expected error for wrong user")
	}
}

func TestHits_DefaultLimit(t *testing.T) {
	s := newTestStore(t)
	w, _ := s.Create(1, "q", "", 1, "")
	_, err := s.Hits(1, w.ID, 0)
	if err != nil {
		t.Fatalf("Hits with zero limit: %v", err)
	}
}

func TestGet_WrongUser(t *testing.T) {
	s := newTestStore(t)
	w, _ := s.Create(1, "q", "", 1, "")
	_, err := s.Get(2, w.ID)
	if err == nil {
		t.Fatal("expected error for wrong user")
	}
}

type fakeSearcher struct {
	results []jackett.Result
	err     error
}

func (f *fakeSearcher) Search(query, category string, indexers []string) ([]jackett.Result, error) {
	return f.results, f.err
}

type recorderNotifier struct {
	notifications []ntfyCall
}

type ntfyCall struct {
	topic, title, body, magnet string
}

func (n *recorderNotifier) Notify(ctx context.Context, topic, title, body, magnet string) error {
	n.notifications = append(n.notifications, ntfyCall{topic, title, body, magnet})
	return nil
}

func TestWorker_RunOnce(t *testing.T) {
	s := newTestStore(t)
	s.Create(1, "test", "", 1, "mytopic")
	searcher := &fakeSearcher{
		results: []jackett.Result{
			{InfoHash: "abc", Title: "Movie 1", MagnetURI: "magnet:abc", Seeders: 10, Size: 1000},
			{InfoHash: "def", Title: "Movie 2", MagnetURI: "magnet:def", Seeders: 0, Size: 2000},
		},
	}
	notifier := &recorderNotifier{}
	w := NewWorker(s, searcher, notifier, "mytopic", 15*time.Minute)
	w.RunOnce()
	if len(notifier.notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifier.notifications))
	}
	if notifier.notifications[0].title != "Movie 1" {
		t.Fatalf("title = %q, want Movie 1", notifier.notifications[0].title)
	}
}

func TestWorker_RunOnce_NoResults(t *testing.T) {
	s := newTestStore(t)
	s.Create(1, "test", "", 1, "")
	searcher := &fakeSearcher{results: []jackett.Result{}}
	notifier := &recorderNotifier{}
	w := NewWorker(s, searcher, notifier, "topic", 15*time.Minute)
	w.RunOnce()
	if len(notifier.notifications) != 0 {
		t.Fatal("expected no notifications")
	}
}

func TestWorker_RunOnce_NoWatchlists(t *testing.T) {
	s := newTestStore(t)
	searcher := &fakeSearcher{}
	notifier := &recorderNotifier{}
	w := NewWorker(s, searcher, notifier, "topic", 15*time.Minute)
	w.RunOnce()
}

func TestWorker_RunOnce_BelowMinSeeders(t *testing.T) {
	s := newTestStore(t)
	s.Create(1, "test", "", 5, "")
	searcher := &fakeSearcher{
		results: []jackett.Result{
			{InfoHash: "abc", Title: "Movie", MagnetURI: "magnet:abc", Seeders: 3, Size: 1000},
		},
	}
	notifier := &recorderNotifier{}
	w := NewWorker(s, searcher, notifier, "topic", 15*time.Minute)
	w.RunOnce()
	if len(notifier.notifications) != 0 {
		t.Fatal("expected no notification below min seeders")
	}
}

func TestWorker_RunOnce_NoInfoHash(t *testing.T) {
	s := newTestStore(t)
	s.Create(1, "test", "", 1, "")
	searcher := &fakeSearcher{
		results: []jackett.Result{
			{Title: "No hash", MagnetURI: "magnet:xyz", Seeders: 10, Size: 1000},
		},
	}
	notifier := &recorderNotifier{}
	w := NewWorker(s, searcher, notifier, "topic", 15*time.Minute)
	w.RunOnce()
	if len(notifier.notifications) != 0 {
		t.Fatal("expected no notification without info hash")
	}
}

func TestWorker_RunOnce_ResolveTopic_Fallback(t *testing.T) {
	s := newTestStore(t)
	s.Create(1, "test", "", 1, "")
	searcher := &fakeSearcher{
		results: []jackett.Result{
			{InfoHash: "abc", Title: "Movie", MagnetURI: "magnet:abc", Seeders: 10, Size: 1000},
		},
	}
	notifier := &recorderNotifier{}
	w := NewWorker(s, searcher, notifier, "default-topic", 15*time.Minute)
	w.RunOnce()
	if len(notifier.notifications) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifier.notifications))
	}
	if notifier.notifications[0].topic != "default-topic" {
		t.Fatalf("topic = %q, want default-topic", notifier.notifications[0].topic)
	}
}

func TestPickMagnet(t *testing.T) {
	if got := pickMagnet(jackett.Result{MagnetURI: "magnet:abc", Link: "http://link"}); got != "magnet:abc" {
		t.Fatalf("expected magnet URI, got %q", got)
	}
	if got := pickMagnet(jackett.Result{Link: "http://link"}); got != "http://link" {
		t.Fatalf("expected link fallback, got %q", got)
	}
}

func TestHumanSize(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.00 KB"},
		{2048, "2.00 KB"},
		{1048576, "1.00 MB"},
		{1073741824, "1.00 GB"},
		{1099511627776, "1.00 TB"},
	}
	for _, tc := range cases {
		if got := humanSize(tc.n); got != tc.want {
			t.Errorf("humanSize(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestNewWorker_DefaultInterval(t *testing.T) {
	s := newTestStore(t)
	w := NewWorker(s, &fakeSearcher{}, &recorderNotifier{}, "", 0)
	if w.interval != 15*time.Minute {
		t.Fatalf("interval = %v, want 15m", w.interval)
	}
}

func TestNewWorker_PositiveInterval(t *testing.T) {
	s := newTestStore(t)
	w := NewWorker(s, &fakeSearcher{}, &recorderNotifier{}, "", 5*time.Minute)
	if w.interval != 5*time.Minute {
		t.Fatalf("interval = %v, want 5m", w.interval)
	}
}

func TestWorker_StartAndStop(t *testing.T) {
	s := newTestStore(t)
	s.Create(1, "test", "", 1, "")
	w := NewWorker(s, &fakeSearcher{}, &recorderNotifier{}, "topic", 100*time.Millisecond)
	w.Start()
	w.Stop()
}

func TestWorker_StopTwice(t *testing.T) {
	s := newTestStore(t)
	w := NewWorker(s, &fakeSearcher{}, &recorderNotifier{}, "", 15*time.Minute)
	w.Start()
	w.Stop()
	w.Stop()
}

func TestNtfyPoster_EmptyTopic(t *testing.T) {
	n := &NtfyPoster{}
	err := n.Notify(context.Background(), "", "title", "body", "magnet:abc")
	if err != nil {
		t.Fatalf("expected nil for empty topic, got: %v", err)
	}
}
