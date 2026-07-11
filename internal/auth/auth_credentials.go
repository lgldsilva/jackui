package auth

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

// TokenInfo is the resolved payload of a consumed single-use token.
type TokenInfo struct {
	UserID  int    // 0 when the token isn't tied to a user (invites)
	Email   string // optional pre-set email (invites)
	Purpose string
}

// CreateToken issues a single-use token for a purpose (invite/verify/reset) and
// returns the PLAINTEXT (only its SHA-256 is stored). userID 0 → NULL row.
func (s *Store) CreateToken(purpose string, userID int, email string, ttl time.Duration) (string, error) {
	plain, err := randomToken(32)
	if err != nil {
		return "", err
	}
	var uid any
	if userID > 0 {
		uid = userID
	}
	_, err = s.db.Exec(
		`INSERT INTO auth_tokens(token_hash, user_id, purpose, email, expires_at) VALUES(?, ?, ?, ?, ?)`,
		sha256Hex(plain), uid, purpose, email, time.Now().Add(ttl).UTC(),
	)
	if err != nil {
		return "", err
	}
	return plain, nil
}

// ConsumeToken validates a token for the given purpose (exists, right purpose,
// not used, not expired) and marks it used (single-use). Returns its payload.
func (s *Store) ConsumeToken(plain, purpose string) (*TokenInfo, error) {
	hash := sha256Hex(plain)
	var uid sql.NullInt64
	var email string
	var exp time.Time
	var used sql.NullTime
	err := s.db.QueryRow(
		`SELECT user_id, email, expires_at, used_at FROM auth_tokens WHERE token_hash = ? AND purpose = ?`,
		hash, purpose,
	).Scan(&uid, &email, &exp, &used)
	if err == sql.ErrNoRows {
		return nil, errors.New("token inválido")
	}
	if err != nil {
		return nil, err
	}
	if used.Valid {
		return nil, errors.New("token já utilizado")
	}
	if time.Now().After(exp) {
		return nil, errors.New("token expirado")
	}
	if _, err := s.db.Exec(`UPDATE auth_tokens SET used_at = CURRENT_TIMESTAMP WHERE token_hash = ?`, hash); err != nil {
		return nil, err
	}
	ti := &TokenInfo{Email: email, Purpose: purpose}
	if uid.Valid {
		ti.UserID = int(uid.Int64)
	}
	return ti, nil
}

// ─── WebAuthn credentials ───────────────────────────────────────────────────

func credKey(id []byte) string { return base64.RawURLEncoding.EncodeToString(id) }

// AddCredential persists a newly-registered passkey for a user.
func (s *Store) AddCredential(userID int, cred *webauthn.Credential) error {
	blob, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO webauthn_credentials(cred_id, user_id, data) VALUES(?, ?, ?)
		 ON CONFLICT(cred_id) DO UPDATE SET user_id = excluded.user_id, data = excluded.data`,
		credKey(cred.ID), userID, string(blob),
	)
	return err
}

// Credentials returns all passkeys registered by a user (empty slice if none).
func (s *Store) Credentials(userID int) ([]webauthn.Credential, error) {
	rows, err := s.db.Query("SELECT data FROM webauthn_credentials WHERE user_id = ?", userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []webauthn.Credential{}
	for rows.Next() {
		var blob string
		if err := rows.Scan(&blob); err != nil {
			continue
		}
		var c webauthn.Credential
		if json.Unmarshal([]byte(blob), &c) == nil {
			out = append(out, c)
		}
	}
	return out, rows.Err()
}

// UpdateCredential rewrites a credential after a successful login (the sign
// counter advances and must be persisted to detect cloned authenticators).
func (s *Store) UpdateCredential(cred *webauthn.Credential) error {
	blob, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("UPDATE webauthn_credentials SET data = ? WHERE cred_id = ?", string(blob), credKey(cred.ID))
	return err
}

// HasPasskey reports whether a user has at least one registered passkey.
func (s *Store) HasPasskey(userID int) bool {
	var n int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?", userID).Scan(&n)
	return n > 0
}

// DeleteCredential removes one passkey (by base64url id) owned by a user.
func (s *Store) DeleteCredential(userID int, credIDB64 string) error {
	_, err := s.db.Exec("DELETE FROM webauthn_credentials WHERE cred_id = ? AND user_id = ?", credIDB64, userID)
	return err
}
