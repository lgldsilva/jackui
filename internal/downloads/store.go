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
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lgldsilva/jackui/internal/dbutil"
)

// Status is the lifecycle of a Download row. The scheduler promotes rows from
// `queued`→`downloading` up to the active limit; the worker acts on rows in
// `downloading`; the rest are terminal or user-paused.
const (
	StatusQueued      = "queued"
	StatusDownloading = "downloading"
	// StatusMoving is the post-download phase: every byte is on disk and the
	// worker is relocating the file(s) from the streaming cache into their final
	// home (downloadDir / *arr SharedDir). It runs OFF the tick loop in its own
	// goroutine, so a slow cross-filesystem copy doesn't freeze other downloads,
	// and it's durable: a `moving` row left behind by a restart is re-dispatched
	// on boot (registerExistingDownloads), since the idempotent move can re-run.
	StatusMoving    = "moving"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusPaused    = "paused"
)

// Priority drives queue scheduling. Aligned with the streamer's piece-priority
// labels (low/normal/high) so the UI dropdown is shared.
const (
	PriorityHigh   = "high"
	PriorityNormal = "normal"
	PriorityLow    = "low"
)

// FileIndex sentinels. Non-negative values address one concrete file inside
// the torrent; negatives select a resolution strategy:
//
//   - FileIndexAuto (-1): "pick the best file" — the JackUI UI's single-file /
//     streaming path (handlers/downloads.go) when no concrete file is chosen;
//     the worker resolves it to a real index after metadata and persists it via
//     SetFileIndex.
//   - FileIndexWholeTorrent (-2): download the ENTIRE torrent as ONE queue
//     item (t.DownloadAll, aggregate progress, completion moves every file).
//     Created by the Transmission RPC shim (Sonarr/Radarr expect the whole
//     release on disk so they can import every file in a multi-file/season pack).
//
// A sentinel (instead of a new whole_torrent column) keeps the
// UNIQUE(user_id, info_hash, file_index) constraint doing the dedupe work for
// free — exactly one whole-torrent row per (user, torrent) — and every store
// query that already keys on file_index (GetByKey, GetCompletedPath, Create's
// idempotent re-queue) works unchanged.
const (
	FileIndexAuto         = -1
	FileIndexWholeTorrent = -2
)

// SourceArr marks a download created via the Transmission RPC shim (the *arr
// apps). Used to scope the auto-promote-to-Downloads behavior to *arr downloads,
// leaving JackUI UI downloads untouched.
const SourceArr = "arr"

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
	Username        string     `json:"username,omitempty"` // populated only for admin listing
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
	DownRate        int64      `json:"downRate,omitempty"`      // bytes/sec, populated by handler
	UpRate          int64      `json:"upRate,omitempty"`        // bytes/sec, populated by handler (seeding)
	BytesUploaded   int64      `json:"bytesUploaded,omitempty"` // cumulative bytes served this session, populated by handler
	Seeders         int        `json:"seeders,omitempty"`       // live swarm seeders, populated by handler
	ETA             int        `json:"eta,omitempty"`           // remaining seconds, populated by handler
	StartedAt       *time.Time `json:"startedAt"`
	CompletedAt     *time.Time `json:"completedAt"`
	Error           string     `json:"error"`
	CreatedAt       time.Time  `json:"createdAt"`
	// Promoted is true when the file has been moved outside the download dir (computed, not stored).
	Promoted bool `json:"promoted,omitempty"`
	// Queue scheduling fields.
	Priority      string     `json:"priority"`                // high/normal/low
	Stalls        int        `json:"stalls,omitempty"`        // times demoted for no-seed
	QueuedSince   *time.Time `json:"queuedSince,omitempty"`   // base for fair ordering + aging
	QueuePosition int        `json:"queuePosition,omitempty"` // 1-based rank among the user's queued rows (computed by handler)
	// Source rotation (Phase 2): the magnet currently active when it differs from
	// the original (an alternative source). Empty = downloading the original.
	ActiveMagnet string `json:"activeMagnet,omitempty"`
	// Origin of the download: SourceArr when created via the Transmission RPC shim
	// (Sonarr/Radarr/Prowlarr), empty for the JackUI UI. Drives auto-promote.
	Source string `json:"source,omitempty"`
	// Chosen destination (download-to-bulk). DestBase is a writable destination the
	// user picked (a mount or promote dir, validated at create against the user's
	// allowed destinations); DestSubdir is an optional subfolder under it. Empty
	// DestBase → the default downloadDir[/username]. The worker's completionBaseDir
	// prefers these when set.
	DestBase   string `json:"destBase,omitempty"`
	DestSubdir string `json:"destSubdir,omitempty"`
	// CompletionDest is the per-torrent destination dir frozen at metadata-resolve
	// (completionBaseDir + sanitized name). Empty until resolved / for legacy rows.
	CompletionDest string `json:"completionDest,omitempty"`
}

