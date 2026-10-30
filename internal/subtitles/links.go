package subtitles

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lgldsilva/jackui/internal/dbutil"
)

// Link statuses — the only values the subtitle_links.status column may hold.
const (
	// StatusPending: an automatic search started and has not resolved yet.
	StatusPending = "pending"
	// StatusReady: a subtitle was fetched and persisted (vtt_path set).
	StatusReady = "ready"
	// StatusFailed: the automatic search gave up (see the Reason* constants).
	StatusFailed = "failed"
)

// Failure reasons stored in the reason column while status is StatusFailed
// (” while pending/ready). The UI surfaces them as a quiet hint instead of
// retrying forever.
const (
	ReasonNoResults = "no_results"
	ReasonQuota     = "quota"
	ReasonNoAPIKey  = "no_api_key"
	ReasonError     = "error"
)

// Search sources stored in the source column: the cascade step that produced a
// ready link (” while pending/failed).
const (
	SourceHash  = "hash"  // frame-exact OS moviehash search
	SourceIMDB  = "imdb"  // imdb_id search after TMDB matching
	SourceQuery = "query" // title/season/episode text search
)

// SubtitleLink is one persisted auto-fetch decision for a playable item —
// it mirrors the subtitle_links table (migration 0007).
type SubtitleLink struct {
	ID              int64     `json:"id"`
	InfoHash        string    `json:"infoHash"`
	FileIndex       int       `json:"fileIndex"`
	Status          string    `json:"status"`
	Reason          string    `json:"reason"`
	FileID          string    `json:"fileId"`
	Language        string    `json:"language"`
	Languages       string    `json:"languages"`
	HearingImpaired bool      `json:"hearingImpaired"`
	Forced          bool      `json:"forced"`
	VTTPath         string    `json:"vttPath"`
	Source          string    `json:"source"`
	HashRefined     bool      `json:"hashRefined"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// LinksStore persists one auto-fetched subtitle decision per
// (info_hash, file_index), so a second play of the same item skips the
// OpenSubtitles search entirely and the decision survives restarts.
//
// info_hash is a plain text key: the hex infohash for torrents, or the web's
// synthetic `local:...` hash string for local files.
type LinksStore struct {
	db *dbutil.DB
}

// NewLinksStore wires the store onto the shared Postgres pool. Schema is
// applied centrally (internal/db migrations).
func NewLinksStore(pool *sql.DB) (*LinksStore, error) {
	return &LinksStore{db: dbutil.Wrap(pool)}, nil
}

const linkColumns = `id, info_hash, file_index, status, reason, file_id, language,
	languages, hearing_impaired, forced, vtt_path, source, hash_refined,
	created_at, updated_at`

// validateStatus enforces the closed status vocabulary; writers pass their
// package constant, so the guard only trips if the constants above are ever
// edited into an invalid value.
func validateStatus(status string) error {
	switch status {
	case StatusPending, StatusReady, StatusFailed:
		return nil
	default:
		return fmt.Errorf("subtitles: invalid subtitle_links status %q (want %q, %q or %q)",
			status, StatusPending, StatusReady, StatusFailed)
	}
}

// Get returns the persisted decision for (infoHash, fileIndex), or (nil, nil)
// when none exists. Nil-safe receiver.
func (s *LinksStore) Get(ctx context.Context, infoHash string, fileIndex int) (*SubtitleLink, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	var l SubtitleLink
	err := s.db.QueryRowContext(ctx, `
		SELECT `+linkColumns+`
		FROM subtitle_links
		WHERE info_hash = ? AND file_index = ?`, infoHash, fileIndex).Scan(
		&l.ID, &l.InfoHash, &l.FileIndex, &l.Status, &l.Reason, &l.FileID,
		&l.Language, &l.Languages, &l.HearingImpaired, &l.Forced, &l.VTTPath,
		&l.Source, &l.HashRefined, &l.CreatedAt, &l.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("subtitles: get subtitle link %s#%d: %w", infoHash, fileIndex, err)
	}
	return &l, nil
}

// UpsertPending records that an automatic search is starting for
// (infoHash, fileIndex) with the settings language list used for the search.
// An existing row is reset to pending with a cleared reason; its id and
// created_at are kept, and prior outcome columns (file_id, vtt_path, source,
// hash_refined, …) stay until MarkReady/MarkFailed overwrite them — consumers
// must only read outcome fields while status is ready. Concurrent calls for
// the same key are safe (ON CONFLICT). Nil-safe receiver; an empty infoHash is
// a no-op.
func (s *LinksStore) UpsertPending(ctx context.Context, infoHash string, fileIndex int, languages string) error {
	if s == nil || s.db == nil || infoHash == "" {
		return nil
	}
	if err := validateStatus(StatusPending); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO subtitle_links (info_hash, file_index, status, languages)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (info_hash, file_index) DO UPDATE SET
			status = excluded.status,
			reason = '',
			languages = excluded.languages,
			updated_at = now()`,
		infoHash, fileIndex, StatusPending, languages)
	if err != nil {
		return fmt.Errorf("subtitles: upsert pending subtitle link %s#%d: %w", infoHash, fileIndex, err)
	}
	return nil
}

