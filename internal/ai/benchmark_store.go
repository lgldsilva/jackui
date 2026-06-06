package ai

import (
	"database/sql"
	"path/filepath"

	"github.com/luizg/jackui/internal/dbutil"
	_ "modernc.org/sqlite"
)

// BenchmarkStore persists benchmark results, the (user-editable) case set, and
// the resulting chain order so a re-ranking survives restarts and the chain
// boots in its best-known order without re-running the benchmark.
type BenchmarkStore struct {
	db *sql.DB
}

func NewBenchmarkStore(path string) (*BenchmarkStore, error) {
	db, err := sql.Open(dbutil.DriverName, path+dbutil.PragmaWAL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS benchmark_result (
			slot_id        TEXT PRIMARY KEY,
			provider       TEXT NOT NULL DEFAULT '',
			model          TEXT NOT NULL DEFAULT '',
			accuracy       REAL NOT NULL DEFAULT 0,
			avg_latency_ms INTEGER NOT NULL DEFAULT 0,
			composite      REAL NOT NULL DEFAULT 0,
			chain_order    INTEGER NOT NULL DEFAULT 0,
			samples        INTEGER NOT NULL DEFAULT 0,
			failure_reason TEXT NOT NULL DEFAULT '',
			incomplete     INTEGER NOT NULL DEFAULT 0,
			updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS benchmark_case (
			id     INTEGER PRIMARY KEY AUTOINCREMENT,
			raw    TEXT NOT NULL,
			expect TEXT NOT NULL
		);
	`); err != nil {
		_ = db.Close()
		return nil, err
	}
	// Migrate older DBs created before the column existed. Best-effort: a
	// "duplicate column" error on an already-migrated DB is expected and ignored.
	_, _ = db.Exec(`ALTER TABLE benchmark_result ADD COLUMN incomplete INTEGER NOT NULL DEFAULT 0`)
	return &BenchmarkStore{db: db}, nil
}

func (s *BenchmarkStore) Close() error {
	if s == nil {
		return nil
	}
	return s.db.Close()
}

// SaveResults replaces the stored results with a fresh run. The slice is assumed
// sorted best-first (as Run returns it), so the index becomes chain_order.
func (s *BenchmarkStore) SaveResults(scores []SlotScore) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM benchmark_result`); err != nil {
		return err
	}
	for i, sc := range scores {
		if _, err := tx.Exec(`
			INSERT INTO benchmark_result(slot_id, provider, model, accuracy, avg_latency_ms, composite, chain_order, samples, failure_reason, incomplete)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, sc.SlotID, sc.Provider, sc.Model, sc.Accuracy, sc.AvgLatencyMs, sc.Composite, i, sc.Samples, sc.FailureReason, sc.Incomplete); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Results returns the last benchmark, ordered best-first by chain_order.
func (s *BenchmarkStore) Results() []SlotScore {
	if s == nil {
		return nil
	}
	rows, err := s.db.Query(`SELECT slot_id, provider, model, accuracy, avg_latency_ms, composite, samples, failure_reason, incomplete FROM benchmark_result ORDER BY chain_order`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []SlotScore
	for rows.Next() {
		var sc SlotScore
		if err := rows.Scan(&sc.SlotID, &sc.Provider, &sc.Model, &sc.Accuracy, &sc.AvgLatencyMs, &sc.Composite, &sc.Samples, &sc.FailureReason, &sc.Incomplete); err == nil {
			out = append(out, sc)
		}
	}
	return out
}

// Order returns the persisted chain order (slot ids, best-first), or nil if no
// benchmark has run — the chain then keeps its config order.
func (s *BenchmarkStore) Order() []string {
	if s == nil {
		return nil
	}
	rows, err := s.db.Query(`SELECT slot_id FROM benchmark_result ORDER BY chain_order`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// Cases returns the user-editable case set, seeding the defaults on first use.
func (s *BenchmarkStore) Cases() []BenchmarkCase {
	if s == nil {
		return DefaultBenchmarkCases
	}
	rows, err := s.db.Query(`SELECT raw, expect FROM benchmark_case ORDER BY id`)
	if err != nil {
		return DefaultBenchmarkCases
	}
	defer rows.Close()
	var out []BenchmarkCase
	for rows.Next() {
		var bc BenchmarkCase
		if rows.Scan(&bc.Raw, &bc.Expect) == nil {
			out = append(out, bc)
		}
	}
	if len(out) == 0 {
		_ = s.SetCases(DefaultBenchmarkCases)
		return DefaultBenchmarkCases
	}
	return out
}

// SetCases replaces the entire case set (the UI sends the full edited list).
func (s *BenchmarkStore) SetCases(cases []BenchmarkCase) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM benchmark_case`); err != nil {
		return err
	}
	for _, bc := range cases {
		if bc.Raw == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO benchmark_case(raw, expect) VALUES(?, ?)`, bc.Raw, bc.Expect); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DefaultBenchmarkStorePath returns the standard location inside the data dir.
func DefaultBenchmarkStorePath(dataDir string) string {
	return filepath.Join(dataDir, ".ai-benchmark.db")
}
