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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

// Role identifies a user's authorization level.
type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
	RoleGuest Role = "guest"
)

// Status is the account lifecycle state. Only "active" users may log in.
type Status string

const (
	StatusActive   Status = "active"   // can log in
	StatusPending  Status = "pending"  // self-registered, awaiting admin approval
	StatusDisabled Status = "disabled" // blocked by an admin
)

// Token purposes — single-use, TTL'd links sent by email (or copied by an admin).
const (
	TokenInvite        = "invite"         // authorizes a registration (no user yet)
	TokenVerifyEmail   = "verify_email"   // confirms a user's email address
	TokenResetPassword = "reset_password" // password recovery
)

// User is the public, password-less representation.
type User struct {
	ID            int       `json:"id"`
	Username      string    `json:"username"`
	Email         string    `json:"email"`
	Role          Role      `json:"role"`
	Status        Status    `json:"status"`
	EmailVerified bool      `json:"emailVerified"`
	MfaEnabled    bool      `json:"mfaEnabled"`
	NtfyTopic     string    `json:"ntfyTopic"`
	CreatedAt     time.Time `json:"createdAt"`
}

// Store wraps the SQLite-backed user + refresh token persistence.
type Store struct {
	db *sql.DB
}

// New opens (or creates) the auth DB at path.
func New(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() { _ = s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			username      TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			role          TEXT NOT NULL DEFAULT 'user',
			created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS refresh_tokens (
			token_hash  TEXT PRIMARY KEY,
			user_id     INTEGER NOT NULL,
			expires_at  DATETIME NOT NULL,
			created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			remember_me INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_refresh_user ON refresh_tokens(user_id);
		CREATE INDEX IF NOT EXISTS idx_refresh_exp  ON refresh_tokens(expires_at);
	`)
	if err != nil {
		return err
	}
	// Idempotent ALTER for older DBs that pre-date the remember_me column
	if !s.hasColumn("refresh_tokens", "remember_me") {
		if _, err := s.db.Exec(`ALTER TABLE refresh_tokens ADD COLUMN remember_me INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	// Account-lifecycle columns. Default status 'active' so EXISTING users (incl.
	// the bootstrap admin) keep logging in untouched; new self-registrations are
	// inserted as 'pending' explicitly. email_verified defaults 1 for the same
	// reason — pre-existing accounts aren't retroactively locked out.
	for col, ddl := range map[string]string{
		"email":          `ALTER TABLE users ADD COLUMN email TEXT NOT NULL DEFAULT ''`,
		"status":         `ALTER TABLE users ADD COLUMN status TEXT NOT NULL DEFAULT 'active'`,
		"email_verified": `ALTER TABLE users ADD COLUMN email_verified INTEGER NOT NULL DEFAULT 1`,
		"totp_secret":    `ALTER TABLE users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT ''`,
		"totp_enabled":   `ALTER TABLE users ADD COLUMN totp_enabled INTEGER NOT NULL DEFAULT 0`,
		"ntfy_topic":     `ALTER TABLE users ADD COLUMN ntfy_topic TEXT NOT NULL DEFAULT ''`,
	} {
		if !s.hasColumn("users", col) {
			if _, err := s.db.Exec(ddl); err != nil {
				return err
			}
		}
	}
	// Generic single-use tokens for invite / email-verify / password-reset. Only
	// the SHA-256 of the token is stored. user_id is NULL for invites (no account
	// exists yet); email carries an optional pre-set address for invites.
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS auth_tokens (
			token_hash TEXT PRIMARY KEY,
			user_id    INTEGER,
			purpose    TEXT NOT NULL,
			email      TEXT NOT NULL DEFAULT '',
			expires_at DATETIME NOT NULL,
			used_at    DATETIME,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_authtok_exp ON auth_tokens(expires_at);
	`); err != nil {
		return err
	}
	// WebAuthn (passkey) credentials. cred_id is the base64url credential id
	// (stable PK); data is the full webauthn.Credential JSON (public key, sign
	// count, transports, flags). One user may register several authenticators.
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS webauthn_credentials (
			cred_id    TEXT PRIMARY KEY,
			user_id    INTEGER NOT NULL,
			data       TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_wacred_user ON webauthn_credentials(user_id);
	`); err != nil {
		return err
	}
	// MFA backup codes — single-use recovery codes shown once when TOTP is
	// enabled. Only the SHA-256 of each code is stored; used_at marks redemption.
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS mfa_backup_codes (
			code_hash  TEXT PRIMARY KEY,
			user_id    INTEGER NOT NULL,
			used_at    DATETIME,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_backupcode_user ON mfa_backup_codes(user_id);
	`); err != nil {
		return err
	}
	return nil
}

func (s *Store) hasColumn(table, col string) bool {
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err == nil && n == col {
			return true
		}
	}
	return false
}

// Bootstrap ensures an admin user exists. If no users at all, creates "admin" with the given password.
// Use this once at startup with the password from config/env.
func (s *Store) Bootstrap(adminUser, adminPass string) error {
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	if adminUser == "" {
		adminUser = "admin"
	}
	if adminPass == "" {
		return errors.New("admin password required for bootstrap")
	}
	_, err := s.CreateUser(adminUser, adminPass, RoleAdmin)
	return err
}

// CreateUser hashes the password and inserts a new user. Returns the inserted ID.
func (s *Store) CreateUser(username, password string, role Role) (int, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return 0, fmt.Errorf(errHashPassword, err)
	}
	if role != RoleAdmin && role != RoleUser && role != RoleGuest {
		role = RoleUser
	}
	res, err := s.db.Exec(
		"INSERT INTO users(username, password_hash, role) VALUES(?, ?, ?)",
		username, string(hash), string(role),
	)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return int(id), nil
}

// ChangePassword verifies the current password and sets a new one (self-service).
func (s *Store) ChangePassword(userID int, current, new string) error {
	var hash string
	err := s.db.QueryRow("SELECT password_hash FROM users WHERE id = ?", userID).Scan(&hash)
	if err == sql.ErrNoRows {
		return errors.New("usuário não encontrado")
	}
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(current)) != nil {
		return errors.New("senha atual incorreta")
	}
	return s.SetPassword(userID, new)
}

// SetPassword overwrites a user's password hash (used by ChangePassword + reset).
func (s *Store) SetPassword(userID int, password string) error {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf(errHashPassword, err)
	}
	_, err = s.db.Exec("UPDATE users SET password_hash = ? WHERE id = ?", string(h), userID)
	return err
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

// ─── MFA backup codes ───────────────────────────────────────────────────────

const (
	errHashPassword = "hash password: %w"
	timeFormat      = "2006-01-02 15:04:05"
)

// backupCodeAlphabet excludes visually ambiguous characters (0/o, 1/l/i).
const backupCodeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// normalizeBackupCode lowercases and strips separators so "ABCD-EFGH" and
// "abcdefgh" hash identically — users mistype dashes/case.
func normalizeBackupCode(s string) string {
	var b []byte
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b = append(b, byte(r))
		}
	}
	return string(b)
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

// SetStatus changes an account's lifecycle state (approve/disable/re-enable).
func (s *Store) SetStatus(userID int, status Status) error {
	_, err := s.db.Exec("UPDATE users SET status = ? WHERE id = ?", string(status), userID)
	return err
}

// CreateUserFull creates a user with email + lifecycle status (used by the
// registration flow). Returns the new id. Username uniqueness is enforced by
// the table; email uniqueness is checked by the caller (Register handler).
func (s *Store) CreateUserFull(username, email, password string, role Role, status Status) (int, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return 0, fmt.Errorf(errHashPassword, err)
	}
	if role != RoleAdmin && role != RoleUser && role != RoleGuest {
		role = RoleUser
	}
	res, err := s.db.Exec(
		"INSERT INTO users(username, email, password_hash, role, status, email_verified) VALUES(?, ?, ?, ?, ?, 0)",
		username, email, string(hash), string(role), string(status),
	)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return int(id), nil
}

// SetNtfyTopic updates a user's ntfy.sh notification topic.
func (s *Store) SetNtfyTopic(userID int, topic string) error {
	_, err := s.db.Exec("UPDATE users SET ntfy_topic = ? WHERE id = ?", topic, userID)
	return err
}

// SetEmailVerified flips a user's email_verified flag (after they click the
// confirmation link). Optionally promotes the account to a new status (an
// invited user becomes active on confirmation).
func (s *Store) SetEmailVerified(userID int, promoteTo Status) error {
	if promoteTo != "" {
		_, err := s.db.Exec("UPDATE users SET email_verified = 1, status = ? WHERE id = ?", string(promoteTo), userID)
		return err
	}
	_, err := s.db.Exec("UPDATE users SET email_verified = 1 WHERE id = ?", userID)
	return err
}

// GetUserByEmail returns the (verified-or-not) user with a given email, or nil
// when none. Used by password recovery. Empty email never matches.
func (s *Store) GetUserByEmail(email string) (*User, error) {
	if email == "" {
		return nil, nil
	}
	var u User
	var ts string
	err := s.db.QueryRow(
		"SELECT id, username, role, email, status, email_verified, ntfy_topic, created_at FROM users WHERE email = ? AND email != '' LIMIT 1",
		email,
	).Scan(&u.ID, &u.Username, &u.Role, &u.Email, &u.Status, &u.EmailVerified, &u.NtfyTopic, &ts)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.CreatedAt, _ = parseTime(ts)
	return &u, nil
}

// Exists reports whether a username or (non-empty) email is already taken.
func (s *Store) Exists(username, email string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM users WHERE username = ? OR (email != '' AND email = ?)",
		username, email,
	).Scan(&n)
	return n > 0, err
}

// VerifyPassword loads a user by username and checks bcrypt against the supplied password.
// Returns the User on match.
func (s *Store) VerifyPassword(username, password string) (*User, error) {
	var u User
	var hash string
	var ts string
	err := s.db.QueryRow(
		"SELECT id, username, password_hash, role, email, status, email_verified, totp_enabled, ntfy_topic, created_at FROM users WHERE username = ?",
		username,
	).Scan(&u.ID, &u.Username, &hash, &u.Role, &u.Email, &u.Status, &u.EmailVerified, &u.MfaEnabled, &u.NtfyTopic, &ts)
	if err == sql.ErrNoRows {
		return nil, errors.New("usuário ou senha inválidos")
	}
	if err != nil {
		return nil, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return nil, errors.New("usuário ou senha inválidos")
	}
	u.CreatedAt, _ = parseTime(ts)
	return &u, nil
}

// GetUserByUsername loads a user by login name (no password check). Used by the
// passkey login flow, which authenticates via the authenticator assertion rather
// than a password. Returns nil when no such user.
func (s *Store) GetUserByUsername(username string) (*User, error) {
	if username == "" {
		return nil, nil
	}
	var u User
	var ts string
	err := s.db.QueryRow(
		"SELECT id, username, role, email, status, email_verified, totp_enabled, ntfy_topic, created_at FROM users WHERE username = ?",
		username,
	).Scan(&u.ID, &u.Username, &u.Role, &u.Email, &u.Status, &u.EmailVerified, &u.MfaEnabled, &u.NtfyTopic, &ts)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.CreatedAt, _ = parseTime(ts)
	return &u, nil
}

// GetUserByID is used by middleware after JWT validation to load current user state.
func (s *Store) GetUserByID(id int) (*User, error) {
	var u User
	var ts string
	err := s.db.QueryRow(
		"SELECT id, username, role, email, status, email_verified, totp_enabled, ntfy_topic, created_at FROM users WHERE id = ?", id,
	).Scan(&u.ID, &u.Username, &u.Role, &u.Email, &u.Status, &u.EmailVerified, &u.MfaEnabled, &u.NtfyTopic, &ts)
	if err == sql.ErrNoRows {
		return nil, errors.New("user not found")
	}
	if err != nil {
		return nil, err
	}
	u.CreatedAt, _ = parseTime(ts)
	return &u, nil
}

// ListUsers returns all users (admin only).
func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query("SELECT id, username, role, email, status, email_verified, totp_enabled, ntfy_topic, created_at FROM users ORDER BY created_at")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []User
	for rows.Next() {
		var u User
		var ts string
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.Email, &u.Status, &u.EmailVerified, &u.MfaEnabled, &u.NtfyTopic, &ts); err != nil {
			continue
		}
		u.CreatedAt, _ = parseTime(ts)
		out = append(out, u)
	}
	return out, rows.Err()
}

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
		sha256Hex(plain), uid, purpose, email, time.Now().Add(ttl).UTC().Format(timeFormat),
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
	var email, expStr string
	var used sql.NullString
	err := s.db.QueryRow(
		`SELECT user_id, email, expires_at, used_at FROM auth_tokens WHERE token_hash = ? AND purpose = ?`,
		hash, purpose,
	).Scan(&uid, &email, &expStr, &used)
	if err == sql.ErrNoRows {
		return nil, errors.New("token inválido")
	}
	if err != nil {
		return nil, err
	}
	if used.Valid && used.String != "" {
		return nil, errors.New("token já utilizado")
	}
	if exp, perr := parseTime(expStr); perr == nil && time.Now().After(exp) {
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

// DeleteUser removes a user (and cascades refresh tokens via FK).
func (s *Store) DeleteUser(id int) error {
	_, err := s.db.Exec("DELETE FROM users WHERE id = ?", id)
	return err
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
		"INSERT OR REPLACE INTO webauthn_credentials(cred_id, user_id, data) VALUES(?, ?, ?)",
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

// ─── Refresh tokens ────────────────────────────────────────────────────────

// CreateRefreshToken generates a fresh random token, stores its hash, returns the plain string.
// `remember` controls TTL behavior on refresh: when true, every successful refresh re-extends
// the expiration by 30 days from now (sliding window — only logs out after 30d of inactivity).
func (s *Store) CreateRefreshToken(userID int, ttl time.Duration, remember bool) (string, error) {
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
		"INSERT INTO refresh_tokens(token_hash, user_id, expires_at, remember_me) VALUES(?, ?, ?, ?)",
		hash, userID, time.Now().Add(ttl).UTC().Format(timeFormat), rem,
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
	var expStr string
	var remember int
	err := s.db.QueryRow(
		"SELECT user_id, expires_at, remember_me FROM refresh_tokens WHERE token_hash = ?",
		hash,
	).Scan(&userID, &expStr, &remember)
	if err == sql.ErrNoRows {
		return nil, false, errors.New("refresh token inválido")
	}
	if err != nil {
		return nil, false, err
	}
	exp, err := parseTime(expStr)
	if err != nil {
		return nil, false, err
	}
	if time.Now().UTC().After(exp) {
		return nil, false, errors.New("refresh token expirado")
	}
	u, err := s.GetUserByID(userID)
	return u, remember == 1, err
}

// ConsumeRefreshToken deletes a refresh token (use on logout, or rolling rotation).
func (s *Store) ConsumeRefreshToken(plain string) error {
	hash := sha256Hex(plain)
	_, err := s.db.Exec("DELETE FROM refresh_tokens WHERE token_hash = ?", hash)
	return err
}

// CleanupExpired removes refresh tokens past their TTL. Call periodically.
func (s *Store) CleanupExpired() error {
	now := time.Now().UTC().Format(timeFormat)
	_, err := s.db.Exec("DELETE FROM refresh_tokens WHERE expires_at < ?", now)
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
}

// ListSessions returns a user's active sessions, newest first. currentPlain (the
// caller's own refresh token, may be empty) flags which row is "this device".
func (s *Store) ListSessions(userID int, currentPlain string) ([]SessionInfo, error) {
	rows, err := s.db.Query(
		"SELECT token_hash, created_at, expires_at, remember_me FROM refresh_tokens WHERE user_id = ? ORDER BY created_at DESC",
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
		var hash, created, expires string
		var remember int
		if err := rows.Scan(&hash, &created, &expires, &remember); err != nil {
			continue
		}
		si := SessionInfo{ID: hash, Remember: remember == 1, Current: hash == curHash}
		si.CreatedAt, _ = parseTime(created)
		si.ExpiresAt, _ = parseTime(expires)
		out = append(out, si)
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

// parseTime accepts the formats SQLite may emit for DATETIME columns:
//   - "2006-01-02 15:04:05"           (CURRENT_TIMESTAMP default format)
//   - "2006-01-02T15:04:05Z"          (driver-normalized ISO 8601)
//   - RFC3339 with sub-second precision
func parseTime(s string) (time.Time, error) {
	for _, layout := range []string{
		timeFormat,
		"2006-01-02T15:04:05Z",
		time.RFC3339,
		time.RFC3339Nano,
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized time format: %q", s)
}
