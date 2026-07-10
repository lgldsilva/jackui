// Package auth implements user authentication with JWT access tokens + opaque refresh tokens.
//
// Design:
//   - Access token: stateless JWT, short TTL (15min). Validated cryptographically per-request.
//   - Refresh token: opaque random string, long TTL (1 day normal, 30 days "remember me"),
//     stored hashed in DB so we can revoke (logout = delete row).
//   - Login returns both. Frontend hits /refresh when access expires to get a new pair (rolling refresh).
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

// ─── Refresh tokens ────────────────────────────────────────────────────────

// CreateRefreshToken generates a fresh random token, stores its hash, returns the plain string.
// `remember` controls TTL behavior on refresh: when true, every successful refresh re-extends
// the expiration by 30 days from now (sliding window — only logs out after 30d of inactivity).
// userAgent/ip identify the creating device so sessions are recognizable in the UI.
func (s *Store) CreateRefreshToken(userID int, ttl time.Duration, remember bool, userAgent, ip string) (string, error) {
	plain, err := randomToken(32)
	if err != nil {
		return "", err
	}
	hash := sha256Hex(plain)
	rem := 0
	if remember {
		rem = 1
	}
	_, err = s.db.Exec(
		"INSERT INTO refresh_tokens(token_hash, user_id, expires_at, remember_me, user_agent, ip) VALUES(?, ?, ?, ?, ?, ?)",
		hash, userID, time.Now().Add(ttl).UTC(), rem, userAgent, ip,
	)
	if err != nil {
		return "", err
	}
	return plain, nil
}

// ValidateRefreshToken looks up a token, checks expiry, returns the owning user
// plus the `remember` flag that the session was created with.
func (s *Store) ValidateRefreshToken(plain string) (*User, bool, error) {
	hash := sha256Hex(plain)
	var userID int
	var exp time.Time
	var remember int
	err := s.db.QueryRow(
		"SELECT user_id, expires_at, remember_me FROM refresh_tokens WHERE token_hash = ?",
		hash,
	).Scan(&userID, &exp, &remember)
	if err == sql.ErrNoRows {
		return nil, false, errors.New("refresh token inválido")
	}
	if err != nil {
		return nil, false, err
	}
	if time.Now().UTC().After(exp) {
		return nil, false, errors.New("refresh token expirado")
	}
	u, err := s.GetUserByID(userID)
	return u, remember == 1, err
}

// RefreshOutcome is the decision of a rotation attempt (RotateRefreshToken).
type RefreshOutcome int

const (
	RefreshInvalid      RefreshOutcome = iota // unknown or expired token
	RefreshRotated                            // we won the race; the token was consumed now
	RefreshGraceReissue                       // recently consumed by a concurrent refresh — reissue, don't revoke
	RefreshReuse                              // consumed long ago and presented again — treat as theft
)

// RotateRefreshToken atomically decides what to do with a presented refresh
// token, replacing the validate-then-consume sequence in the handler:
//
//   - Invalid: unknown/expired token → reject (no revoke).
//   - Rotated: the token was active and THIS call consumed it → issue a fresh pair.
//   - GraceReissue: the token was consumed within `grace` (a concurrent refresh
//     from another tab, or the request burst when the backend returns from a
//     deploy) → issue a fresh pair WITHOUT revoking. This is what stops the
//     re-login-after-deploy: the loser of a concurrent rotation no longer nukes
//     the whole session family.
//   - Reuse: the token was consumed BEFORE the grace window → a real replay of a
//     rotated (possibly stolen) token → caller revokes all sessions.
//
// Returns the owning user + remember flag for the issue-tokens outcomes.
func (s *Store) RotateRefreshToken(plain string, grace time.Duration) (*User, bool, RefreshOutcome, error) {
	hash := sha256Hex(plain)
	var userID, remember int
	var exp time.Time
	var consumed sql.NullTime
	err := s.db.QueryRow(
		"SELECT user_id, expires_at, remember_me, consumed_at FROM refresh_tokens WHERE token_hash = ?",
		hash,
	).Scan(&userID, &exp, &remember, &consumed)
	if err == sql.ErrNoRows {
		return nil, false, RefreshInvalid, nil
	}
	if err != nil {
		return nil, false, RefreshInvalid, err
	}
	if time.Now().UTC().After(exp) {
		return nil, false, RefreshInvalid, nil
	}
	u, err := s.GetUserByID(userID)
	if err != nil {
		return nil, false, RefreshInvalid, err
	}
	if consumed.Valid {
		if time.Since(consumed.Time) <= grace {
			return u, remember == 1, RefreshGraceReissue, nil
		}
		return u, remember == 1, RefreshReuse, nil
	}
	// Active token: try to consume it. We may lose to a concurrent refresh that
	// flipped consumed_at first — that's a benign grace reissue, not reuse.
	res, err := s.db.Exec(
		"UPDATE refresh_tokens SET consumed_at = ? WHERE token_hash = ? AND consumed_at IS NULL",
		time.Now().UTC(), hash,
	)
	if err != nil {
		return nil, false, RefreshInvalid, err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return u, remember == 1, RefreshRotated, nil
	}
	return u, remember == 1, RefreshGraceReissue, nil
}

