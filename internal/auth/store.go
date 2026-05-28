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
	"fmt"
	"time"

	_ "modernc.org/sqlite"
	"golang.org/x/crypto/bcrypt"
)

// Role identifies a user's authorization level.
type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
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
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() { s.db.Close() }

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
	return nil
}

func (s *Store) hasColumn(table, col string) bool {
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false
	}
	defer rows.Close()
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
		return 0, fmt.Errorf("hash password: %w", err)
	}
	if role != RoleAdmin && role != RoleUser {
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
		return fmt.Errorf("hash password: %w", err)
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

// DisableTOTP clears the secret + disables MFA.
func (s *Store) DisableTOTP(userID int) error {
	_, err := s.db.Exec("UPDATE users SET totp_secret = '', totp_enabled = 0 WHERE id = ?", userID)
	return err
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
		return 0, fmt.Errorf("hash password: %w", err)
	}
	if role != RoleAdmin && role != RoleUser {
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
		"SELECT id, username, role, email, status, email_verified, created_at FROM users WHERE email = ? AND email != '' LIMIT 1",
		email,
	).Scan(&u.ID, &u.Username, &u.Role, &u.Email, &u.Status, &u.EmailVerified, &ts)
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
		"SELECT id, username, password_hash, role, email, status, email_verified, totp_enabled, created_at FROM users WHERE username = ?",
		username,
	).Scan(&u.ID, &u.Username, &hash, &u.Role, &u.Email, &u.Status, &u.EmailVerified, &u.MfaEnabled, &ts)
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

// GetUserByID is used by middleware after JWT validation to load current user state.
func (s *Store) GetUserByID(id int) (*User, error) {
	var u User
	var ts string
	err := s.db.QueryRow(
		"SELECT id, username, role, email, status, email_verified, totp_enabled, created_at FROM users WHERE id = ?", id,
	).Scan(&u.ID, &u.Username, &u.Role, &u.Email, &u.Status, &u.EmailVerified, &u.MfaEnabled, &ts)
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
	rows, err := s.db.Query("SELECT id, username, role, email, status, email_verified, totp_enabled, created_at FROM users ORDER BY created_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var ts string
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.Email, &u.Status, &u.EmailVerified, &u.MfaEnabled, &ts); err != nil {
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
		sha256Hex(plain), uid, purpose, email, time.Now().Add(ttl).UTC().Format("2006-01-02 15:04:05"),
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
		hash, userID, time.Now().Add(ttl).UTC().Format("2006-01-02 15:04:05"), rem,
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
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	_, err := s.db.Exec("DELETE FROM refresh_tokens WHERE expires_at < ?", now)
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

// parseTime accepts the formats SQLite may emit for DATETIME columns:
//   - "2006-01-02 15:04:05"           (CURRENT_TIMESTAMP default format)
//   - "2006-01-02T15:04:05Z"          (driver-normalized ISO 8601)
//   - RFC3339 with sub-second precision
func parseTime(s string) (time.Time, error) {
	for _, layout := range []string{
		"2006-01-02 15:04:05",
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
