// Package push persists Web Push subscriptions + the in-app notification feed
// and delivers notifications to both. The VAPID key pair is generated once and
// stored alongside the subscriptions, so reinstalling the container keeps the
// browser subscriptions valid as long as the state dir survives.
package push

import (
	"database/sql"
	"errors"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/lgldsilva/jackui/internal/dbutil"
)

// Subscription is one browser push endpoint owned by a user.
type Subscription struct {
	UserID   int
	Endpoint string
	P256dh   string
	Auth     string
}

// Notification is one entry of the in-app feed (the bell).
type Notification struct {
	ID        int       `json:"id"`
	UserID    int       `json:"userId"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Magnet    string    `json:"magnet,omitempty"`
	Read      bool      `json:"read"`
	CreatedAt time.Time `json:"createdAt"`
}

type Store struct {
	db *dbutil.DB
}

// New wires the push store onto the shared Postgres pool. Schema is applied
// centrally (internal/db migrations).
func New(pool *sql.DB) (*Store, error) {
	return &Store{db: dbutil.Wrap(pool)}, nil
}

// Close is a no-op: the shared pool's lifecycle is owned by main.
func (s *Store) Close() {
	// No-op: shared Postgres pool lifecycle is owned by main (S1186).
}

// LoadOrCreateVAPID returns the persisted VAPID pair, generating it on first
// boot. Rotating the pair would invalidate every browser subscription, so it
// is generated exactly once.
func (s *Store) LoadOrCreateVAPID() (publicKey, privateKey string, err error) {
	row := s.db.QueryRow(`SELECT public_key, private_key FROM vapid_keys WHERE id=1`)
	switch err = row.Scan(&publicKey, &privateKey); {
	case err == nil:
		return publicKey, privateKey, nil
	case errors.Is(err, sql.ErrNoRows):
		privateKey, publicKey, err = webpush.GenerateVAPIDKeys()
		if err != nil {
			return "", "", err
		}
		_, err = s.db.Exec(`INSERT INTO vapid_keys(id, public_key, private_key) VALUES(1, ?, ?)`, publicKey, privateKey)
		return publicKey, privateKey, err
	default:
		return "", "", err
	}
}

// Subscribe upserts a browser endpoint for the user. An endpoint that changes
// hands (browser profile re-login as another user) is re-owned, not duplicated.
func (s *Store) Subscribe(userID int, endpoint, p256dh, auth string) error {
	if endpoint == "" || p256dh == "" || auth == "" {
		return errors.New("endpoint, p256dh and auth are required")
	}
	_, err := s.db.Exec(`
		INSERT INTO push_subscriptions(user_id, endpoint, p256dh, auth) VALUES(?, ?, ?, ?)
		ON CONFLICT(endpoint) DO UPDATE SET user_id=excluded.user_id, p256dh=excluded.p256dh, auth=excluded.auth
	`, userID, endpoint, p256dh, auth)
	return err
}

// Unsubscribe removes the endpoint when owned by the user.
func (s *Store) Unsubscribe(userID int, endpoint string) error {
	_, err := s.db.Exec(`DELETE FROM push_subscriptions WHERE user_id=? AND endpoint=?`, userID, endpoint)
	return err
}

// DeleteEndpoint drops an endpoint regardless of owner — used when the push
// service reports it gone (404/410).
func (s *Store) DeleteEndpoint(endpoint string) error {
	_, err := s.db.Exec(`DELETE FROM push_subscriptions WHERE endpoint=?`, endpoint)
	return err
}

// SubscriptionsFor lists the user's registered endpoints.
func (s *Store) SubscriptionsFor(userID int) ([]Subscription, error) {
	rows, err := s.db.Query(`SELECT user_id, endpoint, p256dh, auth FROM push_subscriptions WHERE user_id=?`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Subscription{}
	for rows.Next() {
		sub := Subscription{}
		if err := rows.Scan(&sub.UserID, &sub.Endpoint, &sub.P256dh, &sub.Auth); err != nil {
			continue
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

const maxFeedPerUser = 200

// AddNotification appends to the in-app feed, trimming the oldest entries past
// maxFeedPerUser so the table can't grow unbounded.
func (s *Store) AddNotification(userID int, title, body, magnet string) error {
	if _, err := s.db.Exec(`INSERT INTO notifications(user_id, title, body, magnet) VALUES(?, ?, ?, ?)`,
		userID, title, body, magnet); err != nil {
		return err
	}
	_, err := s.db.Exec(`
		DELETE FROM notifications WHERE user_id=? AND id NOT IN (
			SELECT id FROM notifications WHERE user_id=? ORDER BY id DESC LIMIT ?
		)`, userID, userID, maxFeedPerUser)
	return err
}

// Notifications returns the newest entries of the user's feed.
func (s *Store) Notifications(userID, limit int) ([]Notification, error) {
	if limit <= 0 || limit > maxFeedPerUser {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT id, user_id, title, body, magnet, read, created_at
		FROM notifications WHERE user_id=? ORDER BY id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Notification{}
	for rows.Next() {
		n := Notification{}
		var read int
		if err := rows.Scan(&n.ID, &n.UserID, &n.Title, &n.Body, &n.Magnet, &read, &n.CreatedAt); err != nil {
			continue
		}
		n.Read = read != 0
		out = append(out, n)
	}
	return out, rows.Err()
}

// UnreadCount returns how many feed entries the user hasn't read.
func (s *Store) UnreadCount(userID int) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE user_id=? AND read=0`, userID).Scan(&n)
	return n, err
}

// MarkAllRead flags the whole feed as read.
func (s *Store) MarkAllRead(userID int) error {
	_, err := s.db.Exec(`UPDATE notifications SET read=1 WHERE user_id=? AND read=0`, userID)
	return err
}
