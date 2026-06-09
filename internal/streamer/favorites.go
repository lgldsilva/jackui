package streamer

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/lgldsilva/jackui/internal/dbutil"
	_ "modernc.org/sqlite"
)

// FavoritesStore persists "favorite" markings for streamed torrents.
// Favorites are protected from cache eviction (both LRU and manual clear-all).
//
// Schema: one row per torrent name (as stored on disk), nullable info_hash for cross-reference.
type FavoritesStore struct {
	db *sql.DB
}

type Favorite struct {
	Name        string    `json:"name"`     // matches CacheEntry.Path (filesystem name)
	InfoHash    string    `json:"infoHash"` // hex hash, if known
	Magnet      string    `json:"magnet"`   // magnet URI — enables Play from /favorites without re-search
	UserID      int       `json:"userId"`
	FavoritedAt time.Time `json:"favoritedAt"`
	Reason      string    `json:"reason"`   // "manual" | "auto-5min"
	FolderID    *int      `json:"folderId"` // nil = root level; otherwise nested in a FavoriteFolder
}

// FavoriteFolder represents an organizational folder in the user's favorites tree.
// Subfolders are modeled via ParentID (nil = root level). One user's tree
// can be arbitrarily deep; cycle prevention is handled at Move time.
type FavoriteFolder struct {
	ID        int       `json:"id"`
	UserID    int       `json:"userId"`
	Name      string    `json:"name"`
	ParentID  *int      `json:"parentId"`
	Position  int       `json:"position"`
	Hidden    bool      `json:"hidden"`
	CreatedAt time.Time `json:"createdAt"`
}

// NewFavorites opens (or creates) the favorites SQLite DB at given path.
// Typically `<stream_dir>/.favorites.db`.
func NewFavorites(path string) (*FavoritesStore, error) {
	db, err := sql.Open(dbutil.DriverName, path+dbutil.PragmaWAL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	// CREATE with full schema (fresh DBs). Existing DBs get the columns via ALTER below.
	// `idx_fav_user` is created AFTER the ALTER to avoid "no such column" on legacy DBs.
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS favorites (
			name         TEXT PRIMARY KEY,
			info_hash    TEXT,
			favorited_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			reason       TEXT NOT NULL DEFAULT 'manual',
			user_id      INTEGER NOT NULL DEFAULT 0,
			magnet       TEXT NOT NULL DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS idx_fav_hash ON favorites(info_hash);
	`); err != nil {
		db.Close()
		return nil, err
	}
	s := &FavoritesStore{db: db}
	// Idempotent ALTERs for DBs that pre-date user_id / magnet
	if !s.hasColumn("favorites", "user_id") {
		if _, err := db.Exec(`ALTER TABLE favorites ADD COLUMN user_id INTEGER NOT NULL DEFAULT 0`); err != nil {
			db.Close()
			return nil, err
		}
	}
	if !s.hasColumn("favorites", "magnet") {
		if _, err := db.Exec(`ALTER TABLE favorites ADD COLUMN magnet TEXT NOT NULL DEFAULT ''`); err != nil {
			db.Close()
			return nil, err
		}
	}
	// Now safe — user_id column exists
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_fav_user ON favorites(user_id)`); err != nil {
		db.Close()
		return nil, err
	}
	// Folder tree + folder_id column. Idempotent: hasColumn gate keeps it
	// safe to run on existing DBs that pre-date the feature. ON DELETE SET
	// NULL means deleting a folder drops favorites back to root instead of
	// deleting them — closer to user expectation than CASCADE.
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS favorite_folders (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id    INTEGER NOT NULL,
			name       TEXT    NOT NULL,
			parent_id  INTEGER REFERENCES favorite_folders(id) ON DELETE CASCADE,
			position   INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(user_id, parent_id, name)
		);
		CREATE INDEX IF NOT EXISTS idx_fav_folders_user_parent ON favorite_folders(user_id, parent_id);
	`); err != nil {
		db.Close()
		return nil, err
	}
	if !s.hasColumn("favorites", "folder_id") {
		if _, err := db.Exec(`ALTER TABLE favorites ADD COLUMN folder_id INTEGER REFERENCES favorite_folders(id) ON DELETE SET NULL`); err != nil {
			db.Close()
			return nil, err
		}
	}
	// `hidden` folders are kept out of the normal listing (sidebar AND the "all"
	// view) unless the caller explicitly opts in (the UI's easter egg). A light
	// privacy curtain, not encryption — for a category you'd rather not show on a
	// casual glance.
	if !s.hasColumn("favorite_folders", "hidden") {
		if _, err := db.Exec(`ALTER TABLE favorite_folders ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0`); err != nil {
			db.Close()
			return nil, err
		}
	}
	// The hidden curtain also covers local files, which have no info_hash — they
	// are keyed by (mount, path). A row here hides that path (and everything
	// under it) from the local browser unless the easter egg is open. Lives in
	// the favourites DB because it's the same "hidden curtain" domain.
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS hidden_local_paths (
			user_id    INTEGER NOT NULL,
			mount      TEXT    NOT NULL,
			path       TEXT    NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (user_id, mount, path)
		);
	`); err != nil {
		db.Close()
		return nil, err
	}
	// Recovery: an earlier client bug (PlayerModal favoriteAdd args swap) wrote the literal
	// string "manual" into the magnet column instead of the reason. Repair those rows in-place
	// by reconstructing a tracker-less magnet from info_hash. Idempotent: runs on every open
	// but matches zero rows once fixed.
	if _, err := db.Exec(`
		UPDATE favorites
		SET magnet = 'magnet:?xt=urn:btih:' || info_hash
		WHERE magnet = 'manual' AND info_hash != ''
	`); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// hasColumn returns true if the table has the given column. Used for idempotent migrations.
func (f *FavoritesStore) hasColumn(table, col string) bool {
	rows, err := f.db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err == nil && n == col {
			return true
		}
	}
	return false
}

