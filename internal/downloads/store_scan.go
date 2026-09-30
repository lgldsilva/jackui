package downloads

import (
	"database/sql"
)

// ─── scanning helpers ─────────────────────────────────────────────────────

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRow(row *sql.Row) (*Download, error) {
	return scanGeneric(row)
}

func scanRows(rows *sql.Rows) (*Download, error) {
	return scanGeneric(rows)
}

func scanGeneric(r rowScanner) (*Download, error) {
	d := &Download{}
	if err := scanGenericInto(r, d); err != nil {
		return nil, err
	}
	return d, nil
}

// scanGenericInto scans the row directly into an existing *Download, with no
// allocation — used by scanSlice to populate the slice in-place
// (eliminates 1 heap alloc + ~400 B copy per row on polls).
func scanGenericInto(r rowScanner, d *Download) error {
	var startedAt, completedAt, queuedSince, seedStoppedAt sql.NullTime
	var linkedInt int // SMALLINT 0/1 → bool (keeps the schema's int-flag convention)
	err := r.Scan(
		&d.ID, &d.UserID, &d.InfoHash, &d.FileIndex, &d.FilePath, &d.FileSize,
		&d.Name, &d.Magnet, &d.Tracker, &d.Category, &d.Status, &d.BytesDownloaded,
		&startedAt, &completedAt, &d.Error, &d.CreatedAt,
		&d.Priority, &d.Stalls, &queuedSince, &d.ActiveMagnet, &d.Source,
		&d.DestBase, &d.DestSubdir, &d.CompletionDest, &linkedInt,
		&seedStoppedAt,
	)
	if err != nil {
		return err
	}
	d.Linked = linkedInt != 0
	if startedAt.Valid {
		t := startedAt.Time
		d.StartedAt = &t
	}
	if completedAt.Valid {
		t := completedAt.Time
		d.CompletedAt = &t
	}
	if queuedSince.Valid {
		t := queuedSince.Time
		d.QueuedSince = &t
	}
	if seedStoppedAt.Valid {
		t := seedStoppedAt.Time
		d.SeedStoppedAt = &t
	}
	if d.FileSize > 0 {
		d.Progress = float64(d.BytesDownloaded) / float64(d.FileSize)
		if d.Progress > 1 {
			d.Progress = 1
		}
	}
	return nil
}

func validStatus(s string) bool {
	switch s {
	case StatusQueued, StatusDownloading, StatusMoving, StatusCompleted, StatusFailed, StatusPaused:
		return true
	}
	return false
}

func validPriority(p string) bool {
	switch p {
	case PriorityHigh, PriorityNormal, PriorityLow:
		return true
	}
	return false
}
