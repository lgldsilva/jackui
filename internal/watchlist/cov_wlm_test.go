package watchlist

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luizg/jackui/internal/jackett"
)

// wlmErrSearcher always fails — exercises processOne's search-error branch.
type wlmErrSearcher struct{}

func (wlmErrSearcher) Search(query, category string, indexers []string) ([]jackett.Result, error) {
	return nil, context.DeadlineExceeded
}

// wlmErrNotifier always returns an error — exercises processOneResult's notify
// failure branch (the error is logged, not returned).
type wlmErrNotifier struct{ called int32 }

func (n *wlmErrNotifier) Notify(ctx context.Context, topic, title, body, magnet string) error {
	atomic.AddInt32(&n.called, 1)
	return context.Canceled
}

// TestWlmRunOnceSearchError drives processOne down the jackett-search-failure
// path: no notification fires and MarkChecked is still skipped on that list.
func Test_wlmRunOnceSearchError(t *testing.T) {
	s := newTestStore(t)
	s.Create(1, "boom", "", 1, "topic")
	notifier := &recorderNotifier{}
	w := NewWorker(s, wlmErrSearcher{}, notifier, "topic", 15*time.Minute)
	w.RunOnce()
	if len(notifier.notifications) != 0 {
		t.Fatalf("wlm: expected no notifications on search error, got %d", len(notifier.notifications))
	}
}

// TestWlmRunOnceListAllError closes the store first so ListAll returns an error
// inside runOnce (the early-return logging branch).
func Test_wlmRunOnceListAllError(t *testing.T) {
	s := newTestStore(t)
	s.Close() // queries now fail on the closed DB
	w := NewWorker(s, &fakeSearcher{}, &recorderNotifier{}, "topic", 15*time.Minute)
	w.RunOnce() // must not panic
}

// TestWlmNotifyFailureIsSwallowed confirms a notifier error during processOne
// does not abort the pass and the notifier was actually invoked.
func Test_wlmNotifyFailureIsSwallowed(t *testing.T) {
	s := newTestStore(t)
	s.Create(1, "q", "", 1, "topic")
	searcher := &fakeSearcher{results: []jackett.Result{
		{InfoHash: "h1", Title: "M", MagnetURI: "magnet:h1", Seeders: 9, Size: 4096},
	}}
	notifier := &wlmErrNotifier{}
	w := NewWorker(s, searcher, notifier, "topic", 15*time.Minute)
	w.RunOnce()
	if atomic.LoadInt32(&notifier.called) != 1 {
		t.Fatalf("wlm: expected notifier called once, got %d", notifier.called)
	}
}

// TestWlmNtfyPosterSuccess posts against an httptest server and asserts the
// request shape (path, Title/Tags/Actions headers, body).
func Test_wlmNtfyPosterSuccess(t *testing.T) {
	var wlmPath, wlmTitle, wlmTags, wlmActions, wlmBody string
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		wlmPath = r.URL.Path
		wlmTitle = r.Header.Get("Title")
		wlmTags = r.Header.Get("Tags")
		wlmActions = r.Header.Get("Actions")
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		wlmBody = string(buf)
		rw.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// trailing slash on BaseURL exercises the TrimRight path.
	n := &NtfyPoster{BaseURL: srv.URL + "/", Client: srv.Client()}
	if err := n.Notify(context.Background(), "mytopic", "A Title", "the body", "magnet:xyz"); err != nil {
		t.Fatalf("wlm: Notify success: %v", err)
	}
	if wlmPath != "/mytopic" {
		t.Fatalf("wlm: path = %q, want /mytopic", wlmPath)
	}
	if wlmTitle != "A Title" {
		t.Fatalf("wlm: Title = %q", wlmTitle)
	}
	if wlmTags != "jackui,torrent" {
		t.Fatalf("wlm: Tags = %q", wlmTags)
	}
	if wlmActions == "" {
		t.Fatalf("wlm: expected Actions header with magnet")
	}
	if wlmBody != "the body" {
		t.Fatalf("wlm: body = %q", wlmBody)
	}
}

// TestWlmNtfyPosterNoMagnet covers the branch where no Actions header is set.
func Test_wlmNtfyPosterNoMagnet(t *testing.T) {
	var wlmActions string
	wlmSeen := false
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		wlmSeen = true
		wlmActions = r.Header.Get("Actions")
		rw.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	n := &NtfyPoster{BaseURL: srv.URL, Client: srv.Client()}
	if err := n.Notify(context.Background(), "t", "title", "body", ""); err != nil {
		t.Fatalf("wlm: Notify no-magnet: %v", err)
	}
	if !wlmSeen {
		t.Fatal("wlm: server never received request")
	}
	if wlmActions != "" {
		t.Fatalf("wlm: expected no Actions header, got %q", wlmActions)
	}
}

// TestWlmNtfyPosterBadStatus covers the resp.StatusCode >= 300 error branch.
func Test_wlmNtfyPosterBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := &NtfyPoster{BaseURL: srv.URL, Client: srv.Client()}
	err := n.Notify(context.Background(), "t", "title", "body", "magnet:abc")
	if err == nil {
		t.Fatal("wlm: expected error on 5xx status")
	}
}

// TestWlmNtfyPosterRequestError forces http.NewRequestWithContext to fail by
// passing a nil context, hitting the request-construction error branch.
func Test_wlmNtfyPosterRequestError(t *testing.T) {
	n := &NtfyPoster{BaseURL: "https://example.invalid"}
	//nolint:staticcheck // intentionally nil ctx to trigger NewRequestWithContext error
	err := n.Notify(nil, "topic", "title", "body", "")
	if err == nil {
		t.Fatal("wlm: expected error from nil context")
	}
}

// TestWlmNtfyPosterDoError covers the client.Do failure branch and the default
// BaseURL/Client fallbacks by pointing at an unroutable address with a context
// already cancelled so no real network call escapes the test.
func Test_wlmNtfyPosterDoError(t *testing.T) {
	// Default Client (n.Client nil) and explicit unreachable BaseURL.
	n := &NtfyPoster{BaseURL: "http://127.0.0.1:0"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Do() fails immediately on the cancelled context
	err := n.Notify(ctx, "topic", "title", "body", "magnet:abc")
	if err == nil {
		t.Fatal("wlm: expected client.Do error")
	}
}

// TestWlmNewBadPath exercises New's migrate-failure cleanup path: a directory
// path can't be opened as a SQLite file, so migrate() errors and New returns.
func Test_wlmNewBadPath(t *testing.T) {
	dir := t.TempDir() // a directory is not a valid sqlite file target
	if _, err := New(dir); err == nil {
		t.Fatal("wlm: expected error opening a directory as a DB")
	}
}
