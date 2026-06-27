package main

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/auth"
)

// TestRunMigrateAuth exercises the SQLite -> Postgres auth ETL end to end:
// a legacy auth.db is built with the real auth store, migrate-auth copies it
// into an isolated Postgres schema, and the user (with bcrypt hash) + refresh
// token land with the original id preserved. Skips without a test Postgres.
func TestRunMigrateAuth(t *testing.T) {
	base := os.Getenv("JACKUI_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("JACKUI_TEST_DATABASE_URL not set")
	}

	// Legacy SQLite auth.db with one user + refresh token + invite token.
	authPath := filepath.Join(t.TempDir(), "auth.db")
	s, err := auth.New(authPath)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	uid, err := s.CreateUserFull("alice", "alice@example.com", "s3cret-pass", auth.RoleUser, auth.StatusActive)
	if err != nil {
		t.Fatalf("CreateUserFull: %v", err)
	}
	if _, err := s.CreateRefreshToken(uid, time.Hour, true, "ua", "ip"); err != nil {
		t.Fatalf("CreateRefreshToken: %v", err)
	}
	if _, err := s.CreateToken("invite", 0, "invitee@example.com", time.Hour); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	s.Close()

	// Isolated destination schema.
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
		gotID   int64
		gotUser string
		hash    string
	)
	if err := dst.QueryRow(`SELECT id, username, password_hash FROM users WHERE username='alice'`).
		Scan(&gotID, &gotUser, &hash); err != nil {
		t.Fatalf("query user: %v", err)
	}
	if gotID != int64(uid) {
		t.Errorf("user id = %d, want preserved %d", gotID, uid)
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
