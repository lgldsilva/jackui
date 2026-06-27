package ai

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/lgldsilva/jackui/internal/dbutil"
)

// BenchmarkStore persists benchmark results, the (user-editable) case set, and
// the resulting chain order so a re-ranking survives restarts and the chain
// boots in its best-known order without re-running the benchmark.
type BenchmarkStore struct {
	db *dbutil.DB
}

// NewBenchmarkStore wires the benchmark store onto the shared Postgres pool.
// Schema is applied centrally (internal/db migrations).
func NewBenchmarkStore(pool *sql.DB) (*BenchmarkStore, error) {
	return &BenchmarkStore{db: dbutil.Wrap(pool)}, nil
}

// Close is a no-op: the shared pool's lifecycle is owned by main.
func (s *BenchmarkStore) Close() error { return nil }

// b2i maps a Go bool to the SMALLINT 0/1 the schema uses (pgx won't implicitly
// cast a bool param into a smallint column).
func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
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
		`, sc.SlotID, sc.Provider, sc.Model, sc.Accuracy, sc.AvgLatencyMs, sc.Composite, i, sc.Samples, sc.FailureReason, b2i(sc.Incomplete), sc.CostPer1M, tasksJSON); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RecordRun updates the durable per-slot history from a set of FRESHLY-measured
// scores (the ones RunSlots/RerunIncomplete just produced — never carried-over
// rows, or the failure streak would bump for a run that never happened). It runs
// independently of SaveResults (different table) so the timeline survives the
// DELETE+INSERT there. Transitions:
//   - ok         → last_success_at = now, streak cleared
//   - incomplete → last_success_at preserved, streak cleared (rate-limited, not a hard error)
//   - error      → last_success_at preserved, streak extended (first_failure_at kept; +1)
func (s *BenchmarkStore) RecordRun(fresh []SlotScore) error {
	if s == nil || len(fresh) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	for _, sc := range fresh {
		var prev histState
		_ = tx.QueryRow(`SELECT last_outcome, last_success_at, first_failure_at, consecutive_failures FROM benchmark_history WHERE slot_id = ?`, sc.SlotID).
			Scan(&prev.outcome, &prev.successAt, &prev.firstFailAt, &prev.consec)
		next := nextHistState(prev, sc, now)
		if _, err := tx.Exec(`
			INSERT INTO benchmark_history(slot_id, last_outcome, last_error, last_success_at, last_run_at, first_failure_at, consecutive_failures)
			VALUES(?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(slot_id) DO UPDATE SET
				last_outcome = excluded.last_outcome,
				last_error = excluded.last_error,
				last_success_at = excluded.last_success_at,
				last_run_at = excluded.last_run_at,
				first_failure_at = excluded.first_failure_at,
				consecutive_failures = excluded.consecutive_failures
		`, sc.SlotID, next.outcome, next.lastError, next.successAt, now, next.firstFailAt, next.consec); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// histState is one slot's durable run history (prior state on read, computed next
// state on write). Kept as a struct so the transition logic is a pure function
// (nextHistState) — flat, unit-testable, and out of RecordRun's DB loop.
type histState struct {
	outcome     string
	lastError   string
	successAt   sql.NullTime
	firstFailAt sql.NullTime
	consec      int
}

// nextHistState computes the new history from the previous state and a fresh score:
//   - ok         → stamp last_success_at = now, clear the failure streak
//   - incomplete → preserve last_success_at, clear the streak (cut short, not a hard error)
//   - error      → preserve last_success_at, extend the streak (keep its start date)
func nextHistState(prev histState, sc SlotScore, now time.Time) histState {
	next := histState{outcome: RunOutcome(sc), successAt: prev.successAt} // success preserved by default
	switch next.outcome {
	case OutcomeOK:
		next.successAt = sql.NullTime{Time: now, Valid: true}
	case OutcomeError:
		next.lastError = sc.FailureReason
		next.consec, next.firstFailAt = extendStreak(prev, now)
	default: // incomplete: responded but cut short
		next.lastError = sc.FailureReason
	}
	return next
}

// extendStreak grows the consecutive-failure counter. A streak already running
// (prev was also an error) keeps its first_failure_at start date — backfilled to
// now when a legacy row never recorded one; otherwise the streak begins now.
func extendStreak(prev histState, now time.Time) (int, sql.NullTime) {
	nowTS := sql.NullTime{Time: now, Valid: true}
	if prev.outcome != OutcomeError {
		return 1, nowTS
	}
	base := prev.consec
	if base < 1 {
		base = 1
	}
	if !prev.firstFailAt.Valid {
		return base + 1, nowTS
	}
	return base + 1, prev.firstFailAt
}

// tsRFC3339 re-emits a nullable SQLite timestamp as RFC3339 so the frontend can
// `new Date()` it reliably (Safari rejects the bare "YYYY-MM-DD HH:MM:SS" form).
// Returns "" for null/empty/unparseable.
func tsRFC3339(ns sql.NullTime) string {
	if !ns.Valid || ns.Time.IsZero() {
		return ""
	}
	return ns.Time.UTC().Format(time.RFC3339)
}

// Results returns the last benchmark, ordered best-first by chain_order, with each
// row's durable run history (outcome, success/run timestamps, failure streak)
// joined in. Legacy rows with no history row leave those fields empty/zero.
func (s *BenchmarkStore) Results() []SlotScore {
	if s == nil {
		return nil
	}
	rows, err := s.db.Query(`
		SELECT r.slot_id, r.provider, r.model, r.accuracy, r.avg_latency_ms, r.composite, r.samples, r.failure_reason, r.incomplete, r.cost_per_1m, r.tasks,
		       h.last_outcome, h.last_error, h.last_success_at, h.last_run_at, h.first_failure_at, h.consecutive_failures
		FROM benchmark_result r
		LEFT JOIN benchmark_history h ON h.slot_id = r.slot_id
		ORDER BY r.chain_order`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []SlotScore
	for rows.Next() {
		var sc SlotScore
		var tasksJSON string
		var outcome, lastErr sql.NullString
		var successAt, runAt, firstFailAt sql.NullTime
		var consec sql.NullInt64
		if err := rows.Scan(&sc.SlotID, &sc.Provider, &sc.Model, &sc.Accuracy, &sc.AvgLatencyMs, &sc.Composite, &sc.Samples, &sc.FailureReason, &sc.Incomplete, &sc.CostPer1M, &tasksJSON,
			&outcome, &lastErr, &successAt, &runAt, &firstFailAt, &consec); err == nil {
			if tasksJSON != "" {
				_ = json.Unmarshal([]byte(tasksJSON), &sc.Tasks) // best-effort; legacy rows have ''
			}
			sc.LastOutcome = outcome.String
			sc.LastError = lastErr.String
			sc.LastSuccessAt = tsRFC3339(successAt)
			sc.LastRunAt = tsRFC3339(runAt)
			sc.FirstFailureAt = tsRFC3339(firstFailAt)
			sc.ConsecutiveFailures = int(consec.Int64)
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