// IsWholeTorrent reports whether this row downloads the entire torrent as one
// item (FileIndexWholeTorrent sentinel) rather than a single file.
func (d Download) IsWholeTorrent() bool {
	return d.FileIndex == FileIndexWholeTorrent
}

// EffectiveMagnet returns the magnet the worker should download: the active
// alternative source when set, otherwise the original.
func (d Download) EffectiveMagnet() string {
	if d.ActiveMagnet != "" {
		return d.ActiveMagnet
	}
	return d.Magnet
}

// SeedSource returns the source to (re)activate a COMPLETED download for
// seeding. It prefers a bare info_hash magnet so the streamer resolves the
// torrent from its CACHED metainfo and seeds the on-disk data in place, instead
// of re-fetching the original indexer .torrent URL — an ephemeral Jackett/proxy
// link that 404s once its token expires (the "auto-seed failed: .torrent URL
// returned 404" log). The bare magnet is just the lookup key: the cached
// metainfo carries the full announce list, so the seed still reaches the
// tracker. Falls back to the stored source when the info_hash is unknown.
func (d Download) SeedSource() string {
	if d.InfoHash != "" {
		return "magnet:?xt=urn:btih:" + d.InfoHash
	}
	return d.EffectiveMagnet()
}

type Store struct {
	db *dbutil.DB
}

// New wires the downloads store onto the shared Postgres pool. Schema is applied
// centrally (internal/db migrations).
func New(pool *sql.DB) (*Store, error) {
	return &Store{db: dbutil.Wrap(pool)}, nil
}

// Close is a no-op: the shared pool's lifecycle is owned by main.
func (s *Store) Close() {}

// execer is the subset of *sql.DB / *sql.Tx that createOne needs. Sharing it lets
// Create (autocommit) and BatchCreate (single transaction) run the SAME idempotent
// insert logic — no duplicated SQL, and a partial failure inside a batch can roll
// the whole thing back (all-or-nothing).
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

// createOne runs the idempotent enqueue for a single row against `x` (a DB or a
// Tx): validate → re-queue an existing paused/failed row (GetByKey) → otherwise
// INSERT. It returns the resulting row plus `inserted` (true only for a fresh
// INSERT; false when an existing row was returned/re-queued). BOTH reads and
// writes go through `x`: in a batch (x is an open Tx) the idempotency read must
// SEE the rows inserted earlier in the SAME transaction, so it can't read off
// s.db (a different connection that wouldn't see the uncommitted writes).
func (s *Store) createOne(x execer, d Download) (row *Download, inserted bool, err error) {
	if d.InfoHash == "" || d.Magnet == "" {
		return nil, false, errors.New("infoHash e magnet são obrigatórios")
	}
	if d.FileIndex < FileIndexWholeTorrent {
		return nil, false, fmt.Errorf("invalid fileIndex %d (min %d)", d.FileIndex, FileIndexWholeTorrent)
	}
	priority := d.Priority
	if !validPriority(priority) {
		priority = PriorityNormal
	}
	// Try to fetch existing first — idempotent enqueue.
	if existing, _ := s.getByKeyWith(x, d.UserID, d.InfoHash, d.FileIndex); existing != nil {
		if err := requeueExisting(x, existing, d); err != nil {
			return nil, false, err
		}
		return existing, false, nil
	}
	var id int64
	err = x.QueryRow(`
		INSERT INTO downloads(user_id, info_hash, file_index, file_path, file_size, name, magnet, tracker, category, status, priority, source, dest_base, dest_subdir, queued_since)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP) RETURNING id
	`, d.UserID, d.InfoHash, d.FileIndex, d.FilePath, d.FileSize, d.Name, d.Magnet, d.Tracker, d.Category, StatusQueued, priority, d.Source, d.DestBase, d.DestSubdir).Scan(&id)
	if err != nil {
		return nil, false, err
	}
	got, err := s.getWith(x, d.UserID, int(id))
	return got, true, err
}

// requeueExisting applies the idempotent re-enqueue to an already-present row
// (mutating `existing` in place to match): a paused/failed row goes back to the
// queue, and the tracker/category from the new request override. The scheduler
// honors the active limit, so a re-queue never jumps straight to downloading.
func requeueExisting(x execer, existing *Download, d Download) error {
	if existing.Status == StatusPaused || existing.Status == StatusFailed {
		if _, err := x.Exec(`UPDATE downloads SET status=?, error='', queued_since=CURRENT_TIMESTAMP WHERE id=?`, StatusQueued, existing.ID); err != nil {
			return err
		}
		existing.Status = StatusQueued
		existing.Error = ""
	}
	if d.Tracker != "" || d.Category != "" {
		if _, err := x.Exec(`UPDATE downloads SET tracker=?, category=? WHERE id=?`, d.Tracker, d.Category, existing.ID); err != nil {
			return err
		}
		existing.Tracker = d.Tracker
		existing.Category = d.Category
	}
	return nil
}

