package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite" // read the legacy auth.db

	"github.com/lgldsilva/jackui/internal/db"
	"github.com/lgldsilva/jackui/internal/dbutil"
)

// runMigrateAuth is the one-time ETL that copies the auth data (the only data
// preserved in the SQLite -> PostgreSQL migration) from the legacy auth.db into
// the unified Postgres schema. Everything else is recreated empty.
//
//	jackui migrate-auth --from /data/auth.db --to postgres://…
//
// Idempotent (ON CONFLICT DO NOTHING). Preserves original user ids and resyncs
// the identity sequence so newly created users don't collide.
func runMigrateAuth(args []string) error {
	fs := flag.NewFlagSet("migrate-auth", flag.ContinueOnError)
	from := fs.String("from", "/data/auth.db", "path to the legacy SQLite auth.db")
	to := fs.String("to", os.Getenv("JACKUI_DATABASE_URL"), "destination PostgreSQL DSN (default $JACKUI_DATABASE_URL)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *to == "" {
		return fmt.Errorf("missing --to DSN (or set JACKUI_DATABASE_URL)")
	}
	if _, err := os.Stat(*from); err != nil {
		return fmt.Errorf("source auth.db: %w", err)
	}

	src, err := sql.Open(dbutil.DriverName, *from+"?mode=ro")
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	defer func() { _ = src.Close() }()

	dst, err := sql.Open("pgx", *to)
	if err != nil {
		return fmt.Errorf("open postgres: %w", err)
	}
	defer func() { _ = dst.Close() }()

	// Ensure the destination schema exists before copying.
	if err := db.Migrate(dst); err != nil {
		return fmt.Errorf("ensure schema: %w", err)
	}

	tx, err := dst.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	counts := map[string]int{}
	for _, step := range []struct {
		name string
		fn   func(*sql.DB, *sql.Tx) (int, error)
	}{
		{"users", copyUsers},
		{"refresh_tokens", copyRefreshTokens},
		{"auth_tokens", copyAuthTokens},
		{"webauthn_credentials", copyWebauthn},
		{"mfa_backup_codes", copyBackupCodes},
	} {
		n, err := step.fn(src, tx)
		if err != nil {
			return fmt.Errorf("copy %s: %w", step.name, err)
		}
		counts[step.name] = n
	}

	// Resync the users identity sequence past the highest preserved id so the
	// next INSERT (bootstrap admin / register) gets a fresh id.
	if _, err := tx.Exec(
		`SELECT setval(pg_get_serial_sequence('users','id'),
		                GREATEST((SELECT COALESCE(MAX(id),0) FROM users), 1))`); err != nil {
		return fmt.Errorf("resync users sequence: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	for _, name := range []string{"users", "refresh_tokens", "auth_tokens", "webauthn_credentials", "mfa_backup_codes"} {
		fmt.Printf("  %-22s %d rows\n", name, counts[name])
	}
	fmt.Println("migrate-auth: done")
	return nil
}

// nullTime reads a SQLite datetime (TEXT in mixed formats) into a NullTime,
// tolerating the formats modernc emits via dbutil.ParseTime.
func nullTime(s sql.NullString) sql.NullTime {
	if !s.Valid || s.String == "" {
		return sql.NullTime{}
	}
	t := dbutil.ParseTime(s.String)
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}

// reqTime is for NOT NULL timestamp columns: falls back to now() when the source
// value is missing/unparseable so the NOT NULL constraint holds.
func reqTime(s sql.NullString) time.Time {
	if t := nullTime(s); t.Valid {
		return t.Time
	}
	return time.Now()
}

func copyUsers(src *sql.DB, tx *sql.Tx) (int, error) {
	rows, err := src.Query(`SELECT id, username, password_hash, role, created_at,
		email, status, email_verified, totp_secret, totp_enabled, ntfy_topic FROM users`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	n := 0
	for rows.Next() {
		var (
			id                         int64
			username, passwordHash     string
			role                       string
			createdAt                  sql.NullString
			email, status              string
			emailVerified, totpEnabled int
			totpSecret, ntfyTopic      string
		)
		if err := rows.Scan(&id, &username, &passwordHash, &role, &createdAt,
			&email, &status, &emailVerified, &totpSecret, &totpEnabled, &ntfyTopic); err != nil {
			return n, err
		}
		if _, err := tx.Exec(`INSERT INTO users
			(id, username, password_hash, role, created_at, email, status, email_verified, totp_secret, totp_enabled, ntfy_topic)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT (id) DO NOTHING`,
			id, username, passwordHash, role, reqTime(createdAt), email, status, emailVerified, totpSecret, totpEnabled, ntfyTopic); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

func copyRefreshTokens(src *sql.DB, tx *sql.Tx) (int, error) {
	rows, err := src.Query(`SELECT token_hash, user_id, expires_at, created_at,
		remember_me, user_agent, ip, consumed_at FROM refresh_tokens`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	n := 0
	for rows.Next() {
		var (
			tokenHash            string
			userID               int64
			expiresAt, createdAt sql.NullString
			rememberMe           int
			userAgent, ip        string
			consumedAt           sql.NullString
		)
		if err := rows.Scan(&tokenHash, &userID, &expiresAt, &createdAt, &rememberMe, &userAgent, &ip, &consumedAt); err != nil {
			return n, err
		}
		if _, err := tx.Exec(`INSERT INTO refresh_tokens
			(token_hash, user_id, expires_at, created_at, remember_me, user_agent, ip, consumed_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (token_hash) DO NOTHING`,
			tokenHash, userID, reqTime(expiresAt), reqTime(createdAt), rememberMe, userAgent, ip, nullTime(consumedAt)); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

func copyAuthTokens(src *sql.DB, tx *sql.Tx) (int, error) {
	rows, err := src.Query(`SELECT token_hash, user_id, purpose, email, expires_at, used_at, created_at FROM auth_tokens`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	n := 0
	for rows.Next() {
		var (
			tokenHash         string
			userID            sql.NullInt64 // NULL for invites
			purpose, email    string
			expiresAt         sql.NullString
			usedAt, createdAt sql.NullString
		)
		if err := rows.Scan(&tokenHash, &userID, &purpose, &email, &expiresAt, &usedAt, &createdAt); err != nil {
			return n, err
		}
		if _, err := tx.Exec(`INSERT INTO auth_tokens
			(token_hash, user_id, purpose, email, expires_at, used_at, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (token_hash) DO NOTHING`,
			tokenHash, userID, purpose, email, reqTime(expiresAt), nullTime(usedAt), reqTime(createdAt)); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

func copyWebauthn(src *sql.DB, tx *sql.Tx) (int, error) {
	rows, err := src.Query(`SELECT cred_id, user_id, data, created_at FROM webauthn_credentials`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	n := 0
	for rows.Next() {
		var (
			credID    string
			userID    int64
			data      string
			createdAt sql.NullString
		)
		if err := rows.Scan(&credID, &userID, &data, &createdAt); err != nil {
			return n, err
		}
		if _, err := tx.Exec(`INSERT INTO webauthn_credentials (cred_id, user_id, data, created_at)
			VALUES ($1,$2,$3,$4) ON CONFLICT (cred_id) DO NOTHING`,
			credID, userID, data, reqTime(createdAt)); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

func copyBackupCodes(src *sql.DB, tx *sql.Tx) (int, error) {
	rows, err := src.Query(`SELECT code_hash, user_id, used_at, created_at FROM mfa_backup_codes`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	n := 0
	for rows.Next() {
		var (
			codeHash          string
			userID            int64
			usedAt, createdAt sql.NullString
		)
		if err := rows.Scan(&codeHash, &userID, &usedAt, &createdAt); err != nil {
			return n, err
		}
		if _, err := tx.Exec(`INSERT INTO mfa_backup_codes (code_hash, user_id, used_at, created_at)
			VALUES ($1,$2,$3,$4) ON CONFLICT (code_hash) DO NOTHING`,
			codeHash, userID, nullTime(usedAt), reqTime(createdAt)); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}
