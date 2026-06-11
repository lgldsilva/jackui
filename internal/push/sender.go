package push

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// Sender fans a notification out to the user's in-app feed and every Web Push
// subscription they registered. Failures are best-effort: a dead endpoint
// (404/410 from the push service) is dropped, anything else is just logged.
type Sender struct {
	store      *Store
	publicKey  string
	privateKey string
	subscriber string // VAPID `sub` claim — contact for the push service
	ttl        int    // seconds the push service may retain an undelivered message
}

// NewSender loads (or generates) the VAPID pair and returns a ready Sender.
func NewSender(store *Store) (*Sender, error) {
	pub, priv, err := store.LoadOrCreateVAPID()
	if err != nil {
		return nil, err
	}
	return &Sender{
		store:      store,
		publicKey:  pub,
		privateKey: priv,
		subscriber: "mailto:admin@jackui.local",
		ttl:        24 * 3600,
	}, nil
}

// PublicKey exposes the VAPID public key for PushManager.subscribe.
func (s *Sender) PublicKey() string { return s.publicKey }

// payload is what sw.js receives in the push event.
type payload struct {
	Title  string `json:"title"`
	Body   string `json:"body"`
	Magnet string `json:"magnet,omitempty"`
	URL    string `json:"url"`
}

// NotifyUser records the notification in the in-app feed and pushes it to all
// of the user's browser subscriptions. The feed write is the source of truth —
// push delivery is opportunistic.
func (s *Sender) NotifyUser(ctx context.Context, userID int, title, body, magnet string) error {
	if err := s.store.AddNotification(userID, title, body, magnet); err != nil {
		return err
	}
	subs, err := s.store.SubscriptionsFor(userID)
	if err != nil || len(subs) == 0 {
		return err
	}
	msg, err := json.Marshal(payload{Title: title, Body: body, Magnet: magnet, URL: "/watchlist"})
	if err != nil {
		return err
	}
	for _, sub := range subs {
		s.sendOne(ctx, sub, msg)
	}
	return nil
}

func (s *Sender) sendOne(ctx context.Context, sub Subscription, msg []byte) {
	resp, err := webpush.SendNotificationWithContext(ctx, msg, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
	}, &webpush.Options{
		TTL:             s.ttl,
		Subscriber:      s.subscriber,
		VAPIDPublicKey:  s.publicKey,
		VAPIDPrivateKey: s.privateKey,
		HTTPClient:      &http.Client{Timeout: 10 * time.Second},
	})
	if err != nil {
		log.Printf("push: send to %.40s... failed: %v", sub.Endpoint, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	// The push service says this endpoint no longer exists — stop trying.
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		_ = s.store.DeleteEndpoint(sub.Endpoint)
		return
	}
	if resp.StatusCode >= 300 {
		log.Printf("push: service returned %d for %.40s...", resp.StatusCode, sub.Endpoint)
	}
}
