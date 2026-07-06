package audiometa

import (
	"database/sql"

	"github.com/lgldsilva/jackui/internal/dbutil"
)

// Store caches parsed audio tags in PostgreSQL (shared pool; schema in internal/db migrations).
//
// Why a dedicated DB and not the library/history store: those run MaxOpenConns(1)
// and back hot read paths (Continue Watching, Library page). Folding a lazy tag
// cache onto the same single connection would let a slow read (rclone mount)
// serialise behind, or block, a page load. The dedicated handle isolates that
// contention and is opened with busy_timeout so a concurrent writer waits
// instead of erroring. Writes are single-row upserts (short transactions), never
// a long "scan the whole folder" transaction.
//
// SECURITY: rows are keyed by absolute file_path. Isolation between users is NOT
// enforced here — it is enforced by the HANDLER, which always resolves the path
// through checkMountAccess + scopePath (UserSubpath mounts yield per-user
// absolute paths; AllowedUsers gate visibility) BEFORE reading/writing the cache.
// Never expose a lookup that bypasses that resolution.
type Store struct {
	db *dbutil.DB
}

// New wires the audio-metadata cache onto the shared Postgres pool. Schema is
// applied centrally (internal/db migrations).
func New(pool *sql.DB) (*Store, error) {
	return &Store{db: dbutil.Wrap(pool)}, nil
}

// Close is a no-op: the shared pool's lifecycle is owned by main.
func (s *Store) Close() {}

// Get returns the cached tags for absPath IFF the cached row's last_mod matches
// the file's current modtime (incremental invalidation: a re-rip / promote that
// changes the file changes its mtime, so a stale row is treated as a miss). ok
// is false on miss, stale row, or nil store.
func (s *Store) Get(absPath string, modUnix int64) (Tags, bool) {
	if s == nil {
		return Tags{}, false
	}
	var t Tags
	var hasCover int
	var lastMod int64
	err := s.db.QueryRow(`
		SELECT title, artist, album, album_artist, genre, year, track_number, disc_number, has_cover, last_mod
		FROM audio_metadata WHERE file_path = ?`, absPath).
		Scan(&t.Title, &t.Artist, &t.Album, &t.AlbumArtist, &t.Genre, &t.Year, &t.TrackNumber, &t.DiscNumber, &hasCover, &lastMod)
	if err != nil || lastMod != modUnix {
		return Tags{}, false
	}
	t.HasCover = hasCover == 1
	return t, true
}

// Save upserts the parsed tags for absPath at the given modtime. Single-row,
// short transaction — never holds the connection for a folder-wide scan.
func (s *Store) Save(absPath string, modUnix int64, t Tags) error {
	if s == nil {
		return nil
	}
	hasCover := 0
	if t.HasCover {
		hasCover = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO audio_metadata(file_path, title, artist, album, album_artist, genre, year, track_number, disc_number, has_cover, last_mod, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(file_path) DO UPDATE SET
			title=excluded.title, artist=excluded.artist, album=excluded.album,
			album_artist=excluded.album_artist, genre=excluded.genre, year=excluded.year,
			track_number=excluded.track_number, disc_number=excluded.disc_number,
			has_cover=excluded.has_cover, last_mod=excluded.last_mod, updated_at=CURRENT_TIMESTAMP
	`, absPath, t.Title, t.Artist, t.Album, t.AlbumArtist, t.Genre, t.Year, t.TrackNumber, t.DiscNumber, hasCover, modUnix)
	return err
}
