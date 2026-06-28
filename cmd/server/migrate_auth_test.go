package main

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// legacyAuthDB writes a SQLite auth.db in the pre-migration schema with one
// user (+ refresh token + invite). auth.New no longer opens SQLite, so the ETL
// fixture is built with raw SQL — which is also what migrate-auth reads in prod.
func legacyAuthDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()
	stmts := []string{
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'user',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			email TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'active',
			email_verified INTEGER NOT NULL DEFAULT 1, totp_secret TEXT NOT NULL DEFAULT '',
			totp_enabled INTEGER NOT NULL DEFAULT 0, ntfy_topic TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE refresh_tokens (
			token_hash TEXT PRIMARY KEY, user_id INTEGER NOT NULL, expires_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, remember_me INTEGER NOT NULL DEFAULT 0,
			user_agent TEXT NOT NULL DEFAULT '', ip TEXT NOT NULL DEFAULT '', consumed_at DATETIME)`,
		`CREATE TABLE auth_tokens (
			token_hash TEXT PRIMARY KEY, user_id INTEGER, purpose TEXT NOT NULL,
			email TEXT NOT NULL DEFAULT '', expires_at DATETIME NOT NULL, used_at DATETIME,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE webauthn_credentials (cred_id TEXT PRIMARY KEY, user_id INTEGER NOT NULL,
			data TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE mfa_backup_codes (code_hash TEXT PRIMARY KEY, user_id INTEGER NOT NULL,
			used_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`INSERT INTO users(id, username, password_hash, role, created_at, email, status, email_verified)
			VALUES(1, 'alice', '$2a$10$abcdefghijklmnopqrstuvCkzZ0123456789ABCDEFGHIJKLMNOPQRST', 'user',
			       '2024-01-02 03:04:05', 'alice@example.com', 'active', 1)`,
		`INSERT INTO refresh_tokens(token_hash, user_id, expires_at, remember_me, user_agent, ip)
			VALUES('rt-hash', 1, '2099-01-01 00:00:00', 1, 'ua', 'ip')`,
		`INSERT INTO auth_tokens(token_hash, user_id, purpose, email, expires_at)
			VALUES('inv-hash', NULL, 'invite', 'invitee@example.com', '2099-01-01 00:00:00')`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("seed sqlite: %v\n%s", err, q)
		}
	}
	return path
}

// TestRunMigrateAuth exercises the SQLite -> Postgres auth ETL end to end:
// a legacy auth.db is migrated into an isolated Postgres schema, and the user
// (with bcrypt hash) + refresh token land with the original id preserved.
// Skips without a test Postgres.
func TestRunMigrateAuth(t *testing.T) {
	base := os.Getenv("JACKUI_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("JACKUI_TEST_DATABASE_URL not set")
	}
	authPath := legacyAuthDB(t)
	const wantID int64 = 1

	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	defer func() { _ = admin.Close() }()
	schema := fmt.Sprintf("authmig_%d", time.Now().UnixNano())
	if _, err := admin.Exec(fmt.Sprintf("CREATE SCHEMA %q", schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(fmt.Sprintf("DROP SCHEMA %q CASCADE", schema)) })

	u, _ := url.Parse(base)
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	dsn := u.String()

	if err := runMigrateAuth([]string{"--from", authPath, "--to", dsn}); err != nil {
		t.Fatalf("runMigrateAuth: %v", err)
	}

	dst, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open dst: %v", err)
	}
	defer func() { _ = dst.Close() }()

	var (
		gotID int64
		hash  string
	)
	if err := dst.QueryRow(`SELECT id, password_hash FROM users WHERE username='alice'`).
		Scan(&gotID, &hash); err != nil {
		t.Fatalf("query user: %v", err)
	}
	if gotID != wantID {
		t.Errorf("user id = %d, want preserved %d", gotID, wantID)
	}
	if len(hash) < 4 || hash[:4] != "$2a$" {
		t.Errorf("bcrypt hash not preserved: %q", hash)
	}

	var rt, at int
	_ = dst.QueryRow(`SELECT count(*) FROM refresh_tokens WHERE user_id=$1`, gotID).Scan(&rt)
	_ = dst.QueryRow(`SELECT count(*) FROM auth_tokens WHERE purpose='invite' AND user_id IS NULL`).Scan(&at)
	if rt != 1 {
		t.Errorf("refresh_tokens = %d, want 1", rt)
	}
	if at != 1 {
		t.Errorf("invite auth_tokens with NULL user_id = %d, want 1", at)
	}

	// Sequence resynced: a fresh insert must not collide with the preserved id.
	var newID int64
	if err := dst.QueryRow(`INSERT INTO users(username,password_hash) VALUES('bob','y') RETURNING id`).Scan(&newID); err != nil {
		t.Fatalf("insert bob: %v", err)
	}
	if newID <= gotID {
		t.Errorf("new user id %d collides with preserved id %d (sequence not resynced)", newID, gotID)
	}
}