func (f *FavoritesStore) Close() { f.db.Close() }

// Add marks a stream as favorite. Idempotent — re-adding refreshes the timestamp.
// userID=0 means "no auth/legacy". magnet may be empty if unknown.
func (f *FavoritesStore) Add(name, infoHash, magnet, reason string, userID int) error {
	if f == nil {
		return nil
	}
	_, err := f.db.Exec(`
		INSERT INTO favorites(name, info_hash, magnet, reason, user_id) VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			info_hash    = excluded.info_hash,
			magnet       = CASE WHEN excluded.magnet != '' THEN excluded.magnet ELSE favorites.magnet END,
			reason       = excluded.reason,
			user_id      = excluded.user_id,
			favorited_at = CURRENT_TIMESTAMP
	`, name, infoHash, magnet, reason, userID)
	return err
}

// Remove unmarks a favorite. If userID > 0 and not includeAll, only that user's row is removed.
func (f *FavoritesStore) Remove(name string, userID int, includeAll bool) error {
	if f == nil {
		return nil
	}
	if includeAll {
		_, err := f.db.Exec("DELETE FROM favorites WHERE name = ?", name)
		return err
	}
	_, err := f.db.Exec("DELETE FROM favorites WHERE name = ? AND user_id = ?", name, userID)
	return err
}

// IsFavorite reports whether the given on-disk name is favorited (by any user).
// Used by Streamer cache eviction logic — protects favorites of all users from auto-delete.
func (f *FavoritesStore) IsFavorite(name string) bool {
	if f == nil {
		return false
	}
	var n int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM favorites WHERE name = ?", name).Scan(&n); err != nil {
		// Fail-closed: on a transient DB error (e.g. SQLITE_BUSY) assume favorite,
		// so eviction / ClearAll don't delete protected content on a fluke.
		return true
	}
	return n > 0
}

// IsFavoriteOf reports whether the given name is favorited specifically by a user.
func (f *FavoritesStore) IsFavoriteOf(name string, userID int) bool {
	if f == nil {
		return false
	}
	var n int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM favorites WHERE name = ? AND user_id = ?", name, userID).Scan(&n); err != nil {
		return true // fail-closed (see IsFavorite)
	}
	return n > 0
}

