package push

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
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
	// endpointGuard re-checks every endpoint at send time (defence in depth
	// against subscriptions persisted before validation existed). nil disables
	// the check — only tests delivering to loopback httptest doubles set that.
	endpointGuard func(string) error
}

// NewSender loads (or generates) the VAPID pair and returns a ready Sender.
func NewSender(store *Store) (*Sender, error) {
	pub, priv, err := store.LoadOrCreateVAPID()
	if err != nil {
		return nil, err
	}
	return &Sender{
		store:         store,
		publicKey:     pub,
		privateKey:    priv,
		subscriber:    "mailto:admin@jackui.local",
		ttl:           24 * 3600,
		endpointGuard: ValidateEndpoint,
	}, nil
}

// PublicKey exposes the VAPID public key for PushManager.subscribe.
func (s *Sender) PublicKey() string { return s.publicKey }

// isBlockedPushIP mirrors isBlockedFetchIP (internal/streamer/ssrf.go — unexported
// there, and pushing must not import the streamer): loopback, private
// RFC1918/ULA, link-local and unspecified addresses are off-limits for
// server-side POSTs.
func isBlockedPushIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// resolveEndpointHost is the DNS hook ValidateEndpoint uses; tests stub it so
// they never touch the network.
var resolveEndpointHost = func(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

// ValidateEndpoint gates Web Push subscription endpoints. The server later
// POSTs to them with the VAPID Authorization header attached, so an
// attacker-chosen endpoint is an authenticated SSRF probe of the homelab /
// cloud-metadata network. Require https:// and a resolvable, public host.
// Residual note: between this resolve-and-check and webpush's own dial a DNS
// rebinding window remains; the send-time re-check narrows it (fully closing it
// would mean dialing the validated IPs, as streamer's ssrfDialContext does).
func ValidateEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return errors.New("push endpoint must be an https:// URL")
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedPushIP(ip) {
			return fmt.Errorf("push endpoint host %s is not a public address", host)
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := resolveEndpointHost(ctx, host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("push endpoint host %q does not resolve", host)
	}
	for _, ia := range ips {
		if isBlockedPushIP(ia.IP) {
			return fmt.Errorf("push endpoint host %s is not a public address", host)
		}
	}
	return nil
}

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
	// Send-time re-check: a subscription stored before subscribe-time
	// validation existed (or re-owned across users) must not turn into a
	// VAPID-signed POST at an internal address. Skip + log, keep the row —
	// only a push service 404/410 proves the endpoint dead.
	if s.endpointGuard != nil {
		if err := s.endpointGuard(sub.Endpoint); err != nil {
			log.Printf("push: skipping non-public endpoint %.40s...: %v", sub.Endpoint, err)
			return
		}
	}
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
