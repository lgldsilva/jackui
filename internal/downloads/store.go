// Package downloads persists per-user background downloads — full-file
// torrent transfers that run to completion regardless of player activity.
//
// Unlike streaming (where pieces are evicted under LRU pressure once the user
// stops watching), a queued download keeps its torrent alive until every byte
// is on disk. Status transitions are durable so the worker can resume across
// process restarts.
package downloads

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/luizg/jackui/internal/dbutil"
	_ "modernc.org/sqlite"
)

// Status is the lifecycle of a Download row. The scheduler promotes rows from
// `queued`→`downloading` up to the active limit; the worker acts on rows in
// `downloading`; the rest are terminal or user-paused.
const (
	StatusQueued      = "queued"
	StatusDownloading = "downloading"
	StatusCompleted   = "completed"
	StatusFailed      = "failed"
	StatusPaused      = "paused"
)

// Priority drives queue scheduling. Aligned with the streamer's piece-priority
// labels (low/normal/high) so the UI dropdown is shared.
const (
	PriorityHigh   = "high"
	PriorityNormal = "normal"
	PriorityLow    = "low"
)

const errInvalidStatus = "invalid status: %s"
const errInvalidPriority = "invalid priority: %s"

// ListFilter groups optional filtering / sorting params for ListFiltered and ListFilteredAll.
// Empty fields are ignored. UserID is for per-user queries; UserIDFilter is for admin
// cross-user filtering.
type ListFilter struct {
	UserID       int
	Status       string
	Tracker      string
	Category     string
	Search       string
	SortCol      string // "created_at" (default), "name", "size", "progress", "status", "tracker", "category"
	SortDir      string // "desc" (default) or "asc"
	UserIDFilter string // admin-only: filter by user ID string
}

// Download is one tracked background transfer.
type Download struct {
	ID              int        `json:"id"`
	UserID          int        `json:"userId"`
	Username        string     `json:"username,omitempty"`   // populated only for admin listing
	InfoHash        string     `json:"infoHash"`
	FileIndex       int        `json:"fileIndex"`
	FilePath        string     `json:"filePath"`
	FileSize        int64      `json:"fileSize"`
	Name            string     `json:"name"`
	Magnet          string     `json:"magnet"`
	Tracker         string     `json:"tracker,omitempty"`
	Category        string     `json:"category,omitempty"`
	Status          string     `json:"status"`
	BytesDownloaded int64      `json:"bytesDownloaded"`
	Progress        float64    `json:"progress"`
	DownRate        int64      `json:"downRate,omitempty"`   // bytes/sec, populated by handler
	ETA             int        `json:"eta,omitempty"`         // remaining seconds, populated by handler
	StartedAt       *time.Time `json:"startedAt"`
	CompletedAt     *time.Time `json:"completedAt"`
	Error           string     `json:"error"`
	CreatedAt       time.Time  `json:"createdAt"`
	// Promoted is true when the file has been moved outside the download dir (computed, not stored).
	Promoted        bool       `json:"promoted,omitempty"`
	// Queue scheduling fields.
	Priority        string     `json:"priority"`                  // high/normal/low
	Stalls          int        `json:"stalls,omitempty"`          // times demoted for no-seed
	QueuedSince     *time.Time `json:"queuedSince,omitempty"`     // base for fair ordering + aging
	QueuePosition   int        `json:"queuePosition,omitempty"`   // 1-based rank among the user's queued rows (computed by handler)
}

type Store struct {
	db *sql.DB
}

