package downloads

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"
)

// benchSchema cria uma tabela downloads mínima compatível com dlSelect
// (sqlite in-memory — o driver modernc registra "sqlite").
func benchSchema(b *testing.B) *Store {
	b.Helper()
	pool, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		b.Fatalf("open sqlite: %v", err)
	}
	b.Cleanup(func() { _ = pool.Close() })
	ddl := `
	CREATE TABLE downloads (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id          INTEGER NOT NULL,
		info_hash        TEXT NOT NULL,
		file_index       INTEGER NOT NULL,
		file_path        TEXT NOT NULL,
		file_size        INTEGER NOT NULL,
		name             TEXT NOT NULL,
		magnet           TEXT NOT NULL,
		tracker          TEXT NOT NULL DEFAULT '',
		category         TEXT NOT NULL DEFAULT '',
		status           TEXT NOT NULL DEFAULT 'queued',
		bytes_downloaded INTEGER NOT NULL DEFAULT 0,
		started_at       TIMESTAMP,
		completed_at     TIMESTAMP,
		error            TEXT NOT NULL DEFAULT '',
		created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		priority         TEXT NOT NULL DEFAULT 'normal',
		queued_since     TIMESTAMP,
		stalls           INTEGER NOT NULL DEFAULT 0,
		active_magnet    TEXT NOT NULL DEFAULT '',
		source           TEXT NOT NULL DEFAULT '',
		dest_base        TEXT NOT NULL DEFAULT '',
		dest_subdir      TEXT NOT NULL DEFAULT '',
		completion_dest  TEXT NOT NULL DEFAULT '',
		linked           INTEGER NOT NULL DEFAULT 0,
		seed_stopped_at  TIMESTAMP
	);`
	if _, err := pool.Exec(ddl); err != nil {
		b.Fatalf("create table: %v", err)
	}
	store, err := New(pool)
	if err != nil {
		b.Fatalf("new store: %v", err)
	}
	return store
}

func benchSeedRows(b *testing.B, s *Store, n int) {
	b.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		b.Fatalf("begin: %v", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO downloads
		(user_id, info_hash, file_index, file_path, file_size, name, magnet, tracker, category, status, bytes_downloaded, started_at, completed_at, error, priority, stalls, active_magnet, source, dest_base, dest_subdir, completion_dest, linked)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		b.Fatalf("prepare: %v", err)
	}
	for i := 0; i < n; i++ {
		status := StatusDownloading
		if i%3 == 0 {
			status = StatusCompleted
		}
		if _, err := stmt.Exec(1,
			fmt.Sprintf("%040x", i),
			i,
			fmt.Sprintf("/data/pack/file-%04d.mkv", i),
			int64(4<<30)+int64(i),
			fmt.Sprintf("Show.S01E%04d.1080p.WEB-DL.x265-GROUP", i),
			"magnet:?xt=urn:btih:"+fmt.Sprintf("%040x", i),
			"udp://tracker.example:1337",
			"series",
			status,
			int64(2<<30),
			nil, nil, "",
			"normal", 0, "", "", "", "", "",
			0,
		); err != nil {
			b.Fatalf("insert: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatalf("commit: %v", err)
	}
}

// BenchmarkStoreList mede o caminho completo do poll do frontend:
// GET /api/downloads → Store.List → scanSlice (5000 = ListMaxResults).
func BenchmarkStoreList(b *testing.B) {
	for _, n := range []int{100, 1000, 5000} {
		b.Run(fmt.Sprintf("rows=%d", n), func(b *testing.B) {
			s := benchSchema(b)
			benchSeedRows(b, s, n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				list, err := s.List(1)
				if err != nil {
					b.Fatalf("list: %v", err)
				}
				if len(list) != n {
					b.Fatalf("want %d rows, got %d", n, len(list))
				}
			}
		})
	}
}

// BenchmarkMarshalDownloadsList mede a serialização JSON da resposta do poll
// (c.JSON no handler usa encoding/json por baixo).
func BenchmarkMarshalDownloadsList(b *testing.B) {
	s := benchSchema(b)
	benchSeedRows(b, s, 5000)
	list, err := s.List(1)
	if err != nil {
		b.Fatalf("list: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, err := marshalJSON(list)
		if err != nil {
			b.Fatalf("marshal: %v", err)
		}
		sinkBytes = data
	}
}

var sinkBytes []byte

func marshalJSON(v any) ([]byte, error) {
	return jsonMarshal(v)
}

func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}