// MarkReady flips the item's decision to ready with the resolved subtitle.
// source == SourceHash also sets hash_refined (a frame-exact hash search
// succeeded). Written as an upsert so a resolved decision is never lost when
// the pending row is absent (e.g. the row was removed between the search and
// the mark): an existing row keeps its id and created_at. Nil-safe receiver;
// an empty infoHash is a no-op.
func (s *LinksStore) MarkReady(ctx context.Context, infoHash string, fileIndex int,
	fileID, language, languages string, hearingImpaired, forced bool, vttPath, source string) error {
	if s == nil || s.db == nil || infoHash == "" {
		return nil
	}
	if err := validateStatus(StatusReady); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO subtitle_links
			(info_hash, file_index, status, reason, file_id, language, languages,
			 hearing_impaired, forced, vtt_path, source, hash_refined)
		VALUES (?, ?, ?, '', ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (info_hash, file_index) DO UPDATE SET
			status = excluded.status,
			reason = excluded.reason,
			file_id = excluded.file_id,
			language = excluded.language,
			languages = excluded.languages,
			hearing_impaired = excluded.hearing_impaired,
			forced = excluded.forced,
			vtt_path = excluded.vtt_path,
			source = excluded.source,
			hash_refined = excluded.hash_refined,
			updated_at = now()`,
		infoHash, fileIndex, StatusReady, fileID, language, languages,
		hearingImpaired, forced, vttPath, source, source == SourceHash)
	if err != nil {
		return fmt.Errorf("subtitles: mark subtitle link ready %s#%d: %w", infoHash, fileIndex, err)
	}
	return nil
}

// MarkRefined sets hash_refined on the EXISTING row for (infoHash, fileIndex)
// without touching any other column: the previous decision (status, file_id,
// vtt_path, source, …) stays the authoritative one. The AutoFetcher records a
// FAILED refinement with it (spec point 7: on failure keep the old row, still
// mark hash_refined), so a restart never re-runs the same failing search. Unlike
// MarkReady/MarkFailed this is deliberately not an upsert: without a row there
// is nothing to refine, and that is reported as an error instead of inventing
// one. Nil-safe receiver; an empty infoHash is a no-op.
func (s *LinksStore) MarkRefined(ctx context.Context, infoHash string, fileIndex int) error {
	if s == nil || s.db == nil || infoHash == "" {
		return nil
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE subtitle_links
		SET hash_refined = true,
		    updated_at = now()
		WHERE info_hash = ? AND file_index = ?`, infoHash, fileIndex)
	if err != nil {
		return fmt.Errorf("subtitles: mark subtitle link refined %s#%d: %w", infoHash, fileIndex, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("subtitles: mark subtitle link refined %s#%d: no such row", infoHash, fileIndex)
	}
	return nil
}

// MarkFailed flips the item's decision to failed with the given reason.
// Upsert like MarkReady; other columns are left untouched so a previously
// ready link keeps its outcome fields for debugging. Nil-safe receiver; an
// empty infoHash is a no-op.
func (s *LinksStore) MarkFailed(ctx context.Context, infoHash string, fileIndex int, reason string) error {
	if s == nil || s.db == nil || infoHash == "" {
		return nil
	}
	if err := validateStatus(StatusFailed); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO subtitle_links (info_hash, file_index, status, reason)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (info_hash, file_index) DO UPDATE SET
			status = excluded.status,
			reason = excluded.reason,
			updated_at = now()`,
		infoHash, fileIndex, StatusFailed, reason)
	if err != nil {
		return fmt.Errorf("subtitles: mark subtitle link failed %s#%d: %w", infoHash, fileIndex, err)
	}
	return nil
}
