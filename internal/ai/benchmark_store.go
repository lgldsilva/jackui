package ai

import (
	"database/sql"
	"encoding/json"
	"path/filepath"

	"github.com/lgldsilva/jackui/internal/dbutil"
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
			cost_per_1m    REAL NOT NULL DEFAULT 0,
			tasks          TEXT NOT NULL DEFAULT '',
			updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS benchmark_case (
			id     INTEGER PRIMARY KEY AUTOINCREMENT,
			raw    TEXT NOT NULL,
			expect TEXT NOT NULL,
			task   TEXT NOT NULL DEFAULT '',
			origin TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS benchmark_setting (
			key   TEXT PRIMARY KEY,
			value REAL NOT NULL
		);
	`); err != nil {
		_ = db.Close()
		return nil, err
	}
	// Migrate older DBs created before these columns existed. Best-effort: a
	// "duplicate column" error on an already-migrated DB is expected and ignored.
	_, _ = db.Exec(`ALTER TABLE benchmark_result ADD COLUMN incomplete INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE benchmark_result ADD COLUMN cost_per_1m REAL NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE benchmark_case ADD COLUMN origin TEXT NOT NULL DEFAULT ''`)
	// Multi-task benchmark: a per-case task column ('' = rename, retrocompat). A
	// "duplicate column" error on an already-migrated DB is expected and ignored.
	_, _ = db.Exec(`ALTER TABLE benchmark_case ADD COLUMN task TEXT NOT NULL DEFAULT ''`)
	// Per-task accuracy breakdown, stored as a JSON blob so the UI can show it after
	// a restart. Empty/legacy rows hold '' → decoded to a nil map (UI falls back to
	// the global accuracy), so this is fully retrocompatible.
	_, _ = db.Exec(`ALTER TABLE benchmark_result ADD COLUMN tasks TEXT NOT NULL DEFAULT ''`)
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
		tasksJSON := ""
		if len(sc.Tasks) > 0 {
			if b, err := json.Marshal(sc.Tasks); err == nil {
				tasksJSON = string(b)
			}
		}
		if _, err := tx.Exec(`
			INSERT INTO benchmark_result(slot_id, provider, model, accuracy, avg_latency_ms, composite, chain_order, samples, failure_reason, incomplete, cost_per_1m, tasks)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, sc.SlotID, sc.Provider, sc.Model, sc.Accuracy, sc.AvgLatencyMs, sc.Composite, i, sc.Samples, sc.FailureReason, sc.Incomplete, sc.CostPer1M, tasksJSON); err != nil {
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
	rows, err := s.db.Query(`SELECT slot_id, provider, model, accuracy, avg_latency_ms, composite, samples, failure_reason, incomplete, cost_per_1m, tasks FROM benchmark_result ORDER BY chain_order`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []SlotScore
	for rows.Next() {
		var sc SlotScore
		var tasksJSON string
		if err := rows.Scan(&sc.SlotID, &sc.Provider, &sc.Model, &sc.Accuracy, &sc.AvgLatencyMs, &sc.Composite, &sc.Samples, &sc.FailureReason, &sc.Incomplete, &sc.CostPer1M, &tasksJSON); err == nil {
			if tasksJSON != "" {
				_ = json.Unmarshal([]byte(tasksJSON), &sc.Tasks) // best-effort; legacy rows have ''
			}
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
// The seed is the FULL multi-task set (rename + schedule + identify), so a fresh
// install benchmarks every AI task out of the box.
func (s *BenchmarkStore) Cases() []BenchmarkCase {
	if s == nil {
		return AllDefaultBenchmarkCases()
	}
	rows, err := s.db.Query(`SELECT raw, expect, task, origin FROM benchmark_case ORDER BY id`)
	if err != nil {
		return AllDefaultBenchmarkCases()
	}
	defer rows.Close()
	var out []BenchmarkCase
	for rows.Next() {
		var bc BenchmarkCase
		if rows.Scan(&bc.Raw, &bc.Expect, &bc.Task, &bc.Origin) == nil {
			out = append(out, bc)
		}
	}
	if len(out) == 0 {
		def := AllDefaultBenchmarkCases()
		_ = s.SetCases(def)
		return def
	}
	// Upgrade path: a store still holding the original 7-case seed untouched, OR the
	// rename-only broad set with no schedule/identify task yet, gets the new full
	// multi-task default set. Any user-edited set is left alone.
	if isLegacySeed(out) || isRenameOnlySeed(out) {
		def := AllDefaultBenchmarkCases()
		_ = s.SetCases(def)
		return def
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
		if _, err := tx.Exec(`INSERT INTO benchmark_case(raw, expect, task, origin) VALUES(?, ?, ?, ?)`, bc.Raw, bc.Expect, bc.Task, bc.Origin); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SaveCostConfig persists the runtime cost knobs (set from the Settings UI) so
// they survive a restart and override the env/yaml defaults on boot.
func (s *BenchmarkStore) SaveCostConfig(cc CostConfig) error {
	if s == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range map[string]float64{"max_cost_per_1m": cc.MaxCostPer1M, "kwh_price": cc.KWhPrice, "local_watts": cc.LocalWatts} {
		if _, err := tx.Exec(`INSERT INTO benchmark_setting(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LoadCostConfig returns the persisted cost knobs and ok=true when any were saved
// (so boot can apply a UI override; otherwise the env/yaml config stands).
func (s *BenchmarkStore) LoadCostConfig() (CostConfig, bool) {
	if s == nil {
		return CostConfig{}, false
	}
	rows, err := s.db.Query(`SELECT key, value FROM benchmark_setting`)
	if err != nil {
		return CostConfig{}, false
	}
	defer rows.Close()
	var cc CostConfig
	found := false
	for rows.Next() {
		var k string
		var v float64
		if rows.Scan(&k, &v) != nil {
			continue
		}
		found = true
		switch k {
		case "max_cost_per_1m":
			cc.MaxCostPer1M = v
		case "kwh_price":
			cc.KWhPrice = v
		case "local_watts":
			cc.LocalWatts = v
		}
	}
	return cc, found
}

// DefaultBenchmarkStorePath returns the standard location inside the data dir.
func DefaultBenchmarkStorePath(dataDir string) string {
	return filepath.Join(dataDir, ".ai-benchmark.db")
}
