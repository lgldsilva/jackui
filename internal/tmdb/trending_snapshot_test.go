package tmdb

import (
	"io"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/dbtest"
)

func newSnapTestClient(t *testing.T) *Client {
	t.Helper()
	c, err := New("key", "", dbtest.NewDB(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestWeekKey(t *testing.T) {
	// 2026-01-01 is a Thursday → ISO week 1 of 2026.
	got := weekKey(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	if got != "2026-W01" {
		t.Errorf("weekKey = %q, want 2026-W01", got)
	}
}

func TestSetDirection(t *testing.T) {
	prev := map[int]int{10: 5, 20: 0, 30: 2} // tmdb 10 was rank 5, 20 was rank 0, 30 was rank 2
	cases := []struct {
		id, rank      int
		wantDirection string
		wantDelta     int
	}{
		{10, 2, "up", 3},   // 5 → 2 : up 3
		{20, 4, "down", 4}, // 0 → 4 : down 4
		{30, 2, "same", 0}, // unchanged
		{99, 1, "new", 0},  // not in prev
	}
	for _, c := range cases {
		m := &Match{TmdbID: c.id}
		setDirection(m, c.rank, prev)
		if m.Direction != c.wantDirection || m.RankDelta != c.wantDelta {
			t.Errorf("id=%d rank=%d → %q/%d, want %q/%d", c.id, c.rank, m.Direction, m.RankDelta, c.wantDirection, c.wantDelta)
		}
	}
}

func TestApplyTrendingDirection_FirstWeekAllNew(t *testing.T) {
	c := newSnapTestClient(t)
	items := []Match{{TmdbID: 1}, {TmdbID: 2}, {TmdbID: 3}}
	c.applyTrendingDirection(items)
	for _, m := range items {
		if m.Direction != "new" {
			t.Errorf("#%d: first week should be 'new', got %q", m.TmdbID, m.Direction)
		}
	}
	// Snapshot was persisted.
	ranks := c.prevWeekRanks("9999-W99")
	if len(ranks) != 3 {
		t.Fatalf("expected 3 rows persisted, got %d", len(ranks))
	}
}

func TestPrevWeekRanks_ComparesAcrossWeeks(t *testing.T) {
	c := newSnapTestClient(t)
	// Seed last week's ranking directly.
	last := weekKey(time.Now().AddDate(0, 0, -7))
	for id, rank := range map[int]int{1: 0, 2: 1, 3: 2} {
		if _, err := c.cache.Exec(`INSERT INTO trending_snapshot(week_key, tmdb_id, rank) VALUES(?, ?, ?)`, last, id, rank); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	// This week: #3 jumped to the top, #1 fell.
	items := []Match{{TmdbID: 3}, {TmdbID: 2}, {TmdbID: 1}}
	c.applyTrendingDirection(items)

	byID := map[int]Match{}
	for _, m := range items {
		byID[m.TmdbID] = m
	}
	if byID[3].Direction != "up" || byID[3].RankDelta != 2 {
		t.Errorf("#3 should be up 2, got %q/%d", byID[3].Direction, byID[3].RankDelta)
	}
	if byID[1].Direction != "down" || byID[1].RankDelta != 2 {
		t.Errorf("#1 should be down 2, got %q/%d", byID[1].Direction, byID[1].RankDelta)
	}
	if byID[2].Direction != "same" {
		t.Errorf("#2 should be same, got %q", byID[2].Direction)
	}
}

func TestSaveSnapshot_EmptyIsNoOp(t *testing.T) {
	c := newSnapTestClient(t)
	c.saveSnapshot("2030-W01", nil) // empty → no write, no panic
	var n int
	_ = c.cache.QueryRow(`SELECT COUNT(*) FROM trending_snapshot`).Scan(&n)
	if n != 0 {
		t.Errorf("empty save should write nothing, got %d rows", n)
	}
}

func TestSnapshot_DBErrorsAreSafe(t *testing.T) {
	// With the DB closed, the snapshot helpers must fail gracefully (no panic):
	// applyTrendingDirection still tags everything "new", saveSnapshot bails on
	// the failed transaction, and prevWeekRanks returns empty.
	c := newSnapTestClient(t)
	_ = c.cache.Close()
	items := []Match{{TmdbID: 1}, {TmdbID: 2}}
	c.applyTrendingDirection(items)
	for _, m := range items {
		if m.Direction != "new" {
			t.Errorf("with no DB, items should default to 'new', got %q", m.Direction)
		}
	}
	if got := c.prevWeekRanks("2099-W01"); len(got) != 0 {
		t.Errorf("prevWeekRanks on closed DB should be empty, got %d", len(got))
	}
}

func TestPrevWeekRanks_DiscardsPartialOnRowsErr(t *testing.T) {
	// The week lookup and the rank scan need different row shapes. A single
	// FailRowsDB result can't serve both, so the rank scan is the only query
	// that fails mid-iteration. The clean scan proves those rows are readable;
	// the failed scan must not publish them (a partial week would badge the
	// rest of the list as "new").
	ranks := [][]any{{int64(10), int64(0)}, {int64(20), int64(1)}}
	week := dbtest.QueryResult{Rows: [][]any{{"2026-W40"}}}

	okDB := dbtest.NewFailRowsDBQueries(t,
		dbtest.QueryResult{Match: "tmdb_id, rank", Rows: ranks},
		week,
	)
	okClient, err := New("key", "", okDB.DB)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := okClient.prevWeekRanks("2026-W41")
	if len(got) != 2 || got[10] != 0 || got[20] != 1 {
		t.Fatalf("clean scan = %v, want map[10:0 20:1]", got)
	}

	badDB := dbtest.NewFailRowsDBQueries(t,
		dbtest.QueryResult{Match: "tmdb_id, rank", Rows: ranks, Err: io.ErrUnexpectedEOF},
		week,
	)
	badClient, err := New("key", "", badDB.DB)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := badClient.prevWeekRanks("2026-W41"); len(got) != 0 {
		t.Fatalf("prevWeekRanks = %v, want an empty map after rows.Err", got)
	}
	if n := badDB.Queries(); n != 2 {
		t.Fatalf("driver queries = %d, want 2 (the week lookup must succeed so the rank scan is what fails)", n)
	}
}

func TestSaveSnapshot_PrunesOldWeeks(t *testing.T) {
	c := newSnapTestClient(t)
	// Insert 6 distinct old weeks, then save → only snapshotHistoryWeeks kept.
	for i := 1; i <= 6; i++ {
		wk := "2020-W0" + string(rune('0'+i))
		if _, err := c.cache.Exec(`INSERT INTO trending_snapshot(week_key, tmdb_id, rank) VALUES(?, 1, 0)`, wk); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	c.saveSnapshot(weekKey(time.Now()), []Match{{TmdbID: 1}})
	var weeks int
	_ = c.cache.QueryRow(`SELECT COUNT(DISTINCT week_key) FROM trending_snapshot`).Scan(&weeks)
	if weeks > snapshotHistoryWeeks {
		t.Errorf("expected <= %d weeks retained, got %d", snapshotHistoryWeeks, weeks)
	}
}