// Create inserts a new download row in `queued` state. The scheduler promotes it
// to `downloading` once a slot is free (active limit). If a row already exists
// for the (user, info_hash, file_index) tuple, returns it unchanged — re-queueing
// an existing download is idempotent.
func (s *Store) Create(d Download) (*Download, error) {
	row, _, err := s.createOne(s.db, d)
	return row, err
}

// BatchResult reports the outcome of a BatchCreate: every resulting row (in input
// order) plus how many were pre-existing (re-queued / unchanged) vs freshly created.
type BatchResult struct {
	Rows     []*Download
	Created  int // freshly inserted rows
	Requeued int // rows that already existed (idempotent hit)
}

// BatchCreate enqueues N rows (typically every selected file of ONE torrent) in
// a SINGLE transaction: either all rows land or none do. It reuses Create's
// idempotent logic (re-queue paused/failed, dedupe on the UNIQUE key) via
// createOne against the Tx. A failure on any row rolls the whole batch back.
func (s *Store) BatchCreate(rows []Download) (*BatchResult, error) {
	if len(rows) == 0 {
		return nil, errors.New("no rows to create")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	res := &BatchResult{Rows: make([]*Download, 0, len(rows))}
	for _, d := range rows {
		created, inserted, err := s.createOne(tx, d)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		res.Rows = append(res.Rows, created)
		if inserted {
			res.Created++
		} else {
			res.Requeued++
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

// UserStats aggregates the user's download history for the stats endpoint.
func (s *Store) UserStats(userID int) (total, completed int, bytes int64, err error) {
	row := s.db.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN status=? THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(bytes_downloaded), 0)
		FROM downloads WHERE user_id=?
	`, StatusCompleted, userID)
	err = row.Scan(&total, &completed, &bytes)
	return total, completed, bytes, err
}

// EnqueueMagnet creates a queued download from a bare magnet, before any
// torrent metadata is known. FileIndex -1 means "pick the best file" — the
// worker resolves it after GotInfo (same contract as the Transmission RPC
// shim). Used by automation (watchlist auto-download); idempotent via Create.
func (s *Store) EnqueueMagnet(userID int, infoHash, name, magnet, tracker string) error {
	_, err := s.Create(Download{
		UserID:   userID,
		InfoHash: strings.ToLower(infoHash),
		// FileIndex -1: best-file sentinel (see resolveFileIndex in worker.go)
		FileIndex: -1,
		Name:      name,
		Magnet:    magnet,
		Tracker:   tracker,
	})
	return err
}

// Get returns one download owned by userID.
func (s *Store) Get(userID, id int) (*Download, error) {
	return s.getWith(s.db, userID, id)
}

// getWith reads a row through `x`. createOne passes the open Tx so a batch's
// idempotency read sees rows inserted earlier in the SAME transaction — reading
// off s.db (a separate connection) wouldn't see those uncommitted writes.
func (s *Store) getWith(x execer, userID, id int) (*Download, error) {
	row := x.QueryRow(dlSelect+"WHERE id=? AND user_id=?", id, userID)
	return scanRow(row)
}

// GetByKey looks up a download by its uniqueness tuple. Used by Create() to
// dedupe and by the worker when reconciling state.
func (s *Store) GetByKey(userID int, infoHash string, fileIndex int) (*Download, error) {
	return s.getByKeyWith(s.db, userID, infoHash, fileIndex)
}

// getByKeyWith is GetByKey through an explicit execer (see getWith for why).
func (s *Store) getByKeyWith(x execer, userID int, infoHash string, fileIndex int) (*Download, error) {
	row := x.QueryRow(dlSelect+"WHERE user_id=? AND info_hash=? AND file_index=?", userID, infoHash, fileIndex)
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

// GetCompletedPathRel resolves the on-disk path of ONE file from a completed
// download. Per-file rows behave exactly like GetCompletedPath. When the
// torrent was downloaded as a single whole-torrent item, only the
// FileIndexWholeTorrent row exists and its file_path is the torrent's
// destination DIRECTORY (moveCompletedTree preserved the in-torrent structure
// under it), so the file is located by joining that directory with relPath —
// the torrent-relative path the caller reads from the cached metainfo; the
// store alone can't map a file index to a path without activating the torrent.
//
// relPath is untrusted (it ultimately comes from torrent metadata): traversal
// is rejected and the resolved path must be an existing regular file under the
// destination directory. Empty relPath skips the whole-torrent fallback.
func (s *Store) GetCompletedPathRel(infoHash string, fileIndex int, relPath string) (string, error) {
	path, err := s.GetCompletedPath(infoHash, fileIndex)
	if err != nil || path != "" {
		return path, err
	}
	if s == nil || relPath == "" || fileIndex < 0 {
		return "", nil
	}
	var destDir, name string
	err = s.db.QueryRow(`
		SELECT file_path, name FROM downloads
		WHERE info_hash=? AND file_index=? AND status='completed' AND file_path != ''
		LIMIT 1`, infoHash, FileIndexWholeTorrent).Scan(&destDir, &name)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return resolveWholeTorrentFile(destDir, name, relPath), nil
}

// resolveWholeTorrentFile maps a torrent-relative path into the moved tree
// under destDir, mirroring wholeTorrentDest (the move that produced the tree).
// Traversal attempts, paths escaping destDir and entries missing from disk all
// resolve to "" — the caller falls back to the streamer.
func resolveWholeTorrentFile(destDir, torrentName, relPath string) string {
	dst, err := wholeTorrentDest(destDir, torrentName, relPath)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(destDir, dst)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	if st, err := os.Stat(dst); err != nil || st.IsDir() {
		return ""
	}
	return dst
}

const dlSelect = `SELECT id, user_id, info_hash, file_index, file_path, file_size, name, magnet,
	tracker, category, status, bytes_downloaded,
	started_at, completed_at, error, created_at,
	COALESCE(priority, 'normal'), COALESCE(stalls, 0), queued_since,
	COALESCE(active_magnet, ''), COALESCE(source, ''),
	COALESCE(dest_base, ''), COALESCE(dest_subdir, ''),
	COALESCE(completion_dest, '') FROM downloads `

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

// Delete removes a download row (used for user-initiated cancel).
// Cancelling does NOT erase on-disk pieces — those are cleaned by the
// streamer cache LRU once the torrent is no longer protected.
//
// IDEMPOTENT: a row that is already gone returns nil, not an error. The
// previous "download não encontrado" error turned every double-delete (and
// every admin delete of another user's row before DeleteScoped existed) into a
// 500 the frontend swallowed silently — the 2s poll then re-showed the row, so
// the user saw "clicked Remove, nothing happened". The DELETE is now
// authoritative: once it returns, the row does not exist, whether we removed it
// now or it was already gone.
func (s *Store) Delete(userID, id int) error {
	_, err := s.deleteScoped(id, userID, false)
	return err
}

// DeleteScoped removes a download row, returning the row as it was BEFORE the
// delete (so the caller can drop the torrent / notify the worker by infoHash)
// or nil when no matching row existed. Idempotent: a missing row is not an
// error. When isAdmin is true the delete is NOT scoped to userID — an admin in
// the "all users" view (DownloadsListAll returns rows of every user) can remove
// any row. Without this, store.Delete(adminID, otherUsersRowID) matched 0 rows
// and the row survived — the confirmed root cause of the intermittent
// "sometimes Remove doesn't remove" (it depended on whose row you clicked).
func (s *Store) DeleteScoped(userID, id int, isAdmin bool) (*Download, error) {
	return s.deleteScoped(id, userID, isAdmin)
}

func (s *Store) deleteScoped(id, userID int, isAdmin bool) (*Download, error) {
	row, err := s.lookupForDelete(id, userID, isAdmin)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil // already gone — idempotent no-op
	}
	q := `DELETE FROM downloads WHERE id=?`
	args := []any{id}
	if !isAdmin {
		q += ` AND user_id=?`
		args = append(args, userID)
	}
	if _, err := s.db.Exec(q, args...); err != nil {
		return nil, err
	}
	return row, nil
}

// lookupForDelete fetches the row a delete is about to remove, honoring the
// same ownership scope as the delete itself. Returns (nil, nil) when no row
// matches — keeping delete idempotent.
func (s *Store) lookupForDelete(id, userID int, isAdmin bool) (*Download, error) {
	q := dlSelect + "WHERE id=?"
	args := []any{id}
	if !isAdmin {
		q += " AND user_id=?"
		args = append(args, userID)
	}
	d, err := scanRow(s.db.QueryRow(q, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return d, nil
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
	var startedAt, completedAt, queuedSince sql.NullTime
	err := r.Scan(
		&d.ID, &d.UserID, &d.InfoHash, &d.FileIndex, &d.FilePath, &d.FileSize,
		&d.Name, &d.Magnet, &d.Tracker, &d.Category, &d.Status, &d.BytesDownloaded,
		&startedAt, &completedAt, &d.Error, &d.CreatedAt,
		&d.Priority, &d.Stalls, &queuedSince, &d.ActiveMagnet, &d.Source,
		&d.DestBase, &d.DestSubdir, &d.CompletionDest,
	)
	if err != nil {
		return nil, err
	}
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
