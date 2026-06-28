package push

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/lgldsilva/jackui/internal/dbtest"
)

// browserKeys fabricates a valid browser-side subscription key pair: p256dh is
// an uncompressed P-256 public key and auth a 16-byte secret — webpush-go
// encrypts against them before any HTTP happens, so they must be real.
func browserKeys(t *testing.T) (p256dh, auth string) {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	authBytes := make([]byte, 16)
	if _, err := rand.Read(authBytes); err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(priv.PublicKey().Bytes()), enc.EncodeToString(authBytes)
}

func newTestSender(t *testing.T) (*Sender, *Store) {
	t.Helper()
	s := newTestStore(t)
	sender, err := NewSender(s)
	if err != nil {
		t.Fatal(err)
	}
	return sender, s
}

func TestNotifyUser_WritesFeedWithoutSubscriptions(t *testing.T) {
	sender, store := newTestSender(t)
	if sender.PublicKey() == "" {
		t.Fatal("expected a VAPID public key")
	}
	if err := sender.NotifyUser(context.Background(), 1, "Title", "body", "magnet:x"); err != nil {
		t.Fatal(err)
	}
	items, _ := store.Notifications(1, 10)
	if len(items) != 1 || items[0].Title != "Title" || items[0].Magnet != "magnet:x" {
		t.Fatalf("feed mismatch: %+v", items)
	}
}

func TestNotifyUser_DeliversToEndpointAndDropsGone(t *testing.T) {
	sender, store := newTestSender(t)
	var delivered atomic.Int32
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		delivered.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer okSrv.Close()
	goneSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer goneSrv.Close()

	p256dh, auth := browserKeys(t)
	if err := store.Subscribe(1, okSrv.URL, p256dh, auth); err != nil {
		t.Fatal(err)
	}
	if err := store.Subscribe(1, goneSrv.URL, p256dh, auth); err != nil {
		t.Fatal(err)
	}

	if err := sender.NotifyUser(context.Background(), 1, "Hit", "5 seeders", "magnet:y"); err != nil {
		t.Fatal(err)
	}
	if delivered.Load() != 1 {
		t.Fatalf("expected 1 delivery to the live endpoint, got %d", delivered.Load())
	}
	subs, _ := store.SubscriptionsFor(1)
	if len(subs) != 1 || subs[0].Endpoint != okSrv.URL {
		t.Fatalf("gone endpoint should be dropped, kept %+v", subs)
	}
}

func TestNewSender_ErrorOnClosedStore(t *testing.T) {
	pool := dbtest.NewDB(t)
	s, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	pool.Close() // Store.Close is a no-op; close the pool to force the error
	if _, err := NewSender(s); err == nil {
		t.Fatal("NewSender on closed store should error")
	}
}

func TestSendOne_TransportErrorAndServerError(t *testing.T) {
	sender, store := newTestSender(t)
	p256dh, auth := browserKeys(t)
	// Unreachable endpoint → transport error path (just logs).
	_ = store.Subscribe(1, "http://127.0.0.1:1/unreachable", p256dh, auth)
	// 500 from the push service → logged, subscription kept.
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer errSrv.Close()
	_ = store.Subscribe(1, errSrv.URL, p256dh, auth)

	if err := sender.NotifyUser(context.Background(), 1, "T", "b", ""); err != nil {
		t.Fatal(err)
	}
	subs, _ := store.SubscriptionsFor(1)
	if len(subs) != 2 {
		t.Fatalf("non-410 failures must keep subscriptions, got %d", len(subs))
	}
}
