package downloads

import (
	"fmt"
)

// Operações de grupo / scheduling / requeue do store — extraído de store.go.
// PromoteGroup flips every queued row of a torrent group to downloading in ONE
// transaction, preserving PromoteToDownloading's status guard (a row not in
// `queued` — already downloading, paused, or removed — is skipped) and its
// started_at COALESCE. Returns the IDs actually promoted.
func (s *Store) PromoteGroup(ids []int) ([]int, error) {
	return s.txGuardedStatus(ids, StatusQueued,
		`UPDATE downloads SET status=?, started_at=COALESCE(started_at, CURRENT_TIMESTAMP) WHERE id=? AND status=?`,
		StatusDownloading)
}

// PreemptGroup sends every downloading row of a group back to the queue WITHOUT
// counting a stall (over-limit / out-prioritized), in ONE transaction. Mirrors
// PreemptToQueued's guard + queued_since reset. Returns the IDs actually demoted.
func (s *Store) PreemptGroup(ids []int) ([]int, error) {
	return s.txGuardedStatus(ids, StatusDownloading,
		`UPDATE downloads SET status=?, queued_since=CURRENT_TIMESTAMP WHERE id=? AND status=?`,
		StatusQueued)
}

// DemoteGroup sends every downloading row of a group back to the queue counting a
// stall (no-seed), in ONE transaction. Mirrors DemoteToQueued's guard + bump.
// Returns the IDs actually demoted (their stall counters now incremented).
func (s *Store) DemoteGroup(ids []int) ([]int, error) {
	return s.txGuardedStatus(ids, StatusDownloading,
		`UPDATE downloads SET status=?, queued_since=CURRENT_TIMESTAMP, stalls=stalls+1 WHERE id=? AND status=?`,
		StatusQueued)
}

// MoveGroup flips every DOWNLOADING row of a group into `moving` in ONE
// transaction, guarded on status so a row paused/removed between the tick's
// snapshot and this call is skipped (not clobbered). Returns the IDs that
// actually transitioned — the completion move should relocate only those.
func (s *Store) MoveGroup(ids []int) ([]int, error) {
	return s.txGuardedStatus(ids, StatusDownloading,
		`UPDATE downloads SET status=? WHERE id=? AND status=?`, StatusMoving)
}

// CompleteGroup flips every `moving` row of a group into `completed` (with
// completed_at) in ONE transaction, guarded on status. Returns the IDs finalized.
func (s *Store) CompleteGroup(ids []int) ([]int, error) {
	return s.txGuardedStatus(ids, StatusMoving,
		`UPDATE downloads SET status=?, completed_at=CURRENT_TIMESTAMP WHERE id=? AND status=?`,
		StatusCompleted)
}