// HashSetForUser returns all info_hashes the user has favorited as a set. Used
// pelo handler de busca pra enriquecer SearchResult com isFavorited via uma
// query só, em vez de IsFavoriteByHash N vezes. includeAll=true devolve hashes
// de todos os usuários (admin "all=1"). Hashes vazios são pulados.
func (f *FavoritesStore) HashSetForUser(userID int, includeAll bool) (map[string]bool, error) {
	if f == nil {
		return map[string]bool{}, nil
	}
	q := `SELECT info_hash FROM favorites WHERE info_hash != ''`
	args := []any{}
	if !includeAll {
		q += ` AND user_id = ?`
		args = append(args, userID)
	}
	rows, err := f.db.Query(q, args...)
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

// HiddenHashSet returns, as a set, the info_hashes of favourites that live in a
// hidden folder. Used to keep hidden-folder titles out of Continue Watching and
// the downloads list the same way they're kept out of the favourites view.
// includeAll=true (admin) spans every user's hidden folders.
func (f *FavoritesStore) HiddenHashSet(userID int, includeAll bool) (map[string]bool, error) {
	if f == nil {
		return map[string]bool{}, nil
	}
	q := `SELECT info_hash FROM favorites
	      WHERE info_hash != ''
	        AND folder_id IN (SELECT id FROM favorite_folders WHERE hidden = 1`
	args := []any{}
	if !includeAll {
		q += ` AND user_id = ?`
		args = append(args, userID)
	}
	q += `)`
	rows, err := f.db.Query(q, args...)
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

// HiddenLocalPath is a (mount, path) the user has marked hidden in the local browser.
type HiddenLocalPath struct {
	Mount string `json:"mount"`
	Path  string `json:"path"`
}

// HiddenLocalPaths returns the local (mount, path) pairs the user has hidden.
func (f *FavoritesStore) HiddenLocalPaths(userID int) ([]HiddenLocalPath, error) {
	if f == nil {
		return nil, nil
	}
	rows, err := f.db.Query(`SELECT mount, path FROM hidden_local_paths WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HiddenLocalPath
	for rows.Next() {
		var hp HiddenLocalPath
		if err := rows.Scan(&hp.Mount, &hp.Path); err != nil {
			return nil, err
		}
		out = append(out, hp)
	}
	return out, rows.Err()
}

// SetLocalPathHidden hides (hidden=true) or unhides a local (mount, path) for a
// user. Idempotent: hiding an already-hidden path is a no-op.
func (f *FavoritesStore) SetLocalPathHidden(userID int, mount, path string, hidden bool) error {
	if f == nil {
		return fmt.Errorf(ErrFavoritesUnavail)
	}
	if hidden {
		_, err := f.db.Exec(
			`INSERT OR IGNORE INTO hidden_local_paths(user_id, mount, path) VALUES(?, ?, ?)`,
			userID, mount, path)
		return err
	}
	_, err := f.db.Exec(
		`DELETE FROM hidden_local_paths WHERE user_id = ? AND mount = ? AND path = ?`,
		userID, mount, path)
	return err
}

// IsFavoriteByHash reports whether any favorite row references this infoHash.
func (f *FavoritesStore) IsFavoriteByHash(infoHash string) bool {
	if f == nil || infoHash == "" {
		return false
	}
	var n int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM favorites WHERE info_hash = ?", infoHash).Scan(&n); err != nil {
		return true // fail-closed (see IsFavorite)
	}
	return n > 0
}

// List returns favorites for a user (or all when includeAll=true), most recent first.
func (f *FavoritesStore) List(userID int, includeAll, includeHidden bool) ([]Favorite, error) {
	if f == nil {
		return nil, fmt.Errorf(ErrFavoritesUnavail)
	}
	q := `SELECT name, COALESCE(info_hash,''), COALESCE(magnet,''), user_id, favorited_at, reason, folder_id FROM favorites`
	args := []any{}
	conds := []string{}
	if !includeAll {
		conds = append(conds, "user_id = ?")
		args = append(args, userID)
	}
	// Keep favourites that live inside a hidden folder out of the default view —
	// otherwise the "all" listing would leak the items even though the folder is
	// hidden from the sidebar. The easter egg opts in via includeHidden.
	if !includeHidden {
		conds = append(conds, "(folder_id IS NULL OR folder_id NOT IN (SELECT id FROM favorite_folders WHERE hidden = 1))")
	}
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY favorited_at DESC"
	rows, err := f.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Favorite{}
	for rows.Next() {
		var fav Favorite
		var ts string
		var folderID sql.NullInt64
		if err := rows.Scan(&fav.Name, &fav.InfoHash, &fav.Magnet, &fav.UserID, &ts, &fav.Reason, &folderID); err != nil {
			continue
		}
		fav.FavoritedAt = dbutil.ParseTime(ts)
		if folderID.Valid {
			v := int(folderID.Int64)
			fav.FolderID = &v
		}
		out = append(out, fav)
	}
	return out, rows.Err()
}

// DefaultFavoritesPath returns the standard location inside the stream data dir.
func DefaultFavoritesPath(dataDir string) string {
	return filepath.Join(dataDir, ".favorites.db")
}

// ───── Folders (user-organized favorites tree) ─────

// ListFolders returns all folders for a user. The UI builds the tree
// client-side via parent_id linkage — simpler than recursive SQL.
func (f *FavoritesStore) ListFolders(userID int, includeHidden bool) ([]FavoriteFolder, error) {
	if f == nil {
		return nil, nil
	}
	q := `
		SELECT id, user_id, name, parent_id, position, hidden, created_at
		FROM favorite_folders
		WHERE user_id = ?`
	if !includeHidden {
		q += ` AND hidden = 0`
	}
	q += ` ORDER BY parent_id, position, name`
	rows, err := f.db.Query(q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]FavoriteFolder, 0)
	for rows.Next() {
		var fl FavoriteFolder
		var parent sql.NullInt64
		var ts string
		if err := rows.Scan(&fl.ID, &fl.UserID, &fl.Name, &parent, &fl.Position, &fl.Hidden, &ts); err != nil {
			return nil, err
		}
		if parent.Valid {
			v := int(parent.Int64)
			fl.ParentID = &v
		}
		fl.CreatedAt = dbutil.ParseTime(ts)
		out = append(out, fl)
	}
	return out, rows.Err()
}

// CreateFolder makes a new folder under the optional parent. parentID nil
// creates a root-level folder. hidden keeps it out of the default listing.
func (f *FavoritesStore) CreateFolder(userID int, name string, parentID *int, hidden bool) (*FavoriteFolder, error) {
	if f == nil {
		return nil, fmt.Errorf("favorites store nil")
	}
	var parent interface{}
	if parentID != nil {
		parent = *parentID
	}
	res, err := f.db.Exec(`
		INSERT INTO favorite_folders (user_id, name, parent_id, position, hidden)
		VALUES (?, ?, ?, COALESCE((SELECT MAX(position)+1 FROM favorite_folders WHERE user_id = ? AND parent_id IS ?), 0), ?)
	`, userID, name, parent, userID, parent, hidden)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return f.GetFolder(userID, int(id))
}

// SetFolderHidden flips a folder's hidden curtain.
func (f *FavoritesStore) SetFolderHidden(userID, id int, hidden bool) error {
	if f == nil {
		return nil
	}
	_, err := f.db.Exec(`UPDATE favorite_folders SET hidden = ? WHERE id = ? AND user_id = ?`, hidden, id, userID)
	return err
}

// GetFolder fetches a single folder; returns error if it doesn't belong to user.
func (f *FavoritesStore) GetFolder(userID, id int) (*FavoriteFolder, error) {
	row := f.db.QueryRow(`
		SELECT id, user_id, name, parent_id, position, hidden, created_at
		FROM favorite_folders WHERE id = ? AND user_id = ?
	`, id, userID)
	var fl FavoriteFolder
	var parent sql.NullInt64
	var ts string
	if err := row.Scan(&fl.ID, &fl.UserID, &fl.Name, &parent, &fl.Position, &fl.Hidden, &ts); err != nil {
		return nil, err
	}
	if parent.Valid {
		v := int(parent.Int64)
		fl.ParentID = &v
	}
	fl.CreatedAt = dbutil.ParseTime(ts)
	return &fl, nil
}

// RenameFolder updates the display name.
func (f *FavoritesStore) RenameFolder(userID, id int, newName string) error {
	if f == nil {
		return nil
	}
	_, err := f.db.Exec(`UPDATE favorite_folders SET name = ? WHERE id = ? AND user_id = ?`, newName, id, userID)
	return err
}

// MoveFolder re-parents a folder. Pass nil to move to root. Cycle prevention:
// walk the parent chain — if we encounter the folder being moved, reject.
func (f *FavoritesStore) MoveFolder(userID, id int, newParent *int) error {
	if f == nil {
		return nil
	}
	if newParent != nil {
		// Walk up the chain from newParent and ensure we never see `id`.
		// Tree depth in practice is small (~5); linear walk is fine.
		cur := *newParent
		for hops := 0; hops < 64; hops++ {
			if cur == id {
				return fmt.Errorf("cannot move folder into one of its descendants")
			}
			var p sql.NullInt64
			row := f.db.QueryRow(`SELECT parent_id FROM favorite_folders WHERE id = ? AND user_id = ?`, cur, userID)
			if err := row.Scan(&p); err != nil {
				break
			}
			if !p.Valid {
				break
			}
			cur = int(p.Int64)
		}
	}
	var parent interface{}
	if newParent != nil {
		parent = *newParent
	}
	_, err := f.db.Exec(`UPDATE favorite_folders SET parent_id = ? WHERE id = ? AND user_id = ?`, parent, id, userID)
	return err
}

// DeleteFolder removes a folder. ON DELETE CASCADE removes subfolders too;
// favorites in deleted folders fall back to root (ON DELETE SET NULL).
func (f *FavoritesStore) DeleteFolder(userID, id int) error {
	if f == nil {
		return nil
	}
	_, err := f.db.Exec(`DELETE FROM favorite_folders WHERE id = ? AND user_id = ?`, id, userID)
	return err
}

// MoveFavoriteToFolder reassigns a favorite to a folder (nil = root).
func (f *FavoritesStore) MoveFavoriteToFolder(userID int, name string, folderID *int) error {
	if f == nil {
		return nil
	}
	var folder interface{}
	if folderID != nil {
		folder = *folderID
	}
	_, err := f.db.Exec(`UPDATE favorites SET folder_id = ? WHERE name = ? AND user_id = ?`, folder, name, userID)
	return err
}
