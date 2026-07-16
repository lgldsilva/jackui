package streamer

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/lgldsilva/jackui/internal/dbutil"
)

// b2i maps a Go bool to the SMALLINT 0/1 the schema uses (pgx won't implicitly
// cast a bool param into a smallint column).
func b2iFav(b bool) int {
	if b {
		return 1
	}
	return 0
}

// FavoritesStore persists "favorite" markings for streamed torrents.
// Favorites are protected from cache eviction (both LRU and manual clear-all).
//
// Schema: one row per torrent name (as stored on disk), nullable info_hash for cross-reference.
type FavoritesStore struct {
	db *dbutil.DB
}

type Favorite struct {
	Name        string    `json:"name"`     // matches CacheEntry.Path (filesystem name)
	InfoHash    string    `json:"infoHash"` // hex hash, if known
	Magnet      string    `json:"magnet"`   // magnet URI — enables Play from /favorites without re-search
	UserID      int       `json:"userId"`
	FavoritedAt time.Time `json:"favoritedAt"`
	Reason      string    `json:"reason"`   // "manual" | "auto-5min"
	FolderID    *int      `json:"folderId"` // nil = root level; otherwise nested in a FavoriteFolder
	// Sort hints filled by the handler from the metadata cache (a separate DB, so
	// no JOIN here). Zero/nil = unknown (never resolved/probed) — sorts last.
	TotalSize int64 `json:"totalSize,omitempty"` // bytes; 0 = unknown
	Seeders   *int  `json:"seeders,omitempty"`   // nil = never probed
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

// NewFavorites wires the favorites store onto the shared Postgres pool. Schema
// is applied centrally (internal/db migrations).
func NewFavorites(pool *sql.DB) (*FavoritesStore, error) {
	return &FavoritesStore{db: dbutil.Wrap(pool)}, nil
}

// Close is a no-op: the shared pool's lifecycle is owned by main.
func (f *FavoritesStore) Close() {
	// No-op: shared Postgres pool lifecycle is owned by main (S1186).
}

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

// HiddenLocalPathOwned is a hidden local (mount, path) together with the user
// that hid it. Used by the admin "all users" download view, which must resolve
// each path under the OWNER's scope (UserSubpath mounts) — not the requester's.
type HiddenLocalPathOwned struct {
	UserID int
	Mount  string
	Path   string
}

// HiddenLocalPathsAll returns every hidden local (mount, path) across all users,
// tagged with the owning user_id. Admin-only callers use it so an item one user
// hid stays hidden in the cross-user listing too.
func (f *FavoritesStore) HiddenLocalPathsAll() ([]HiddenLocalPathOwned, error) {
	if f == nil {
		return nil, nil
	}
	rows, err := f.db.Query(`SELECT user_id, mount, path FROM hidden_local_paths`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []HiddenLocalPathOwned
	for rows.Next() {
		var hp HiddenLocalPathOwned
		if err := rows.Scan(&hp.UserID, &hp.Mount, &hp.Path); err != nil {
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
			`INSERT INTO hidden_local_paths(user_id, mount, path) VALUES(?, ?, ?) ON CONFLICT DO NOTHING`,
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
		var folderID sql.NullInt64
		if err := rows.Scan(&fav.Name, &fav.InfoHash, &fav.Magnet, &fav.UserID, &fav.FavoritedAt, &fav.Reason, &folderID); err != nil {
			continue
		}
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
		if err := rows.Scan(&fl.ID, &fl.UserID, &fl.Name, &parent, &fl.Position, &fl.Hidden, &fl.CreatedAt); err != nil {
			return nil, err
		}
		if parent.Valid {
			v := int(parent.Int64)
			fl.ParentID = &v
		}
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
	var id int64
	err := f.db.QueryRow(`
		INSERT INTO favorite_folders (user_id, name, parent_id, position, hidden)
		VALUES (?, ?, ?, COALESCE((SELECT MAX(position)+1 FROM favorite_folders WHERE user_id = ? AND parent_id IS NOT DISTINCT FROM ?), 0), ?) RETURNING id
	`, userID, name, parent, userID, parent, b2iFav(hidden)).Scan(&id)
	if err != nil {
		return nil, err
	}
	return f.GetFolder(userID, int(id))
}

// SetFolderHidden flips a folder's hidden curtain.
func (f *FavoritesStore) SetFolderHidden(userID, id int, hidden bool) error {
	if f == nil {
		return nil
	}
	_, err := f.db.Exec(`UPDATE favorite_folders SET hidden = ? WHERE id = ? AND user_id = ?`, b2iFav(hidden), id, userID)
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
	if err := row.Scan(&fl.ID, &fl.UserID, &fl.Name, &parent, &fl.Position, &fl.Hidden, &fl.CreatedAt); err != nil {
		return nil, err
	}
	if parent.Valid {
		v := int(parent.Int64)
		fl.ParentID = &v
	}
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