// ConsumeRefreshToken deletes a refresh token (use on logout, or rolling rotation).
func (s *Store) ConsumeRefreshToken(plain string) error {
	hash := sha256Hex(plain)
	_, err := s.db.Exec("DELETE FROM refresh_tokens WHERE token_hash = ?", hash)
	return err
}

// ConsumeRefreshTokenOnce atomically deletes a refresh token and reports whether
// THIS call was the one that removed it (RowsAffected == 1). Used by rotation to
// close the validate-then-delete TOCTOU: with two concurrent refreshes of the
// same token, only one gets `true` and may issue a new pair; the loser gets
// `false` (the token was already consumed) and must be rejected.
func (s *Store) ConsumeRefreshTokenOnce(plain string) (bool, error) {
	hash := sha256Hex(plain)
	res, err := s.db.Exec("DELETE FROM refresh_tokens WHERE token_hash = ?", hash)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// CleanupExpired removes refresh tokens past their TTL, plus soft-consumed tokens
// older than an hour (well past the rotation grace window) so they don't linger
// until their original TTL. Call periodically.
func (s *Store) CleanupExpired() error {
	now := time.Now().UTC()
	_, err := s.db.Exec(
		"DELETE FROM refresh_tokens WHERE expires_at < ? OR (consumed_at IS NOT NULL AND consumed_at < ?)",
		now, now.Add(-time.Hour),
	)
	return err
}

// ─── Session management ──────────────────────────────────────────────────────

// SessionInfo is one active refresh-token session, safe to show its owner. ID is
// the token_hash — exposing the HASH to the authenticated owner is harmless (it
// can't be used to authenticate, only to revoke that same session).
type SessionInfo struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	Remember  bool      `json:"remember"`
	Current   bool      `json:"current"`
	UserAgent string    `json:"userAgent"`
	IP        string    `json:"ip"`
}

// ListSessions returns a user's active sessions, newest first. currentPlain (the
// caller's own refresh token, may be empty) flags which row is "this device".
func (s *Store) ListSessions(userID int, currentPlain string) ([]SessionInfo, error) {
	rows, err := s.db.Query(
		"SELECT token_hash, created_at, expires_at, remember_me, user_agent, ip FROM refresh_tokens WHERE user_id = ? AND consumed_at IS NULL ORDER BY created_at DESC",
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	curHash := ""
	if currentPlain != "" {
		curHash = sha256Hex(currentPlain)
	}
	out := []SessionInfo{}
	for rows.Next() {
		var hash, ua, ip string
		var created, expires time.Time
		var remember int
		if err := rows.Scan(&hash, &created, &expires, &remember, &ua, &ip); err != nil {
			continue
		}
		out = append(out, SessionInfo{
			ID: hash, Remember: remember == 1, Current: hash == curHash,
			UserAgent: ua, IP: ip, CreatedAt: created, ExpiresAt: expires,
		})
	}
	return out, rows.Err()
}

// RevokeSession deletes one session by its id (token_hash), scoped to the owner
// so a user can't revoke another account's session.
func (s *Store) RevokeSession(userID int, id string) error {
	_, err := s.db.Exec("DELETE FROM refresh_tokens WHERE token_hash = ? AND user_id = ?", id, userID)
	return err
}

// RevokeOtherSessions deletes every session for a user EXCEPT the caller's own
// (identified by currentPlain). Returns how many were dropped.
func (s *Store) RevokeOtherSessions(userID int, currentPlain string) (int, error) {
	res, err := s.db.Exec(
		"DELETE FROM refresh_tokens WHERE user_id = ? AND token_hash != ?",
		userID, sha256Hex(currentPlain),
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// RevokeAllSessions deletes every session for a user (used when an admin disables
// the account so existing logins can't keep refreshing).
func (s *Store) RevokeAllSessions(userID int) error {
	_, err := s.db.Exec("DELETE FROM refresh_tokens WHERE user_id = ?", userID)
	return err
}

// ─── helpers ───────────────────────────────────────────────────────────────

func randomToken(byteLen int) (string, error) {
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b), nil
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
