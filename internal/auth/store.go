// Package auth implements user authentication with JWT access tokens + opaque refresh tokens.
//
// Design:
//   - Access token: stateless JWT, short TTL (15min). Validated cryptographically per-request.
//   - Refresh token: opaque random string, long TTL (1 day normal, 30 days "remember me"),
//     stored hashed in DB so we can revoke (logout = delete row).
//   - Login returns both. Frontend hits /refresh when access expires to get a new pair (rolling refresh).
package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/lgldsilva/jackui/internal/dbutil"
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

// Store wraps the PostgreSQL-backed user + refresh token persistence.
type Store struct {
	db *dbutil.DB
}

// New wires the auth store onto the shared Postgres pool. The schema is applied
// centrally (internal/db migrations), so there's no per-store migrate here.
func New(pool *sql.DB) (*Store, error) {
	return &Store{db: dbutil.Wrap(pool)}, nil
}

// Bootstrap ensures an admin user exists. If no users at all, creates "admin" with the given password.
// Use this once at startup with the password from config/env.
func (s *Store) Bootstrap(adminUser, adminPass string) error {
	var count int
	// Exclude the anonymous sentinel (id 0, seeded by the schema migration) so
	// the admin is still bootstrapped on a fresh install.
	if err := s.db.QueryRow("SELECT COUNT(*) FROM users WHERE id <> 0").Scan(&count); err != nil {
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
	var id int64
	err = s.db.QueryRow(
		"INSERT INTO users(username, password_hash, role) VALUES(?, ?, ?) RETURNING id",
		username, string(hash), string(role),
	).Scan(&id)
	if err != nil {
		return 0, err
	}
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
	var id int64
	err = s.db.QueryRow(
		"INSERT INTO users(username, email, password_hash, role, status, email_verified) VALUES(?, ?, ?, ?, ?, 0) RETURNING id",
		username, email, string(hash), string(role), string(status),
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	return int(id), nil
}

// UpdateEmail changes a user's email and resets email_verified — the new
// address must be (re)confirmed via the verify-email link.
func (s *Store) UpdateEmail(userID int, email string) error {
	_, err := s.db.Exec("UPDATE users SET email = ?, email_verified = 0 WHERE id = ?", email, userID)
	return err
}

// EmailInUse reports whether a non-empty email belongs to any user other than
// excludeID (so changing the case of your own address never collides).
func (s *Store) EmailInUse(email string, excludeID int) (bool, error) {
	if email == "" {
		return false, nil
	}
	var n int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM users WHERE email = ? AND id != ?",
		email, excludeID,
	).Scan(&n)
	return n > 0, err
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
	err := s.db.QueryRow(
		"SELECT id, username, role, email, status, email_verified, ntfy_topic, created_at FROM users WHERE email = ? AND email != '' LIMIT 1",
		email,
	).Scan(&u.ID, &u.Username, &u.Role, &u.Email, &u.Status, &u.EmailVerified, &u.NtfyTopic, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
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
	err := s.db.QueryRow(
		"SELECT id, username, password_hash, role, email, status, email_verified, totp_enabled, ntfy_topic, created_at FROM users WHERE username = ?",
		username,
	).Scan(&u.ID, &u.Username, &hash, &u.Role, &u.Email, &u.Status, &u.EmailVerified, &u.MfaEnabled, &u.NtfyTopic, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, errors.New("usuário ou senha inválidos")
	}
	if err != nil {
		return nil, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return nil, errors.New("usuário ou senha inválidos")
	}
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
	err := s.db.QueryRow(
		"SELECT id, username, role, email, status, email_verified, totp_enabled, ntfy_topic, created_at FROM users WHERE username = ?",
		username,
	).Scan(&u.ID, &u.Username, &u.Role, &u.Email, &u.Status, &u.EmailVerified, &u.MfaEnabled, &u.NtfyTopic, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetUserByID is used by middleware after JWT validation to load current user state.
func (s *Store) GetUserByID(id int) (*User, error) {
	var u User
	err := s.db.QueryRow(
		"SELECT id, username, role, email, status, email_verified, totp_enabled, ntfy_topic, created_at FROM users WHERE id = ?", id,
	).Scan(&u.ID, &u.Username, &u.Role, &u.Email, &u.Status, &u.EmailVerified, &u.MfaEnabled, &u.NtfyTopic, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, errors.New("user not found")
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// ListUsers returns all users (admin only).
func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query("SELECT id, username, role, email, status, email_verified, totp_enabled, ntfy_topic, created_at FROM users WHERE id <> 0 ORDER BY created_at")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.Email, &u.Status, &u.EmailVerified, &u.MfaEnabled, &u.NtfyTopic, &u.CreatedAt); err != nil {
			continue
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// DeleteUser removes a user (and cascades refresh tokens via FK).
func (s *Store) DeleteUser(id int) error {
	_, err := s.db.Exec("DELETE FROM users WHERE id = ?", id)
	return err
}