func New(path string) (*Store, error) {
	db, err := sql.Open(dbutil.DriverName, path+dbutil.PragmaWAL+dbutil.PragmaFK)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() { _ = s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS downloads (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id          INTEGER NOT NULL,
			info_hash        TEXT    NOT NULL,
			file_index       INTEGER NOT NULL,
			file_path        TEXT    NOT NULL,
			file_size        INTEGER NOT NULL,
			name             TEXT    NOT NULL,
			magnet           TEXT    NOT NULL,
			tracker          TEXT    NOT NULL DEFAULT '',
			category         TEXT    NOT NULL DEFAULT '',
			status           TEXT    NOT NULL DEFAULT 'queued',
			bytes_downloaded INTEGER NOT NULL DEFAULT 0,
			started_at       DATETIME,
			completed_at     DATETIME,
			error            TEXT    NOT NULL DEFAULT '',
			created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(user_id, info_hash, file_index)
		);
		CREATE INDEX IF NOT EXISTS idx_dl_user ON downloads(user_id);
		CREATE INDEX IF NOT EXISTS idx_dl_status ON downloads(status);
	`)
	if err != nil {
		return err
	}
	for _, col := range []string{"tracker", "category"} {
		if !s.hasColumn("downloads", col) {
			if _, e := s.db.Exec("ALTER TABLE downloads ADD COLUMN " + col + " TEXT NOT NULL DEFAULT ''"); e != nil {
				return e
			}
		}
	}
	// Queue-scheduling columns (priority + fair ordering + no-seed stall count).
	if !s.hasColumn("downloads", "priority") {
		if _, e := s.db.Exec("ALTER TABLE downloads ADD COLUMN priority TEXT NOT NULL DEFAULT 'normal'"); e != nil {
			return e
		}
	}
	if !s.hasColumn("downloads", "queued_since") {
		if _, e := s.db.Exec("ALTER TABLE downloads ADD COLUMN queued_since DATETIME"); e != nil {
			return e
		}
	}
	if !s.hasColumn("downloads", "stalls") {
		if _, e := s.db.Exec("ALTER TABLE downloads ADD COLUMN stalls INTEGER NOT NULL DEFAULT 0"); e != nil {
			return e
		}
	}
	return nil
}

func (s *Store) hasColumn(table, col string) bool {
	rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dflt sql.NullString
		var pk int
		if rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk) == nil && name == col {
			return true
		}
	}
	return false
}

// Create inserts a new download row in `queued` state. The scheduler promotes it
// to `downloading` once a slot is free (active limit). If a row already exists
// for the (user, info_hash, file_index) tuple, returns it unchanged — re-queueing
// an existing download is idempotent.
func (s *Store) Create(d Download) (*Download, error) {
	if d.InfoHash == "" || d.Magnet == "" {
		return nil, errors.New("infoHash e magnet são obrigatórios")
	}
	priority := d.Priority
	if !validPriority(priority) {
		priority = PriorityNormal
	}
	// Try to fetch existing first — idempotent enqueue
	if existing, _ := s.GetByKey(d.UserID, d.InfoHash, d.FileIndex); existing != nil {
		// If user re-enqueued a paused/failed entry, send it back to the queue
		// (the scheduler honors the active limit; never jump straight to downloading).
		if existing.Status == StatusPaused || existing.Status == StatusFailed {
			_, _ = s.db.Exec(`UPDATE downloads SET status=?, error='', queued_since=CURRENT_TIMESTAMP WHERE id=?`, StatusQueued, existing.ID)
			existing.Status = StatusQueued
			existing.Error = ""
		}
		// Update tracker/category even if re-queueing
		if d.Tracker != "" || d.Category != "" {
			_, _ = s.db.Exec(`UPDATE downloads SET tracker=?, category=? WHERE id=?`, d.Tracker, d.Category, existing.ID)
			existing.Tracker = d.Tracker
			existing.Category = d.Category
		}
		return existing, nil
	}
	res, err := s.db.Exec(`
		INSERT INTO downloads(user_id, info_hash, file_index, file_path, file_size, name, magnet, tracker, category, status, priority, queued_since)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	`, d.UserID, d.InfoHash, d.FileIndex, d.FilePath, d.FileSize, d.Name, d.Magnet, d.Tracker, d.Category, StatusQueued, priority)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.Get(d.UserID, int(id))
}

// Get returns one download owned by userID.
func (s *Store) Get(userID, id int) (*Download, error) {
	row := s.db.QueryRow(dlSelect+"WHERE id=? AND user_id=?", id, userID)
	return scanRow(row)
}

// GetByKey looks up a download by its uniqueness tuple. Used by Create() to
// dedupe and by the worker when reconciling state.
func (s *Store) GetByKey(userID int, infoHash string, fileIndex int) (*Download, error) {
	row := s.db.QueryRow(dlSelect+"WHERE user_id=? AND info_hash=? AND file_index=?", userID, infoHash, fileIndex)
	return scanRow(row)
}

func (s *Store) GetCompletedPath(infoHash string, fileIndex int) (string, error) {
	if s == nil {
		return "", nil
	}
	var filePath string
	err := s.db.QueryRow(`
		SELECT file_path FROM downloads 
		WHERE info_hash=? AND file_index=? AND status='completed' AND file_path != '' 
		LIMIT 1`, infoHash, fileIndex).Scan(&filePath)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return filePath, nil
}

const dlSelect = `SELECT id, user_id, info_hash, file_index, file_path, file_size, name, magnet,
	tracker, category, status, bytes_downloaded,
	COALESCE(started_at, ''), COALESCE(completed_at, ''), error, created_at,
	COALESCE(priority, 'normal'), COALESCE(stalls, 0), COALESCE(queued_since, '') FROM downloads `

// HashSetForUser returns all info_hashes the user has in the downloads table
// as a set. Usado pelo handler de busca pra enriquecer SearchResult com
// isDownloaded em uma única query. includeAll=true devolve hashes de todos
// os usuários (admin "all=1"). Inclui qualquer status (queued/downloading/
// completed/failed) — uma vez que o user iniciou, "já baixei isso" continua
// valendo pro filtro de UI.
func (s *Store) HashSetForUser(userID int, includeAll bool) (map[string]bool, error) {
	if s == nil {
		return map[string]bool{}, nil
	}
	q := `SELECT info_hash FROM downloads WHERE info_hash != ''`
	args := []any{}
	if !includeAll {
		q += ` AND user_id = ?`
		args = append(args, userID)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := map[string]bool{}
	for rows.Next() {
		var h string
		if rows.Scan(&h) == nil && h != "" {
			set[h] = true
		}
	}
	return set, rows.Err()
}

// FindByPathPrefix returns the downloads whose file_path equals absPath or
// lives under it (when absPath is a directory). Used to remove the torrent(s)
// linked to a local file/folder when it's deleted from "Meus downloads".
// Filtering happens in Go to avoid LIKE-escaping issues with special chars in
// paths; the downloads table is small.
func (s *Store) FindByPathPrefix(absPath string) ([]Download, error) {
	if s == nil || absPath == "" {
		return nil, nil
	}
	rows, err := s.db.Query(dlSelect + "WHERE file_path != ''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	all, err := scanSlice(rows)
	if err != nil {
		return nil, err
	}
	prefix := strings.TrimRight(absPath, "/") + "/"
	out := make([]Download, 0)
	for _, d := range all {
		if d.FilePath == absPath || strings.HasPrefix(d.FilePath, prefix) {
			out = append(out, d)
		}
	}
	return out, nil
}

// List returns all downloads for the user, newest first.
func (s *Store) List(userID int) ([]Download, error) {
	rows, err := s.db.Query(dlSelect+"WHERE user_id=? ORDER BY created_at DESC", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSlice(rows)
}

// ListFiltered returns downloads for the user filtered by optional criteria.
// Empty filters are ignored. Sort is "created_at" (default), "name", "size", "progress".
func (s *Store) ListFiltered(f ListFilter) ([]Download, error) {
	q := "WHERE user_id=?"
	args := []any{f.UserID}
	if f.Status != "" {
		q += " AND status=?"
		args = append(args, f.Status)
	}
	if f.Tracker != "" {
		q += " AND tracker=?"
		args = append(args, f.Tracker)
	}
	if f.Category != "" {
		q += " AND category=?"
		args = append(args, f.Category)
	}
	if f.Search != "" {
		q += " AND (name LIKE ? OR file_path LIKE ?)"
		s := "%" + f.Search + "%"
		args = append(args, s, s)
	}
	order := "created_at"
	switch f.SortCol {
	case "name":
		order = "name"
	case "size":
		order = "file_size"
	case "progress":
		order = "bytes_downloaded"
	case "status":
		order = "status"
	case "tracker":
		order = "tracker"
	case "category":
		order = "category"
	}
	dir := "DESC"
	if f.SortDir == "asc" {
		dir = "ASC"
	}
	rows, err := s.db.Query(dlSelect+q+" ORDER BY "+order+" "+dir, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSlice(rows)
}

// ListActive returns every download in `downloading` status across all users.
// The worker uses this to schedule downloads each tick.
func (s *Store) ListActive() ([]Download, error) {
	rows, err := s.db.Query(dlSelect+"WHERE status=? ORDER BY id", StatusDownloading)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSlice(rows)
}

// ListAll returns every download — used by the streamer to compute the
// "protected from eviction" set on startup (any non-final entry should keep
// its torrent data on disk).
func (s *Store) ListAll() ([]Download, error) {
	rows, err := s.db.Query(dlSelect+"ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSlice(rows)
}

// ListFilteredAll returns downloads across ALL users, filtered by optional
// criteria. Used by admin listing. Empty filters are ignored.
func (s *Store) ListFilteredAll(f ListFilter) ([]Download, error) {
	q := "WHERE 1=1"
	args := []any{}
	if f.Status != "" {
		q += " AND status=?"
		args = append(args, f.Status)
	}
	if f.Tracker != "" {
		q += " AND tracker=?"
		args = append(args, f.Tracker)
	}
	if f.Category != "" {
		q += " AND category=?"
		args = append(args, f.Category)
	}
	if f.UserIDFilter != "" {
		q += " AND user_id=?"
		args = append(args, f.UserIDFilter)
	}
	if f.Search != "" {
		q += " AND (name LIKE ? OR file_path LIKE ?)"
		s := "%" + f.Search + "%"
		args = append(args, s, s)
	}
	order := "created_at"
	switch f.SortCol {
	case "name":
		order = "name"
	case "size":
		order = "file_size"
	case "progress":
		order = "bytes_downloaded"
	case "status":
		order = "status"
	case "tracker":
		order = "tracker"
	case "category":
		order = "category"
	case "user_id", "username":
		order = "user_id"
	}
	dir := "DESC"
	if f.SortDir == "asc" {
		dir = "ASC"
	}
	rows, err := s.db.Query(dlSelect+q+" ORDER BY "+order+" "+dir, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSlice(rows)
}

// DistinctUsers returns all distinct user_ids that have downloads.
func (s *Store) DistinctUsers() ([]int, error) {
	rows, err := s.db.Query("SELECT DISTINCT user_id FROM downloads ORDER BY user_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var uid int
		if rows.Scan(&uid) == nil {
			out = append(out, uid)
		}
	}
	return out, rows.Err()
}

// DistinctTrackers returns all distinct tracker values for the user.
func (s *Store) DistinctTrackers(userID int) ([]string, error) {
	rows, err := s.db.Query("SELECT DISTINCT tracker FROM downloads WHERE user_id=? AND tracker!='' ORDER BY tracker", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if rows.Scan(&v) == nil {
			out = append(out, v)
		}
	}
	return out, rows.Err()
}

// DistinctCategories returns all distinct category values for the user.
func (s *Store) DistinctCategories(userID int) ([]string, error) {
	rows, err := s.db.Query("SELECT DISTINCT category FROM downloads WHERE user_id=? AND category!='' ORDER BY category", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if rows.Scan(&v) == nil {
			out = append(out, v)
		}
	}
	return out, rows.Err()
}

func scanSlice(rows *sql.Rows) ([]Download, error) {
	out := []Download{}
	for rows.Next() {
		d, err := scanRows(rows)
		if err != nil {
			continue
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// SetStatus updates the lifecycle column and clears the error message when
// transitioning back to an active state. Scoped by user_id so a row can only be
// mutated by its owner (defense-in-depth: handlers also check ownership, the
// worker passes the row's own UserID).
func (s *Store) SetStatus(userID, id int, status string) error {
	if !validStatus(status) {
		return fmt.Errorf(errInvalidStatus, status)
	}
	var err error
	switch status {
	case StatusDownloading:
		_, err = s.db.Exec(`
			UPDATE downloads SET status=?, error='',
			started_at = COALESCE(started_at, CURRENT_TIMESTAMP)
			WHERE id=? AND user_id=?`, status, id, userID)
	case StatusCompleted:
		_, err = s.db.Exec(`
			UPDATE downloads SET status=?, completed_at=CURRENT_TIMESTAMP WHERE id=? AND user_id=?`,
			status, id, userID)
	default:
		_, err = s.db.Exec(`UPDATE downloads SET status=? WHERE id=? AND user_id=?`, status, id, userID)
	}
	return err
}

// SetStatusForUser updates status for ALL non-terminal rows owned by userID.
// Terminal statuses (completed, failed) are left unchanged.
func (s *Store) SetStatusForUser(userID int, status string) (int64, error) {
	if !validStatus(status) {
		return 0, fmt.Errorf(errInvalidStatus, status)
	}
	res, err := s.db.Exec(`UPDATE downloads SET status=? WHERE user_id=? AND status NOT IN (?, ?)`,
		status, userID, StatusCompleted, StatusFailed)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SetStatusByIDs updates status for specific download IDs owned by userID.
func (s *Store) SetStatusByIDs(userID int, ids []int, status string) (int64, error) {
	if !validStatus(status) {
		return 0, fmt.Errorf(errInvalidStatus, status)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	q := "UPDATE downloads SET status=? WHERE user_id=? AND id IN ("
	args := []any{status, userID}
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

// SetError flips a download into `failed` with a captured error message. Scoped
// by user_id (the worker passes the row's own UserID).
func (s *Store) SetError(userID, id int, msg string) error {
	_, err := s.db.Exec(`UPDATE downloads SET status=?, error=? WHERE id=? AND user_id=?`,
		StatusFailed, msg, id, userID)
	return err
}

// SetFilePath updates the on-disk path after the worker moves a completed file
// to the download directory. Scoped by user_id (worker passes the row's own UserID).
func (s *Store) SetFilePath(userID, id int, path string) error {
	_, err := s.db.Exec(`UPDATE downloads SET file_path=? WHERE id=? AND user_id=?`, path, id, userID)
	return err
}

// UpdateName records the actual torrent folder name resolved from metadata.
// The row is created with the search-result title, but the real torrent name
// (t.Name()) is what the streamer registers for eviction protection — they
// often differ. Persisting the real name keeps the boot-time RegisterDownload
// in NewWorker consistent so a restart doesn't protect the wrong path.
func (s *Store) UpdateName(userID, id int, name string) error {
	_, err := s.db.Exec(`UPDATE downloads SET name=? WHERE id=? AND user_id=?`, name, id, userID)
	return err
}

// SetCategory updates the download's category/label. Used pela Transmission RPC
// (torrent-set "labels"). Scoped by user_id.
func (s *Store) SetCategory(userID, id int, category string) error {
	_, err := s.db.Exec(`UPDATE downloads SET category=? WHERE id=? AND user_id=?`, category, id, userID)
	return err
}

// UpdateMetadata updates the resolved torrent name, file path inside the torrent, and file size in bytes.
// Called by the background worker once torrent metadata is fully resolved.
func (s *Store) UpdateMetadata(userID, id int, name string, filePath string, fileSize int64) error {
	_, err := s.db.Exec(`UPDATE downloads SET name=?, file_path=?, file_size=? WHERE id=? AND user_id=?`, name, filePath, fileSize, id, userID)
	return err
}

// UpdateProgress records the latest bytes_downloaded — called periodically
// by the worker. Errors are non-fatal; the next tick will retry. Scoped by
// user_id (worker passes the row's own UserID).
func (s *Store) UpdateProgress(userID, id int, bytes int64) error {
	_, err := s.db.Exec(`UPDATE downloads SET bytes_downloaded=? WHERE id=? AND user_id=?`, bytes, id, userID)
	return err
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

// Delete removes a download row (used for user-initiated cancel).
// Cancelling does NOT erase on-disk pieces — those are cleaned by the
// streamer cache LRU once the torrent is no longer protected.
func (s *Store) Delete(userID, id int) error {
	res, err := s.db.Exec(`DELETE FROM downloads WHERE id=? AND user_id=?`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("download não encontrado")
	}
	return nil
}

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
	var startedAt, completedAt, createdAt, queuedSince string
	err := r.Scan(
		&d.ID, &d.UserID, &d.InfoHash, &d.FileIndex, &d.FilePath, &d.FileSize,
		&d.Name, &d.Magnet, &d.Tracker, &d.Category, &d.Status, &d.BytesDownloaded,
		&startedAt, &completedAt, &d.Error, &createdAt,
		&d.Priority, &d.Stalls, &queuedSince,
	)
	if err != nil {
		return nil, err
	}
	if t := dbutil.ParseTime(startedAt); !t.IsZero() {
		d.StartedAt = &t
	}
	if t := dbutil.ParseTime(completedAt); !t.IsZero() {
		d.CompletedAt = &t
	}
	if t := dbutil.ParseTime(queuedSince); !t.IsZero() {
		d.QueuedSince = &t
	}
	d.CreatedAt = dbutil.ParseTime(createdAt)
	if d.FileSize > 0 {
		d.Progress = float64(d.BytesDownloaded) / float64(d.FileSize)
		if d.Progress > 1 {
			d.Progress = 1
		}
	}
	return d, nil
}

func validStatus(s string) bool {
	switch s {
	case StatusQueued, StatusDownloading, StatusCompleted, StatusFailed, StatusPaused:
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
