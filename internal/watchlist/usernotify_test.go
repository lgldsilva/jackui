package watchlist

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/jackett"
)

type recorderUserNotifier struct {
	calls []userNotifyCall
}

type userNotifyCall struct {
	userID              int
	title, body, magnet string
}

func (r *recorderUserNotifier) NotifyUser(ctx context.Context, userID int, title, body, magnet string) error {
	r.calls = append(r.calls, userNotifyCall{userID, title, body, magnet})
	return nil
}

func TestWorker_UserNotifierReceivesHits(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, 9, params("show", "", 1, "")) // no ntfy topic — user channel still fires
	primeChecked(t, s, mustFirstID(t, s, 9))       // past the silent baseline: this pass must notify
	searcher := &fakeSearcher{results: []jackett.Result{
		{InfoHash: "aaa", Title: "Show.S01E01.1080p", MagnetURI: "magnet:aaa", Seeders: 4, Size: 100},
	}}
	w := NewWorker(s, searcher, nil, "", 15*time.Minute)
	un := &recorderUserNotifier{}
	w.SetUserNotifier(un)
	w.RunOnce()
	if len(un.calls) != 1 {
		t.Fatalf("expected 1 user notification, got %d", len(un.calls))
	}
	c := un.calls[0]
	if c.userID != 9 || c.title != "Show.S01E01.1080p" || c.magnet != "magnet:aaa" {
		t.Fatalf("call mismatch: %+v", c)
	}
	if !strings.Contains(c.body, "4 seeders") {
		t.Fatalf("body should carry seeders, got %q", c.body)
	}
	// Second pass with no new results → no duplicate notification.
	w.RunOnce()
	if len(un.calls) != 1 {
		t.Fatalf("seen hits must not re-notify, got %d", len(un.calls))
	}
}
