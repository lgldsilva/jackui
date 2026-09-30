package auth

import (
	"crypto/rand"
	"strings"
	"sync"
)

// TOTPReplayCache is a process-local memory of the newest TOTP step each user
// has already spent. A code that matched at step N is burnt: N and anything
// older can never validate again, so an observed code can't be replayed within
// its ±1 window. Deliberately in-memory only: restarts clear it and each
// process enforces its own (a multi-instance deployment would need a shared
// store — this single-binary app has exactly one).
type TOTPReplayCache struct {
	mu   sync.Mutex
	last map[int]uint64
}

// NewTOTPReplayCache returns an empty replay cache.
func NewTOTPReplayCache() *TOTPReplayCache {
	return &TOTPReplayCache{last: make(map[int]uint64)}
}

// Allow reports whether a code for userID that matched at `step` may be
// accepted, recording it when so. A step equal to or older than the user's
// last accepted step is a replay → false.
func (c *TOTPReplayCache) Allow(userID int, step uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == nil {
		c.last = make(map[int]uint64)
	}
	if last, ok := c.last[userID]; ok && step <= last {
		return false
	}
	c.last[userID] = step
	return true
}

// ValidateTOTPForUser validates a login TOTP and enforces single use per step:
// a code that just logged the user in cannot do so again until the next 30s
// window. Zero-value Stores (built without New — tests only) skip the replay
// memory.
func (s *Store) ValidateTOTPForUser(userID int, secret, code string) bool {
	step, ok := validateTOTPStep(secret, code)
	if !ok {
		return false
	}
	if s.totpReplay == nil {
		return true
	}
	return s.totpReplay.Allow(userID, step)
}

// ─── MFA backup codes ───────────────────────────────────────────────────────

const (
	errHashPassword = "hash password: %w"
)

// backupCodeAlphabet excludes visually ambiguous characters (0/o, 1/l/i).
const backupCodeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// normalizeBackupCode lowercases and strips separators so "ABCD-EFGH" and
// "abcdefgh" hash identically — users mistype dashes/case.
func normalizeBackupCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			// The filter above leaves only ASCII, so WriteRune emits exactly
			// the same single byte a byte(r) append would — with no conversion.
			b.WriteRune(r)
		}
	}
	return b.String()
}

// SetTOTPSecret stores a (not-yet-enabled) TOTP secret during enrollment.
func (s *Store) SetTOTPSecret(userID int, secret string) error {
	_, err := s.db.Exec("UPDATE users SET totp_secret = ?, totp_enabled = 0 WHERE id = ?", secret, userID)
	return err
}

// GetTOTPSecret returns the stored secret + whether MFA is enabled.
func (s *Store) GetTOTPSecret(userID int) (secret string, enabled bool, err error) {
	var en int
	err = s.db.QueryRow("SELECT totp_secret, totp_enabled FROM users WHERE id = ?", userID).Scan(&secret, &en)
	return secret, en == 1, err
}

// EnableTOTP marks MFA active (after the user confirms a code during enrollment).
func (s *Store) EnableTOTP(userID int) error {
	_, err := s.db.Exec("UPDATE users SET totp_enabled = 1 WHERE id = ?", userID)
	return err
}

// DisableTOTP clears the secret + disables MFA, and drops any backup codes
// (they're meaningless once MFA is off).
func (s *Store) DisableTOTP(userID int) error {
	if _, err := s.db.Exec("UPDATE users SET totp_secret = '', totp_enabled = 0 WHERE id = ?", userID); err != nil {
		return err
	}
	_, err := s.db.Exec("DELETE FROM mfa_backup_codes WHERE user_id = ?", userID)
	return err
}

// GenerateBackupCodes replaces a user's backup codes with n fresh ones and
// returns the PLAINTEXT (formatted "xxxx-xxxx") — shown once, never recoverable.
func (s *Store) GenerateBackupCodes(userID, n int) ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec("DELETE FROM mfa_backup_codes WHERE user_id = ?", userID); err != nil {
		return nil, err
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		raw, err := randomFromAlphabet(8, backupCodeAlphabet)
		if err != nil {
			return nil, err
		}
		formatted := raw[:4] + "-" + raw[4:]
		if _, err := tx.Exec(
			"INSERT INTO mfa_backup_codes(code_hash, user_id) VALUES(?, ?)",
			sha256Hex(normalizeBackupCode(raw)), userID,
		); err != nil {
			return nil, err
		}
		out = append(out, formatted)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// ConsumeBackupCode validates a code for a user and marks it used (single-use).
// Returns true on a successful redemption.
func (s *Store) ConsumeBackupCode(userID int, code string) bool {
	hash := sha256Hex(normalizeBackupCode(code))
	res, err := s.db.Exec(
		"UPDATE mfa_backup_codes SET used_at = CURRENT_TIMESTAMP WHERE code_hash = ? AND user_id = ? AND used_at IS NULL",
		hash, userID,
	)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// CountBackupCodes returns how many unused backup codes a user has left.
func (s *Store) CountBackupCodes(userID int) int {
	var n int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM mfa_backup_codes WHERE user_id = ? AND used_at IS NULL", userID).Scan(&n)
	return n
}

// randomFromAlphabet returns n characters drawn uniformly from alphabet using
// crypto/rand. Rejection sampling keeps the distribution unbiased.
func randomFromAlphabet(n int, alphabet string) (string, error) {
	out := make([]byte, n)
	size := len(alphabet)
	limit := 256 - (256 % size) // largest multiple of size that fits in a byte
	buf := make([]byte, 1)
	for i := 0; i < n; {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		if int(buf[0]) >= limit { // reject the biased tail
			continue
		}
		out[i] = alphabet[int(buf[0])%size]
		i++
	}
	return string(out), nil
}