// txGuardedStatus runs a status-guarded UPDATE for each id in one transaction.
// query must take (newStatus, id, fromStatus) in that order; only rows currently
// in fromStatus change (the guard that makes a concurrent pause/cancel a no-op).
// Returns the ids whose row actually changed. Kept tiny so the group helpers stay
// well under the cognitive-complexity gate.
func (s *Store) txGuardedStatus(ids []int, fromStatus, query, newStatus string) ([]int, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	stmt, err := tx.Prepare(query)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	defer stmt.Close()
	changed := make([]int, 0, len(ids))
	for _, id := range ids {
		res, err := stmt.Exec(newStatus, id, fromStatus)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			changed = append(changed, id)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return changed, nil
}

// SetFileIndex updates the target file index after metadata resolves for
// auto-picked downloads (FileIndex = -1 from Transmission RPC).
func (s *Store) SetFileIndex(userID, id int, fileIndex int) error {
	_, err := s.db.Exec(`UPDATE downloads SET file_index=? WHERE id=? AND user_id=?`, fileIndex, id, userID)
	return err
}

// ─── queue scheduling ─────────────────────────────────────────────────────

// SetPriority updates the queue priority (high/normal/low). Scoped by user.
func (s *Store) SetPriority(userID, id int, priority string) error {
	if !validPriority(priority) {
		return fmt.Errorf(errInvalidPriority, priority)
	}
	_, err := s.db.Exec(`UPDATE downloads SET priority=? WHERE id=? AND user_id=?`, priority, id, userID)
	return err
}

// ListSchedulable returns every row in `downloading` or `queued`, across ALL
// users — the scheduler enforces a single global active limit. Ordering is left
// to the scheduler (Go-side, see scheduler.go), so this returns the raw set.
func (s *Store) ListSchedulable() ([]Download, error) {
	rows, err := s.db.Query(dlSelect+"WHERE status IN (?, ?)", StatusDownloading, StatusQueued)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSlice(rows)
}

// PromoteToDownloading flips a queued row to downloading. Guarded on status so a
// concurrent pause/cancel is a no-op. Returns true when the row was promoted.
func (s *Store) PromoteToDownloading(id int) (bool, error) {
	res, err := s.db.Exec(`
		UPDATE downloads SET status=?, started_at=COALESCE(started_at, CURRENT_TIMESTAMP)
		WHERE id=? AND status=?`, StatusDownloading, id, StatusQueued)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DemoteToQueued sends a downloading row back to the queue (no-seed stall). It
// resets queued_since (→ end of its priority group) and bumps the stall counter.
// Guarded on status. Returns the new stall count and whether the row was demoted.
func (s *Store) DemoteToQueued(id int) (stalls int, demoted bool, err error) {
	res, err := s.db.Exec(`
		UPDATE downloads SET status=?, queued_since=CURRENT_TIMESTAMP, stalls=stalls+1
		WHERE id=? AND status=?`, StatusQueued, id, StatusDownloading)
	if err != nil {
		return 0, false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, false, nil
	}
	_ = s.db.QueryRow(`SELECT stalls FROM downloads WHERE id=?`, id).Scan(&stalls)
	return stalls, true, nil
}

// PreemptToQueued sends a downloading row back to the queue WITHOUT counting a
// stall — used when a higher-priority download preempts it, or when bootstrap
// trims actives over the limit. Resets queued_since for fair re-ordering.
// Guarded on status. Returns true when the row was demoted.
func (s *Store) PreemptToQueued(id int) (bool, error) {
	res, err := s.db.Exec(`
		UPDATE downloads SET status=?, queued_since=CURRENT_TIMESTAMP
		WHERE id=? AND status=?`, StatusQueued, id, StatusDownloading)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Requeue puts a single row into the queue (used by Resume so the active limit
// is honored instead of jumping straight to downloading). Sets queued_since for
// fair ordering and clears any prior error. Scoped by user; no-op on
// completed/downloading rows.
func (s *Store) Requeue(userID, id int) error {
	_, err := s.db.Exec(`
		UPDATE downloads SET status=?, queued_since=CURRENT_TIMESTAMP, error=''
		WHERE id=? AND user_id=? AND status NOT IN (?, ?)`,
		StatusQueued, id, userID, StatusCompleted, StatusDownloading)
	return err
}

// RequeueForUser queues every paused row owned by userID (Resume-All). Leaves
// terminal and already-active rows untouched.
func (s *Store) RequeueForUser(userID int) (int64, error) {
	res, err := s.db.Exec(`
		UPDATE downloads SET status=?, queued_since=CURRENT_TIMESTAMP
		WHERE user_id=? AND status NOT IN (?, ?, ?)`,
		StatusQueued, userID, StatusCompleted, StatusFailed, StatusDownloading)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RequeueByIDs queues specific rows owned by userID (Batch-Resume). Leaves
// already-active rows untouched.
func (s *Store) RequeueByIDs(userID int, ids []int) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	q := `UPDATE downloads SET status=?, queued_since=CURRENT_TIMESTAMP WHERE user_id=? AND status != ? AND id IN (`
	args := []any{StatusQueued, userID, StatusDownloading}
	for i, id := range ids {
		if i > 0 {
			q += ","
		}
		q += "?"
		args = append(args, id)
	}
	q += ")"
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
